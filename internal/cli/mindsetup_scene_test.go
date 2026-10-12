package cli

// sceneApprovalHook（scene.md §6 审批卡桌面化）的直接单测：审批卡的
// 生产者——进入审批等待发 confront choice 卡（surface=approve-<hash8>，
// context 带脚本全文，供操作员知情决策），离开等待撤卡（批准/拒绝/
// 超时/取消共用同一离开回调）。此前只有下游 intent→Decide 有测试，
// 发卡端是空白。

import (
	"context"
	"strings"
	"testing"

	"github.com/lomehong/mindloop/internal/identity"
	"github.com/lomehong/mindloop/internal/policy"
	"github.com/lomehong/mindloop/internal/runner"
	"github.com/lomehong/mindloop/internal/traj"
)

func TestSceneApprovalHookUpsertsAndRemoves(t *testing.T) {
	newTestHome(t)
	id, err := identity.Create(context.Background(), "ada")
	if err != nil {
		t.Fatal(err)
	}
	tl := id.Timeline
	script := "echo hello"
	ex := runner.Execution{Script: script, WorkDir: ".", RunID: "run-hook", TaskID: "task-hook", Attempt: 1}
	wantID := "approve-" + policy.ScriptHash(script)[:8]

	hook := sceneApprovalHook(tl, nil)

	// 进入等待：发 confront choice 卡。
	hook(ex, true)
	steps, err := tl.Steps()
	if err != nil {
		t.Fatal(err)
	}
	var card *traj.Step
	for i := range steps {
		if steps[i].Type == traj.TypeScene {
			card = &steps[i]
		}
	}
	if card == nil {
		t.Fatal("等待审批应落 scene upsert 步骤")
	}
	for field, want := range map[string]string{
		"op":     "upsert",
		"id":     wantID,
		"kind":   "choice",
		"intent": "confront",
		"focus":  "true",
	} {
		if got, _ := card.Field(field); got != want {
			t.Fatalf("卡片字段 %s=%q，应为 %q", field, got, want)
		}
	}
	data, _ := card.Field("data")
	for _, want := range []string{`"approve"`, `"deny"`, script} {
		if !strings.Contains(data, want) {
			t.Fatalf("卡片 data 应含 %s: %s", want, data)
		}
	}

	// 离开等待（批准/拒绝/超时/取消任一结局）：撤卡。
	hook(ex, false)
	steps, err = tl.Steps()
	if err != nil {
		t.Fatal(err)
	}
	removed := false
	for _, s := range steps {
		if s.Type != traj.TypeScene {
			continue
		}
		if op, _ := s.Field("op"); op == "remove" {
			if sid, _ := s.Field("id"); sid == wantID {
				removed = true
			}
		}
	}
	if !removed {
		t.Fatal("离开等待应落 remove 步骤撤销同 id 卡片")
	}
}
