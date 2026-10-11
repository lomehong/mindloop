package mind

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/lomehong/mindloop/internal/mem"
	"github.com/lomehong/mindloop/internal/obs"
	"github.com/lomehong/mindloop/internal/traj"
)

// TestRecallAuditLedger：monolith 与 responder 的记忆检索都落召回
// 台账（usage/mem-recall.jsonl）——命中（hit_count/injected/hits）、
// 无命中（skipped=no_hits）、未启用（MemDir 空）三种路径的观测行为。
func TestRecallAuditLedger(t *testing.T) {
	t.Setenv("MINDLOOP_HOME", t.TempDir())
	ctx := context.Background()
	tl, err := traj.Create(ctx, "recall-audit")
	if err != nil {
		t.Fatal(err)
	}
	memDir := filepath.Join(t.TempDir(), "memories")
	store := mem.Store{Dir: memDir}
	if _, err := store.AddWith(ctx, "fact", "操作员住在杭州，喜欢西湖边跑步", mem.AddOpts{Source: "msg00000042"}); err != nil {
		t.Fatal(err)
	}
	// 轨迹带相关查询文本（monolith 的查询源是近期思维流）。
	s := traj.NewStep("observation")
	s.Fields["content"] = "杭州西湖今天的天气如何"
	if err := tl.Append(ctx, s); err != nil {
		t.Fatal(err)
	}

	m := NewMonolith(MonolithOptions{Timeline: tl, MemDir: memDir,
		Thinker: &scriptThinker{responses: []string{fence(`FINAL="IDLE"`)}},
	}).(*monolith)
	if lines := m.relatedMemories(Wake{}); len(lines) == 0 {
		t.Fatal("应有相关记忆命中")
	}

	// monolith 台账：命中形态。
	rows, err := obs.LoadRecall(tl.Dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Source != "monolith" || rows[0].Injected != 1 {
		t.Fatalf("monolith 台账不符: %+v", rows)
	}
	if rows[0].HitCount != 1 || len(rows[0].Hits) != 1 || rows[0].Hits[0].ID == "" {
		t.Fatalf("命中明细缺失: %+v", rows[0])
	}

	// responder 台账：同库检索（查询是人话本身）。
	r := NewResponder(ResponderOptions{Timeline: tl, SelfName: "ada",
		MemDir: memDir, Thinker: &scriptThinker{responses: []string{fence("好的")}}}).(*Responder)
	if r.relatedMemories("杭州的天气") == "" {
		t.Fatal("responder 应有命中")
	}
	rows, err = obs.LoadRecall(tl.Dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Source != "responder" {
		t.Fatalf("responder 台账缺失: %+v", rows)
	}

	// 无命中路径：不相关查询 → skipped=no_hits。
	if r.relatedMemories("完全无关的量子物理问题") != "" {
		t.Fatal("无关查询应无命中")
	}
	rows, _ = obs.LoadRecall(tl.Dir, 10)
	if rows[0].Skipped != "no_hits" {
		t.Fatalf("无命中应记 skipped=no_hits: %+v", rows[0])
	}
}
