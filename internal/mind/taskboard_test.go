package mind

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/lomehong/mindloop/internal/ids"
	"github.com/lomehong/mindloop/internal/task"
)

// TestTaskBoardProjection：唤醒上下文携带任务看板（roadmap §2.4 的
// "计划即轨迹"投影）：未结任务在前、最近结束在后；短 id 可回查。
// 看板必须不含任何任务内容——自主上下文不得重放未授权要求
// （TestAutonomousContextExcludesChatTasksAndLegacyRecap 钉的安全不变量）。
func TestTaskBoardProjection(t *testing.T) {
	ctx := context.Background()
	tl := newTestTimeline(t)
	store := task.New(tl, "ada")

	// 一条走到终态（claim → advance succeeded）+ 一条排队。
	done, err := store.Submit(ctx, task.Submission{From: "operator", ClientMessageID: "m1", Content: "整理上季度销售报表"})
	if err != nil {
		t.Fatal(err)
	}
	running, err := store.Claim(ctx, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Advance(ctx, running.ID, task.Event{State: task.Succeeded, Attempt: running.Attempt, RunID: running.RunID, Result: "done"}); err != nil {
		t.Fatal(err)
	}
	queued, err := store.Submit(ctx, task.Submission{From: "operator", ClientMessageID: "m2", Content: "修复构建失败"})
	if err != nil {
		t.Fatal(err)
	}

	m := NewMonolith(MonolithOptions{Timeline: tl, SelfName: "ada"}).(*monolith)
	board := m.taskBoard(ctx)
	if board == "" {
		t.Fatal("有任务时应渲染看板")
	}
	if !strings.Contains(board, "[queued] "+ids.Short(queued.ID, 8)) {
		t.Fatalf("未结任务应带短 id: %q", board)
	}
	// 内容无关：任务标题是未授权要求，不得随状态投影回流自主上下文。
	for _, leaked := range []string{"整理上季度销售报表", "修复构建失败"} {
		if strings.Contains(board, leaked) {
			t.Fatalf("看板泄漏任务内容 %q: %q", leaked, board)
		}
	}
	if !strings.Contains(board, "最近结束") || !strings.Contains(board, "[succeeded] "+ids.Short(done.ID, 8)) {
		t.Fatalf("终态任务应进最近结束: %q", board)
	}
	// 唤醒任务文本携带看板段（注入点在 wakeTask）。
	wake := m.wakeTask(ctx, "test-wake", Wake{})
	if !strings.Contains(wake, "任务看板") {
		t.Fatalf("唤醒上下文应含任务看板: %.200q", wake)
	}
	if strings.Contains(wake, "整理上季度销售报表") || strings.Contains(wake, "修复构建失败") {
		t.Fatalf("唤醒上下文泄漏任务内容: %.400q", wake)
	}
}

// TestTaskBoardEmptyOmitted：无任务时看板整段不进唤醒上下文——
// 空看板是噪音，不该在每次唤醒里固化提示。
func TestTaskBoardEmptyOmitted(t *testing.T) {
	ctx := context.Background()
	tl := newTestTimeline(t)
	m := NewMonolith(MonolithOptions{Timeline: tl, SelfName: "ada"}).(*monolith)
	if board := m.taskBoard(ctx); board != "" {
		t.Fatalf("无任务应返回空看板，得 %q", board)
	}
	if wake := m.wakeTask(ctx, "test-wake", Wake{}); strings.Contains(wake, "任务看板") {
		t.Fatalf("无任务时唤醒上下文不该出现看板: %.200q", wake)
	}
}

// TestTaskBoardBounds：未结列表有界（12 条 + 溢出指示），积压不撑爆上下文。
func TestTaskBoardBounds(t *testing.T) {
	ctx := context.Background()
	tl := newTestTimeline(t)
	store := task.New(tl, "ada")
	for i := 0; i < 14; i++ {
		if _, err := store.Submit(ctx, task.Submission{From: "operator", ClientMessageID: fmt.Sprintf("m%d", i), Content: fmt.Sprintf("待办事项 %d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	m := NewMonolith(MonolithOptions{Timeline: tl, SelfName: "ada"}).(*monolith)
	board := m.taskBoard(ctx)
	if got := strings.Count(board, "[queued]"); got != taskBoardOpenMax {
		t.Fatalf("未结应列 %d 条，得 %d: %q", taskBoardOpenMax, got, board)
	}
	if !strings.Contains(board, "另有 2 条未结") {
		t.Fatalf("溢出应有指示: %q", board)
	}
}
