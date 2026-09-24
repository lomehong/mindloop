package web

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mindloop/internal/identity"
	"mindloop/internal/traj"
)

// makeLive 在身份目录伪造一个属主活着的运行锁——属主 pid 写测试
// 进程自己（必然存活），isIdentityLive 据此判定 live。测完清理。
func makeLive(t *testing.T, id *identity.Identity) {
	t.Helper()
	lockDir := filepath.Join(id.Timeline.Dir, "run", "dispatcher.lock")
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		t.Fatal(err)
	}
	owner := fmt.Sprintf(`{"pid":%d,"created":%q}`, os.Getpid(), traj.NowString())
	if err := os.WriteFile(filepath.Join(lockDir, "owner.json"), []byte(owner), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(lockDir) })
}

// postJSON 是写端点的测试辅助：POST JSON 体并解码响应。
func postJSON(t *testing.T, ts *httptest.Server, path string, body any) (*http.Response, map[string]any) {
	t.Helper()
	data, _ := json.Marshal(body)
	resp, err := http.Post(ts.URL+path, "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp, out
}

// TestIdentityCreateEndpoint：POST /api/identities 真建身份并进列表；
// 非法名 400。此前 POST 落进 GET 分支只返回数组，身份从未创建。
func TestIdentityCreateEndpoint(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	ts, _ := newTestServer(t, identity.Home(), "")

	resp, out := postJSON(t, ts, "/api/identities", map[string]string{"name": "bob"})
	if resp.StatusCode != 200 || out["id"] != "bob" {
		t.Fatalf("create = %d %v", resp.StatusCode, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "identities", "bob", "identity.txt")); err != nil {
		t.Fatalf("身份未落盘: %v", err)
	}
	resp, _ = postJSON(t, ts, "/api/identities", map[string]string{"name": "../evil"})
	if resp.StatusCode != 400 {
		t.Fatalf("非法名应 400，得到 %d", resp.StatusCode)
	}
}

// TestChatSendAndChatLog：POST chat 落盘消息 + GET chat 返回完整
// ChatLog 契约（identity 对象/live/outcomes/tail/with 过滤）。
func TestChatSendAndChatLog(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	home := identity.Home()
	id, err := identity.Create(context.Background(), "ada")
	if err != nil {
		t.Fatal(err)
	}

	ts, _ := newTestServer(t, home, "")
	resp, out := postJSON(t, ts, "/api/identities/ada/chat",
		map[string]string{"content": "你好", "from_name": "you"})
	if resp.StatusCode != 200 || out["from"] != "you" || out["to"] != "ada" || out["ok"] != true {
		t.Fatalf("send = %d %v", resp.StatusCode, out)
	}

	// responder 式回复：盖 reply_to 章指向刚 POST 落盘的那条消息。
	steps, _ := id.Timeline.Steps()
	sent := steps[len(steps)-1]
	reply := traj.NewStep("message")
	reply.Fields["from"] = "ada"
	reply.Fields["to"] = "you"
	reply.Fields["content"] = "你好呀"
	reply.Fields["reply_to"] = sent.StepID
	id.Timeline.Append(context.Background(), reply)

	get, err := http.Get(ts.URL + "/api/identities/ada/chat?with=you")
	if err != nil {
		t.Fatal(err)
	}
	defer get.Body.Close()
	var log struct {
		Identity struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"identity"`
		Live     bool              `json:"live"`
		Messages []map[string]any  `json:"messages"`
		Outcomes map[string]string `json:"outcomes"`
	}
	if err := json.NewDecoder(get.Body).Decode(&log); err != nil {
		t.Fatal(err)
	}
	if log.Identity.ID != "ada" {
		t.Fatalf("identity 契约应为对象 {id,name}，得到 %v", log.Identity)
	}
	if len(log.Messages) != 2 || log.Messages[0]["content"] != "你好" {
		t.Fatalf("with=you 应过滤出往返两条，得到 %v", log.Messages)
	}
	sentID := log.Messages[0]["step_id"].(string)
	if log.Outcomes[sentID] != "replied" {
		t.Fatalf("已回复的入站消息 outcome 应为 replied，得到 %v", log.Outcomes)
	}
}

// TestThinkersControlContract：控制面端点返回 viewer 期望的字段
// （ControlResult 的 names / toggle 的 name+disabled）。此前前端读
// result.names 直接 TypeError、停用提示"已启用"。
func TestThinkersControlContract(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	home := identity.Home()
	id, err := identity.Create(context.Background(), "ada")
	if err != nil {
		t.Fatal(err)
	}
	makeLive(t, id)
	ts, _ := newTestServer(t, home, "")

	resp, out := postJSON(t, ts, "/api/identities/ada/thinkers/monolith/disable", map[string]any{})
	if resp.StatusCode != 200 || out["name"] != "monolith" || out["disabled"] != true || out["ok"] != true {
		t.Fatalf("disable 契约错位: %d %v", resp.StatusCode, out)
	}
	resp, out = postJSON(t, ts, "/api/identities/ada/thinkers/monolith/enable", map[string]any{})
	if resp.StatusCode != 200 || out["disabled"] != false {
		t.Fatalf("enable 契约错位: %d %v", resp.StatusCode, out)
	}
	resp, out = postJSON(t, ts, "/api/identities/ada/thinkers/responder/step", map[string]any{})
	if resp.StatusCode != 200 {
		t.Fatalf("step = %d", resp.StatusCode)
	}
	names, ok := out["names"].([]any)
	if !ok || len(names) != 1 || names[0] != "responder" {
		t.Fatalf("step 应返回 ControlResult.names，得到 %v", out)
	}
	// wake 信号确实写了。
	if _, err := os.Stat(filepath.Join(id.Timeline.Dir, "run", "wake.responder")); err != nil {
		t.Fatalf("wake 信号未写入: %v", err)
	}
}

// TestThinkersStartRejectsGET：写端点收到 GET 必须 405——此前 GET
// 即拉起 mind run 子进程，<img src> 就能驱动。
func TestThinkersStartRejectsGET(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	home := identity.Home()
	identity.Create(context.Background(), "ada")
	ts, _ := newTestServer(t, home, "")

	resp, err := http.Get(ts.URL + "/api/identities/ada/thinkers/start")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET 写端点应 405，得到 %d", resp.StatusCode)
	}
}

// TestKillallDryRun：dry_run=true 只报告不落任何停机标志；
// dry_run=false 才真停（KillallResult 契约 stdout/stderr）。
func TestKillallDryRun(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	home := identity.Home()
	id, err := identity.Create(context.Background(), "ada")
	if err != nil {
		t.Fatal(err)
	}
	makeLive(t, id)
	ts, _ := newTestServer(t, home, "")

	resp, out := postJSON(t, ts, "/api/killall", map[string]bool{"dry_run": true})
	if resp.StatusCode != 200 || out["dry_run"] != true {
		t.Fatalf("dry_run 应原样返回: %d %v", resp.StatusCode, out)
	}
	if s, _ := out["stdout"].(string); !strings.Contains(s, "ada") {
		t.Fatalf("dry_run 摘要应列出将停的心智，得到 %q", out["stdout"])
	}
	if _, err := os.Stat(filepath.Join(id.Dir, "run", "stop")); !os.IsNotExist(err) {
		t.Fatal("dry_run 绝不能写停机标志")
	}

	resp, out = postJSON(t, ts, "/api/killall", map[string]bool{"dry_run": false})
	if resp.StatusCode != 200 || out["dry_run"] != false {
		t.Fatalf("真停: %d %v", resp.StatusCode, out)
	}
	if _, err := os.Stat(filepath.Join(id.Dir, "run", "stop")); err != nil {
		t.Fatalf("停机标志未写入: %v", err)
	}
}

// TestRecapRefreshRouteReachable：POST recap/refresh 必须到达
// handleRecapRefresh（模型未配置 → 503）——此前 case "recap/refresh"
// 永不可达，POST 静默落入 GET recap 返回 200 视图。
func TestRecapRefreshRouteReachable(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	home := identity.Home()
	identity.Create(context.Background(), "ada")
	ts, _ := newTestServer(t, home, "")

	resp, out := postJSON(t, ts, "/api/identities/ada/recap/refresh", map[string]any{})
	if resp.StatusCode != 503 {
		t.Fatalf("未配置模型时重算应 503（证明路由可达），得到 %d %v", resp.StatusCode, out)
	}
}

// TestExportJobsLifecycle：建任务 → 轮询到 done → 下载 → 删除 → 404。
func TestExportJobsLifecycle(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	home := identity.Home()
	id, err := identity.Create(context.Background(), "ada")
	if err != nil {
		t.Fatal(err)
	}
	addMessage(t, id, "you", "ada", "hello")
	ts, _ := newTestServer(t, home, "")

	resp, out := postJSON(t, ts, "/api/identities/ada/export-jobs",
		map[string]bool{"soul_only": true, "slim": true})
	if resp.StatusCode != 200 {
		t.Fatalf("create job = %d", resp.StatusCode)
	}
	jobID, _ := out["job_id"].(string)
	if jobID == "" {
		t.Fatalf("缺 job_id: %v", out)
	}
	// 轮询直到 done（小归档应为毫秒级；3 秒兜底）。
	var status string
	for i := 0; i < 60; i++ {
		g, err := http.Get(ts.URL + "/api/export-jobs/" + jobID)
		if err != nil {
			t.Fatal(err)
		}
		var job map[string]any
		json.NewDecoder(g.Body).Decode(&job)
		g.Body.Close()
		status, _ = job["status"].(string)
		if status == "done" || status == "failed" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if status != "done" {
		t.Fatalf("任务应 done，得到 %s", status)
	}
	dl, err := http.Get(ts.URL + "/api/export-jobs/" + jobID + "/download")
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	buf.ReadFrom(dl.Body)
	dl.Body.Close()
	if dl.StatusCode != 200 || buf.Len() < 20 {
		t.Fatalf("下载失败: %d %d 字节", dl.StatusCode, buf.Len())
	}
	gr, err := gzip.NewReader(&buf)
	if err != nil {
		t.Fatalf("不是有效 gzip: %v", err)
	}
	tr := tar.NewReader(gr)
	found := false
	for {
		hdr, err := tr.Next()
		if err != nil {
			break
		}
		if strings.HasSuffix(hdr.Name, "persona.md") {
			found = true
		}
		if strings.Contains(hdr.Name, "trajectory") {
			t.Fatalf("soul_only 不应含轨迹: %s", hdr.Name)
		}
	}
	if !found {
		t.Fatal("soul_only 归档缺少 persona.md")
	}

	del, _ := http.NewRequest("DELETE", ts.URL+"/api/export-jobs/"+jobID, nil)
	dresp, err := http.DefaultClient.Do(del)
	if err != nil {
		t.Fatal(err)
	}
	dresp.Body.Close()
	if dresp.StatusCode != 200 {
		t.Fatalf("delete = %d", dresp.StatusCode)
	}
	g2, _ := http.Get(ts.URL + "/api/export-jobs/" + jobID)
	g2.Body.Close()
	if g2.StatusCode != 404 {
		t.Fatalf("删除后应 404，得到 %d", g2.StatusCode)
	}
}

// TestImportRejectsForeignPaths：归档条目不在 identities/ 下时拒绝
// 落盘——此前任何两段路径都会在身份根下创建任意目录。
func TestImportRejectsForeignPaths(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	ts, _ := newTestServer(t, identity.Home(), "")

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	content := []byte("payload")
	hdr := &tar.Header{Name: "evil/payload.txt", Mode: 0o644, Size: int64(len(content))}
	tw.WriteHeader(hdr)
	tw.Write(content)
	tw.Close()
	gz.Close()

	resp, err := http.Post(ts.URL+"/api/identities/import", "application/gzip", &buf)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("外来路径应 400，得到 %d", resp.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(identity.Home(), "evil")); !os.IsNotExist(err) {
		t.Fatal("外来目录绝不能落盘")
	}
}

// TestEnvValueNewlineRejected：环境变量值含换行必须拒绝——dotenv
// 按行解析，换行即变量注入。
func TestEnvValueNewlineRejected(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	home := identity.Home()
	identity.Create(context.Background(), "ada")
	ts, _ := newTestServer(t, home, "")

	req, _ := http.NewRequest("PUT", ts.URL+"/api/identities/ada/env",
		strings.NewReader(`{"key":"A","value":"x\nMINDLOOP_BASE_URL=http://evil"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("含换行的值应 400，得到 %d", resp.StatusCode)
	}
}

// TestSameOriginGuard：跨源 Origin 与回环部署下的非回环 Host 都必须
// 被 403（CSRF / DNS rebinding 防线）。
func TestSameOriginGuard(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	ts, _ := newTestServer(t, identity.Home(), "")

	// 跨源 Origin（恶意网页 <img>/fetch 的形态）。
	req, _ := http.NewRequest("GET", ts.URL+"/api/identities", nil)
	req.Header.Set("Origin", "https://evil.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("跨源 Origin 应 403，得到 %d", resp.StatusCode)
	}

	// DNS rebinding：Host 是攻击者域名（无 Origin 的顶层导航形态）。
	req2, _ := http.NewRequest("GET", ts.URL+"/api/identities", nil)
	req2.Host = "evil.example"
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != 403 {
		t.Fatalf("非回环 Host 应 403，得到 %d", resp2.StatusCode)
	}

	// 同源请求不受影响。
	resp3, err := http.Get(ts.URL + "/api/identities")
	if err != nil {
		t.Fatal(err)
	}
	resp3.Body.Close()
	if resp3.StatusCode != 200 {
		t.Fatalf("同源应 200，得到 %d", resp3.StatusCode)
	}
}

// TestNonLoopbackBindRequiresToken：绑 0.0.0.0 而无 Token 必须拒绝
// 启动——无鉴权写端点暴露到局域网等价于交出机器。
func TestNonLoopbackBindRequiresToken(t *testing.T) {
	_, err := New(Config{Root: t.TempDir(), Addr: "0.0.0.0:8080", Token: ""})
	if err == nil {
		t.Fatal("非回环绑定无 Token 应拒绝启动")
	}
	if _, err := New(Config{Root: t.TempDir(), Addr: "0.0.0.0:8080", Token: "s"}); err != nil {
		t.Fatalf("带 Token 的非回环绑定应可用: %v", err)
	}
}
