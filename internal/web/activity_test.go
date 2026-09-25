package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mindloop/internal/identity"
	"mindloop/internal/traj"
)

// newForkChild 造一个子轨迹目录（trajectory.jsonl 数行），返回相对
// 根轨迹目录的 child_ref——与 traj fork 步骤的真实落盘形态一致。
func newForkChild(t *testing.T, id *identity.Identity, slug string, steps int) string {
	t.Helper()
	tlDir := filepath.Dir(id.Timeline.Path)
	childDir := filepath.Join(tlDir, slug)
	if err := os.MkdirAll(childDir, 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for i := 0; i < steps; i++ {
		fmt.Fprintf(&b, `{"type":"message","step_id":"child-%d","ts":"2026-01-0%dT00:00:00.000Z"}`+"\n", i, i+1)
	}
	b.WriteString(`{"type":"final","step_id":"child-final","ts":"2026-01-09T00:00:00.000Z"}` + "\n")
	if err := os.WriteFile(filepath.Join(childDir, "trajectory.jsonl"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return slug + "/trajectory.jsonl"
}

// TestHandleActivityShape：/activity 返回 IdentityActivity 契约的
// 全部字段（viewer 首页横幅与 talk 页共同消费）。
func TestHandleActivityShape(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	home := identity.Home()
	identity.Create(context.Background(), "ada")
	ts, _ := newTestServer(t, home, "")

	resp, err := http.Get(ts.URL + "/api/identities/ada/activity")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"state", "dispatcher_running", "busy_thinkers",
		"last_step_ts", "last_step_age_s", "queued_messages", "pending_total"} {
		if _, ok := got[k]; !ok {
			t.Fatalf("activity 缺少字段 %q: %v", k, got)
		}
	}
	if got["state"] != "idle" {
		t.Fatalf("无运行锁时应为 idle，得到 %v", got["state"])
	}
}

// TestHandleTreeWithForkChild：/tree 的 depth 语义——默认只含根；
// depth=1 且轨迹带 fork 步骤时列出子轨迹（步数/final 标记来自
// 子轨迹文件的实读）。
func TestHandleTreeWithForkChild(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	home := identity.Home()
	id, err := identity.Create(context.Background(), "ada")
	if err != nil {
		t.Fatal(err)
	}
	ref := newForkChild(t, id, "abc10000-child", 3)

	fork := traj.NewStep("fork")
	fork.Fields["child_ref"] = ref
	if err := id.Timeline.Append(context.Background(), fork); err != nil {
		t.Fatal(err)
	}

	ts, _ := newTestServer(t, home, "")

	// depth=0：仅当前节点，无 children。
	resp, err := http.Get(ts.URL + "/api/identities/ada/tree?depth=0")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var root map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&root); err != nil {
		t.Fatal(err)
	}
	if _, hasChildren := root["children"]; hasChildren {
		t.Fatalf("depth=0 不应带 children: %v", root)
	}

	// depth=1：fork 步骤指向的子轨迹出现在 children 里。
	resp2, err := http.Get(ts.URL + "/api/identities/ada/tree?depth=1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	var root2 struct {
		TrajID     string `json:"traj_id"`
		StepCount  int    `json:"step_count"`
		ChildCount int    `json:"child_count"`
		Children   *[]struct {
			TrajID    string `json:"traj_id"`
			StepCount int    `json:"step_count"`
			HasFinal  bool   `json:"has_final"`
		} `json:"children"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&root2); err != nil {
		t.Fatal(err)
	}
	if root2.ChildCount != 1 || root2.Children == nil || len(*root2.Children) != 1 {
		t.Fatalf("depth=1 应列出 1 个子轨迹，得到 child_count=%d", root2.ChildCount)
	}
	child := (*root2.Children)[0]
	if !strings.Contains(child.TrajID, "child") {
		t.Fatalf("子轨迹 id 不符: %q", child.TrajID)
	}
	if child.StepCount != 4 { // 3 条 message + 1 条 final
		t.Fatalf("子轨迹步数应实读为 4，得到 %d", child.StepCount)
	}
	if !child.HasFinal {
		t.Fatal("子轨迹含 final 步骤，has_final 应为 true")
	}
}

// TestHandleLogsAndTail：/logs 列出 runs/ 下的日志文件；
// /logs/{name}?tail_bytes=N 返回尾部字节；穿越名 400、缺文件 404。
func TestHandleLogsAndTail(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	home := identity.Home()
	id, err := identity.Create(context.Background(), "ada")
	if err != nil {
		t.Fatal(err)
	}
	runsDir := filepath.Join(id.Timeline.Dir, "runs")
	if err := os.MkdirAll(runsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runsDir, "monolith.log"), []byte("0123456789abcdef"), 0o644); err != nil {
		t.Fatal(err)
	}
	ts, _ := newTestServer(t, home, "")

	resp, err := http.Get(ts.URL + "/api/identities/ada/logs")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var logs []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&logs); err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 || logs[0]["name"] != "monolith.log" {
		t.Fatalf("应列出 monolith.log，得到 %v", logs)
	}

	tail, err := http.Get(ts.URL + "/api/identities/ada/logs/monolith.log?tail_bytes=4")
	if err != nil {
		t.Fatal(err)
	}
	defer tail.Body.Close()
	var got struct {
		Name    string `json:"name"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(tail.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Content != "cdef" {
		t.Fatalf("tail_bytes=4 应返回尾部 4 字节 cdef，得到 %q", got.Content)
	}

	// 反斜杠穿越（Windows 分隔符，mux 的 cleanPath 不消化）。
	bad, err := http.Get(ts.URL + "/api/identities/ada/logs/a%5C..%5C..%5Cpersona.md")
	if err != nil {
		t.Fatal(err)
	}
	bad.Body.Close()
	if bad.StatusCode != 400 {
		t.Fatalf("穿越日志名应 400，得到 %d", bad.StatusCode)
	}

	missing, err := http.Get(ts.URL + "/api/identities/ada/logs/nope.log")
	if err != nil {
		t.Fatal(err)
	}
	missing.Body.Close()
	if missing.StatusCode != 404 {
		t.Fatalf("缺失日志应 404，得到 %d", missing.StatusCode)
	}
}

// TestHandleThinkersStatus：/thinkers 从轨迹的 launched_by 提炼
// thinker 列表与统计（ThinkersStatus 契约）。
func TestHandleThinkersStatus(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	home := identity.Home()
	id, err := identity.Create(context.Background(), "ada")
	if err != nil {
		t.Fatal(err)
	}
	addMessage(t, id, "you", "ada", "hi")
	for _, by := range []string{"responder", "responder", "monolith"} {
		s := traj.NewStep("message")
		s.Fields["launched_by"] = by
		if err := id.Timeline.Append(context.Background(), s); err != nil {
			t.Fatal(err)
		}
	}
	ts, _ := newTestServer(t, home, "")

	resp, err := http.Get(ts.URL + "/api/identities/ada/thinkers")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got struct {
		Identity       map[string]string `json:"identity"`
		Dispatcher     map[string]any    `json:"dispatcher"`
		ThinkersTotal  int               `json:"thinkers_total"`
		ActiveThinkers int               `json:"active_thinkers"`
		Thinkers       []map[string]any  `json:"thinkers"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Identity == nil || got.Identity["id"] != "ada" {
		t.Fatalf("identity 契约缺失: %v", got.Identity)
	}
	if got.ThinkersTotal != 2 {
		t.Fatalf("应提炼出 2 个 thinker（responder/monolith），得到 %d", got.ThinkersTotal)
	}
	if got.Dispatcher == nil {
		t.Fatal("dispatcher 字段缺失")
	}
	for _, ti := range got.Thinkers {
		for _, k := range []string{"name", "state", "steps_in_flight", "pending"} {
			if _, ok := ti[k]; !ok {
				t.Fatalf("thinker %v 缺少字段 %q", ti, k)
			}
		}
	}
}

// TestHandleSubTrajectory：子轨迹视图恒返回 mindlog 骨架——viewer
// 缺字段会崩在 TypeError（契约注释里的真实事故）。
func TestHandleSubTrajectory(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	home := identity.Home()
	identity.Create(context.Background(), "ada")
	ts, _ := newTestServer(t, home, "")

	resp, err := http.Get(ts.URL + "/api/identities/ada/traj/abc12345")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var got struct {
		Mindlog struct {
			TrajID   string           `json:"traj_id"`
			Steps    []map[string]any `json:"steps"`
			Identity map[string]any   `json:"identity"`
			Runs     []map[string]any `json:"runs"`
		} `json:"mindlog"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Mindlog.TrajID != "abc12345" || got.Mindlog.Steps == nil ||
		got.Mindlog.Identity == nil || got.Mindlog.Runs == nil {
		t.Fatalf("子轨迹骨架字段缺失: %+v", got.Mindlog)
	}
}

// TestMindlogRunTldrDerived：运行组的 tldr 从 final 步骤正文派生
// （"视图皆派生"——模型自己写的结论就是这轮运行的摘要，不额外
// 烧一次模型调用）。
func TestMindlogRunTldrDerived(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	home := identity.Home()
	id, err := identity.Create(context.Background(), "ada")
	if err != nil {
		t.Fatal(err)
	}

	// 组一轮完整的 run：prompt → reasoning → final。
	header := traj.NewStep("run")
	if err := id.Timeline.Append(context.Background(), header); err != nil {
		t.Fatal(err)
	}
	rid := header.StepID
	mk := func(typ, content string) {
		s := traj.NewStep(typ)
		s.Fields["run_id"] = rid
		if content != "" {
			s.Fields["content"] = content
		}
		if err := id.Timeline.Append(context.Background(), s); err != nil {
			t.Fatal(err)
		}
	}
	mk("prompt", "任务：清点目录")
	mk("reasoning", "思考过程")
	mk("final", "统计了 12 个文件，共 3400 行。")

	ts, _ := newTestServer(t, home, "")
	resp, err := http.Get(ts.URL + "/api/identities/ada/mindlog?tail=10")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got struct {
		Runs []struct {
			RunID  string  `json:"run_id"`
			Tldr   *string `json:"tldr"`
			Status string  `json:"status"`
		} `json:"runs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got.Runs) != 1 {
		t.Fatalf("应聚合出 1 个运行组，得到 %d", len(got.Runs))
	}
	run := got.Runs[0]
	if run.RunID != rid || run.Status != "done" {
		t.Fatalf("运行组错位: %+v", run)
	}
	if run.Tldr == nil || *run.Tldr != "统计了 12 个文件，共 3400 行。" {
		t.Fatalf("tldr 应派生自 final 正文，得到 %v", run.Tldr)
	}
}
