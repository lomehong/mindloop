package web

import (
	"encoding/json"
	"testing"
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
		Rev      int              `json:"rev"`
		Surfaces []map[string]any `json:"surfaces"`
	}
	if err := json.Unmarshal([]byte(snap), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Rev != 2 || len(parsed.Surfaces) != 2 {
		t.Fatalf("快照不符: %s", snap)
	}
	// id 排序稳定：a 在前。
	if parsed.Surfaces[0]["id"] != "a" {
		t.Fatalf("快照应按 id 排序: %s", snap)
	}
	// 信封字段收窄：step_id/ts/type 不进投影。
	if _, ok := parsed.Surfaces[0]["step_id"]; ok {
		t.Fatalf("信封字段不应进投影: %s", snap)
	}

	// remove：卡片消失，patch 带 op=remove。
	if f := sv.applyScene(line(`op":"remove","id":"a"`)); f == nil || f.event != "scene.patch" {
		t.Fatal("remove 应产生 patch 帧")
	}
	snap = sv.snapshot()
	if err := json.Unmarshal([]byte(snap), &parsed); err != nil {
		t.Fatal(err)
	}
	if len(parsed.Surfaces) != 1 || parsed.Surfaces[0]["id"] != "b" {
		t.Fatalf("remove 后快照不符: %s", snap)
	}
}
