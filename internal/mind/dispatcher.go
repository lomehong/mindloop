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
	"sync"
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
)

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
type worker struct {
	t   Thinker
	sub Subscription

	mu        sync.Mutex
	busy      bool
	fifo      []Wake          // message 类：FIFO，保序投递
	coalesced map[string]Wake // 其余类型：last-wins 合并
	wakeAt    time.Time       // 思考者预约的自发性唤醒时刻
	lastUsed  time.Time       // 最近一次投递（watchdog 判据）
}

func (w *worker) enqueue(step traj.Step) {
	w.mu.Lock()
	defer w.mu.Unlock()
	wake := Wake{Step: step, Kind: WakeStep}
	if step.Type == "message" {
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
	logger func(format string, args ...any)

	mu      sync.Mutex
	workers []*worker
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

// step 是一次心跳：feeder → watchdog/自发性 → 投递。
func (d *Dispatcher) step(ctx context.Context) error {
	// 1. feeder：新步骤路由给订阅者。
	steps, err := d.cursor.ReadNew()
	if err != nil {
		return fmt.Errorf("mind: feeder: %w", err)
	}
	for _, s := range steps {
		d.route(s)
	}

	// 2. watchdog 与到点的自发性唤醒。
	now := time.Now()
	d.mu.Lock()
	for _, w := range d.workers {
		w.mu.Lock()
		due := !w.wakeAt.IsZero() && !now.Before(w.wakeAt)
		idle := w.sub.Watchdog > 0 && now.Sub(w.lastUsed) >= w.sub.Watchdog
		w.mu.Unlock()
		if due {
			w.wakeAt = time.Time{}
			d.deliver(ctx, w, Wake{Step: syntheticStep("monolith-wake"), Kind: WakeScheduled})
			continue
		}
		if idle {
			w.mu.Lock()
			w.lastUsed = now
			w.mu.Unlock()
			d.deliver(ctx, w, Wake{Step: syntheticStep("monolith-wake"), Kind: WakeWatchdog})
		}
	}
	d.mu.Unlock()

	// 3. 投递：空闲槽位优先给 FIFO 里的消息。
	d.mu.Lock()
	for _, w := range d.workers {
		w.mu.Lock()
		busy := w.busy
		w.mu.Unlock()
		if busy {
			continue
		}
		if wake, ok := w.next(); ok {
			d.deliver(ctx, w, wake)
		}
	}
	d.mu.Unlock()
	return nil
}

// route 把一个新步骤按订阅路由（不投递，只入队）。
func (d *Dispatcher) route(step traj.Step) {
	d.mu.Lock()
	defer d.mu.Unlock()
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
		w.enqueue(step)
	}
}

// deliver 投递一次唤醒到独立 goroutine，并预约下次。
func (d *Dispatcher) deliver(ctx context.Context, w *worker, wake Wake) {
	w.mu.Lock()
	w.busy = true
	w.lastUsed = time.Now()
	name := w.t.Name()
	w.mu.Unlock()
	d.logf("→ %s 收到 %s 唤醒（%s）", name, wake.Kind, wake.Step.Type)

	go func() {
		outcome := w.t.Wake(ctx, wake)
		w.mu.Lock()
		w.busy = false
		if outcome.WantWake {
			delay := outcome.NextWakeIn
			const minGap = time.Second // 防紧密自旋的最小间隔
			if delay < minGap {
				delay = minGap
			}
			w.wakeAt = time.Now().Add(delay)
		}
		w.mu.Unlock()
		if outcome.Note != "" {
			d.logf("← %s: %s", name, outcome.Note)
		}
	}()
}

// syntheticStep 构造合成唤醒的内存步骤（不落盘）。
func syntheticStep(typ string) traj.Step {
	s := traj.NewStep(typ)
	s.Fields["synthetic"] = "true"
	return s
}
