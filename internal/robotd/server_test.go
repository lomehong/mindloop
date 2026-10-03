package robotd

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/lomehong/mindloop/internal/mcp"
	"github.com/lomehong/mindloop/internal/robot"
)

// newTestServer 注入假前台与假截屏——安全链的验证不依赖测试机的
// 真实桌面状态。动作函数换成记录器（测试绝不真实点击）。
func newTestServer(t *testing.T, allow []string, foreground string) (*Server, *[]string, *bool) {
	t.Helper()
	s := NewServer(allow, func(string) {})
	s.foreground = func() (string, bool) { return foreground, foreground != "" }
	s.capture = func() ([]byte, robot.ScreenInfo, error) {
		return []byte("fakepng"), robot.ScreenInfo{Width: 100, Height: 80}, nil
	}
	actions := &[]string{}
	acted := &falsePtr
	s.click = func(x, y int, right bool) error {
		*actions = append(*actions, fmt.Sprintf("click %d,%d right=%v", x, y, right))
		return nil
	}
	s.typeText = func(text string) error { *acted = true; return nil }
	s.focus = func(substr string) (string, error) {
		*actions = append(*actions, "focus "+substr)
		return "fake-" + substr, nil
	}
	return s, actions, acted
}

var falsePtr = false

func TestObserveModeDeniesActions(t *testing.T) {
	s, actions, _ := newTestServer(t, nil, "记事本 -Untitled")
	for _, tc := range []struct {
		tool, args string
	}{
		{"screen_click", `{"x":10,"y":20}`},
		{"screen_type", `{"text":"hi"}`},
		{"screen_focus", `{"title":"记事本"}`},
	} {
		res, err := callTool(t, s, tc.tool, tc.args)
		if err == nil {
			t.Fatalf("%s 在观察模式应被拒绝: %+v", tc.tool, res)
		}
		if !strings.Contains(err.Error(), "观察模式") {
			t.Fatalf("%s 拒绝理由应点名观察模式: %v", tc.tool, err)
		}
	}
	if len(*actions) != 0 {
		t.Fatalf("被拒动作不应执行: %v", *actions)
	}
}

func TestWhitelistTripwire(t *testing.T) {
	// 前台不在白名单：拒绝且不执行。
	s, actions, _ := newTestServer(t, []string{"记事本"}, "Visual Studio Code")
	if _, err := callTool(t, s, "screen_click", `{"x":1,"y":2}`); err == nil || !strings.Contains(err.Error(), "不在白名单") {
		t.Fatalf("白名单外前台应被拒: %v", err)
	}
	if len(*actions) != 0 {
		t.Fatalf("拒绝的动作不应执行: %v", *actions)
	}
	// 前台命中白名单：放行（动作经注入的记录器执行）。
	s2, _, _ := newTestServer(t, []string{"记事本"}, "记事本 - README.md")
	res, err := callTool(t, s2, "screen_click", `{"x":1,"y":2}`)
	if err != nil {
		t.Fatalf("白名单内应放行: %v", err)
	}
	if !strings.Contains(res.Text, "click 完成") || !strings.Contains(res.Text, "后回读") {
		t.Fatalf("回读证据缺失: %q", res.Text)
	}
}

func TestSensitiveForegroundBeatsEverything(t *testing.T) {
	// 敏感前台在白名单命中时也拒绝——宁可多拒。
	s, _, _ := newTestServer(t, []string{"Password"}, "1Password - 主保险库")
	for _, tc := range []struct{ tool, args string }{
		{"screen_shot", `{}`},
		{"screen_click", `{"x":1,"y":2}`},
		{"screen_type", `{"text":"secret"}`},
	} {
		if _, err := callTool(t, s, tc.tool, tc.args); err == nil || !strings.Contains(err.Error(), "敏感") {
			t.Fatalf("%s 对敏感前台应拒绝: %v", tc.tool, err)
		}
	}
}

func TestNoForegroundDenied(t *testing.T) {
	s, _, _ := newTestServer(t, []string{"x"}, "")
	if _, err := callTool(t, s, "screen_click", `{"x":1,"y":2}`); err == nil || !strings.Contains(err.Error(), "无前台窗口") {
		t.Fatalf("无前台应拒绝: %v", err)
	}
}

func TestReadbackFailureIsError(t *testing.T) {
	// 动作执行了但回读截屏失败 = 验证不完整 = 报错（触觉异常路径）。
	s := NewServer([]string{"记事本"}, func(string) {})
	s.foreground = func() (string, bool) { return "记事本", true }
	s.capture = func() ([]byte, robot.ScreenInfo, error) { return nil, robot.ScreenInfo{}, fmt.Errorf("截屏失败") }
	s.click = func(int, int, bool) error { return nil }
	s.typeText = func(string) error { return nil }
	if _, err := callTool(t, s, "screen_click", `{"x":1,"y":2}`); err == nil || !strings.Contains(err.Error(), "回读失败") {
		t.Fatalf("回读失败应报错: %v", err)
	}
}

func TestActionFailureIsError(t *testing.T) {
	s := NewServer([]string{"记事本"}, func(string) {})
	s.foreground = func() (string, bool) { return "记事本", true }
	s.capture = func() ([]byte, robot.ScreenInfo, error) { return []byte("x"), robot.ScreenInfo{}, nil }
	s.click = func(int, int, bool) error { return fmt.Errorf("SendInput 失败") }
	s.typeText = func(string) error { return nil }
	if _, err := callTool(t, s, "screen_click", `{"x":1,"y":2}`); err == nil || !strings.Contains(err.Error(), "触觉异常") {
		t.Fatalf("动作失败应走触觉异常路径: %v", err)
	}
}

func TestShotReturnsImageContent(t *testing.T) {
	s, _, _ := newTestServer(t, nil, "记事本")
	res, err := callTool(t, s, "screen_shot", `{}`)
	if err != nil {
		t.Fatalf("screen_shot: %v", err)
	}
	if len(res.Images) != 1 || res.Images[0].MIMEType != "image/png" {
		t.Fatalf("截屏应返回图片内容块: %+v", res.Images)
	}
	if _, err := base64.StdEncoding.DecodeString(res.Images[0].Data); err != nil {
		t.Fatalf("图片数据应为 base64: %v", err)
	}
	if !strings.Contains(res.Text, "100x80") {
		t.Fatalf("几何信息缺失: %q", res.Text)
	}
}

func TestAuditTrail(t *testing.T) {
	var lines []string
	s := NewServer([]string{"记事本"}, func(l string) { lines = append(lines, l) })
	s.foreground = func() (string, bool) { return "记事本", true }
	s.capture = func() ([]byte, robot.ScreenInfo, error) { return []byte("x"), robot.ScreenInfo{}, nil }
	s.click = func(int, int, bool) error { return nil }
	s.typeText = func(string) error { return nil }
	if _, err := callTool(t, s, "screen_click", `{"x":3,"y":4}`); err != nil {
		t.Fatalf("click: %v", err)
	}
	if len(lines) != 1 || !strings.Contains(lines[0], "ACTION=click") || !strings.Contains(lines[0], "前台=记事本") {
		t.Fatalf("审计留痕缺失: %v", lines)
	}
	// 拒绝也留痕。
	s2, _, _ := newTestServer(t, nil, "VSCode")
	s2.audit = func(l string) { lines = append(lines, l) }
	_, _ = callTool(t, s2, "screen_click", `{"x":1,"y":1}`)
	if !strings.Contains(lines[len(lines)-1], "ACTION=click-denied") {
		t.Fatalf("拒绝应留痕: %v", lines)
	}
}

func TestScreenModeTransparency(t *testing.T) {
	s, _, _ := newTestServer(t, nil, "x")
	res, _ := callTool(t, s, "screen_mode", `{}`)
	if !strings.Contains(res.Text, "观察模式") {
		t.Fatalf("模式应透明: %q", res.Text)
	}
	s2, _, _ := newTestServer(t, []string{"记事本"}, "x")
	res2, _ := callTool(t, s2, "screen_mode", `{}`)
	if !strings.Contains(res2.Text, "记事本") {
		t.Fatalf("授权模式应列白名单: %q", res2.Text)
	}
}

func TestToolsListed(t *testing.T) {
	s, _, _ := newTestServer(t, nil, "x")
	tools := s.Tools()
	want := map[string]bool{}
	for _, tl := range tools {
		want[tl.Name] = true
	}
	for _, name := range []string{"screen_mode", "screen_windows", "screen_shot", "screen_cursor", "screen_focus", "screen_click", "screen_type"} {
		if !want[name] {
			t.Fatalf("缺少工具 %s", name)
		}
	}
}

// callTool 走真实的 serve 管道调用工具（协议往返也一并验证）。
func callTool(t *testing.T, s *Server, name, args string) (mcp.ToolResult, error) {
	t.Helper()
	byName := map[string]mcp.ServerTool{}
	for _, tl := range s.Tools() {
		byName[tl.Name] = tl
	}
	tl, ok := byName[name]
	if !ok {
		t.Fatalf("未知工具 %s", name)
	}
	return tl.Handler(context.Background(), json.RawMessage(args))
}
