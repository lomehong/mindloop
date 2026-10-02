package web

// 外部连接面（/api/identities/{n}/connections）的契约测试：MCP
// 清单合并与来源标注、配置文件状态、bridge 空态槽，以及安全红线
// ——env/headers 的值绝不回显。

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lomehong/mindloop/internal/identity"
	"github.com/lomehong/mindloop/internal/traj"
)

// connectionsJSON 是测试侧对契约的镜像。
type connectionsJSON struct {
	Identity   map[string]string `json:"identity"`
	MCPServers []struct {
		Name       string   `json:"name"`
		Transport  string   `json:"transport"`
		Command    string   `json:"command"`
		Args       []string `json:"args"`
		URL        string   `json:"url"`
		EnvKeys    []string `json:"env_keys"`
		HeaderKeys []string `json:"header_keys"`
		Source     string   `json:"source"`
	} `json:"mcp_servers"`
	MCPConfigError string `json:"mcp_config_error"`
	MCPFiles       []struct {
		Label  string `json:"label"`
		Path   string `json:"path"`
		Exists bool   `json:"exists"`
	} `json:"mcp_files"`
	Channels []struct {
		Channel   string   `json:"channel"`
		Label     string   `json:"label"`
		BotID     string   `json:"bot_id"`
		SecretSet bool     `json:"secret_set"`
		Allow     []string `json:"allow"`
		Ready     bool     `json:"ready"`
		Cursor    bool     `json:"cursor_exists"`
		Note      string   `json:"note"`
	} `json:"channels"`
}

// connectionsGet 取 connections 响应（原始字节 + 解析视图）——
// 原始字节用于"值不泄露"断言。
func connectionsGet(t *testing.T, tsURL string) ([]byte, connectionsJSON) {
	t.Helper()
	resp, err := http.Get(tsURL + "/api/identities/ada/connections")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("connections = %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var out connectionsJSON
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return raw, out
}

// TestConnectionsEmpty 无任何 mcp.json：空清单 + 两个文件未创建 +
// bridge 空态槽。
func TestConnectionsEmpty(t *testing.T) {
	_, _ = newIdentityHome(t)
	ts, _ := newTestServer(t, identity.Home(), "")
	defer ts.Close()

	raw, out := connectionsGet(t, ts.URL)
	if len(out.MCPServers) != 0 || out.MCPConfigError != "" {
		t.Fatalf("空连接面异常: %s", raw)
	}
	if !strings.Contains(string(raw), `"mcp_servers":[]`) {
		t.Fatalf("空数组形态不符: %s", raw)
	}
	if len(out.MCPFiles) != 2 {
		t.Fatalf("mcp_files = %d，应 2", len(out.MCPFiles))
	}
	for _, f := range out.MCPFiles {
		if f.Exists || f.Path == "" || f.Label == "" {
			t.Fatalf("文件状态异常: %+v", f)
		}
	}
	// 渠道桥：未配置时 wecom 槽可见但 Not Ready；凭据绝不回显。
	if len(out.Channels) != 1 || out.Channels[0].Channel != "wecom" {
		t.Fatalf("channels = %+v，应含 wecom 槽", out.Channels)
	}
	if out.Channels[0].Ready || out.Channels[0].SecretSet || out.Channels[0].Note == "" {
		t.Fatalf("wecom 空配置态异常: %+v", out.Channels[0])
	}
	// 空白名单必须序列化为 [] 而不是 null——前端对 null 的 .length
	// 访问会崩进错误边界（实测事故）。
	if !strings.Contains(string(raw), `"allow":[]`) || strings.Contains(string(raw), `"allow":null`) {
		t.Fatalf("空白名单应序列化为 []: %s", raw)
	}
}

// TestChannelsPutRoundTrip 渠道配置写入：PUT 后 .env 落键、视图
// Ready 翻真、secret 永不回显；secret 留空 = 保持既有值；空白名单
// 拒绝（fail-closed 红线）。
func TestChannelsPutRoundTrip(t *testing.T) {
	idDir, _ := newIdentityHome(t)
	ts, _ := newTestServer(t, identity.Home(), "")
	defer ts.Close()

	doPut := func(body string) (int, []byte) {
		req, _ := http.NewRequest(http.MethodPut,
			ts.URL+"/api/identities/ada/channels/wecom", strings.NewReader(body))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, raw
	}

	code, raw := doPut(`{"bot_id":"ww123","secret":"sec-abc","allow":["zhangsan","lisi"]}`)
	if code != 200 {
		t.Fatalf("PUT = %d: %s", code, raw)
	}
	var view struct {
		BotID     string   `json:"bot_id"`
		SecretSet bool     `json:"secret_set"`
		Allow     []string `json:"allow"`
		Ready     bool     `json:"ready"`
	}
	if err := json.Unmarshal(raw, &view); err != nil {
		t.Fatal(err)
	}
	if view.BotID != "ww123" || !view.SecretSet || !view.Ready || len(view.Allow) != 2 {
		t.Fatalf("写入后视图异常: %+v", view)
	}
	if strings.Contains(string(raw), "sec-abc") {
		t.Fatalf("secret 泄漏进响应: %s", raw)
	}
	envBytes, err := os.ReadFile(filepath.Join(idDir, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"WECOM_BOT_ID=ww123", "WECOM_BOT_SECRET=sec-abc", "WECOM_ALLOW=zhangsan,lisi"} {
		if !strings.Contains(string(envBytes), want) {
			t.Fatalf(".env 缺键 %s: %s", want, envBytes)
		}
	}

	// 更新：secret 留空保持既有值，bot_id/allow 替换。
	code, raw = doPut(`{"bot_id":"ww999","secret":"","allow":["zhangsan"]}`)
	if code != 200 {
		t.Fatalf("二次 PUT = %d: %s", code, raw)
	}
	envBytes, _ = os.ReadFile(filepath.Join(idDir, ".env"))
	if !strings.Contains(string(envBytes), "WECOM_BOT_SECRET=sec-abc") {
		t.Fatalf("secret 留空应保持既有值: %s", envBytes)
	}
	if !strings.Contains(string(envBytes), "WECOM_BOT_ID=ww999") {
		t.Fatalf("bot_id 应替换: %s", envBytes)
	}

	// 空白名单拒绝（fail-closed 红线）。
	code, _ = doPut(`{"bot_id":"ww1","allow":[]}`)
	if code != 400 {
		t.Fatalf("空白名单应 400: %d", code)
	}
}

// TestConnectionsMCPServersMergeAndSource 两层合并（身份覆盖全局）、
// 来源标注，以及安全红线：env/headers 的值绝不回显。
func TestConnectionsMCPServersMergeAndSource(t *testing.T) {
	_, id := newIdentityHome(t)
	globalPath := filepath.Join(traj.Home(), "mcp.json")
	identityPath := filepath.Join(id.Dir, "mcp.json")
	globalBody := `{"mcpServers":{
  "github":{"command":"npx","args":["-y","@mcp/server-github"],"env":{"GITHUB_TOKEN":"ghp_SUPERSECRET_VALUE"}},
  "shared":{"url":"https://global.example.com/mcp","headers":{"Authorization":"Bearer HEADER_SECRET_VALUE"}}
}}`
	identityBody := `{"mcpServers":{
  "shared":{"url":"https://identity.example.com/mcp"},
  "local":{"command":"node","args":["local.js"],"env":{"LOCAL_KEY":"local-secret-value"}}
}}`
	if err := os.WriteFile(globalPath, []byte(globalBody), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(identityPath, []byte(identityBody), 0o644); err != nil {
		t.Fatal(err)
	}

	ts, _ := newTestServer(t, identity.Home(), "")
	defer ts.Close()
	raw, out := connectionsGet(t, ts.URL)

	// 安全红线：任何 env/headers 值不得出现在响应里。
	for _, secret := range []string{"ghp_SUPERSECRET_VALUE", "HEADER_SECRET_VALUE", "local-secret-value"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("响应泄露了配置值 %q", secret)
		}
	}

	if len(out.MCPServers) != 3 {
		t.Fatalf("servers = %d，应 3: %+v", len(out.MCPServers), out.MCPServers)
	}
	byName := map[string]int{}
	for i, s := range out.MCPServers {
		byName[s.Name] = i
	}
	gi, ok := byName["github"]
	if !ok {
		t.Fatalf("缺 github: %+v", out.MCPServers)
	}
	if g := out.MCPServers[gi]; g.Transport != "stdio" || g.Source != "global" || g.Command != "npx" ||
		len(g.EnvKeys) != 1 || g.EnvKeys[0] != "GITHUB_TOKEN" {
		t.Fatalf("github: %+v", g)
	}
	si, ok := byName["shared"]
	if !ok {
		t.Fatalf("缺 shared: %+v", out.MCPServers)
	}
	if s := out.MCPServers[si]; s.Source != "identity" || s.Transport != "http" ||
		s.URL != "https://identity.example.com/mcp" || len(s.HeaderKeys) != 0 {
		t.Fatalf("shared 应被身份层整体覆盖: %+v", s)
	}
	li, ok := byName["local"]
	if !ok {
		t.Fatalf("缺 local: %+v", out.MCPServers)
	}
	if l := out.MCPServers[li]; l.Source != "identity" || l.Transport != "stdio" ||
		len(l.EnvKeys) != 1 || l.EnvKeys[0] != "LOCAL_KEY" {
		t.Fatalf("local: %+v", l)
	}
	for _, f := range out.MCPFiles {
		if !f.Exists {
			t.Fatalf("文件应存在: %+v", f)
		}
	}
}

// TestConnectionsConfigError 某层损坏：错误字段非空、另一层仍
// 可见（错误横幅说明当前不生效）。
func TestConnectionsConfigError(t *testing.T) {
	_, id := newIdentityHome(t)
	globalPath := filepath.Join(traj.Home(), "mcp.json")
	identityPath := filepath.Join(id.Dir, "mcp.json")
	if err := os.WriteFile(globalPath, []byte(`{"mcpServers":{"github":{"command":"npx"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(identityPath, []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	ts, _ := newTestServer(t, identity.Home(), "")
	defer ts.Close()
	_, out := connectionsGet(t, ts.URL)

	if out.MCPConfigError == "" || !strings.Contains(out.MCPConfigError, "mcp.json") {
		t.Fatalf("config_error: %q", out.MCPConfigError)
	}
	if len(out.MCPServers) != 1 || out.MCPServers[0].Name != "github" {
		t.Fatalf("坏配置下应仍可见 global 服务器: %+v", out.MCPServers)
	}
	for _, f := range out.MCPFiles {
		if !f.Exists {
			t.Fatalf("文件均存在: %+v", f)
		}
	}
}
