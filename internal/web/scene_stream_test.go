package web

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lomehong/mindloop/internal/identity"
	"github.com/lomehong/mindloop/internal/traj"
)

// TestSceneSurfacesFoldAndSnapshot：折叠语义（upsert 覆盖 / remove
// 删除 / 非 scene 忽略）与快照序列化（id 排序稳定、收窄到协议字段）。
// 与 mind.SceneStore.Replay 是同一 schema 契约的两端实现。
func TestSceneSurfacesFoldAndSnapshot(t *testing.T) {
	sv := &sceneSurfaces{surfaces: map[string]map[string]any{}}
	line := func(fields string) []byte {
		return []byte(`{"step_id":"s1","type":"scene","ts":"2026-09-28T10:00:00.000Z","` + fields + `}`)
	}

	if f := sv.applyScene(line(`op":"upsert","id":"b","kind":"text","intent":"ambient","summary":"B 卡","data":{"body":"b"}`)); f == nil {
		t.Fatal("upsert 应产生 patch 帧")
	} else if f.event != "scene.patch" {
		t.Fatalf("帧事件 = %q", f.event)
	}
	if f := sv.applyScene(line(`op":"upsert","id":"a","kind":"text","summary":"A 卡"`)); f == nil {
		t.Fatal("upsert 应产生 patch 帧")
	}
	if f := sv.applyScene([]byte(`{"step_id":"s2","type":"message","content":"x"}`)); f != nil {
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
	// id 排序稳定：a 在前。
	if parsed.State.Surfaces[0]["id"] != "a" {
		t.Fatalf("快照应按 id 排序: %s", snap)
	}
	// 信封字段收窄：step_id/ts/type 不进投影。
	if _, ok := parsed.State.Surfaces[0]["step_id"]; ok {
		t.Fatalf("信封字段不应进投影: %s", snap)
	}
	// data 轨迹里是 JSON 字符串，投影端解码为对象（前端按对象读）。
	if _, ok := parsed.State.Surfaces[1]["data"].(map[string]any); !ok {
		t.Fatalf("data 应解码为对象: %s", snap)
	}

	// remove：卡片消失，patch 带 op=remove 与 type 字段。
	frm := sv.applyScene(line(`op":"remove","id":"a"`))
	if frm == nil || frm.event != "scene.patch" {
		t.Fatal("remove 应产生 patch 帧")
	}
	if !strings.Contains(frm.data, `"type":"scene.patch"`) {
		t.Fatalf("patch 帧应带 type 字段: %s", frm.data)
	}
	snap = sv.snapshot()
	if err := json.Unmarshal([]byte(snap), &parsed); err != nil {
		t.Fatal(err)
	}
	if len(parsed.State.Surfaces) != 1 || parsed.State.Surfaces[0]["id"] != "b" {
		t.Fatalf("remove 后快照不符: %s", snap)
	}
}

// TestWatcherScenePrimeReplaysHistory：观察器创建时必须从轨迹回放
// 既有 scene 步骤——"尾随从 EOF 起步、历史不重放"的通例对 scene
// 不成立（scene 的当前状态就存在轨迹里），否则先写卡后订阅的客户
// 端永远拿不到它。真实事故：S17 验收中 CLI 先写卡、浏览器后订阅，
// 快照帧为空，卡片永不上屏。
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
