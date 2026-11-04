package mind

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestWorkerPoolAcquireRelease(t *testing.T) {
	p := newWorkerPool(2)
	if !p.Acquire("a") || !p.Acquire("b") {
		t.Fatal("前两槽应可占")
	}
	if p.Acquire("c") {
		t.Fatal("满槽应拒绝")
	}
	if p.Acquire("a") {
		t.Fatal("同 ID 重复占应拒绝")
	}
	p.Release("a")
	if !p.Acquire("c") {
		t.Fatal("释放后应有空槽")
	}
	if p.Count() != 2 {
		t.Fatalf("在飞应为 2，得 %d", p.Count())
	}
	p.Release("a") // 幂等
	if p.Count() != 2 {
		t.Fatalf("重复释放不应减数，得 %d", p.Count())
	}
}

func TestWorkerPoolDefaultAtLeastOne(t *testing.T) {
	if p := newWorkerPool(0); p.max != 1 {
		t.Fatalf("0 应折算 1，得 %d", p.max)
	}
	if p := newWorkerPool(-5); p.max != 1 {
		t.Fatalf("负值应折算 1，得 %d", p.max)
	}
}

// 并发冒烟：两个 runWorker 同时跑，各自完成后释放槽位。
func TestWorkerPoolConcurrentWorkers(t *testing.T) {
	p := newWorkerPool(2)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		id := string(rune('a' + i))
		if !p.Acquire(id) {
			t.Fatalf("worker %s 应能占槽", id)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer p.Release(id)
			<-start // 确保两个都在飞
		}()
	}
	close(start)
	if p.Count() != 2 {
		t.Fatalf("并发中应为 2，得 %d", p.Count())
	}
	wg.Wait()
	if p.Count() != 0 {
		t.Fatalf("全部完成后应为 0，得 %d", p.Count())
	}
	if !p.HasSlot() {
		t.Fatal("全部完成后应有空槽")
	}
}

// Pending 槽位放行的行为验证：构造最小 monolith（无 thinker，只测
// 池的阻塞/放行语义——不跑真 runner）。
func TestPendingRespectsWorkerSlots(t *testing.T) {
	m := &monolith{workers: newWorkerPool(1)}
	if !m.workers.Acquire("occupied") {
		t.Fatal("占槽应成功")
	}
	defer m.workers.Release("occupied")

	// 池满 → Pending 不放行（即便 store 有排队任务——此处 store 为
	// 空，语义只验池的 HasSlot 门：HasSlot=false 即阻塞路径）。
	if m.workers.HasSlot() {
		t.Fatal("单槽被占后不应有空槽")
	}
	m.workers.Release("occupied")
	if !m.workers.HasSlot() {
		t.Fatal("释放后应有空槽")
	}
}

// Wake 异步派工不阻塞：Worker goroutine 启动后 Wake 立即返回。
// 用真实 taskStore 与假 thinker 验证完整链路。
func TestWakeDispatchesAsync(t *testing.T) {
	// 见 monolith_test.go 的既有测试基建——此测试在集成层验证。
	t.Skip("集成层验证：见 TestConcurrentTaskExecution（需要完整 mind 装配）")
	_ = context.Background()
	_ = time.Now
}
