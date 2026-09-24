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

// TestHandleIdentitiesEmpty：根目录不存在时返回 identities: [] 而不是 500。
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
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if list, ok := got["identities"].([]any); !ok || len(list) != 0 {
		t.Fatalf("identities 应为空数组，得到 %#v", got["identities"])
	}
}

// TestHandleIdentitiesWithOne：真实创建一个身份后能列出。
func TestHandleIdentitiesWithOne(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	// identity.Create writes under ${MINDLOOP_HOME}/identities/<name>/; scan wants the parent.
	identityHome := filepath.Join(dir, "identities")
	if err := os.MkdirAll(identityHome, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
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
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	list := got["identities"].([]any)
	if len(list) != 1 {
		t.Fatalf("期望 1 个身份，得到 %d", len(list))
	}
	first := list[0].(map[string]any)
	if first["name"] != "ada" {
		t.Fatalf("name = %v", first["name"])
	}
	if _, ok := first["dir"].(string); !ok {
		t.Fatal("dir 字段缺失")
	}
	if rt, _ := first["root_trajectory"].(string); rt == "" {
		t.Fatal("root_trajectory 缺失")
	}
	if rts, _ := first["routes"].([]any); len(rts) != 6 {
		t.Fatalf("routes 数量 = %d，应为 6（/i/ada 等 6 端点）", len(rts))
	}
}

// TestHandleMindlogTail：往轨迹追加步骤后 mindlog?tail=10 能返回。
func TestHandleMindlogTail(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	// identity.Create writes under ${MINDLOOP_HOME}/identities/<name>/; scan wants the parent.
	id, err := identity.Create(context.Background(), "ada")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	for _, msg := range []string{"hello", "world", "again"} {
		addMessage(t, id, "user", "ada", msg)
	}

	ts, _ := newTestServer(t, identity.Home(), "")
	resp, err := http.Get(ts.URL + "/api/identities/ada/mindlog?tail=2")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	// viewer Mindlog 契约：identity 是 {id,name} 对象；steps 是
	// NormalizedStep（含 preview/raw/source）；runs 是 RunGroup 数组。
	idObj := got["identity"].(map[string]any)
	if idObj["id"] != "ada" {
		t.Fatalf("identity.id = %v", idObj["id"])
	}
	if _, ok := got["runs"].([]any); !ok {
		t.Fatal("runs 字段缺失")
	}
	if _, ok := got["live"].(bool); !ok {
		t.Fatal("live 字段缺失")
	}
	steps := got["steps"].([]any)
	if len(steps) != 2 {
		t.Fatalf("tail=2 应返回 2 步，得到 %d", len(steps))
	}
	last := steps[1].(map[string]any)
	if last["preview"] != "again" {
		t.Fatalf("末步 preview = %v，应为 again", last["preview"])
	}
	if _, ok := last["step_id"]; !ok {
		t.Fatal("step_id 字段缺失")
	}
	if _, ok := last["raw"].(map[string]any); !ok {
		t.Fatal("raw 字段缺失")
	}
}

// TestHandleChatFiltersToMessageOnly：mindlog 含 message + run，
// chat 只返回 message 类型。
func TestHandleChatFiltersToMessageOnly(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	// identity.Create writes under ${MINDLOOP_HOME}/identities/<name>/; scan wants the parent.
	id, err := identity.Create(context.Background(), "ada")
	if err != nil {
		t.Fatal(err)
	}
	addMessage(t, id, "user", "ada", "hi")
	runStep := traj.NewStep("run")
	id.Timeline.Append(context.Background(), runStep)

	ts, _ := newTestServer(t, identity.Home(), "")
	resp, err := http.Get(ts.URL + "/api/identities/ada/chat")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got map[string]any
	json.NewDecoder(resp.Body).Decode(&got)
	msgs := got["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("chat 应只含 1 条 message，得到 %d", len(msgs))
	}
}

// TestHandleHealthShape：/api/health 返回 version/identities/live_minds。
func TestHandleHealthShape(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	// identity.Create writes under ${MINDLOOP_HOME}/identities/<name>/; scan wants the parent.
	identity.Create(context.Background(), "ada")

	ts, _ := newTestServer(t, identity.Home(), "")
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

// TestIdentityNameSafety：身份名含 .. 或 / 时返回 403-style 400。
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
	ts, _ := newTestServer(t, dir, "")
	defer ts.Close()
	resp, _ := http.Get(ts.URL + "/api/identities/nobody/mindlog")
	if resp.StatusCode != 404 {
		t.Fatalf("status = %d，应为 404", resp.StatusCode)
	}
}

// TestHandleMindlogSearch：搜索框契约——q 命中时返回 index/step_id/
// snippet 的 SearchHit 列表。
func TestHandleMindlogSearch(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	home := identity.Home()
	id, err := identity.Create(context.Background(), "ada")
	if err != nil {
		t.Fatal(err)
	}
	addMessage(t, id, "user", "ada", "关于部署流水线的特殊关键词 zebra-chat")
	addMessage(t, id, "user", "ada", "无关消息")

	ts, _ := newTestServer(t, home, "")
	resp, err := http.Get(ts.URL + "/api/identities/ada/mindlog/search?q=zebra-chat")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got struct {
		Q     string `json:"q"`
		Scope string `json:"scope"`
		Hits  []struct {
			StepID  string `json:"step_id"`
			Snippet string `json:"snippet"`
		} `json:"hits"`
	}
	json.NewDecoder(resp.Body).Decode(&got)
	if len(got.Hits) != 1 {
		t.Fatalf("hits = %d，应为 1", len(got.Hits))
	}
	if !strings.Contains(got.Hits[0].Snippet, "zebra-chat") {
		t.Fatalf("snippet 缺关键词: %q", got.Hits[0].Snippet)
	}
}

// TestHandleMindlogSinceWindow：since/until 窗口语义（轮询增量的
// 数据源）。3 步日志，since=1 应返回第 2、3 步。
func TestHandleMindlogSinceWindow(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	home := identity.Home()
	id, err := identity.Create(context.Background(), "ada")
	if err != nil {
		t.Fatal(err)
	}
	for _, msg := range []string{"one", "two", "three"} {
		addMessage(t, id, "user", "ada", msg)
	}
	ts, _ := newTestServer(t, home, "")
	resp, err := http.Get(ts.URL + "/api/identities/ada/mindlog?since=1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got struct {
		Steps []struct {
			Preview string `json:"preview"`
		} `json:"steps"`
		Since *int `json:"since"`
	}
	json.NewDecoder(resp.Body).Decode(&got)
	if len(got.Steps) != 3 {
		t.Fatalf("since=1 应返回 3 步，得到 %d", len(got.Steps))
	}
	if got.Since == nil || *got.Since != 1 {
		t.Fatalf("since 字段 = %v，应为 1", got.Since)
	}
	if !strings.Contains(got.Steps[0].Preview, "one") {
		t.Fatalf("首步应为 one: %q", got.Steps[0].Preview)
	}
}

// TestHandleRunCommand：run 分组给出 prompt 全文。
func TestHandleRunCommand(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	home := identity.Home()
	id, err := identity.Create(context.Background(), "ada")
	if err != nil {
		t.Fatal(err)
	}
	runStep := traj.NewStep("run")
	runStep.Fields["run_id"] = "rid-1"
	promptStep := traj.NewStep("prompt")
	promptStep.Fields["run_id"] = "rid-1"
	promptStep.Fields["content"] = "这条 prompt 是 run 的命令全文"
	finalStep := traj.NewStep("final")
	finalStep.Fields["run_id"] = "rid-1"
	finalStep.Fields["content"] = "done"
	for _, s := range []traj.Step{runStep, promptStep, finalStep} {
		if err := id.Timeline.Append(context.Background(), s); err != nil {
			t.Fatal(err)
		}
	}
	ts, _ := newTestServer(t, home, "")
	resp, err := http.Get(ts.URL + "/api/identities/ada/runs/rid-1/command")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got struct {
		Command string `json:"command"`
	}
	json.NewDecoder(resp.Body).Decode(&got)
	if !strings.Contains(got.Command, "prompt 是 run 的命令全文") {
		t.Fatalf("command = %q", got.Command)
	}
}

// TestHandleForkWritebackLinks：fork/merge 步骤产出链接字段。
func TestHandleForkWritebackLinks(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	home := identity.Home()
	id, err := identity.Create(context.Background(), "ada")
	if err != nil {
		t.Fatal(err)
	}
	fork := traj.NewStep("fork")
	fork.Fields["child"] = "child-uuid"
	fork.Fields["child_ref"] = "../abcd1234-sub/trajectory.jsonl"
	merge := traj.NewStep("merge")
	merge.Fields["from_traj"] = "child-uuid"
	merge.Fields["from_step"] = "step-9"
	for _, s := range []traj.Step{fork, merge} {
		if err := id.Timeline.Append(context.Background(), s); err != nil {
			t.Fatal(err)
		}
	}
	ts, _ := newTestServer(t, home, "")
	resp, err := http.Get(ts.URL + "/api/identities/ada/mindlog")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got struct {
		Steps []struct {
			Type string `json:"type"`
			Fork *struct {
				ChildTrajID string `json:"child_traj_id"`
			} `json:"fork"`
			Writeback *struct {
				FromTraj string `json:"from_traj"`
			} `json:"writeback"`
		} `json:"steps"`
	}
	json.NewDecoder(resp.Body).Decode(&got)
	var sawFork, sawWB bool
	for _, s := range got.Steps {
		if s.Type == "fork" && s.Fork != nil && s.Fork.ChildTrajID == "child-uuid" {
			sawFork = true
		}
		if s.Type == "merge" && s.Writeback != nil && s.Writeback.FromTraj == "child-uuid" {
			sawWB = true
		}
	}
	if !sawFork || !sawWB {
		t.Fatalf("fork/writeback 链接缺失: fork=%v wb=%v", sawFork, sawWB)
	}
}
