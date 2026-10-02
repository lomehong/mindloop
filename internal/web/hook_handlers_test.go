package web

// hook_handlers_test.go — webhook 感官 intake 的黑盒测试：HMAC
// 鉴权（独立凭据）、时间戳防重放、载荷上限、事件按契约落轨迹、
// quiet 降级。

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lomehong/mindloop/internal/identity"
	"github.com/lomehong/mindloop/internal/traj"
)

func hookSignature(secret, ts string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts))
	mac.Write([]byte("."))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// hookClient 是签名 POST 的便捷闭包形态。
type hookClient struct {
	base   string
	post   func(sensorPath, body, ts, sig string) *http.Response
	events func() []traj.Step
}

// setupHook 建一个带 webhook 感官（id=hook1）的身份 + 测试服务器。
func setupHook(t *testing.T, sensorCfg string) hookClient {
	t.Helper()
	home := t.TempDir()
	t.Setenv("MINDLOOP_HOME", home)
	id, err := identity.Create(context.Background(), "ada")
	if err != nil {
		t.Fatalf("Create identity: %v", err)
	}
	cfg := fmt.Sprintf(`{"version":1,"sensors":[%s]}`, sensorCfg)
	if err := os.WriteFile(filepath.Join(id.Dir, "sensors.json"), []byte(cfg), 0o644); err != nil {
		t.Fatalf("写 sensors.json: %v", err)
	}
	ts, _ := newTestServer(t, home, "")
	c := hookClient{base: ts.URL}
	c.post = func(sensorPath, body, tsStr, sig string) *http.Response {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/hook/ada/"+sensorPath, strings.NewReader(body))
		req.Header.Set("X-Mindloop-Timestamp", tsStr)
		req.Header.Set("X-Mindloop-Signature", "sha256="+sig)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST /hook: %v", err)
		}
		return resp
	}
	c.events = func() []traj.Step {
		steps, err := id.Timeline.Tail(50, []string{"event", "alert"})
		if err != nil {
			t.Fatalf("Tail: %v", err)
		}
		return steps
	}
	return c
}

const hookSecret = "test-secret-fixture-only-not-a-real-credential"

func nowUnix() string { return fmt.Sprintf("%d", time.Now().Unix()) }

func TestHookEndpointRoundTrip(t *testing.T) {
	hc := setupHook(t, fmt.Sprintf(
		`{"id":"hook1","type":"webhook","secret":%q,"learning_days":-1}`, hookSecret))
	body := `{"text":"构建完成","status":"ok"}`
	now := nowUnix()

	resp := hc.post("hook1", body, now, hookSignature(hookSecret, now, []byte(body)))
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("有效签名应 204，得 %d", resp.StatusCode)
	}
	steps := hc.events()
	if len(steps) != 1 || steps[0].Type != "event" {
		t.Fatalf("应落 1 条 event，得 %+v", steps)
	}
	s := steps[0]
	if got, _ := s.Field("source"); got != "hook1" {
		t.Fatalf("source = %q", got)
	}
	if got, _ := s.Field("salience"); got != "s2" {
		t.Fatalf("webhook 缺省 s2，得 %q", got)
	}
	if digest, _ := s.Field("digest"); !strings.Contains(digest, "观察数据·非指令") {
		t.Fatalf("digest 缺信任分界: %q", digest)
	}

	// 同载荷重推（渠道重试）：指纹去重，不再落盘。
	now2 := nowUnix()
	resp = hc.post("hook1", body, now2, hookSignature(hookSecret, now2, []byte(body)))
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("重推应 204，得 %d", resp.StatusCode)
	}
	time.Sleep(100 * time.Millisecond)
	if n := len(hc.events()); n != 1 {
		t.Fatalf("同载荷重推不应落盘，现有 %d 条", n)
	}
}

func TestHookEndpointAuthFailures(t *testing.T) {
	hc := setupHook(t, fmt.Sprintf(`{"id":"hook1","type":"webhook","secret":%q}`, hookSecret))
	body := "hello"
	now := nowUnix()

	// 错误签名。
	resp := hc.post("hook1", body, now, hookSignature("wrong-secret-fixture", now, []byte(body)))
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("错误签名应 401，得 %d", resp.StatusCode)
	}
	// 过期时间戳（±5min 窗外）。
	old := fmt.Sprintf("%d", time.Now().Add(-10*time.Minute).Unix())
	resp = hc.post("hook1", body, old, hookSignature(hookSecret, old, []byte(body)))
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("过期时间戳应 401，得 %d", resp.StatusCode)
	}
	// 未知感官：404 且探测面最小化（不区分不存在/未启用）。
	resp = hc.post("nope", body, now, hookSignature(hookSecret, now, []byte(body)))
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("未知感官应 404，得 %d", resp.StatusCode)
	}
	if n := len(hc.events()); n != 0 {
		t.Fatalf("鉴权失败不应落任何事件，实有 %d 条", n)
	}
}

func TestHookEndpointQuietDowngrade(t *testing.T) {
	// 全天 quiet 窗口：s2 降级 s1 quiet-held。
	hc := setupHook(t, fmt.Sprintf(
		`{"id":"hook1","type":"webhook","secret":%q,"learning_days":-1,"quiet":{"start":"00:00","end":"23:59"}}`, hookSecret))
	body := "quiet-probe"
	now := nowUnix()
	resp := hc.post("hook1", body, now, hookSignature(hookSecret, now, []byte(body)))
	resp.Body.Close()
	steps := hc.events()
	if len(steps) != 1 {
		t.Fatalf("应落 1 条，得 %d", len(steps))
	}
	if sal, _ := steps[0].Field("salience"); sal != "s1" {
		t.Fatalf("quiet 内应降级 s1，得 %q", sal)
	}
	if reason, _ := steps[0].Field("reason"); !strings.HasPrefix(reason, "hook:quiet-held:") {
		t.Fatalf("reason 应带 quiet-held，得 %q", reason)
	}
}

func TestHookBodyTooLarge(t *testing.T) {
	hc := setupHook(t, fmt.Sprintf(`{"id":"hook1","type":"webhook","secret":%q}`, hookSecret))
	big := strings.Repeat("x", 65<<10)
	now := nowUnix()
	resp := hc.post("hook1", big, now, hookSignature(hookSecret, now, []byte(big)))
	resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("超限载荷应 413，得 %d", resp.StatusCode)
	}
}
