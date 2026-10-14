package mind

// TTL 清扫的纯逻辑测试（theater.md §16.2）：ambient 超龄驱逐、其他
// intent 豁免、remove 出列、重发刷新时效、按 id 排序稳定。

import (
	"context"
	"testing"
	"time"

	"github.com/lomehong/mindloop/internal/traj"
)

func sceneStepAt(op, id, intent string, at time.Time) traj.Step {
	s := traj.NewStep(traj.TypeScene)
	s.TS = at.Format(time.RFC3339)
	s.Fields["op"] = op
	s.Fields["id"] = id
	if op != "remove" {
		s.Fields["kind"] = "text"
		s.Fields["intent"] = intent
		s.Fields["summary"] = id
	} else {
		s.Fields["exit_reason"] = "ttl"
	}
	return s
}

func TestExpiredSceneIDs(t *testing.T) {
	base := time.Date(2026, 10, 13, 10, 0, 0, 0, time.UTC)
	steps := []traj.Step{
		sceneStepAt("upsert", "old-ambient", "ambient", base.Add(-2*time.Hour)),
		sceneStepAt("upsert", "fresh-ambient", "ambient", base.Add(-5*time.Minute)),
		sceneStepAt("upsert", "inform-card", "inform", base.Add(-2*time.Hour)),
		sceneStepAt("upsert", "confront-card", "confront", base.Add(-2*time.Hour)),
		sceneStepAt("upsert", "gone", "ambient", base.Add(-3*time.Hour)),
		sceneStepAt("remove", "gone", "", base.Add(-1*time.Hour)),
	}
	got := ExpiredSceneIDs(steps, base, time.Hour)
	// TTL 作用域只有 ambient（inform/confront 豁免）；removed 出列。
	if len(got) != 1 || got[0] != "old-ambient" {
		t.Fatalf("过期集应仅含 old-ambient: %v", got)
	}
}

func TestExpiredSceneIDsRefreshOnResend(t *testing.T) {
	base := time.Date(2026, 10, 13, 10, 0, 0, 0, time.UTC)
	steps := []traj.Step{
		sceneStepAt("upsert", "a", "ambient", base.Add(-2*time.Hour)),
		sceneStepAt("upsert", "a", "ambient", base.Add(-10*time.Minute)), // 重发刷新时效
	}
	if got := ExpiredSceneIDs(steps, base, time.Hour); len(got) != 0 {
		t.Fatalf("重发应刷新时效: %v", got)
	}
}

func TestSceneTTLSweeperWritesRemove(t *testing.T) {
	t.Setenv("MINDLOOP_HOME", t.TempDir())
	tl, err := traj.Create(context.Background(), "sweep-test")
	if err != nil {
		t.Fatal(err)
	}
	if err := AppendSceneStep(tl, map[string]any{
		"op": "upsert", "id": "stale", "kind": "text", "intent": "ambient", "summary": "旧卡",
	}); err != nil {
		t.Fatal(err)
	}
	// 步骤 TS 由 Append 落当前时刻——未来视角下即超龄。
	steps, err := tl.Steps()
	if err != nil {
		t.Fatal(err)
	}
	ids := ExpiredSceneIDs(steps, time.Now().Add(2*time.Hour), time.Hour)
	if len(ids) != 1 || ids[0] != "stale" {
		t.Fatalf("未来视角下 stale 应超龄: %v", ids)
	}
}
