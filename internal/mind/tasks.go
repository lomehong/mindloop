package mind

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/lomehong/mindloop/internal/ids"
	"github.com/lomehong/mindloop/internal/llm"
	"github.com/lomehong/mindloop/internal/runner"
	"github.com/lomehong/mindloop/internal/task"
	"github.com/lomehong/mindloop/internal/traj"
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
		// 取消/代次失效的监督轮询：每次都是全量投影重放，代价随轨迹
		// 增长——250ms 的取消传播延迟远低于模型轮次的量级，不再用
		// 50ms 的密度把读取叠成锁占用。
		tick := time.NewTicker(250 * time.Millisecond)
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
			Snapshots:     m.opts.Snapshots,
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

// 任务看板的渲染边界：条数与字节双上限，防止积压把唤醒上下文撑爆
// （超出留指示，完整列表随时经 task list 取回）。
const (
	taskBoardBytes   = 2048
	taskBoardOpenMax = 12
	taskBoardDoneMax = 3
)

// taskBoard 渲染任务队列的状态投影（roadmap §2.4"计划即轨迹"的落地
// 形态）：未结在前（提交序=FIFO 优先级）、最近结束少量在后，只列
// 状态/id/时间——自主上下文不得重放聊天或任务内容（未授权要求不得
// 回流为执行线索），内容只在领取后的执行上下文里出现。
// 无任务返回空串——空看板不进唤醒上下文。读失败静默返回空：看板是
// 增味不是事实源，而主路径的 Claim 已校验过投影可读性。
func (m *monolith) taskBoard(ctx context.Context) string {
	all, err := m.taskStore().List(ctx)
	if err != nil || len(all) == 0 {
		return ""
	}
	var open, done []task.Task
	for _, t := range all {
		if terminalState(t.State) {
			done = append(done, t)
		} else {
			open = append(open, t)
		}
	}
	var b strings.Builder
	b.WriteString("任务看板（任务队列的状态投影；详情用 \"$MINDLOOP_EXE\" task list）：")
	for i, t := range open {
		if i >= taskBoardOpenMax {
			fmt.Fprintf(&b, "\n- …另有 %d 条未结", len(open)-i)
			break
		}
		b.WriteString("\n- ")
		b.WriteString(taskBoardLine(t, false))
	}
	if len(done) > 0 {
		// UpdatedAt 同源同格式（轨迹步骤时间戳），字典序即时间序。
		sort.Slice(done, func(i, j int) bool { return done[i].UpdatedAt > done[j].UpdatedAt })
		if len(done) > taskBoardDoneMax {
			done = done[:taskBoardDoneMax]
		}
		b.WriteString("\n最近结束：")
		for _, t := range done {
			b.WriteString("\n- ")
			b.WriteString(taskBoardLine(t, true))
		}
	}
	return b.String()
}

// terminalState 是看板对"已结束"的定义（与 task 包的终态集合一致）。
func terminalState(s task.State) bool {
	switch s {
	case task.Succeeded, task.Failed, task.Canceled, task.Interrupted, task.BudgetExceeded:
		return true
	}
	return false
}

// taskBoardLine 渲染单条：状态、短 id、相对时间；重试过的标注 attempt。
func taskBoardLine(t task.Task, ended bool) string {
	ts := t.CreatedAt
	if ended {
		ts = t.UpdatedAt
	}
	line := fmt.Sprintf("[%s] %s", t.State, ids.Short(t.ID, 8))
	if t.Attempt > 1 {
		line += fmt.Sprintf(" attempt %d", t.Attempt)
	}
	if age := ageText(ts); age != "" {
		line += "（" + age + "）"
	}
	return line
}

// ageText 把轨迹时间戳折算成人话相对时间。
func ageText(ts string) string {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "刚刚"
	case d < time.Hour:
		return fmt.Sprintf("%d 分钟前", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d 小时前", int(d.Hours()))
	default:
		return fmt.Sprintf("%d 天前", int(d.Hours()/24))
	}
}
