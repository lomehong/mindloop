package mind

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// hungThinker 模拟一个不尊重 ctx 取消的坏思考者：Wake 永远挂着
// （直到测试放行），调用方等不到它返回。
type hungThinker struct {
	name     string
	sub      Subscription
	release  chan struct{}
	wakes    atomic.Int32
	contexts chan context.Context
	outcome  Outcome
}

func (h *hungThinker) Name() string                { return h.name }
func (h *hungThinker) Subscriptions() Subscription { return h.sub }
func (h *hungThinker) Wake(ctx context.Context, w Wake) Outcome {
	h.wakes.Add(1)
	if h.contexts != nil {
		h.contexts <- ctx
	}
	<-h.release // 无视 ctx：正是 WakeTimeout 要防的坏公民
	return h.outcome
}

// 期限取消不能证明执行退出；旧执行仍占槽，迟归预约不能生效。
func TestDispatcherWakeTimeoutQuarantinesUntilExit(t *testing.T) {
	d, tl := newTestDispatcher(t)
	d.WakeTimeout = 80 * time.Millisecond
	h := &hungThinker{
		name:    "hung",
		sub:     Subscription{Types: []string{"message"}},
		release: make(chan struct{}),
	}
	h.contexts = make(chan context.Context, 2)
	h.outcome = Outcome{WantWake: true, NextWakeIn: time.Second}
	d.Register(h)
	other := &recorderThinker{name: "responder", sub: Subscription{Types: []string{"observation"}}}
	d.Register(other)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); close(h.release); d.WaitIdle(time.Second) })
	appendStep(t, tl, "message", "")
	appendStep(t, tl, "message", "")
	if err := d.step(ctx); err != nil {
		t.Fatal(err)
	}
	var wakeCtx context.Context
	select {
	case wakeCtx = <-h.contexts:
	case <-time.After(time.Second):
		t.Fatal("未投递")
	}
	select {
	case <-wakeCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("期限未取消 context")
	}
	if d.WaitIdle(30 * time.Millisecond) {
		t.Fatal("旧执行未退出却报告空闲")
	}
	appendStep(t, tl, "observation", "")
	for i := 0; i < 5; i++ {
		if err := d.step(ctx); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, time.Second, func() bool { return other.wakeCount() == 1 })
	if got := h.wakes.Load(); got != 1 {
		t.Fatalf("旧执行未退出却投递下一次: %d", got)
	}
	h.release <- struct{}{}
	if !d.WaitIdle(time.Second) {
		t.Fatal("旧执行退出后未释放")
	}
	d.workers[0].mu.Lock()
	wakeAt := d.workers[0].wakeAt
	d.workers[0].mu.Unlock()
	if !wakeAt.IsZero() {
		t.Fatal("采用了超时旧执行的预约")
	}
	if err := d.step(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, time.Second, func() bool { return h.wakes.Load() == 2 })
}

type cancellationThinker struct {
	started chan struct{}
	stopped chan struct{}
}

func (h *cancellationThinker) Name() string { return "cancel-aware" }
func (h *cancellationThinker) Subscriptions() Subscription {
	return Subscription{Types: []string{"message"}}
}
func (h *cancellationThinker) Wake(ctx context.Context, _ Wake) Outcome {
	close(h.started)
	<-ctx.Done()
	close(h.stopped)
	return Outcome{}
}

func TestDispatcherStopCancelsInflightWake(t *testing.T) {
	d, tl := newTestDispatcher(t)
	h := &cancellationThinker{started: make(chan struct{}), stopped: make(chan struct{})}
	d.Register(h)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	t.Cleanup(func() { cancel(); d.WaitIdle(time.Second) })
	appendStep(t, tl, "message", "")
	select {
	case <-h.started:
	case <-time.After(time.Second):
		t.Fatal("未启动")
	}
	if err := RequestStop(tl); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != ErrStopRequested {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("未处理停止")
	}
	select {
	case <-h.stopped:
	case <-time.After(time.Second):
		t.Fatal("停止未传播到在途执行")
	}
	if !d.WaitIdle(time.Second) {
		t.Fatal("取消后未收尾")
	}
}

// TestDispatcherNegativeWakeTimeoutDisables：负值禁用期限（测试与
// 已知长任务的逃生口），0 取默认 30 分钟。
func TestDispatcherNegativeWakeTimeoutDisables(t *testing.T) {
	d := &Dispatcher{}
	if got := d.wakeTimeout(); got != 30*time.Minute {
		t.Fatalf("零值应取默认 30 分钟，得到 %v", got)
	}
	d.WakeTimeout = -time.Second
	if got := d.wakeTimeout(); got != 0 {
		t.Fatalf("负值应禁用期限（返回 0），得到 %v", got)
	}
	d.WakeTimeout = 42 * time.Second
	if got := d.wakeTimeout(); got != 42*time.Second {
		t.Fatalf("正值应原样生效，得到 %v", got)
	}
}
