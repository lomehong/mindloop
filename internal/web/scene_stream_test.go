package web

// 场景投影端测试：折叠语义（upsert 覆盖/remove 删除/exit_reason 透传
// /data 解码）、快照序列化、choreography 批缓冲（同 batch_id 聚合单帧
// + staggerHint、批界成帧、静默定时器兜底）。

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/lomehong/mindloop/internal/identity"
	"github.com/lomehong/mindloop/internal/traj"
)

// sceneLine 构造一条 scene JSONL 行（op/kind 等字段直拼）。
func sceneLine(fields string) []byte {
	return []byte(`{"step_id":"s","type":"scene","ts":"2026-10-13T10:00:00.000Z","` + fields + `}`)
}

func TestSceneSurfacesFoldAndSnapshot(t *testing.T) {
	sv := &sceneSurfaces{surfaces: map[string]map[string]any{}}

	op, batch := sv.applyScene(sceneLine(`op":"upsert","id":"b","kind":"text","intent":"ambient","summary":"B 卡","data":{"body":"b"}`))
	if op == nil || batch != "" {
		t.Fatalf("upsert 应返回 op 且无批号: op=%v batch=%q", op, batch)
	}
	if _, batch := sv.applyScene(sceneLine(`op":"upsert","id":"a","kind":"text","summary":"A 卡","batch_id":"bx"`)); batch != "bx" {
		t.Fatalf("批号应透传: %q", batch)
	}
	if op, _ := sv.applyScene(sceneLine(`"type":"message","content":"x"`)); op != nil {
		t.Fatal("非 scene 步骤应忽略")
	}

	snap := sv.snapshot()
	var parsed struct {
		Type  string `json:"type"`
		State struct {
			Rev      int              `json:"rev"`
			Surfaces []map[string]any `json:"surfaces"`
		} `json:"state"`
	}
	if err := json.Unmarshal([]byte(snap), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Type != "scene" {
		t.Fatalf("快照帧应带 type=scene（客户端按它分发）: %s", snap)
	}
	if parsed.State.Rev != 2 || len(parsed.State.Surfaces) != 2 {
		t.Fatalf("快照不符: %s", snap)
	}
	if parsed.State.Surfaces[0]["id"] != "a" {
		t.Fatalf("快照应按 id 排序: %s", snap)
	}
	if _, ok := parsed.State.Surfaces[0]["step_id"]; ok {
		t.Fatalf("信封字段不应进投影: %s", snap)
	}
	if _, ok := parsed.State.Surfaces[1]["data"].(map[string]any); !ok {
		t.Fatalf("data 应解码为对象: %s", snap)
	}

	// remove：exit_reason 透传（生命周期括号）。
	op, _ = sv.applyScene(sceneLine(`op":"remove","id":"a","exit_reason":"ttl"`))
	if op == nil || op["exit_reason"] != "ttl" {
		t.Fatalf("remove 应透传 exit_reason: %v", op)
	}
	snap = sv.snapshot()
	if err := json.Unmarshal([]byte(snap), &parsed); err != nil {
		t.Fatal(err)
	}
	if len(parsed.State.Surfaces) != 1 || parsed.State.Surfaces[0]["id"] != "b" {
		t.Fatalf("remove 后快照不符: %s", snap)
	}
}

func TestWatcherScenePrimeReplaysHistory(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	id, err := identity.Create(context.Background(), "ada")
	if err != nil {
		t.Fatal(err)
	}

	// 历史写入：message → scene → message（夹在中间验证过滤）。
	msg := traj.NewStep(traj.TypeMessage)
	msg.Fields["content"] = "普通消息"
	if err := id.Timeline.Append(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	card := traj.NewStep(traj.TypeScene)
	card.Fields["op"] = "upsert"
	card.Fields["id"] = "weather-bj"
	card.Fields["kind"] = "text"
	card.Fields["intent"] = "inform"
	card.Fields["summary"] = "北京 18° 晴"
	if err := id.Timeline.Append(context.Background(), card); err != nil {
		t.Fatal(err)
	}
	after := traj.NewStep(traj.TypeMessage)
	after.Fields["content"] = "后续消息"
	if err := id.Timeline.Append(context.Background(), after); err != nil {
		t.Fatal(err)
	}

	w := &identityWatcher{id: id}
	w.primeScene()
	if w.scene == nil {
		t.Fatal("回放后折叠态应已建立")
	}
	snap := w.scene.snapshot()
	var parsed struct {
		State struct {
			Rev      int              `json:"rev"`
			Surfaces []map[string]any `json:"surfaces"`
		} `json:"state"`
	}
	if err := json.Unmarshal([]byte(snap), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.State.Rev != 1 || len(parsed.State.Surfaces) != 1 {
		t.Fatalf("快照应恰好含历史卡片: %s", snap)
	}
	if parsed.State.Surfaces[0]["id"] != "weather-bj" || parsed.State.Surfaces[0]["summary"] != "北京 18° 晴" {
		t.Fatalf("回放卡片字段不符: %s", snap)
	}
}

// TestSceneBatchBuffering：同 batch_id 的连续步骤聚合成一帧（批量 ops
// + staggerHint 40），不同批号/非 scene 行为批界立即成帧。
func TestSceneBatchBuffering(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	id, err := identity.Create(context.Background(), "ada")
	if err != nil {
		t.Fatal(err)
	}
	w := newIdentityWatcher(&streamHub{}, id)
	sub, snap := w.subscribeScene()
	if !strings.Contains(snap.data, `"rev":0`) {
		t.Fatalf("初始快照应为空: %s", snap.data)
	}

	readFrame := func(timeout time.Duration) (map[string]any, bool) {
		select {
		case f := <-sub.ch:
			var m map[string]any
			if json.Unmarshal([]byte(f.data), &m) != nil {
				t.Fatalf("帧 data 非法: %s", f.data)
			}
			return m, true
		case <-time.After(timeout):
			return nil, false
		}
	}

	// 批内第一张：不立即成帧（缓冲等同批兄弟；60ms < 150ms flush 定时）。
	w.mu.Lock()
	w.applySceneLine(sceneLine(`op":"upsert","id":"c1","kind":"text","summary":"一","batch_id":"choreo-1"`))
	w.mu.Unlock()
	if f, ok := readFrame(60 * time.Millisecond); ok {
		t.Fatalf("批内首卡应缓冲不成帧, got %v", f)
	}
	// 批内第二张：仍缓冲（flush 定时器被 Reset，再等 150ms）。
	w.mu.Lock()
	w.applySceneLine(sceneLine(`op":"upsert","id":"c2","kind":"text","summary":"二","batch_id":"choreo-1"`))
	w.mu.Unlock()
	if f, ok := readFrame(60 * time.Millisecond); ok {
		t.Fatalf("批内第二卡应继续缓冲, got %v", f)
	}
	// 150ms 静默定时器兜底：尾批成帧。
	f1, ok := readFrame(1500 * time.Millisecond)
	if !ok {
		t.Fatal("静默定时器应成帧")
	}
	if f1["batchId"] != "choreo-1" || f1["staggerHint"] != float64(40) {
		t.Fatalf("批帧应带 batchId+staggerHint: %v", f1)
	}
	if ops, ok := f1["ops"].([]any); !ok || len(ops) != 2 {
		t.Fatalf("批帧应含 2 ops: %v", f1)
	}

	// 不同批号单卡：150ms 定时成帧（单 op 无 staggerHint）。
	w.mu.Lock()
	w.applySceneLine(sceneLine(`op":"upsert","id":"solo","kind":"text","summary":"单卡","batch_id":"choreo-2"`))
	w.mu.Unlock()
	f2, ok := readFrame(1500 * time.Millisecond)
	if !ok {
		t.Fatal("不同批号应独立成帧")
	}
	if f2["batchId"] != "choreo-2" || f2["staggerHint"] != nil {
		t.Fatalf("单卡批不应带 staggerHint: %v", f2)
	}

	// 无批号：立即成帧，batchId 用 rev 派生。
	w.mu.Lock()
	w.applySceneLine(sceneLine(`op":"upsert","id":"plain","kind":"text","summary":"无批"`))
	w.mu.Unlock()
	f3, ok := readFrame(250 * time.Millisecond)
	if !ok {
		t.Fatal("无批号应立即成帧")
	}
	if !strings.HasPrefix(f3["batchId"].(string), "r") {
		t.Fatalf("无批号帧 batchId 应 rev 派生: %v", f3)
	}
}
