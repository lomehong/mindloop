package robotd

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

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
	s.lastInput = func() (time.Duration, error) { return time.Hour, nil } // 缺省：操作员早已空闲
	actions := &[]string{}
	acted := &falsePtr
	s.click = func(x, y int, right bool) error {
		*actions = append(*actions, fmt.Sprintf("click %d,%d right=%v", x, y, right))
		return nil
	}
	s.typeText = func(text string) error { *acted = true; return nil }
	s.focus = func(substr string) (string, error) {
		*actions = append(*actions, "focus "+substr)
		return "fake-"+substr, nil
	}
	return s, actions, acted
}

var falsePtr = false

// withIdleOperator 把操作员在场守卫置为"早已空闲"——直接构造
// NewServer 的测试必须经此注入（缺省接真实键鼠状态，操作员正在
// 打字时守卫会拒绝动作，测试间歇失败——守卫真实工作反而坑了
// 确定性，2026-10-03 实证）。
func withIdleOperator(s *Server) *Server {
	s.lastInput = func() (time.Duration, error) { return time.Hour, nil }
	return s
}

// TestOperatorPresenceGuard：操作员手不离键鼠时动作拒绝——身不与
// 操作员抢键盘（2026-10-03 实机事故形态：键入中途焦点被操作员抢
// 走，半截文本漏进操作员的前台窗口）。
func TestOperatorPresenceGuard(t *testing.T) {
	s, actions, _ := newTestServer(t, []string{"Notepad"}, "Notepad")
	age := 100 * time.Millisecond
	s.lastInput = func() (time.Duration, error) { return age, nil }

	// 操作员刚输入过：type 与 window 聚焦都拒绝。
	if _, err := callTool(t, s, "screen_type", `{"text":"hi","window":"Notepad"}`); err == nil || !strings.Contains(err.Error(), "不与操作员抢键盘") {
		t.Fatalf("操作员在场应拒绝键入: %v", err)
	}
	if len(*actions) != 0 {
		t.Fatalf("被拒动作不应执行: %v", *actions)
	}

	// 操作员空闲超过阈值：放行。
	age = time.Hour
	if _, err := callTool(t, s, "screen_type", `{"text":"ok","window":"Notepad"}`); err != nil {
		t.Fatalf("操作员空闲后应放行: %v", err)
	}

	// 守卫关闭（--min-idle 0 显式覆盖）：在场也放行。
	s.minIdle = 0
	s.lastInput = func() (time.Duration, error) { return 0, nil }
	if _, err := callTool(t, s, "screen_click", `{"x":1,"y":2}`); err != nil {
		t.Fatalf("守卫关闭后应放行: %v", err)
	}

	// 在场检测本身失败：宁可错杀。
	s.minIdle = DefaultMinIdle
	s.lastInput = func() (time.Duration, error) { return 0, fmt.Errorf("API 失败") }
	if _, err := callTool(t, s, "screen_click", `{"x":1,"y":2}`); err == nil || !strings.Contains(err.Error(), "宁可错杀") {
		t.Fatalf("在场检测失败应拒绝: %v", err)
	}
}

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
	s := withIdleOperator(NewServer([]string{"记事本"}, func(string) {}))
	s.foreground = func() (string, bool) { return "记事本", true }
	s.capture = func() ([]byte, robot.ScreenInfo, error) { return nil, robot.ScreenInfo{}, fmt.Errorf("截屏失败") }
	s.click = func(int, int, bool) error { return nil }
	s.typeText = func(string) error { return nil }
	if _, err := callTool(t, s, "screen_click", `{"x":1,"y":2}`); err == nil || !strings.Contains(err.Error(), "回读失败") {
		t.Fatalf("回读失败应报错: %v", err)
	}
}

func TestActionFailureIsError(t *testing.T) {
	s := withIdleOperator(NewServer([]string{"记事本"}, func(string) {}))
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
	s := withIdleOperator(NewServer([]string{"记事本"}, func(l string) { lines = append(lines, l) }))
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

// TestAtomicWindowFocus：前台被抢（操作员正在用机器）时，带
// window 参数的单次调用先原子聚焦目标再动作——两步调用形态下
// 这会被白名单门拒绝。
func TestAtomicWindowFocus(t *testing.T) {
	s := withIdleOperator(NewServer([]string{"Notepad"}, func(string) {}))
	fg := "ZCode" // 前台被操作员抢走
	s.foreground = func() (string, bool) { return fg, fg != "" }
	s.capture = func() ([]byte, robot.ScreenInfo, error) {
		return []byte("fakepng"), robot.ScreenInfo{Width: 100, Height: 80}, nil
	}
	typed := ""
	s.typeText = func(text string) error { typed = text; return nil }
	s.focus = func(substr string) (string, error) {
		fg = "*scratch - Notepad" // 聚焦成功：前台切到目标
		return "窗口已聚焦", nil
	}

	// 不带 window：前台不在白名单，拒绝且不键入。
	if _, err := callTool(t, s, "screen_type", `{"text":"hi"}`); err == nil || !strings.Contains(err.Error(), "不在白名单") {
		t.Fatalf("前台被抢且无 window 参数应拒绝: %v", err)
	}
	if typed != "" {
		t.Fatal("拒绝路径不应键入")
	}

	// 带 window：原子聚焦 + 键入 + 回读。
	res, err := callTool(t, s, "screen_type", `{"text":"原子键入","window":"Notepad"}`)
	if err != nil {
		t.Fatalf("window 参数应原子恢复: %v", err)
	}
	if typed != "原子键入" {
		t.Fatalf("键入未发生: %q", typed)
	}
	if !strings.Contains(res.Text, "type 完成") {
		t.Fatalf("回读证据缺失: %q", res.Text)
	}

	// 聚焦失败（目标不存在）：报错且不键入。
	s2 := withIdleOperator(NewServer([]string{"Notepad"}, func(string) {}))
	fg2 := "ZCode"
	s2.foreground = func() (string, bool) { return fg2, fg2 != "" }
	s2.capture = func() ([]byte, robot.ScreenInfo, error) { return []byte("x"), robot.ScreenInfo{}, nil }
	typed2 := ""
	s2.typeText = func(text string) error { typed2 = text; return nil }
	s2.focus = func(string) (string, error) { return "", fmt.Errorf("找不到窗口") }
	if _, err := callTool(t, s2, "screen_type", `{"text":"x","window":"Ghost"}`); err == nil {
		t.Fatal("聚焦失败应报错")
	}
	if typed2 != "" {
		t.Fatal("聚焦失败不应键入")
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
