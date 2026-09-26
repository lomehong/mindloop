package mind

import (
	"context"
	"errors"
	"fmt"
	"time"

	"mindloop/internal/llm"
	"mindloop/internal/runner"
	"mindloop/internal/task"
	"mindloop/internal/traj"
)

// durableThinker 从持久事实恢复并提供待办；通知只加速，不持有任务事实。
type durableThinker interface {
	Recover(context.Context) error
	Pending(context.Context) (Wake, bool, error)
}

func (m *monolith) taskStore() *task.Store {
	return task.New(m.opts.Timeline, m.fromName())
}

func (m *monolith) Recover(ctx context.Context) error {
	return m.taskStore().Recover(ctx)
}

func (m *monolith) Pending(ctx context.Context) (Wake, bool, error) {
	all, err := m.taskStore().List(ctx)
	if err != nil {
		return Wake{}, false, err
	}
	for _, item := range all {
		if item.State == task.Running || item.State == task.AwaitingApproval || item.State == task.Canceling {
			return Wake{}, false, nil
		}
	}
	for _, item := range all {
		if item.State == task.Queued {
			step := traj.NewStep("task-wake")
			step.Fields["task_id"] = item.ID
			return Wake{Kind: WakeTask, Step: step}, true, nil
		}
	}
	return Wake{}, false, nil
}

// defaultTaskCallBudget 是一个 task attempt 的默认模型调用尝试
// 上限（含重试）——成本防线，不是建议值。
const defaultTaskCallBudget = 20

// taskCallBudget 折算生效的任务调用预算：<=0 取默认 20。
func (m *monolith) taskCallBudget() int {
	if m.opts.TaskCallBudget > 0 {
		return m.opts.TaskCallBudget
	}
	return defaultTaskCallBudget
}

// executeTask 只执行领取到的当前 attempt；终态必须等 runner 真实退出。
func (m *monolith) executeTask(ctx context.Context, item task.Task) Outcome {
	store := m.taskStore()
	// 调用配额挂在 runCtx 上：runner 的每轮 Think（含重试）过账，
	// 耗尽时 llm 返回 ErrBudgetExceeded，runner 立即中止。
	quotaCtx := llm.WithCallQuota(ctx, llm.NewCallQuota(m.taskCallBudget()))
	// 归因：这是 monolith 的委托执行阶段（task/wake 分开记账）。
	quotaCtx = llm.WithAttrib(quotaCtx, llm.Attrib{Thinker: m.Name(), Phase: "task"})
	runCtx, cancel := context.WithCancelCause(quotaCtx)
	defer cancel(nil)
	watchDone, ready := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(watchDone)
		first := true
		tick := time.NewTicker(50 * time.Millisecond)
		defer tick.Stop()
		for {
			current, err := store.Get(runCtx, item.ID)
			if runCtx.Err() != nil {
				if first {
					close(ready)
				}
				return
			}
			if err == nil && (current.Attempt != item.Attempt || current.RunID != item.RunID || (current.State != task.Running && current.State != task.AwaitingApproval)) {
				err = errors.New("任务已取消或运行代次已失效")
			}
			if err != nil {
				cancel(err)
				if cancelWake, ok := ctx.Value(cancelWakeContextKey{}).(context.CancelCauseFunc); ok {
					cancelWake(err)
				}
			}
			if first {
				close(ready)
				first = false
			}
			if err != nil {
				return
			}
			select {
			case <-runCtx.Done():
				return
			case <-tick.C:
			}
		}
	}()
	<-ready

	thinker := m.opts.Thinker
	if m.opts.RequestThinker != nil {
		thinker = m.opts.RequestThinker
	}
	var result runner.Result
	var runErr error
	func() {
		defer func() {
			if r := recover(); r != nil {
				runErr = fmt.Errorf("任务执行 panic: %v", r)
			}
		}()
		result, runErr = runner.Run(runCtx, runner.Options{
			Timeline: m.opts.Timeline, Thinker: thinker, Task: item.Content,
			TaskID: item.ID, Attempt: item.Attempt, RunID: item.RunID,
			SystemPrompt: m.systemPrompt() + "\n\n当前为显式委托：只完成当前完整任务，不继续其他任务，也不将普通聊天视为授权。最终结果通过 FINAL 回传。",
			LaunchedBy:   m.Name(), MaxIterations: m.opts.MaxIterations,
			Timeout: m.opts.Timeout, IdleTimeout: m.opts.IdleTimeout,
			MaxOutputBytes: m.opts.MaxOutputBytes, ExtraEnv: m.opts.ExtraEnv,
			BeforeExecute: m.opts.BeforeExecute,
		})
	}()
	if runCtx.Err() != nil {
		runErr = context.Cause(runCtx)
	}
	cancel(nil)
	<-watchDone

	// 已退出后才写终态。写失败保持原 active 状态，后续任务不能越过它。
	finishCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer stop()
	ev := task.Event{State: task.Succeeded, Attempt: item.Attempt, RunID: item.RunID,
		Result: result.Final, ResultKind: result.FinalKind, EvidenceStepIDs: result.EvidenceStepIDs}
	if runErr != nil {
		ev.State, ev.Reason = task.Failed, runErr.Error()
		ev.Result, ev.ResultKind, ev.EvidenceStepIDs = "", "", nil
		// 预算类拒绝（任务调用配额 / 身份每日预算）落 budget_exceeded；
		// 熔断冷却属于瞬时失败（可重试），保持 failed。
		if errors.Is(runErr, llm.ErrBudgetExceeded) || errors.Is(runErr, llm.ErrDailyBudget) {
			ev.State = task.BudgetExceeded
		}
		if ctx.Err() != nil {
			ev.State = task.Interrupted
		}
	}
	settled, err := settleTask(finishCtx, store, item, ev)
	if err != nil {
		return Outcome{Note: "任务终态落盘失败，保留执行占用: " + err.Error()}
	}
	return Outcome{Note: fmt.Sprintf("任务 %s attempt %d：%s", settled.ID, settled.Attempt, settled.State)}
}

// settleTask 让先落盘的取消请求胜过完成；代次检查拒绝旧运行迟归。
func settleTask(ctx context.Context, store *task.Store, item task.Task, ev task.Event) (task.Task, error) {
	for i := 0; i < 2; i++ {
		current, err := store.Get(ctx, item.ID)
		if err != nil {
			return task.Task{}, err
		}
		if current.Attempt != item.Attempt || current.RunID != item.RunID {
			return task.Task{}, task.ErrTransition
		}
		if current.State == task.Canceling {
			ev.State, ev.Reason = task.Canceled, "取消已确认，执行已退出"
			ev.Result, ev.ResultKind, ev.EvidenceStepIDs = "", "", nil
		}
		settled, err := store.Advance(ctx, item.ID, ev)
		if !errors.Is(err, task.ErrTransition) {
			return settled, err
		}
	}
	return task.Task{}, task.ErrTransition
}
