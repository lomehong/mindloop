// Package mind 是持久心智的调度器与思考者——Headlong 的
// bin/thinkers + monolith 的 Go 对应物，但形态不同：Headlong 用
// 进程 + FIFO + tail -F 拼出一个操作系统；Go 给了真正的并发原语，
// 所以调度器是一个进程内的服务，背压、看门狗、自发性调度照搬，
// FIFO 文件队列变成内存队列。
//
// 两条从 Headlong 事故里学来的铁律被保留为结构性质：
//   - 活性由调度器保证，不依赖思考者代码路径：watchdog 合成唤醒
//     会治愈任何"思考者忘了预约下次唤醒"的断链。
//   - 调度主循环绝不阻塞：思考者在自己的 goroutine 里跑，即使
//     全部占满，feeder、watchdog、调度照常心跳（Headlong 曾在
//     并发上限处阻塞主循环，那正是它自己的事故清单成员）。
package mind

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"mindloop/internal/traj"
)

// WakeKind 描述一次唤醒的来源。
type WakeKind string

const (
	// WakeStep：由轨迹上的真实步骤触发。
	WakeStep WakeKind = "step"
	// WakeWatchdog：watchdog 合成唤醒——活性兜底。
	WakeWatchdog WakeKind = "watchdog"
	// WakeScheduled：思考者请求的未来唤醒到点（自发性）。
	WakeScheduled WakeKind = "scheduled"
	// WakeTask 来自持久化任务投影，不进入有界消息 FIFO。
	WakeTask WakeKind = "task"
)

// monolithWakeType 是调度器合成唤醒（watchdog/自发性）的步骤类型
// 名——只在内存构造（syntheticStep），不落盘；monolith 的订阅面引
// 用同一个常量，两处字面量漂移会让合成唤醒永远投不进去。
const monolithWakeType = "monolith-wake"

// Wake 是交给思考者的一次唤醒。
type Wake struct {
	// Step 是触发步骤；合成唤醒时是内存构造的步骤，不落盘。
	Step traj.Step
	Kind WakeKind
}

// Subscription 描述思考者的订阅。
type Subscription struct {
	// Types 是关心的步骤类型（精确匹配）。
	Types []string
	// TriggerSelf 为 false 时，自己写下的步骤（launched_by 匹配）
	// 不触发自己——防回路的 Headlong 语义。
	TriggerSelf bool
	// Watchdog 是无活动多久后由调度器合成一次唤醒；0 表示不启用。
	// trigger-self 型思考者必须设置它——这是活性由调度器保证的
	// 机制。
	Watchdog time.Duration
}

// Outcome 是一次唤醒的结束报告。
type Outcome struct {
	// WantWake 表示请求未来的自发性唤醒（自发性是思考者向调度器
	// 预约的，而非自己起定时器——定时器会随思考者一起死去）。
	WantWake bool
	// NextWakeIn 是预约的延迟；调度器强制最小间隔防紧密自旋。
	NextWakeIn time.Duration
	Note       string
}

// Thinker 是被调度器管理的思考者。
type Thinker interface {
	Name() string
	Subscriptions() Subscription
	// Wake 处理一次唤醒。实现应当自行限制耗时；ctx 取消即停机。
	Wake(ctx context.Context, w Wake) Outcome
}

// worker 是调度器内一个思考者的全部状态。
type cancelWakeContextKey struct{}

type worker struct {
	t   Thinker
	sub Subscription

	mu        sync.Mutex
	busy      bool
	gen       uint64          // 取消推进代际以拒绝迟归结果，但不释放执行槽
	fifo      []Wake          // message 类：FIFO，保序投递
	coalesced map[string]Wake // 其余类型：last-wins 合并
	wakeAt    time.Time       // 思考者预约的自发性唤醒时刻
	lastUsed  time.Time       // 最近一次投递（watchdog 判据）
}

func (w *worker) enqueue(step traj.Step) {
	w.mu.Lock()
	defer w.mu.Unlock()
	wake := Wake{Step: step, Kind: WakeStep}
	if step.Type == traj.TypeMessage {
		// 人类消息：FIFO 保序、有界、丢最旧——排序是礼貌，上限是
		// 自保（Headlong 的 pending 目录同款参数）。
		const fifoCap = 16
		w.fifo = append(w.fifo, wake)
		if len(w.fifo) > fifoCap {
			w.fifo = w.fifo[1:]
		}
		return
	}
	// 自我唤醒类：last-wins 合并——自循环思考者永远不该在积压的
	// 过期自我唤醒里打转。
	w.coalesced[step.Type] = wake
}

// next 在锁内取出一批可投递的唤醒：FIFO 优先（人的消息先得到
// 空闲槽位），然后是合并槽。
func (w *worker) next() (Wake, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.fifo) > 0 {
		wake := w.fifo[0]
		w.fifo = w.fifo[1:]
		return wake, true
	}
	for typ, wake := range w.coalesced {
		delete(w.coalesced, typ)
		return wake, true
	}
	return Wake{}, false
}

// Dispatcher 监督一组思考者：tail 轨迹、路由步骤、执行背压策略、
// 维持 watchdog 与自发性唤醒。
type Dispatcher struct {
	tl     *traj.Timeline
	cursor *traj.Cursor
	poll   time.Duration
	evlog  *dispatchLogEvents
	tlDir  string
	logger func(format string, args ...any)

	// WakeTimeout 到点取消 context；尚未退出的 worker 标记为
	// quarantined 并继续占槽，直到真实退出或进程重启。
	// generation 只拒绝迟归结果，不是执行终止的证据。
	// 0 取默认 30 分钟，负数不设期限；停机取消始终有效。
	WakeTimeout time.Duration

	mu       sync.Mutex
	workers  []*worker
	inflight atomic.Int64 // 在途思考计数：WaitIdle 轮询它，不孵化监视 goroutine
	running  atomic.Bool

	// working 投影：忙集的文件化（<RunLockDir>/working）。记账点在
	// deliver（入集）与两个 busy 释放点（出集），转换处重写/删除
	// 状态文件——文件存在 ⇔ 有思考者正在工作。
	workingMu   sync.Mutex
	workingBusy []workingEntry
}

// NewDispatcher 构造一个调度器。poll 是轨迹轮询间隔（默认 200ms）。
func NewDispatcher(tl *traj.Timeline, poll time.Duration) *Dispatcher {
	if poll <= 0 {
		poll = 200 * time.Millisecond
	}
	return &Dispatcher{
		tl:     tl,
		cursor: traj.NewCursorAtEnd(tl.Path),
		poll:   poll,
		evlog:  newDispatchLog(tl.Dir),
		tlDir:  tl.Dir,
	}
}

// SetLogger 注入进度输出（人看的信息，进 stderr）。
func (d *Dispatcher) SetLogger(f func(format string, args ...any)) {
	d.logger = f
}

func (d *Dispatcher) logf(format string, args ...any) {
	if d.logger != nil {
		d.logger(format, args...)
	}
}

// Register 登记一个思考者。
func (d *Dispatcher) Register(t Thinker) {
	sub := t.Subscriptions()
	d.mu.Lock()
	defer d.mu.Unlock()
	d.workers = append(d.workers, &worker{
		t:         t,
		sub:       sub,
		coalesced: make(map[string]Wake),
		lastUsed:  time.Now(),
	})
}

// Run 运行调度器直到 ctx 取消、收到停机标志或 feeder 出错。
// 主循环只有非阻塞操作——心跳永不停摆。
func (d *Dispatcher) Run(ctx context.Context) error {
	if !d.running.CompareAndSwap(false, true) {
		return errors.New("mind: 调度器已在运行")
	}
	defer d.running.Store(false)
	if d.inflight.Load() != 0 {
		return errors.New("mind: 旧执行仍在途，禁止重新启动")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	d.mu.Lock()
	workers := append([]*worker(nil), d.workers...)
	d.mu.Unlock()
	for _, w := range workers {
		if durable, ok := w.t.(durableThinker); ok {
			if _, owned := ctx.Value(runLockContextKey{}).(*RunLock); !owned {
				return errors.New("mind: 持久任务恢复必须使用 RunOwned")
			}
			if err := durable.Recover(ctx); err != nil {
				return fmt.Errorf("mind: 恢复 %s 失败: %w", w.t.Name(), err)
			}
		}
	}
	// 启动清场：调用方持运行锁才会走到这里，此刻无调度器在跑，
	// stream/ 旁路与 replying 状态的残留必为崩溃垃圾（与
	// ClearStopFlag 的启动清理同模式）。
	gcTransients(d.tl.Dir)
	d.logf("调度器启动，跟踪 %s", d.tl.Path)
	tick := time.NewTicker(d.poll)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			d.logf("调度器停机")
			return ctx.Err()
		case <-tick.C:
			if d.checkStop() {
				d.logf("收到停机标志，调度器优雅退出")
				return ErrStopRequested
			}
			if err := d.step(ctx); err != nil {
				if errors.Is(err, ErrStopRequested) {
					d.logf("调度器优雅退出")
					return nil
				}
				return err
			}
		}
	}
}

// step 是一次心跳：控制面 → feeder → watchdog/自发性 → 投递。
func (d *Dispatcher) step(ctx context.Context) error {
	// 持久待办优先，锁外读取投影；没有通知也能处理停机期间的提交。
	d.mu.Lock()
	workers := append([]*worker(nil), d.workers...)
	d.mu.Unlock()
	disabled := readDisabled(d.tlDir)
	for _, w := range workers {
		if w.isBusy() {
			continue
		}
		skip := false
		for _, name := range disabled {
			if name == w.t.Name() {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		if durable, ok := w.t.(durableThinker); ok {
			wake, pending, err := durable.Pending(ctx)
			if err != nil {
				return fmt.Errorf("mind: 读取 %s 待办失败: %w", w.t.Name(), err)
			}
			if pending {
				d.deliver(ctx, w, wake)
			}
		}
	}
	// 0. 控制面：消费 web/CLI 发来的手动唤醒信号（读完即删）。
	// 忙碌的思考者不投递——信号写回文件下个心跳再消费，手动唤醒
	// 绝不被静默丢弃（monolith 依赖"同一思考者至多一次唤醒在跑"）。
	for _, name := range collectWakeSignals(d.tlDir) {
		d.mu.Lock()
		var target *worker
		for _, w := range d.workers {
			if w.t.Name() == name {
				target = w
				break
			}
		}
		d.mu.Unlock()
		if target == nil {
			continue
		}
		if target.isBusy() {
			_ = SignalWake(d.tlDir, name)
			continue
		}
		d.deliver(ctx, target, Wake{Step: syntheticStep("manual-wake"), Kind: WakeScheduled})
	}

	// 1. feeder：新步骤路由给订阅者。
	steps, err := d.cursor.ReadNew()
	if err != nil {
		return fmt.Errorf("mind: feeder: %w", err)
	}
	for _, s := range steps {
		d.route(s)
	}

	// 2. watchdog 与到点的自发性唤醒。活性窗口度量的是"空闲且
	// 安静"的时长（Headlong THINKERS_spec 的判据）：忙碌或还有
	// 排队工作的思考者不算安静——时钟持续刷新，长任务结束的
	// 瞬间不会立即触发补偿性 watchdog（那是浪费一次模型调用）。
	// 忙碌的思考者跳过投递：watchdog 到点预约保持原样，空闲后
	// 下个心跳补投。
	now := time.Now()
	d.mu.Lock()
	for _, w := range d.workers {
		w.mu.Lock()
		due := !w.wakeAt.IsZero() && !now.Before(w.wakeAt)
		idle := w.sub.Watchdog > 0 && now.Sub(w.lastUsed) >= w.sub.Watchdog
		busy := w.busy
		queued := len(w.fifo) > 0 || len(w.coalesced) > 0
		if busy || queued {
			w.lastUsed = now
		}
		w.mu.Unlock()
		if busy {
			continue
		}
		if due {
			w.mu.Lock()
			w.wakeAt = time.Time{}
			w.mu.Unlock()
			d.deliver(ctx, w, Wake{Step: syntheticStep(monolithWakeType), Kind: WakeScheduled})
			continue
		}
		if idle {
			w.mu.Lock()
			w.lastUsed = now
			w.mu.Unlock()
			d.deliver(ctx, w, Wake{Step: syntheticStep(monolithWakeType), Kind: WakeWatchdog})
		}
	}
	d.mu.Unlock()

	// 3. 投递：空闲槽位优先给 FIFO 里的消息。
	d.mu.Lock()
	for _, w := range d.workers {
		if w.isBusy() {
			continue
		}
		if wake, ok := w.next(); ok {
			d.deliver(ctx, w, wake)
		}
	}
	d.mu.Unlock()
	return nil
}

// route 把一个新步骤按订阅路由（不投递，只入队）。文件 IO（禁用
// 名单读取、事件落盘）在 d.mu 外做——磁盘卡顿不能停摆心跳。
func (d *Dispatcher) route(step traj.Step) {
	d.mu.Lock()
	var targets []*worker
	for _, w := range d.workers {
		matched := false
		for _, typ := range w.sub.Types {
			if typ == step.Type {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		// 自触发守卫：自己写下的步骤不唤醒自己（除非显式
		// TriggerSelf）。launched_by 是 runner 盖的作者章。
		if !w.sub.TriggerSelf {
			if by, ok := step.Field("launched_by"); ok && by == w.t.Name() {
				continue
			}
		}
		targets = append(targets, w)
	}
	d.mu.Unlock()

	// 禁用名单整个 route 只读一次。
	disabled := readDisabled(d.tlDir)
	isDisabled := func(name string) bool {
		for _, n := range disabled {
			if n == name {
				return true
			}
		}
		return false
	}
	for _, w := range targets {
		name := w.t.Name()
		if isDisabled(name) {
			if d.evlog != nil {
				d.evlog.Append(DispatchEvent{
					Kind: "other", Type: step.Type, Thinker: name,
					Reason: "disabled", TS: traj.NowString(),
				})
			}
			continue
		}
		if d.evlog != nil {
			src, _ := step.Field("launched_by")
			d.evlog.Append(DispatchEvent{
				Kind: "dispatch", Type: step.Type, Thinker: name,
				Source: src, StepID: step.StepID, TS: step.TS,
			})
		}
		w.enqueue(step)
	}
}

// deliver 投递一次唤醒到独立 goroutine，并预约下次。
func (d *Dispatcher) deliver(ctx context.Context, w *worker, wake Wake) {
	w.mu.Lock()
	if w.busy || ctx.Err() != nil {
		w.mu.Unlock()
		return
	}
	w.busy = true
	w.lastUsed = time.Now()
	gen := w.gen
	name := w.t.Name()
	w.mu.Unlock()
	d.logf("→ %s 收到 %s 唤醒（%s）", name, wake.Kind, wake.Step.Type)
	d.workingMark(name, true, wake.Kind)

	if d.evlog != nil {
		kind := "dispatch"
		if wake.Kind != WakeStep {
			kind = "step"
		}
		d.evlog.Append(DispatchEvent{
			Kind: kind, Type: wake.Step.Type, Thinker: name,
			Synthetic: wake.Kind != WakeStep, TS: traj.NowString(),
		})
	}
	d.inflight.Add(1)
	lock, _ := ctx.Value(runLockContextKey{}).(*RunLock)
	if lock != nil {
		lock.retain()
	}
	go func() {
		defer d.inflight.Add(-1)
		if lock != nil {
			defer lock.drop()
		}

		wakeCtx, cancelWake := context.WithCancelCause(ctx)
		defer cancelWake(nil)
		wakeCtx = context.WithValue(wakeCtx, cancelWakeContextKey{}, cancelWake)
		if timeout := d.wakeTimeout(); timeout > 0 {
			var cancel context.CancelFunc
			wakeCtx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}
		watchDone := make(chan struct{})
		stopWatch := context.AfterFunc(wakeCtx, func() {
			defer close(watchDone)
			w.mu.Lock()
			quarantined := w.busy && w.gen == gen
			if quarantined {
				w.gen++
				d.workingQuarantine(name)
			}
			w.mu.Unlock()
			if quarantined {
				d.logf("!! %s 取消后尚未退出，quarantined：保留执行槽", name)
				if d.evlog != nil {
					d.evlog.Append(DispatchEvent{Kind: "other", Type: wake.Step.Type, Thinker: name,
						Reason: "quarantined: " + wakeCtx.Err().Error(), TS: traj.NowString()})
				}
			}
		})

		// panic 防护：思考者的 panic 绝不能带走调度器进程，也绝不能
		// 把 busy 永久卡死（否则该思考者再也不被投递）。
		var outcome Outcome
		defer func() {
			if r := recover(); r != nil {
				d.logf("!! %s 的 Wake panic（已恢复，工作槽位释放）: %v", name, r)
				if d.evlog != nil {
					d.evlog.Append(DispatchEvent{
						Kind: "other", Type: wake.Step.Type, Thinker: name,
						Reason: fmt.Sprintf("panic: %v", r), TS: traj.NowString(),
					})
				}
			}
			// 等待取消回调结束，避免迟归诊断覆盖下一次运行的投影。
			if !stopWatch() {
				<-watchDone
			}
			w.mu.Lock()
			current := w.gen
			accept := current == gen && wakeCtx.Err() == nil
			w.busy = false
			d.workingMark(name, false, "")
			if accept {
				if outcome.WantWake {
					delay := outcome.NextWakeIn
					const minGap = time.Second // 防紧密自旋的最小间隔
					if delay < minGap {
						delay = minGap
					}
					w.wakeAt = time.Now().Add(delay)
				}
			}
			w.mu.Unlock()
			if outcome.Note != "" && accept {
				d.logf("← %s: %s", name, outcome.Note)
			}
			// 仅在 Wake 真实返回后释放槽位；取消后的预约和结论不再采纳。
		}()
		outcome = w.t.Wake(wakeCtx, wake)
	}()
}

// wakeTimeout 把 WakeTimeout 字段折算成生效期限：0 取默认 30 分钟，
// 负数禁用（返回 0 = 不设期限）。
func (d *Dispatcher) wakeTimeout() time.Duration {
	switch {
	case d.WakeTimeout < 0:
		return 0
	case d.WakeTimeout == 0:
		return 30 * time.Minute
	default:
		return d.WakeTimeout
	}
}

// workingMark 把忙碌变化投影进 <RunLockDir>/working：on=true 把
// thinker 记入忙集并重写文件；on=false 摘除——忙集变空即删除文件，
// 语义是"文件存在 ⇔ 有思考者正在工作"。投影写失败容忍：控制面
// 信号不值得打断调度，读方最多少看一轮状态。
//
// deliver 入集、Wake 返回出集；取消只改变诊断状态，不能冒充退出。
func (d *Dispatcher) workingMark(name string, on bool, wake WakeKind) {
	d.workingMu.Lock()
	defer d.workingMu.Unlock()
	if on {
		for _, e := range d.workingBusy {
			if e.Thinker == name {
				return // 已在忙集：同思考者至多一次在途唤醒（防御）
			}
		}
		d.workingBusy = append(d.workingBusy, workingEntry{
			Thinker: name,
			Wake:    string(wake),
			Since:   time.Now().UTC().Format(time.RFC3339),
		})
	} else {
		for i, e := range d.workingBusy {
			if e.Thinker == name {
				d.workingBusy = append(d.workingBusy[:i], d.workingBusy[i+1:]...)
				break
			}
		}
	}
	if len(d.workingBusy) == 0 {
		_ = os.Remove(workingPath(d.tl.Dir))
		return
	}
	writeWorkingFile(d.tl.Dir, d.workingBusy)
}

func (d *Dispatcher) workingQuarantine(name string) {
	d.workingMu.Lock()
	defer d.workingMu.Unlock()
	for i := range d.workingBusy {
		if d.workingBusy[i].Thinker == name {
			d.workingBusy[i].State = "quarantined"
			writeWorkingFile(d.tl.Dir, d.workingBusy)
			return
		}
	}
}

// WaitIdle 等待全部在途思考收尾（优雅停机的最后一步）。超过
// timeout 返回 false——调用方决定是否放弃。Run 返回后调用。
//
// 轮询在途计数而不是孵化一个 wg.Wait() goroutine：旧实现超时返回
// 后，监视 goroutine 仍吊在 WaitGroup 上——若某个 Wake 永不返回，
// 它与进程同寿，且每次 WaitIdle 都可能新增一个泄漏。
// 计数是原子的，5ms 轮询对停机路径毫无感知差异，也没有东西可泄漏。
func (d *Dispatcher) WaitIdle(timeout time.Duration) bool {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		if d.inflight.Load() == 0 {
			return true
		}
		select {
		case <-deadline.C:
			return false
		case <-tick.C:
		}
	}
}

// isBusy 报告思考者当前是否有唤醒在跑。
func (w *worker) isBusy() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.busy
}

// syntheticStep 构造合成唤醒的内存步骤（不落盘）。
func syntheticStep(typ string) traj.Step {
	s := traj.NewStep(typ)
	s.Fields["synthetic"] = "true"
	return s
}
