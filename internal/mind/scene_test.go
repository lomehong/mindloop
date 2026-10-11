package mind

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lomehong/mindloop/internal/traj"
)

func sceneStep(t *testing.T, tl *traj.Timeline, fields map[string]any) {
	t.Helper()
	s := traj.NewStep(traj.TypeScene)
	for k, v := range fields {
		s.Fields[k] = v
	}
	if err := tl.Append(context.Background(), s); err != nil {
		t.Fatal(err)
	}
}

// TestSceneStoreReplayAndIdempotent：轨迹 replay 重建场景；同 id 同
// 内容的 Upsert 判重（changed=false）；内容变化才 changed。折叠语义
// 与 web 投影端同一 schema 契约。
func TestSceneStoreReplayAndIdempotent(t *testing.T) {
	tl := newTestTimeline(t)
	sceneStep(t, tl, map[string]any{"op": "upsert", "id": "weather", "kind": "text",
		"intent": "ambient", "summary": "北京 18°", "data": `{"body":"晴"}`})
	sceneStep(t, tl, map[string]any{"op": "upsert", "id": "weather", "kind": "text",
		"intent": "ambient", "summary": "北京 21°", "data": `{"body":"多云"}`})
	sceneStep(t, tl, map[string]any{"op": "remove", "id": "ghost"})

	steps, err := tl.Steps()
	if err != nil {
		t.Fatal(err)
	}
	store := NewSceneStore()
	store.Replay(steps)

	// 与最新步骤相同的内容：无变化。
	changed, err := store.Upsert(Surface{ID: "weather", Kind: "text", Intent: "ambient",
		Summary: "北京 21°", Data: json.RawMessage(`{"body":"多云"}`)})
	if err != nil || changed {
		t.Fatalf("同内容 Upsert 应判重: changed=%v err=%v", changed, err)
	}
	// 内容变化：changed。
	changed, err = store.Upsert(Surface{ID: "weather", Kind: "text", Intent: "inform",
		Summary: "北京 21°", Data: json.RawMessage(`{"body":"多云"}`)})
	if err != nil || !changed {
		t.Fatalf("intent 变化应判为变更: changed=%v err=%v", changed, err)
	}

	// Manifest：一行式清单含 id/kind/summary/intent，不含 data 全文。
	m := store.Manifest()
	for _, want := range []string{"当前界面卡片", "(text)", "北京 21°", "inform"} {
		if !strings.Contains(m, want) {
			t.Fatalf("manifest 缺 %q:\n%s", want, m)
		}
	}
	if strings.Contains(m, `"body"`) || strings.Contains(m, "多云") {
		t.Fatalf("manifest 不应回注 data 全文:\n%s", m)
	}

	// Remove 幂等。
	if store.Remove("ghost") {
		t.Fatal("remove 不存在的卡不应 changed")
	}
}

// TestSceneStoreDensityAndValidation：密度上限 8 张；intent 词表校验；
// data 超限拒绝。
func TestSceneStoreDensityAndValidation(t *testing.T) {
	store := NewSceneStore()
	for i := 0; i < sceneMaxSurfaces; i++ {
		_, err := store.Upsert(Surface{ID: strings.Repeat("c", i+1), Kind: "text", Summary: "s"})
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.Upsert(Surface{ID: "overflow", Kind: "text", Summary: "s"}); err == nil {
		t.Fatal("超密度上限应拒绝")
	}
	if _, err := store.Upsert(Surface{ID: "bad", Kind: "text", Intent: "urgent", Summary: "s"}); err == nil {
		t.Fatal("非法 intent 应拒绝")
	}
	if _, err := store.Upsert(Surface{ID: "big", Kind: "text", Summary: "s",
		Data: json.RawMessage(`"` + strings.Repeat("x", maxSceneDataBytes+1) + `"})`)}); err == nil {
		t.Fatal("data 超限应拒绝")
	}
	// 腾位后可再放。
	store.Remove(strings.Repeat("c", 1))
	if _, err := store.Upsert(Surface{ID: "overflow", Kind: "text", Summary: "s"}); err != nil {
		t.Fatalf("腾位后应可放置: %v", err)
	}
}

// TestWakeTaskIncludesSceneManifest：manifest 段进唤醒提示；scene
// 步骤原文（data 全文）不进——渲染排除 + manifest 段互为表里。
func TestWakeTaskIncludesSceneManifest(t *testing.T) {
	t.Setenv("MINDLOOP_HOME", t.TempDir())
	ctx := context.Background()
	tl, err := traj.Create(ctx, "scene-manifest")
	if err != nil {
		t.Fatal(err)
	}
	sceneStep(t, tl, map[string]any{"op": "upsert", "id": "weather-bj", "kind": "text",
		"intent": "ambient", "summary": "北京 18° 晴", "data": `{"body":"完整天气数据不应出现在提示里"}`})

	m := NewMonolith(MonolithOptions{
		Timeline: tl,
		Thinker:  &scriptThinker{responses: []string{fence(`FINAL="IDLE"`)}},
	}).(*monolith)
	prompt := m.wakeTask(ctx, "test-wake", Wake{})
	if !strings.Contains(prompt, "当前界面卡片") || !strings.Contains(prompt, "weather-bj") {
		t.Fatalf("唤醒提示缺场景 manifest:\n%.400s", prompt)
	}
	if strings.Contains(prompt, "完整天气数据不应出现在提示里") {
		t.Fatal("scene data 全文不应回注（红线：注入舞台上有什么，不注入长什么样）")
	}
}
