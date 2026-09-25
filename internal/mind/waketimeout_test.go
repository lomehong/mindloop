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
	name    string
	sub     Subscription
	release chan struct{}
	wakes   atomic.Int32
}

func (h *hungThinker) Name() string                { return h.name }
func (h *hungThinker) Subscriptions() Subscription { return h.sub }
func (h *hungThinker) Wake(ctx context.Context, w Wake) Outcome {
	h.wakes.Add(1)
	<-h.release // 无视 ctx：正是 WakeTimeout 要防的坏公民
	return Outcome{}
}

// TestDispatcherWakeTimeoutForceReleases：调度器侧硬期限必须能把
// 挂死的思考者从 busy 里摘出来，让后续唤醒照常投递。修复前：busy
// 永真，watchdog 又明确跳过 busy，该思考者从此失联且无任何诊断。
func TestDispatcherWakeTimeoutForceReleases(t *testing.T) {
	d, tl := newTestDispatcher(t)
	d.WakeTimeout = 80 * time.Millisecond
	h := &hungThinker{
		name:    "hung",
		sub:     Subscription{Types: []string{"message"}},
		release: make(chan struct{}),
	}
	d.Register(h)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	// 两条消息：第一条唤醒挂死并吃到强制释放；槽位空出后，FIFO 里
	// 的第二条消息必须照常投递——这正是"挂死思考者不再拖死调度"
	// 的核心断言。
	appendStep(t, tl, "message", "")
	appendStep(t, tl, "message", "")
	waitFor(t, 2*time.Second, func() bool { return h.wakes.Load() >= 1 })
	// 期限（80ms）+ 心跳（10ms）：第一次挂死的唤醒被强制释放后，
	// 第二次唤醒应照常到来。
	waitFor(t, 3*time.Second, func() bool { return h.wakes.Load() >= 2 })
	// 放行全部挂死的 Wake：迟归者被代际号拦下，不得影响槽位状态；
	// 在途清零后 WaitIdle 必须为真（顺带覆盖轮询版 WaitIdle）。
	close(h.release)
	waitFor(t, 2*time.Second, func() bool { return d.WaitIdle(time.Second) })
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
