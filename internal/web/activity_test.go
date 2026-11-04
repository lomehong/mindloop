package web

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/lomehong/mindloop/internal/identity"
	"github.com/lomehong/mindloop/internal/traj"
)

// newForkChild 用 traj.ForkAt 造一个真实形态的 fork 子轨迹：与根
// 轨迹同目录的兄弟目录（child_ref 为 "../<dir>" 形态——runWorker
// 的落盘布局），带 trajectory 头与双向引用。返回子轨迹句柄，调用
// 方自行追加步骤。
func newForkChild(t *testing.T, id *identity.Identity, slug string) *traj.Timeline {
	t.Helper()
	tl, err := traj.ForkAt(context.Background(), filepath.Dir(id.Timeline.Dir), slug, id.Timeline)
	if err != nil {
		t.Fatal(err)
	}
	return tl
}

// appendSteps 把 (type, run_id, content) 三元组逐条追加到轨迹。
func appendSteps(t *testing.T, tl *traj.Timeline, steps [][3]string) {
	t.Helper()
	for _, s := range steps {
		st := traj.NewStep(s[0])
		if s[1] != "" {
			st.Fields["run_id"] = s[1]
		}
		if s[2] != "" {
			st.Fields["content"] = s[2]
		}
		if err := tl.Append(context.Background(), st); err != nil {
			t.Fatal(err)
		}
	}
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
		"last_step_ts", "last_step_age_s"} {
		if _, ok := got[k]; !ok {
			t.Fatalf("activity 缺少字段 %q: %v", k, got)
		}
	}
	// queued_messages/steps_in_flight/pending_total 已随契约漂移清理
	// 删除（恒 0/恒空的调度器内存态死字段）。
	for _, gone := range []string{"queued_messages", "steps_in_flight", "pending_total"} {
		if _, ok := got[gone]; ok {
			t.Fatalf("activity 不应再产出 %q: %v", gone, got)
		}
	}
	// 新创建的身份轨迹落在 30s 心跳窗内——live 兜底判定生效
	// （liveness.go：轨迹最近被写 = 心智刚停/刚醒，viewer 不标灰），
	// state 为 working 而非 idle。
	if got["state"] != "working" {
		t.Fatalf("新鲜轨迹应判 working（live 兜底），得到 %v", got["state"])
	}
}

// TestHandleTreeWithForkChild：/tree 的 depth 语义——默认只含根；
// depth=1 且轨迹带 fork 步骤时列出子轨迹。id 上报子轨迹 header 的
// 真 UUID（与 fork 步骤 child 字段、子轨迹页同源）；child_ref 的
// "../<dir>" 兄弟形态必须能通过包含校验（真实 fork 布局——旧实现
// 以根轨迹目录做校验基点曾全数误杀）。
func TestHandleTreeWithForkChild(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	home := identity.Home()
	id, err := identity.Create(context.Background(), "ada")
	if err != nil {
		t.Fatal(err)
	}
	fork := newForkChild(t, id, "worker-tree")
	appendSteps(t, fork, [][3]string{
		{"message", "", "步骤一"},
		{"message", "", "步骤二"},
		{"message", "", "步骤三"},
		{"final", "", "完成"},
	})

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
			Slug      string `json:"slug"`
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
	if child.TrajID != fork.ID {
		t.Fatalf("子轨迹 id 应为真 UUID %s，得到 %q", fork.ID, child.TrajID)
	}
	// 头 + 3 条 message + final = 5（Steps 与索引同口径，含头行）。
	if child.StepCount != 5 {
		t.Fatalf("子轨迹步数应实读为 5，得到 %d", child.StepCount)
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
		// steps_in_flight/pending 已随契约漂移清理删除（恒 0/恒空的
		// 调度器内存态，仪表盘侧永远无法知晓）。
		for _, gone := range []string{"steps_in_flight", "pending"} {
			if _, ok := ti[gone]; ok {
				t.Fatalf("thinker %v 不应再产出 %q", ti, gone)
			}
		}
		for _, k := range []string{"name", "state"} {
			if _, ok := ti[k]; !ok {
				t.Fatalf("thinker %v 缺少字段 %q", ti, k)
			}
		}
	}
}

// TestHandleSubTrajectory：真实实现（subtraj.go）下未知 id 404——
// 前端子轨迹页把 404 语义化为"未找到轨迹"空态（sub-traj.tsx 的
// retry 豁免），stub 时代的"未知 id 也 200 骨架"已废止。存在性
// 与内容断言见 subtraj_test.go。

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

// TestHandleSkillsPage：技能页契约——两层合并列出、安装（本地目录
// 源）、身份层可删、全局层删除被拒。
func TestHandleSkillsPage(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	home := identity.Home()
	if _, err := identity.Create(context.Background(), "ada"); err != nil {
		t.Fatal(err)
	}
	// 全局层放一个技能。
	globalSkills := filepath.Join(filepath.Dir(home), "skills")
	if err := os.MkdirAll(filepath.Join(globalSkills, "global-skill"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(globalSkills, "global-skill", "SKILL.md"),
		[]byte("---\nname: global-skill\ndescription: 全局技能\n---\n正文"), 0o644)

	ts, _ := newTestServer(t, home, "")

	// 列表：空身份层时仍能看到全局层。
	resp, err := http.Get(ts.URL + "/api/identities/ada/skills")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var view struct {
		Skills []struct {
			Name   string `json:"name"`
			Source string `json:"source"`
		} `json:"skills"`
		Problems []string `json:"problems"`
	}
	json.NewDecoder(resp.Body).Decode(&view)
	if len(view.Skills) != 1 || view.Skills[0].Name != "global-skill" || view.Skills[0].Source != "global" {
		t.Fatalf("应列出全局技能: %+v", view.Skills)
	}

	// 安装（本地目录源，含一个多技能包）。
	pack := filepath.Join(dir, "pack")
	if err := os.MkdirAll(filepath.Join(pack, "alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(pack, "alpha", "SKILL.md"),
		[]byte("---\nname: alpha\ndescription: A\n---\n"), 0o644)
	insp, out := postJSON(t, ts, "/api/identities/ada/skills", map[string]string{"source": pack})
	if insp.StatusCode != 200 {
		t.Fatalf("install = %d %v", insp.StatusCode, out)
	}

	// 身份层可删；全局层删除被拒。
	del, _ := http.NewRequest("DELETE", ts.URL+"/api/identities/ada/skills/alpha", nil)
	dresp, err := http.DefaultClient.Do(del)
	if err != nil {
		t.Fatal(err)
	}
	dresp.Body.Close()
	if dresp.StatusCode != 200 {
		t.Fatalf("删除身份层技能应 200，得到 %d", dresp.StatusCode)
	}
	del2, _ := http.NewRequest("DELETE", ts.URL+"/api/identities/ada/skills/global-skill", nil)
	d2, err := http.DefaultClient.Do(del2)
	if err != nil {
		t.Fatal(err)
	}
	d2.Body.Close()
	if d2.StatusCode != 400 {
		t.Fatalf("删除全局层技能应 400，得到 %d", d2.StatusCode)
	}
}
