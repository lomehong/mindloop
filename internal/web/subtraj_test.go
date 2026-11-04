// subtraj_test.go：fork 穿透的服务端验证（Super-agent 双层架构的
// viewer 可见性，2026-11-05）。夹具用 traj.ForkAt 造真实落盘形态的
// fork（兄弟目录 + "../<dir>" 形态的 child_ref）。
package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lomehong/mindloop/internal/identity"
	"github.com/lomehong/mindloop/internal/traj"
)

// subtrajFixture：ada 身份 + 一个带完整 run（prompt/reasoning/final
// + run_id）与 blob 输出的 fork。返回 server 底座与各句柄。
func subtrajFixture(t *testing.T) (*identity.Identity, *traj.Timeline, string, string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	id, err := identity.Create(context.Background(), "ada")
	if err != nil {
		t.Fatal(err)
	}
	fork := newForkChild(t, id, "worker-subtraj")
	runHeader := traj.NewStep("run")
	if err := fork.Append(context.Background(), runHeader); err != nil {
		t.Fatal(err)
	}
	rid := runHeader.StepID
	appendSteps(t, fork, [][3]string{
		{"prompt", rid, "任务：清点目录"},
		{"reasoning", rid, "先 ls 再统计"},
		{"final", rid, "统计了 12 个文件。"},
	})
	// fork 目录下的 blob（runner 大输出的落点）。
	blobDir := filepath.Join(fork.Dir, "blobs")
	if err := os.MkdirAll(blobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	blobName := runHeader.StepID + ".stdout"
	if err := os.WriteFile(filepath.Join(blobDir, blobName), []byte("完整输出正文"), 0o644); err != nil {
		t.Fatal(err)
	}
	return id, fork, rid, blobName
}

// TestSubTrajectoryServesFork：/traj/{id} 返回 fork 的完整 mindlog
// ——步骤、run 卡（groupRuns 复用）、面包屑（root→fork）与 parent
// 链接；UUID、唯一前缀、目录名三种 id 形态都能解析到同一条。
func TestSubTrajectoryServesFork(t *testing.T) {
	id, fork, rid, _ := subtrajFixture(t)
	ts, srv := newTestServer(t, identity.Home(), "")

	type resp struct {
		Mindlog struct {
			TrajID    string `json:"traj_id"`
			DirRel    string `json:"dir_rel"`
			StepCount int    `json:"step_count"`
			Steps     []struct {
				StepID string `json:"step_id"`
				Type   string `json:"type"`
			} `json:"steps"`
			Runs []struct {
				RunID  string  `json:"run_id"`
				Status string  `json:"status"`
				Tldr   *string `json:"tldr"`
			} `json:"runs"`
			Identity map[string]any `json:"identity"`
		} `json:"mindlog"`
		Breadcrumb []struct {
			TrajID string `json:"traj_id"`
			Slug   string `json:"slug"`
		} `json:"breadcrumb"`
		Parent *struct {
			TrajID string `json:"traj_id"`
		} `json:"parent"`
	}

	get := func(ref string) (resp, int) {
		t.Helper()
		res, err := http.Get(ts.URL + "/api/identities/ada/traj/" + ref)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out resp
		if err := json.NewDecoder(res.Body).Decode(&out); err != nil && res.StatusCode == 200 {
			t.Fatal(err)
		}
		return out, res.StatusCode
	}

	// 完整 UUID。
	got, code := get(fork.ID)
	if code != 200 {
		t.Fatalf("UUID 形态应 200，得到 %d", code)
	}
	if got.Mindlog.TrajID != fork.ID {
		t.Fatalf("traj_id 应为 fork UUID，得到 %q", got.Mindlog.TrajID)
	}
	// 头 + fork 步骤无（fork 步骤在根上）+ run + prompt + reasoning + final = 5。
	if got.Mindlog.StepCount != 5 {
		t.Fatalf("步骤数应为 5，得到 %d", got.Mindlog.StepCount)
	}
	if len(got.Mindlog.Runs) != 1 || got.Mindlog.Runs[0].RunID != rid {
		t.Fatalf("应聚合出 1 个 run 卡（run_id=%s）: %+v", rid, got.Mindlog.Runs)
	}
	if got.Mindlog.Runs[0].Status != "done" || got.Mindlog.Runs[0].Tldr == nil {
		t.Fatalf("run 卡应 done 且带 tldr: %+v", got.Mindlog.Runs[0])
	}
	// 面包屑 [root, fork]，parent 指回根。
	if len(got.Breadcrumb) != 2 || got.Breadcrumb[0].TrajID != id.Timeline.ID ||
		got.Breadcrumb[1].TrajID != fork.ID {
		t.Fatalf("面包屑应为 [root, fork]: %+v", got.Breadcrumb)
	}
	if got.Parent == nil || got.Parent.TrajID != id.Timeline.ID {
		t.Fatalf("parent 应指回根轨迹: %+v", got.Parent)
	}
	if got.Mindlog.Identity == nil || got.Mindlog.Identity["id"] != "ada" {
		t.Fatalf("identity 契约缺失: %v", got.Mindlog.Identity)
	}

	// 唯一前缀（人排查现场的形态）。
	if _, code := get(fork.ID[:8]); code != 200 {
		t.Fatalf("UUID 前缀形态应 200，得到 %d", code)
	}
	// 目录名（tree 侧栏上报的形态）。
	if _, code := get(filepath.Base(fork.Dir)); code != 200 {
		t.Fatalf("目录名形态应 200，得到 %d", code)
	}
	// 未知 id → 404（前端语义化为空态）。
	if _, code := get("00000000-0000-0000-0000-000000000000"); code != 404 {
		t.Fatalf("未知 id 应 404，得到 %d", code)
	}

	// 解析器的穿越拒绝（HTTP 层的 mux 会清洗路径，直接打方法）。
	for _, bad := range []string{"..", "../..", `..\..`, "a/b", "a\\b", "/abs"} {
		if _, err := srv.resolveIdentityTraj(id, bad); err == nil {
			t.Fatalf("穿越 ref %q 应被拒绝", bad)
		}
	}
}

// TestSubTrajectoryServesRoot：根轨迹本身也可经 /traj 服务——面包屑
// 退化为 [root]、无 parent。
func TestSubTrajectoryServesRoot(t *testing.T) {
	id, _, _, _ := subtrajFixture(t)
	ts, _ := newTestServer(t, identity.Home(), "")

	res, err := http.Get(ts.URL + "/api/identities/ada/traj/" + id.Timeline.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("根轨迹应 200，得到 %d", res.StatusCode)
	}
	var got struct {
		Mindlog struct {
			TrajID string `json:"traj_id"`
		} `json:"mindlog"`
		Breadcrumb []struct {
			TrajID string `json:"traj_id"`
		} `json:"breadcrumb"`
		Parent any `json:"parent"`
	}
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Mindlog.TrajID != id.Timeline.ID {
		t.Fatalf("traj_id 应为根 UUID: %q", got.Mindlog.TrajID)
	}
	if len(got.Breadcrumb) != 1 || got.Breadcrumb[0].TrajID != id.Timeline.ID {
		t.Fatalf("根轨迹面包屑应为 [root]: %+v", got.Breadcrumb)
	}
	if got.Parent != nil {
		t.Fatalf("根轨迹无 parent: %v", got.Parent)
	}
}

// TestRunCommandScansFork：/runs/{runId}/command 在根未命中时扫
// fork——Worker 的 prompt 全文可展开（此前 404）。
func TestRunCommandScansFork(t *testing.T) {
	_, _, rid, _ := subtrajFixture(t)
	ts, _ := newTestServer(t, identity.Home(), "")

	res, err := http.Get(ts.URL + "/api/identities/ada/runs/" + rid + "/command")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("fork 里的 run 应 200，得到 %d", res.StatusCode)
	}
	var got struct {
		RunID   string `json:"run_id"`
		Command string `json:"command"`
	}
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Command != "任务：清点目录" {
		t.Fatalf("command 应为 fork 里 prompt 全文，得到 %q", got.Command)
	}
}

// TestStepDetailScansFork：/step/{id} 在 fork 里的步骤可查（带同
// run 上下文）。
func TestStepDetailScansFork(t *testing.T) {
	_, fork, _, _ := subtrajFixture(t)
	ts, _ := newTestServer(t, identity.Home(), "")

	steps, err := fork.Steps()
	if err != nil {
		t.Fatal(err)
	}
	var finalID string
	for _, s := range steps {
		if s.Type == traj.TypeFinal {
			finalID = s.StepID
		}
	}
	res, err := http.Get(ts.URL + "/api/identities/ada/step/" + finalID)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("fork 里的步骤应 200，得到 %d", res.StatusCode)
	}
	var got struct {
		Step struct {
			StepID string `json:"step_id"`
			Type   string `json:"type"`
		} `json:"step"`
		Run map[string]any `json:"run"`
	}
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Step.Type != traj.TypeFinal {
		t.Fatalf("应命中 final 步骤: %+v", got.Step)
	}
	if got.Run == nil || got.Run["status"] != "done" {
		t.Fatalf("应带 run 上下文（done）: %v", got.Run)
	}
}

// TestMindlogSearchScansFork：搜索跨 fork——Worker 的 prompt/reasoning
// 内容可搜，命中携带 traj_id/traj_slug；根命中不带轨迹归属（前端按
// 此分流跳转子轨迹页）。
func TestMindlogSearchScansFork(t *testing.T) {
	id, fork, _, _ := subtrajFixture(t)
	// 根里也放一条可命中（含关键词的 message）。
	msg := traj.NewStep("message")
	msg.Fields["content"] = "根里的清点指令"
	if err := id.Timeline.Append(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	ts, _ := newTestServer(t, identity.Home(), "")

	res, err := http.Get(ts.URL + "/api/identities/ada/mindlog/search?q=" + url.QueryEscape("清点"))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("search 应 200，得到 %d", res.StatusCode)
	}
	var got struct {
		Total int `json:"total"`
		Hits  []struct {
			StepID   string `json:"step_id"`
			Type     string `json:"type"`
			TrajID   string `json:"traj_id"`
			TrajSlug string `json:"traj_slug"`
			Snippet  string `json:"snippet"`
		} `json:"hits"`
		StepCount int `json:"step_count"`
	}
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Total != 2 {
		t.Fatalf("根 1 条 + fork prompt 1 条 = 2 个命中，得到 %d: %+v", got.Total, got.Hits)
	}
	var rootHit, forkHit = -1, -1
	for i, h := range got.Hits {
		if h.TrajID == "" {
			rootHit = i
		} else if h.TrajID == fork.ID {
			forkHit = i
		}
	}
	if rootHit == -1 || forkHit == -1 {
		t.Fatalf("应有根命中（无 traj_id）与 fork 命中（traj_id=%s）: %+v", fork.ID, got.Hits)
	}
	if got.Hits[forkHit].Type != traj.TypePrompt {
		t.Fatalf("fork 命中应为 prompt: %+v", got.Hits[forkHit])
	}
	if !strings.Contains(got.Hits[forkHit].Snippet, "清点") {
		t.Fatalf("fork 命中应带摘要: %q", got.Hits[forkHit].Snippet)
	}
	if got.Hits[rootHit].TrajSlug != "" {
		t.Fatalf("根命中不应带轨迹归属: %+v", got.Hits[rootHit])
	}
}

// TestMindlogSearchScopeExcludesForkMechanics：scope=thoughts 时 fork 的
// prompt（运行机制）不进结果——范围语义跨轨迹一致。
func TestMindlogSearchScopeExcludesForkMechanics(t *testing.T) {
	_, _, _, _ = subtrajFixture(t)
	ts, _ := newTestServer(t, identity.Home(), "")

	res, err := http.Get(ts.URL + "/api/identities/ada/mindlog/search?q=" + url.QueryEscape("清点") + "&scope=thoughts")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var got struct {
		Total int `json:"total"`
	}
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Total != 0 {
		t.Fatalf("thoughts 范围不应命中 fork 的 prompt（运行机制），得到 %d", got.Total)
	}
}

// TestTrajectoryBlobFromFork：/traj/{id}/blob/{name} 从 fork 目录
// 读 blob（此前只认根 UUID，fork 的大输出 404）。
func TestTrajectoryBlobFromFork(t *testing.T) {
	_, fork, _, blobName := subtrajFixture(t)
	ts, _ := newTestServer(t, identity.Home(), "")

	res, err := http.Get(ts.URL + "/api/identities/ada/traj/" + fork.ID + "/blob/" + blobName)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("fork 的 blob 应 200，得到 %d", res.StatusCode)
	}
	body, _ := strings.Builder{}, error(nil)
	buf := make([]byte, 64)
	n, _ := res.Body.Read(buf)
	body.Write(buf[:n])
	if !strings.Contains(body.String(), "完整输出正文") {
		t.Fatalf("blob 内容不符: %q", body.String())
	}

	// 非法名仍拒（suffix 白名单不变）。
	bad, err := http.Get(ts.URL + "/api/identities/ada/traj/" + fork.ID + "/blob/evil.txt")
	if err != nil {
		t.Fatal(err)
	}
	bad.Body.Close()
	if bad.StatusCode != 404 {
		t.Fatalf("非白名单后缀应 404，得到 %d", bad.StatusCode)
	}
}
