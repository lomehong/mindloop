package mind

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
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

// taskStore 惰性单例：Store 的投影缓存按实例持有（O(n²) 专项
// 2026-10-13），每次 task.New 都从零全量重放——Pending/executeTask/
// Recover 的高频调用会让缓存形同虚设。首个使用者负责构造，此后
// 全部走同一实例；并发安全由 Store 内部的 cacheMu 承担。
func (m *monolith) taskStore() *task.Store {
	m.storeOnce.Do(func() {
		m.store = task.New(m.opts.Timeline, m.fromName())
	})
	return m.store
}

func (m *monolith) Recover(ctx context.Context) error {
	return m.taskStore().Recover(ctx)
}

func (m *monolith) Pending(ctx context.Context) (Wake, bool, error) {
	all, err := m.taskStore().List(ctx)
	if err != nil {
		return Wake{}, false, err
	}
	// Super-agent 双层（2026-11-04）：槽位池决定并发度——有空槽
	// 就放行下一个排队任务（Worker 池），没有就等（Router 的队列）。
	// 在跑/等批/取消中的任务由 workers 跟踪（runWorker 的
	// Acquire/Release），不再用它阻塞全部新任务。防御性兜底：若
	// store 里有 Running 但槽位池没跟踪到（极端竞态窗口），仍阻塞。
	running := 0
	for _, item := range all {
		if item.State == task.Running || item.State == task.AwaitingApproval || item.State == task.Canceling {
			running++
		}
	}
	if running > m.workers.Count() {
		// store 报告的在跑数超过池的在飞数——有未被池跟踪的运行中
		// 任务（可能是旧进程的遗留），保守阻塞。
		return Wake{}, false, nil
	}
	if !m.workers.HasSlot() {
		return Wake{}, false, nil
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

// runWorker 是异步任务执行体（Super-agent Worker 角色，v2 fork 并行）：
// 在 fork 子轨迹里跑 runner（与根轨迹隔离——并发写安全），完成后
// merge 回根（单次原子追加），task 终态经 store 写根轨迹（store 的
// 事务机制自带串行化）。上下文 WithoutCancel——Worker 的生死不随
// 唤醒 ctx（Router 的某一次调度）结束而中止。
//
// v2 变更（2026-11-04 第二轮）：去掉 v1 的 workerMu 串行化互斥锁
// ——fork 隔离了 runner 的写路径，task store 只读根轨迹（fork 里的
// 步骤不进入根），并发写安全问题消除。真·任务并行（论文「高效」）。
func (m *monolith) runWorker(item task.Task) {
	defer m.workers.Release(item.ID)
	ctx := context.WithoutCancel(context.Background())

	// fork 子轨迹：runner 的全部步骤（reasoning/action/shell-output/
	// final）落在 fork 里，与根轨迹完全隔离。
	fork, err := traj.ForkAt(ctx, filepath.Dir(m.opts.Timeline.Dir),
		"worker-"+ids.Short(item.ID, 8), m.opts.Timeline)
	if err != nil {
		// fork 失败降级为根轨迹同步执行（保正确性优先）。
		m.executeTask(ctx, item, m.opts.Timeline)
		return
	}

	// 在 fork 上执行（监督轮询 + runner.Run + 终态经 store 写根轨迹）。
	m.executeTask(ctx, item, fork)

	// merge 回根：fork 的最终结论合并为一条 merge 步骤（monolith 已
	// 订阅 TypeMerge——「Worker 向 Router 汇报」的现成钩子）。失败
	// 不影响任务终态（已通过 store 落盘）——merge 是轨迹可见性增强。
	mergeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	content := fmt.Sprintf("worker %s attempt %d 完成（fork 轨迹 %s）",
		ids.Short(item.ID, 8), item.Attempt, ids.Short(fork.ID, 8))
	if _, err := m.opts.Timeline.Merge(mergeCtx, fork, content); err != nil {
		// best-effort：merge 失败只记日志语义（不中断）。
		_ = err
	}
}

// defaultTaskCallBudget 是一个 task attempt 的默认模型调用尝试
// 上限（含重试）——成本防线，不是建议值。16 轮任务 + 50% 重试余量。
const defaultTaskCallBudget = 24

// finalContractPrompt 是显式委托的收口契约：runner 对交付物做机械
// 核验（VerifyDeliverables），模型单方面宣告的完成不再被采信。
const finalContractPrompt = `收口契约（系统会机械核验，违者 FINAL 无效并打回）：
完成任务的当轮，写 .mindloop_final 文件收口，内容必须是 JSON：
{"summary":"一句话结果（过去式、有证据支撑）","deliverables":["交付文件相对路径",...]}
- deliverables 列全任务书"交付物"要求的所有文件（相对当前工作目录）；没有文件交付时给 []；
- 每个文件必须已实际落盘且非空。缺件、越出工作目录、格式不符都会被拒绝打回，白费轮次。
- 无文件交付的完整示例——.mindloop_final 文件内容恰好是这一行（JSON 前后不要有任何文字）：
  {"summary":"已推送测试卡片到仪表盘","deliverables":[]}`

func (m *monolith) taskCallBudget() int {
	if m.opts.TaskCallBudget > 0 {
		return m.opts.TaskCallBudget
	}
	return defaultTaskCallBudget
}

// executeTask 只执行领取到的当前 attempt；终态必须等 runner 真实退出。
// workTL 是 runner 的写入轨迹（v2 fork 并行：异步 Worker 传 fork、
// 同步路径传根——runner 步骤的落点由调用方决定，task 终态始终经
// store 写根轨迹）。
func (m *monolith) executeTask(ctx context.Context, item task.Task, workTL *traj.Timeline) Outcome {
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
	// 任务模式的系统提示 = 基础提示 + 界面 manifest + 显式委托契约。
	// manifest 与唤醒路径同一语义：scene 步骤已被渲染层排除，这一段
	// 是模型看到屏上卡片的唯一通道——任务里"更新/撤掉那张卡"的 id
	// 手柄来自这里，不注入则任务对既有卡片视而不见。
	taskSystem := m.systemPrompt(item.Content, Wake{})
	if scene := m.sceneManifest(); scene != "" {
		taskSystem += "\n\n" + scene
	}
	taskSystem += "\n\n当前为显式委托：只完成当前完整任务，不继续其他任务，也不将普通聊天视为授权。\n" + finalContractPrompt
	func() {
		defer func() {
			if r := recover(); r != nil {
				runErr = fmt.Errorf("任务执行 panic: %v", r)
			}
		}()
		result, runErr = runner.Run(runCtx, runner.Options{
			Timeline: workTL, Thinker: thinker, Task: item.Content,
			TaskID: item.ID, Attempt: item.Attempt, RunID: item.RunID,
			SystemPrompt: taskSystem,
			LaunchedBy:   m.Name(), MaxIterations: m.opts.TaskMaxIterations,
			Timeout: m.opts.Timeout, IdleTimeout: m.opts.IdleTimeout,
			MaxOutputBytes: m.opts.MaxOutputBytes, ExtraEnv: m.opts.ExtraEnv,
			Snapshots:     m.opts.Snapshots,
			BeforeExecute: m.opts.BeforeExecute,
			// 完成线机械验收：任务书的验收标准由 runner 核验交付物，
			// 不再采信模型单方面宣告的完成。
			VerifyDeliverables: true,
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
		Result: result.Final, ResultKind: result.FinalKind, EvidenceStepIDs: result.EvidenceStepIDs,
		Deliverables: result.Deliverables}
	if runErr != nil {
		ev.State, ev.Reason = task.Failed, runErr.Error()
		ev.Result, ev.ResultKind, ev.EvidenceStepIDs, ev.Deliverables = "", "", nil, nil
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
			ev.Result, ev.ResultKind, ev.EvidenceStepIDs, ev.Deliverables = "", "", nil, nil
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
