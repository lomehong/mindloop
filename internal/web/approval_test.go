package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lomehong/mindloop/internal/identity"
	"github.com/lomehong/mindloop/internal/policy"
)

// seedApproval 在身份的审批控制面按落盘格式写入一条待批请求，返回
// 完整哈希——与 policy.Gate 的写盘同构（文件名与内嵌哈希一致是
// 控制面的硬校验）。
func seedApproval(t *testing.T, id *identity.Identity, script string) string {
	return seedApprovalExpires(t, id, script, 10*time.Minute)
}

// seedApprovalExpired 写一条已过有效期的请求（默认种子 10 分钟有效期，
// 这里回拨到 10 分钟前）——死运行留下的孤儿形态。
func seedApprovalExpired(t *testing.T, id *identity.Identity, script string) string {
	return seedApprovalExpires(t, id, script, -10*time.Minute)
}

func seedApprovalExpires(t *testing.T, id *identity.Identity, script string, ttl time.Duration) string {
	t.Helper()
	dir := policy.Dir(id.Timeline.Dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	hash := policy.ScriptHash(script)
	p := policy.PendingRequest{
		Hash: hash, Script: script, WorkDir: `D:\work\y`,
		RunID: "run-7", TaskID: "task-2", Attempt: 1,
		Created: time.Now().UTC().Add(-ttl), Expires: time.Now().Add(ttl).UTC(),
		Risks: policy.RiskNotes(script),
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "request-"+hash+".json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return hash
}

// approvalDecision 读控制面里的决定文件（不存在返回空串）。
func approvalDecision(t *testing.T, id *identity.Identity, hash string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(policy.Dir(id.Timeline.Dir), "decision-"+hash+".json"))
	if err != nil {
		return ""
	}
	var d struct {
		Decision string `json:"decision"`
	}
	if json.Unmarshal(data, &d) != nil {
		return ""
	}
	return d.Decision
}

// TestApprovalsListEndpoint：GET 列出待批脚本——正文、工作目录、
// 归属与风险（web 面与 CLI approve 列表同一份事实）。
func TestApprovalsListEndpoint(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	id, err := identity.Create(context.Background(), "ada")
	if err != nil {
		t.Fatal(err)
	}
	hash := seedApproval(t, id, "rm -rf build/\necho done")
	ts, _ := newTestServer(t, identity.Home(), "")

	resp, err := http.Get(ts.URL + "/api/identities/ada/approvals")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var list []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatalf("响应应为数组: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("待批应为 1 条，得到 %d", len(list))
	}
	first := list[0]
	if first["hash"] != hash {
		t.Fatalf("hash = %v", first["hash"])
	}
	if s, _ := first["script"].(string); !strings.Contains(s, "rm -rf build/") {
		t.Fatalf("script 应含完整正文: %v", first["script"])
	}
	if first["work_dir"] != `D:\work\y` {
		t.Fatalf("work_dir = %v", first["work_dir"])
	}
	if first["run_id"] != "run-7" {
		t.Fatalf("run_id = %v", first["run_id"])
	}
	if risks, _ := first["risks"].([]any); len(risks) == 0 {
		t.Fatal("rm -rf 应带风险提示")
	}
}

// TestApprovalsDecideEndpoint：POST approve / deny 写决定文件——等待方
// （policy.Gate）消费后脚本才继续或终止。
func TestApprovalsDecideEndpoint(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	id, err := identity.Create(context.Background(), "ada")
	if err != nil {
		t.Fatal(err)
	}
	h1 := seedApproval(t, id, "echo one")
	h2 := seedApproval(t, id, "echo two")
	ts, _ := newTestServer(t, identity.Home(), "")

	resp, out := postJSON(t, ts, "/api/identities/ada/approvals/"+h1[:8]+"/approve", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("approve status = %d", resp.StatusCode)
	}
	if out["decision"] != "approve" || out["hash"] != h1 {
		t.Fatalf("approve 响应 = %v", out)
	}
	if got := approvalDecision(t, id, h1); got != "approve" {
		t.Fatalf("决定 = %q，应为 approve", got)
	}

	resp, out = postJSON(t, ts, "/api/identities/ada/approvals/"+h2[:8]+"/deny", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("deny status = %d", resp.StatusCode)
	}
	if out["decision"] != "deny" || out["hash"] != h2 {
		t.Fatalf("deny 响应 = %v", out)
	}
	if got := approvalDecision(t, id, h2); got != "deny" {
		t.Fatalf("决定 = %q，应为 deny", got)
	}
}

// TestApprovalsListFiltersExpiredAndDecided：孤儿不进展示面——过期
// 请求（死运行滞留）与已决定的请求（等待方未消费）都不算"待批"，
// 否则操作员会对不可生效的请求点批准。
func TestApprovalsListFiltersExpiredAndDecided(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	id, err := identity.Create(context.Background(), "ada")
	if err != nil {
		t.Fatal(err)
	}
	live := seedApproval(t, id, "echo live")
	seedApprovalExpired(t, id, "echo expired-orphan")
	decided := seedApproval(t, id, "echo decided")
	if err := policy.Decide(policy.Dir(id.Timeline.Dir), decided, true); err != nil {
		t.Fatal(err)
	}
	ts, _ := newTestServer(t, identity.Home(), "")

	resp, err := http.Get(ts.URL + "/api/identities/ada/approvals")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var list []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatalf("响应应为数组: %v", err)
	}
	if len(list) != 1 || list[0]["hash"] != live {
		t.Fatalf("待批应只剩活请求 %q，得到 %v", live, list)
	}
}

// TestApprovalsDecideExpiredGone：对过期孤儿批准 = 410（不是 200 的
// "已批准"假象），且不落任何决定文件。
func TestApprovalsDecideExpiredGone(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	id, err := identity.Create(context.Background(), "ada")
	if err != nil {
		t.Fatal(err)
	}
	hash := seedApprovalExpired(t, id, "echo orphan")
	ts, _ := newTestServer(t, identity.Home(), "")

	resp, out := postJSON(t, ts, "/api/identities/ada/approvals/"+hash[:8]+"/approve", nil)
	if resp.StatusCode != 410 {
		t.Fatalf("过期批准 status = %d，应为 410；out=%v", resp.StatusCode, out)
	}
	if approvalDecision(t, id, hash) != "" {
		t.Fatal("过期请求不应写下决定文件")
	}
}

// TestApprovalsErrors：错误映射绝不把"没做成"伪装成成功——非法哈希
// 400、未命中 404、空列表是 [] 而不是 null、写操作拒绝 GET。
func TestApprovalsErrors(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	id, err := identity.Create(context.Background(), "ada")
	if err != nil {
		t.Fatal(err)
	}
	ts, _ := newTestServer(t, identity.Home(), "")

	// 无待批：合法空态。
	resp, err := http.Get(ts.URL + "/api/identities/ada/approvals")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || strings.TrimSpace(string(body)) != "[]" {
		t.Fatalf("空列表 = %d %q，应为 200 []", resp.StatusCode, string(body))
	}

	// 非法哈希段。
	resp, _ = postJSON(t, ts, "/api/identities/ada/approvals/zzzz/approve", nil)
	if resp.StatusCode != 400 {
		t.Fatalf("非法哈希 = %d，应为 400", resp.StatusCode)
	}

	// 未命中：决定必须有对象。
	resp, _ = postJSON(t, ts, "/api/identities/ada/approvals/deadbeef/approve", nil)
	if resp.StatusCode != 404 {
		t.Fatalf("未命中 = %d，应为 404", resp.StatusCode)
	}

	// 写操作只认 POST：GET 触发决定是整类跨站问题的根。
	h := seedApproval(t, id, "echo x")
	resp, err = http.Get(ts.URL + "/api/identities/ada/approvals/" + h[:8] + "/approve")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET approve = %d，应为 405", resp.StatusCode)
	}
	if allow := resp.Header.Get("Allow"); allow != "POST" {
		t.Fatalf("Allow = %q，应为 POST", allow)
	}
}
