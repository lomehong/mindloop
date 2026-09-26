package mind

import (
	"context"
	"errors"
	"strings"
	"testing"

	"mindloop/internal/llm"
	"mindloop/internal/task"
)

// budgetProbeThinker 构造"配额探针"：在单次 Think 里连续消费 ctx 上
// 的调用配额直到被拒，把拿到的额度写进 granted 并返回被拒错误——
// 不依赖真实沙箱就能验证"配额挂上了 ctx、额度正确、被拒错误映射
// 到 budget_exceeded"的完整链路。
func budgetProbeThinker(granted *int) taskModelFunc {
	return func(ctx context.Context, _ string, _ []llm.Message) (string, error) {
		for i := 0; i < 100 && llm.TakeCall(ctx) == nil; i++ {
			*granted++
		}
		return "", llm.ErrBudgetExceeded
	}
}

func budgetGuardThinker(t *testing.T) taskModelFunc {
	t.Helper()
	return func(context.Context, string, []llm.Message) (string, error) {
		return "", errors.New("显式任务不应使用思考档")
	}
}

// TestExecuteTaskCallBudgetDefault：任务 attempt 的 ctx 上挂着默认
// 20 次的调用配额；ErrBudgetExceeded 经 runner 包装后映射为
// budget_exceeded 状态（runner 对 Think 错误立即中止，不空转轮次
// ——否则会走到轮次耗尽，状态就不是预算语义了）。
func TestExecuteTaskCallBudgetDefault(t *testing.T) {
	tl := newTestTimeline(t)
	s := task.New(tl, "ada")
	submitted := submitTask(t, s, "budget-default")

	granted := 0
	m := NewMonolith(MonolithOptions{Timeline: tl, SelfName: "ada",
		Thinker:        budgetGuardThinker(t),
		RequestThinker: budgetProbeThinker(&granted),
	})
	m.Wake(context.Background(), Wake{Step: syntheticStep(monolithWakeType), Kind: WakeScheduled})

	if granted != 20 {
		t.Fatalf("默认任务调用配额 = %d，应为 20", granted)
	}
	got := readTask(t, s, submitted.ID)
	if got.State != task.BudgetExceeded {
		t.Fatalf("状态 = %s，应为 budget_exceeded", got.State)
	}
	if !strings.Contains(got.Reason, "预算") {
		t.Fatalf("原因应指明预算耗尽: %q", got.Reason)
	}
}

// TestExecuteTaskCallBudgetOverride：MonolithOptions 显式配额覆盖默认。
func TestExecuteTaskCallBudgetOverride(t *testing.T) {
	tl := newTestTimeline(t)
	s := task.New(tl, "ada")
	submitted := submitTask(t, s, "budget-override")

	granted := 0
	m := NewMonolith(MonolithOptions{Timeline: tl, SelfName: "ada", TaskCallBudget: 3,
		Thinker:        budgetGuardThinker(t),
		RequestThinker: budgetProbeThinker(&granted),
	})
	m.Wake(context.Background(), Wake{Step: syntheticStep(monolithWakeType), Kind: WakeScheduled})

	if granted != 3 {
		t.Fatalf("显式配额 = %d，应为 3", granted)
	}
	if got := readTask(t, s, submitted.ID); got.State != task.BudgetExceeded {
		t.Fatalf("状态 = %s，应为 budget_exceeded", got.State)
	}
}

// TestExecuteTaskDailyBudgetMapsBudgetExceeded：身份每日 token 预算
// 拒绝新请求时，任务落 budget_exceeded（与调用配额同一状态语义）。
func TestExecuteTaskDailyBudgetMapsBudgetExceeded(t *testing.T) {
	tl := newTestTimeline(t)
	s := task.New(tl, "ada")
	submitted := submitTask(t, s, "daily")

	m := NewMonolith(MonolithOptions{Timeline: tl, SelfName: "ada",
		Thinker: budgetGuardThinker(t),
		RequestThinker: taskModelFunc(func(context.Context, string, []llm.Message) (string, error) {
			return "", llm.ErrDailyBudget
		}),
	})
	m.Wake(context.Background(), Wake{Step: syntheticStep(monolithWakeType), Kind: WakeScheduled})

	got := readTask(t, s, submitted.ID)
	if got.State != task.BudgetExceeded {
		t.Fatalf("状态 = %s，应为 budget_exceeded（reason=%s）", got.State, got.Reason)
	}
	if !strings.Contains(got.Reason, "每日") {
		t.Fatalf("原因应指明每日预算: %q", got.Reason)
	}
}

// TestExecuteTaskCircuitOpenMapsFailed：熔断冷却拒绝新请求是瞬时
// 失败（可重试），不是预算耗尽——状态落 failed，原因里带出来。
func TestExecuteTaskCircuitOpenMapsFailed(t *testing.T) {
	tl := newTestTimeline(t)
	s := task.New(tl, "ada")
	submitted := submitTask(t, s, "circuit")

	m := NewMonolith(MonolithOptions{Timeline: tl, SelfName: "ada",
		Thinker: budgetGuardThinker(t),
		RequestThinker: taskModelFunc(func(context.Context, string, []llm.Message) (string, error) {
			return "", llm.ErrCircuitOpen
		}),
	})
	m.Wake(context.Background(), Wake{Step: syntheticStep(monolithWakeType), Kind: WakeScheduled})

	got := readTask(t, s, submitted.ID)
	if got.State != task.Failed {
		t.Fatalf("状态 = %s，应为 failed（reason=%s）", got.State, got.Reason)
	}
	if !strings.Contains(got.Reason, "熔断") {
		t.Fatalf("原因应指明熔断冷却: %q", got.Reason)
	}
}
