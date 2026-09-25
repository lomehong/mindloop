package mind

import (
	"context"
	"sync"
	"testing"
	"time"

	"mindloop/internal/traj"
)

// recorderThinker 记录收到的唤醒，可人为阻塞以制造"忙碌"。
type recorderThinker struct {
	name     string
	sub      Subscription
	outcomes []Outcome

	mu      sync.Mutex
	wakes   []Wake
	release chan struct{} // 非 nil 时阻塞直到关闭
	blocks  int           // 前 N 次唤醒阻塞
}

func (r *recorderThinker) Name() string { return r.name }
func (r *recorderThinker) Subscriptions() Subscription {
	return r.sub
}

func (r *recorderThinker) Wake(ctx context.Context, w Wake) Outcome {
	// 先记录再阻塞：投递即算数，不等处理完成。
	r.mu.Lock()
	r.wakes = append(r.wakes, w)
	n := len(r.wakes)
	var out Outcome
	if n <= len(r.outcomes) {
		out = r.outcomes[n-1]
	}
	shouldBlock := r.blocks > 0
	if shouldBlock {
		r.blocks--
		ch := r.release
		r.mu.Unlock()
		<-ch
	} else {
		r.mu.Unlock()
	}
	return out
}

func (r *recorderThinker) wakeCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.wakes)
}

func (r *recorderThinker) wakeTypes() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, w := range r.wakes {
		out = append(out, w.Step.Type)
	}
	return out
}

func newTestDispatcher(t *testing.T) (*Dispatcher, *traj.Timeline) {
	t.Helper()
	t.Setenv("MINDLOOP_HOME", t.TempDir())
	tl, err := traj.Create(context.Background(), "mind-test")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return NewDispatcher(tl, 10*time.Millisecond), tl
}

func appendStep(t *testing.T, tl *traj.Timeline, typ, launchedBy string) traj.Step {
	t.Helper()
	s := traj.NewStep(typ)
	if launchedBy != "" {
		s.Fields["launched_by"] = launchedBy
	}
	if err := tl.Append(context.Background(), s); err != nil {
		t.Fatalf("Append: %v", err)
	}
	return s
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("条件在超时内未满足")
}

func TestDispatcherRoutesSteps(t *testing.T) {
	d, tl := newTestDispatcher(t)
	a := &recorderThinker{name: "a", sub: Subscription{Types: []string{"message", "observation"}}}
	d.Register(a)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	s1 := appendStep(t, tl, "message", "")
	s2 := appendStep(t, tl, "observation", "")
	waitFor(t, 2*time.Second, func() bool { return a.wakeCount() >= 2 })

	types := a.wakeTypes()
	if types[0] != "message" || types[1] != "observation" {
		t.Fatalf("唤醒顺序 = %v", types)
	}
	if a.wakes[0].Step.StepID != s1.StepID {
		t.Fatalf("步骤身份不符")
	}
	_ = s2
}

func TestDispatcherSelfTriggerGuard(t *testing.T) {
	d, tl := newTestDispatcher(t)
	a := &recorderThinker{name: "a", sub: Subscription{Types: []string{"observation"}}}
	b := &recorderThinker{name: "b", sub: Subscription{Types: []string{"observation"}}}
	d.Register(a)
	d.Register(b)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	// a 自己写的 observation 不应唤醒 a，但应唤醒 b。
	appendStep(t, tl, "observation", "a")
	waitFor(t, 2*time.Second, func() bool { return b.wakeCount() >= 1 })
	// 负向断言的观察窗要盖过慢 CI 的调度延迟（10ms 心跳 × 50），
	// 否则迟到的投递会把"守卫失效"误判出来。
	time.Sleep(500 * time.Millisecond)
	if a.wakeCount() != 0 {
		t.Fatalf("自触发守卫失效：a 收到 %d 次唤醒", a.wakeCount())
	}
}

func TestDispatcherCoalescesSelfWakesKeepsFifoForMessages(t *testing.T) {
	d, tl := newTestDispatcher(t)
	tk := &recorderThinker{
		name: "w",
		sub:  Subscription{Types: []string{"message", "observation"}},
		// 第一次唤醒阻塞：制造积压窗口。之后不再预约自发性唤醒
		//（WantWake=false），保证唤醒总数确定。
		blocks:  1,
		release: make(chan struct{}),
		outcomes: []Outcome{
			{WantWake: false},
		},
	}
	d.Register(tk)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	// 人类消息 ×2 + 观察 ×3 在思考者忙碌时涌入。
	appendStep(t, tl, "message", "")
	appendStep(t, tl, "message", "")
	appendStep(t, tl, "observation", "")
	appendStep(t, tl, "observation", "")
	appendStep(t, tl, "observation", "")

	waitFor(t, 2*time.Second, func() bool { return tk.wakeCount() >= 1 })
	close(tk.release) // 放行第一次唤醒

	// 释放后：消息 FIFO 应逐条投递（保序），观察合并为 1 条
	// last-wins。总计 2 条消息 + 1 条合并观察 = 3 次。
	waitFor(t, 3*time.Second, func() bool { return tk.wakeCount() >= 3 })
	time.Sleep(500 * time.Millisecond)
	if got := tk.wakeCount(); got != 3 {
		t.Fatalf("唤醒总数 = %d，应为 3（2 FIFO 消息 + 1 合并观察）", got)
	}
	types := tk.wakeTypes()
	if types[0] != "message" || types[1] != "message" {
		t.Fatalf("消息必须 FIFO 先行: %v", types)
	}
	if types[2] != "observation" {
		t.Fatalf("合并观察应在消息之后: %v", types)
	}
}

func TestDispatcherWatchdogSyntheticWake(t *testing.T) {
	d, _ := newTestDispatcher(t)
	tk := &recorderThinker{
		name: "w",
		sub:  Subscription{Types: []string{"message"}, TriggerSelf: true, Watchdog: 80 * time.Millisecond},
	}
	d.Register(tk)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	// 没有任何轨迹活动：watchdog 必须合成唤醒（活性由调度器保证）。
	waitFor(t, 2*time.Second, func() bool { return tk.wakeCount() >= 2 })
	for _, w := range tk.wakes[:2] {
		if w.Kind != WakeWatchdog {
			t.Fatalf("唤醒类型 = %q，应为 watchdog", w.Kind)
		}
	}
}

func TestDispatcherScheduledSpontaneity(t *testing.T) {
	d, _ := newTestDispatcher(t)
	tk := &recorderThinker{
		name: "w",
		sub:  Subscription{Types: []string{"message"}, Watchdog: time.Hour},
		outcomes: []Outcome{
			{WantWake: true, NextWakeIn: 0}, // 调度器强制最小 1s 间隔
			{WantWake: true, NextWakeIn: 0},
		},
	}
	d.Register(tk)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	// 一次人类消息触发首醒；之后思考者预约的自发性唤醒应自我延续。
	appendStep(t, tl0(t, d), "message", "")
	waitFor(t, 3*time.Second, func() bool { return tk.wakeCount() >= 3 })
	kinds := map[WakeKind]int{}
	for _, w := range tk.wakes {
		kinds[w.Kind]++
	}
	if kinds[WakeStep] != 1 {
		t.Fatalf("应有 1 次步骤唤醒: %v", kinds)
	}
	if kinds[WakeScheduled] < 1 {
		t.Fatalf("应有自发性唤醒: %v", kinds)
	}
}

// tl0 取调度器持有的轨迹（测试辅助）。
func tl0(t *testing.T, d *Dispatcher) *traj.Timeline {
	t.Helper()
	return d.tl
}

