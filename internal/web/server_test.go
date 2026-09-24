package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mindloop/internal/identity"
	"mindloop/internal/traj"
)

// newTestServer 搭一个最简服务端用于测试——用 httptest.Server 启动
// 真实 *Server 而不是 New+ListenAndServe，便于 goroutine 控制。
func newTestServer(t *testing.T, root, viewerDir string) (*httptest.Server, *Server) {
	t.Helper()
	s, err := New(Config{Root: root, ViewerDir: viewerDir, Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	return ts, s
}

// addMessage 是测试用的 message 步骤构造器。
func addMessage(t *testing.T, id *identity.Identity, from, to, content string) {
	t.Helper()
	s := traj.NewStep("message")
	s.Fields["from"] = from
	s.Fields["to"] = to
	s.Fields["content"] = content
	if err := id.Timeline.Append(context.Background(), s); err != nil {
		t.Fatalf("Append: %v", err)
	}
}

// TestHandleIdentitiesEmpty：根目录不存在时返回空数组而不是 500。
func TestHandleIdentitiesEmpty(t *testing.T) {
	dir := t.TempDir()
	ts, _ := newTestServer(t, dir, "")

	resp, err := http.Get(ts.URL + "/api/identities")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var list []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("identities 应为空数组，得到 %d 条", len(list))
	}
}

// TestHandleIdentitiesWithOne：真实创建一个身份后能列出，且字段满足
// viewer Identity 契约（home.tsx 直接读 dispatcher/group 渲染表格）。
func TestHandleIdentitiesWithOne(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	if _, err := identity.Create(context.Background(), "ada"); err != nil {
		t.Fatalf("identity.Create: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "identities", "ada", "identity.txt")); err != nil {
		t.Fatalf("identity.txt 未创建：%v", err)
	}
	ts, _ := newTestServer(t, identity.Home(), "")

	resp, err := http.Get(ts.URL + "/api/identities")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var list []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("期望 1 个身份，得到 %d", len(list))
	}
	first := list[0]
	if first["name"] != "ada" {
		t.Fatalf("name = %v", first["name"])
	}
	if first["group"] != "local" {
		t.Fatalf("group = %v（home.tsx 按 group 分组渲染）", first["group"])
	}
	disp, ok := first["dispatcher"].(map[string]any)
	if !ok {
		t.Fatal("dispatcher 字段缺失")
	}
	if _, ok := disp["running"].(bool); !ok {
		t.Fatal("dispatcher.running 缺失")
	}
	if _, ok := first["root_trajectory"].(string); !ok {
		t.Fatal("root_trajectory 缺失")
	}
	if sc, ok := first["step_count"].(float64); !ok || sc < 1 {
		t.Fatalf("step_count = %v，应至少为 1（头行）", first["step_count"])
	}
}

// TestHandleMindlogTail：往轨迹追加步骤后 mindlog?tail=10 能返回，
// 且 steps 为 NormalizedStep 契约（preview/raw/source）。
func TestHandleMindlogTail(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	home := identity.Home()
	id, err := identity.Create(context.Background(), "ada")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	for _, msg := range []string{"hello", "world", "again"} {
		addMessage(t, id, "user", "ada", msg)
	}

	ts, _ := newTestServer(t, home, "")
	resp, err := http.Get(ts.URL + "/api/identities/ada/mindlog?tail=2")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var got struct {
		TrajID string `json:"traj_id"`
		Steps  []struct {
			StepID  string         `json:"step_id"`
			Type    string         `json:"type"`
			Preview string         `json:"preview"`
			Raw     map[string]any `json:"raw"`
		} `json:"steps"`
		Live     bool `json:"live"`
		Identity struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"identity"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Identity.Name != "ada" {
		t.Fatalf("identity.name = %v", got.Identity.Name)
	}
	if len(got.Steps) != 2 {
		t.Fatalf("tail=2 应返回 2 步，得到 %d", len(got.Steps))
	}
	last := got.Steps[1]
	if last.Preview != "again" {
		t.Fatalf("末步 preview = %q，应为 again", last.Preview)
	}
	if _, ok := last.Raw["content"]; !ok {
		t.Fatal("raw 字段缺失")
	}
}

// TestHandleChatFiltersToMessageOnly：mindlog 含 message + run，
// chat 只返回 message 类型。
func TestHandleChatFiltersToMessageOnly(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	home := identity.Home()
	id, err := identity.Create(context.Background(), "ada")
	if err != nil {
		t.Fatal(err)
	}
	addMessage(t, id, "user", "ada", "hi")
	runStep := traj.NewStep("run")
	id.Timeline.Append(context.Background(), runStep)

	ts, _ := newTestServer(t, home, "")
	resp, err := http.Get(ts.URL + "/api/identities/ada/chat")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got struct {
		Messages []map[string]any `json:"messages"`
	}
	json.NewDecoder(resp.Body).Decode(&got)
	if len(got.Messages) != 1 {
		t.Fatalf("chat 应只含 1 条 message，得到 %d", len(got.Messages))
	}
}

// TestHandleHealthShape：/api/health 返回 version/identities/live_minds。
func TestHandleHealthShape(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	home := identity.Home()
	identity.Create(context.Background(), "ada")

	ts, _ := newTestServer(t, home, "")
	resp, err := http.Get(ts.URL + "/api/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got map[string]any
	json.NewDecoder(resp.Body).Decode(&got)
	for _, k := range []string{"version", "identities", "live_minds", "checked_at"} {
		if _, ok := got[k]; !ok {
			t.Fatalf("health 缺少字段 %q：%v", k, got)
		}
	}
	if got["identities"].(float64) != 1 {
		t.Fatalf("identities = %v", got["identities"])
	}
}

// TestAuthTokenRequired：当 Token 非空时，无 Authorization 头部返回 401。
func TestAuthTokenRequired(t *testing.T) {
	dir := t.TempDir()
	s, err := New(Config{Root: dir, ViewerDir: "", Addr: "127.0.0.1:0", Token: "secret"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts2 := httptest.NewServer(s)
	defer ts2.Close()

	resp, _ := http.Get(ts2.URL + "/api/identities")
	if resp.StatusCode != 401 {
		t.Fatalf("无 token 期望 401，得到 %d", resp.StatusCode)
	}
	resp.Body.Close()

	req, _ := http.NewRequest("GET", ts2.URL+"/api/identities", nil)
	req.Header.Set("Authorization", "Bearer secret")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp2.StatusCode != 200 {
		t.Fatalf("正确 token 期望 200，得到 %d", resp2.StatusCode)
	}
	resp2.Body.Close()
}

// TestIdentityNameSafety：身份名含路径穿越时不应返回 200。
// （mux 会先做路径清洗，/api/identities/../etc 在到达 handler 前
// 就被规范化为 /api/etc——无论 404 还是 400，都不是 200。）
func TestIdentityNameSafety(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	ts, _ := newTestServer(t, dir, "")
	defer ts.Close()
	for _, name := range []string{"../etc", "foo/bar", "a..b", "x y", "Σ"} {
		resp, err := http.Get(ts.URL + "/api/identities/" + name + "/mindlog")
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode == 200 {
			t.Fatalf("不安全名 %q 不应返回 200，得到 %d", name, resp.StatusCode)
		}
		resp.Body.Close()
	}
}

// TestStaticAssetServing：当 ViewerDir 给定且 index.html 存在，GET /
// 应返回该文件内容（SPA catch-all）。
func TestStaticAssetServing(t *testing.T) {
	dir := t.TempDir()
	viewer := t.TempDir()
	indexPath := filepath.Join(viewer, "index.html")
	if err := os.WriteFile(indexPath, []byte("<html>SPA</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	assetsDir := filepath.Join(viewer, "assets")
	os.MkdirAll(assetsDir, 0o755)
	os.WriteFile(filepath.Join(assetsDir, "main.js"), []byte("JS"), 0o644)

	ts, _ := newTestServer(t, dir, viewer)
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "SPA") {
		t.Fatalf("catch-all 未服务 index.html，得到 %q", string(body))
	}
	resp2, err := http.Get(ts.URL + "/assets/main.js")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != 200 {
		t.Fatalf("assets/* status = %d", resp2.StatusCode)
	}
}

// TestUnknownIdentityReturned404（确保身份不存在时不静默返回空数组）。
func TestUnknownIdentityReturned404(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	ts, _ := newTestServer(t, dir, "")
	defer ts.Close()
	resp, _ := http.Get(ts.URL + "/api/identities/nobody/mindlog")
	if resp.StatusCode != 404 {
		t.Fatalf("status = %d，应为 404", resp.StatusCode)
	}
}
