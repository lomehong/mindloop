package web

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"mindloop/internal/identity"
	"mindloop/internal/mind"
	"mindloop/internal/traj"
)

// 本文件钉死契约漂移清理后的端点形状（与前端评审员同步删除，决策
// 固定）：恒 0 的调度器内存态字段与恒空死字段从后端契约中移除——
// 后端不再产出，前端不再读取，任何一侧回潮都会在这里爆红。

// TestIdentitiesContractShape：/api/identities 不再产出
// steps_in_flight/mindlog_path/persona_path/live_badge；仍保留的
// 契约字段一个都不能少（home.tsx 直接读，缺了崩进错误边界）。
func TestIdentitiesContractShape(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	home := identity.Home()
	if _, err := identity.Create(context.Background(), "ada"); err != nil {
		t.Fatal(err)
	}
	ts, _ := newTestServer(t, home, "")

	resp, err := http.Get(ts.URL + "/api/identities")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var list []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("应返回 1 个身份，得到 %d", len(list))
	}
	entry := list[0]
	for _, gone := range []string{"steps_in_flight", "mindlog_path", "persona_path", "live_badge"} {
		if _, ok := entry[gone]; ok {
			t.Fatalf("/api/identities 不应再产出 %q: %v", gone, entry)
		}
	}
	for _, keep := range []string{"id", "name", "path_rel", "group", "live",
		"last_activity_ts", "step_count", "dispatcher", "thinkers_total", "thinkers_active"} {
		if _, ok := entry[keep]; !ok {
			t.Fatalf("/api/identities 缺少保留字段 %q: %v", keep, entry)
		}
	}
}

// TestActivityContractShape：/activity 不再产出任何恒 0 调度器内存态
// 死字段——queued_messages（第一轮清理）与 steps_in_flight/
// pending_total（决策延伸，与前端同步删除）。
func TestActivityContractShape(t *testing.T) {
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
	for _, gone := range []string{"queued_messages", "steps_in_flight", "pending_total"} {
		if _, ok := got[gone]; ok {
			t.Fatalf("/activity 不应再产出 %q: %v", gone, got)
		}
	}
	for _, keep := range []string{"state", "dispatcher_running", "busy_thinkers"} {
		if _, ok := got[keep]; !ok {
			t.Fatalf("/activity 缺少保留字段 %q: %v", keep, got)
		}
	}
}

// TestThinkersContractShape：/thinkers 的 thinker 条目不再产出
// steps_in_flight/pending，顶层不再产出 steps_in_flight/pending_total
// （决策延伸：全部恒 0 调度器内存态死字段，与前端同步删除）；
// disabled 计数真实统计（此前恒 0——禁用名单里有一个人也只显示 0）。
func TestThinkersContractShape(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	home := identity.Home()
	id, err := identity.Create(context.Background(), "ada")
	if err != nil {
		t.Fatal(err)
	}
	// 两个 thinker 产生轨迹事实，其中一个被禁用。
	for _, by := range []string{"monolith", "responder"} {
		s := traj.NewStep("message")
		s.Fields["launched_by"] = by
		if err := id.Timeline.Append(context.Background(), s); err != nil {
			t.Fatal(err)
		}
	}
	if err := mind.SetThinkerEnabled(id.Timeline.Dir, "monolith", false); err != nil {
		t.Fatal(err)
	}
	ts, _ := newTestServer(t, home, "")

	resp, err := http.Get(ts.URL + "/api/identities/ada/thinkers")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{"steps_in_flight", "pending_total"} {
		if _, ok := got[gone]; ok {
			t.Fatalf("/thinkers 顶层不应再产出 %q: %v", gone, got)
		}
	}
	if _, ok := got["thinkers_disabled"]; !ok {
		t.Fatalf("/thinkers 顶层缺少 thinkers_disabled: %v", got)
	}
	thinkers, _ := got["thinkers"].([]any)
	if len(thinkers) != 2 {
		t.Fatalf("应有 2 个 thinker，得到 %d", len(thinkers))
	}
	for _, raw := range thinkers {
		ti := raw.(map[string]any)
		for _, gone := range []string{"steps_in_flight", "pending"} {
			if _, ok := ti[gone]; ok {
				t.Fatalf("/thinkers 条目不应再产出 %q: %v", gone, ti)
			}
		}
		if _, ok := ti["name"]; !ok {
			t.Fatalf("thinker 条目缺少 name: %v", ti)
		}
	}
	disabled, _ := got["thinkers_disabled"].(float64)
	if disabled != 1 {
		t.Fatalf("thinkers_disabled 应为 1（monolith 被禁用），得到 %v", got["thinkers_disabled"])
	}
	// 禁用状态落在条目上。
	for _, raw := range thinkers {
		ti := raw.(map[string]any)
		if ti["name"] == "monolith" && ti["state"] != "disabled" {
			t.Fatalf("monolith 应为 disabled，得到 %v", ti["state"])
		}
	}
}

// TestTreeIgnoresEscapingChildRef：child_ref 来自轨迹内容（可被
// traj append --field 任意写入），越界引用必须被跳过——不 Stat、
// 不读取、不出现在 children 里。
func TestTreeIgnoresEscapingChildRef(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	home := identity.Home()
	id, err := identity.Create(context.Background(), "ada")
	if err != nil {
		t.Fatal(err)
	}
	// 身份根外的真实文件（越界引用的靶子）。
	outside := filepath.Join(dir, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "trajectory.jsonl"),
		[]byte(`{"type":"final","step_id":"evil","ts":"2026-01-01T00:00:00.000Z"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, ref := range []string{
		filepath.Join("..", "..", "outside", "trajectory.jsonl"),
		filepath.Join("..", "..", "outside"),
	} {
		fork := traj.NewStep("fork")
		fork.Fields["child_ref"] = ref
		if err := id.Timeline.Append(context.Background(), fork); err != nil {
			t.Fatal(err)
		}
	}

	ts, _ := newTestServer(t, home, "")
	resp, err := http.Get(ts.URL + "/api/identities/ada/tree?depth=1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var root struct {
		ChildCount int `json:"child_count"`
		Children   *[]struct {
			TrajID string `json:"traj_id"`
		} `json:"children"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&root); err != nil {
		t.Fatal(err)
	}
	if root.ChildCount != 0 || (root.Children != nil && len(*root.Children) != 0) {
		t.Fatalf("越界 child_ref 应全部被跳过，得到 child_count=%d children=%v",
			root.ChildCount, root.Children)
	}
}
