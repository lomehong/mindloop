package mind

import (
	"context"
	"sync"
	"testing"
	"time"
)

// gateThinker 是可控阻塞的假思考者：第一次 Wake 在 gate 上阻塞，
// 用于复现"长任务占着工作槽位"的时序。
type gateThinker struct {
	name string
	sub  Subscription

	mu     sync.Mutex
	count  int
	gate   chan struct{} // 第 1 次 Wake 阻塞在此，关闭后返回
	before chan struct{} // 第 1 次 Wake 进入后关闭（供测试同步）
}

func (g *gateThinker) Name() string                { return g.name }
func (g *gateThinker) Subscriptions() Subscription { return g.sub }

func (g *gateThinker) Wake(ctx context.Context, w Wake) Outcome {
	g.mu.Lock()
	g.count++
	n := g.count
	g.mu.Unlock()
	if n == 1 {
		if g.before != nil {
			close(g.before)
		}
		if g.gate != nil {
			<-g.gate
		}
	}
	return Outcome{}
}

func (g *gateThinker) wakeCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.count
}

// TestDispatcherWatchdogQuietWhileFree：活性窗口度量"空闲且安静"
// 的时长——思考者忙碌期间时钟持续刷新，长任务（超过整个 watchdog
// 窗口）结束的瞬间不得立即补火 watchdog；释放后再安静满一个窗口
// 才触发。Headlong THINKERS_spec 的判据（实测教训：长运行结束
// 瞬间的补偿性唤醒纯属浪费）。
func TestDispatcherWatchdogQuietWhileFree(t *testing.T) {
	d, tl := newTestDispatcher(t)
	gate := make(chan struct{})
	before := make(chan struct{})
	tk := &gateThinker{
		name:   "w",
		sub:    Subscription{Types: []string{"message"}, TriggerSelf: true, Watchdog: 150 * time.Millisecond},
		gate:   gate,
		before: before,
	}
	d.Register(tk)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	// 一次真实投递让思考者进入长任务（持锁超过 2 个 watchdog 窗口）。
	appendStep(t, tl, "message", "")
	select {
	case <-before:
	case <-time.After(3 * time.Second):
		t.Fatal("思考者未收到第一次唤醒")
	}
	time.Sleep(300 * time.Millisecond) // 忙碌横跨 2 个窗口：时钟应被持续刷新

	close(gate) // 任务结束

	// 释放后的短窗内（不足一个安静窗口）不应有 watchdog 补火。
	deadline := time.Now().Add(80 * time.Millisecond)
	for time.Now().Before(deadline) {
		if n := tk.wakeCount(); n > 1 {
			t.Fatalf("长任务结束瞬间不应立即触发 watchdog，已触发 %d 次", n)
		}
		time.Sleep(5 * time.Millisecond)
	}

	// 安静满一个窗口后，watchdog 照常兜底（活性保证不受影响）。
	waitFor(t, 3*time.Second, func() bool { return tk.wakeCount() >= 2 })
}
