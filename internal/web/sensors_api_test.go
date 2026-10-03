package web

// sensors_api_test.go — 感知管理 API 的凭据红线钉子（system.md §3）：
// HMAC 密钥只在创建响应里出现一次，列表永远剥除；sensors.json 是
// 凭据文件（Unix 下 0600）。

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lomehong/mindloop/internal/identity"
)

const apiSecret = "api-test-hmac-secret-fixture-only"

func getSensors(t *testing.T, base, name string) string {
	t.Helper()
	resp, err := http.Get(base + "/api/identities/" + name + "/sensors")
	if err != nil {
		t.Fatalf("GET sensors: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("GET sensors 状态 %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	return string(body)
}

func TestSensorsGetNeverReturnsSecret(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MINDLOOP_HOME", home)
	id, err := identity.Create(context.Background(), "ada")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	cfg := `{"version":1,"sensors":[{"id":"hook1","type":"webhook","secret":"` + apiSecret + `"}]}`
	if err := os.WriteFile(filepath.Join(id.Dir, "sensors.json"), []byte(cfg), 0o600); err != nil {
		t.Fatalf("写 sensors.json: %v", err)
	}
	ts, _ := newTestServer(t, home, "")

	body := getSensors(t, ts.URL, "ada")
	if strings.Contains(body, apiSecret) {
		t.Fatalf("GET 列表不得回显 HMAC 密钥（system.md §3 凭据红线）：%s", body)
	}
	var wrap struct {
		Sensors []map[string]any `json:"sensors"`
	}
	if err := json.Unmarshal([]byte(body), &wrap); err != nil {
		t.Fatalf("解析: %v", err)
	}
	if len(wrap.Sensors) != 1 {
		t.Fatalf("应 1 条感官，得 %d", len(wrap.Sensors))
	}
	if s, ok := wrap.Sensors[0]["secret"]; ok && s != "" {
		t.Fatalf("secret 字段应剥除，得 %v", s)
	}
}

// TestRobotdStatusFromMcpJSON：身（robotd）的授权面从 mcp.json 合并
// 视图读出——未配置 / 观察模式 / 动作模式三态。
func TestRobotdStatusFromMcpJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MINDLOOP_HOME", home)
	id, err := identity.Create(context.Background(), "ada")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	ts, _ := newTestServer(t, home, "")

	// 未配置。
	body := getSensors(t, ts.URL, "ada")
	var wrap struct {
		Robotd map[string]any `json:"robotd"`
	}
	if err := json.Unmarshal([]byte(body), &wrap); err != nil {
		t.Fatalf("解析: %v", err)
	}
	if wrap.Robotd["configured"] != false {
		t.Fatalf("未配置应报 configured=false: %v", wrap.Robotd)
	}

	// 身份级观察模式（无 --window-allow）。
	obs := `{"mcpServers":{"robotd":{"command":"mindloop.exe","args":["robotd"]}}}`
	if err := os.WriteFile(filepath.Join(id.Dir, "mcp.json"), []byte(obs), 0o600); err != nil {
		t.Fatal(err)
	}
	body = getSensors(t, ts.URL, "ada")
	if err := json.Unmarshal([]byte(body), &wrap); err != nil {
		t.Fatal(err)
	}
	if wrap.Robotd["configured"] != true || wrap.Robotd["mode"] != "observe" {
		t.Fatalf("观察模式不符: %v", wrap.Robotd)
	}

	// 动作模式（--window-allow 两种形态混用）。
	act := `{"mcpServers":{"robotd":{"command":"mindloop.exe","args":["robotd","--window-allow","记事本","--window-allow=终端"]}}}`
	if err := os.WriteFile(filepath.Join(id.Dir, "mcp.json"), []byte(act), 0o600); err != nil {
		t.Fatal(err)
	}
	body = getSensors(t, ts.URL, "ada")
	if err := json.Unmarshal([]byte(body), &wrap); err != nil {
		t.Fatal(err)
	}
	if wrap.Robotd["mode"] != "action" {
		t.Fatalf("动作模式不符: %v", wrap.Robotd)
	}
	allow, _ := wrap.Robotd["allow"].([]any)
	if len(allow) != 2 || allow[0] != "记事本" || allow[1] != "终端" {
		t.Fatalf("白名单解析不符: %v", wrap.Robotd["allow"])
	}
}

func TestSensorsPostSecretOnceAndCredentialFilePerms(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MINDLOOP_HOME", home)
	id, err := identity.Create(context.Background(), "ada")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := os.WriteFile(filepath.Join(id.Dir, "sensors.json"), []byte(`{"version":1,"sensors":[]}`), 0o600); err != nil {
		t.Fatalf("写 sensors.json: %v", err)
	}
	ts, _ := newTestServer(t, home, "")

	resp, err := http.Post(ts.URL+"/api/identities/ada/sensors", "application/json",
		strings.NewReader(`{"id":"hook1","type":"webhook"}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("POST 状态 %d: %s", resp.StatusCode, body)
	}
	var created struct {
		Secret     string `json:"secret"`
		SecretNote string `json:"secret_note"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("解析 POST 响应: %v", err)
	}
	if created.Secret == "" || created.SecretNote == "" {
		t.Fatalf("创建响应应只显示一次密钥与说明，得 %s", body)
	}

	// 再取列表：密钥必须已被剥除。
	if got := getSensors(t, ts.URL, "ada"); strings.Contains(got, created.Secret) {
		t.Fatalf("创建后列表仍回显密钥：%s", got)
	}

	// 盘上是凭据文件：内容含密钥（热加载需要），Unix 下权限 0600。
	path := filepath.Join(id.Dir, "sensors.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读 sensors.json: %v", err)
	}
	if !strings.Contains(string(raw), created.Secret) {
		t.Fatalf("盘上应保留密钥（热加载需要）")
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatalf("Stat: %v", err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Fatalf("sensors.json 含凭据应 0600，得 %v", fi.Mode().Perm())
		}
	}
}
