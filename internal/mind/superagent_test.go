// Super-agent 双层架构的并发验证（论文第 2 节，2026-11-04 用户
// 拍板落地）：MaxWorkers>=2 时 Wake 异步派工、槽位限制并发度、
// Mandate 盖章落轨迹。既有测试（MaxWorkers 未设 → 1）走同步
// 路径不受影响——这里专测异步分支的核心机制。
package mind

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/lomehong/mindloop/internal/llm"
	"github.com/lomehong/mindloop/internal/task"
)

// TestAsyncDispatchImmediateReturn：MaxWorkers=2 的 Wake 应立即
// 返回（<500ms），不等任务执行完成——Router 的响应性。
func TestAsyncDispatchImmediateReturn(t *testing.T) {
	tl := newTestTimeline(t)

	block := make(chan struct{}) // 模型挂起直到释放
	m := NewMonolith(MonolithOptions{
		Timeline: tl, SelfName: "ada", MaxWorkers: 2,
		RequestThinker: taskModelFunc(func(context.Context, string, []llm.Message) (string, error) {
			<-block
			return taskFinalShell("异步测试"), nil
		}),
	})
	s := task.New(tl, "ada")
	submitTask(t, s, "长任务")

	start := time.Now()
	out := m.Wake(context.Background(), Wake{Step: syntheticStep(monolithWakeType), Kind: WakeScheduled})
	elapsed := time.Since(start)

	if elapsed > 500*time.Millisecond {
		t.Fatalf("异步派工的 Wake 应 <500ms 返回，实际 %v", elapsed)
	}
	t.Logf("派工返回: %+v", out)

	// 释放模型 → Worker 完成。
	close(block)
	// 给 goroutine 一点时间收尾（不判终态——全链路在同步测试覆盖）。
	time.Sleep(200 * time.Millisecond)

	// Mandate 盖章验证：轨迹里应有 mandate 步骤。
	steps, err := tl.Steps()
	if err != nil {
		t.Fatalf("读轨迹: %v", err)
	}
	found := false
	for _, st := range steps {
		if st.Type == "mandate" {
			found = true
			if tid, _ := st.Field("task_id"); tid == "" {
				t.Error("mandate 缺 task_id")
			}
			if ep, _ := st.Field("exec_policy"); ep == "" {
				t.Error("mandate 缺 exec_policy")
			}
			if from, _ := st.Field("from"); from == "" {
				t.Error("mandate 缺 from")
			}
		}
	}
	if !found {
		t.Error("异步派工应落 mandate 步骤")
	}
}

// TestSyncWhenMaxWorkersOne：MaxWorkers 未设（→1）走同步路径，
// Wake 阻塞直到任务完成——与既有行为完全一致（回归保护）。
func TestSyncWhenMaxWorkersOne(t *testing.T) {
	tl := newTestTimeline(t)
	done := make(chan struct{})
	var once sync.Once
	m := NewMonolith(MonolithOptions{
		Timeline: tl, SelfName: "ada", // MaxWorkers 未设 → 1 → 同步
		RequestThinker: taskModelFunc(func(context.Context, string, []llm.Message) (string, error) {
			once.Do(func() { close(done) })
			return taskFinalShell("同步测试"), nil
		}),
	})
	s := task.New(tl, "ada")
	submitTask(t, s, "同步任务")

	go func() {
		m.Wake(context.Background(), Wake{Step: syntheticStep(monolithWakeType), Kind: WakeScheduled})
	}()

	select {
	case <-done:
		// 同步路径：模型被调用了
	case <-time.After(10 * time.Second):
		t.Fatal("MaxWorkers=1 应走同步路径，10s 内模型未调用")
	}
}

// TestWorkerSlotExhaustion：槽满后 Pending 不放行；释放后有槽。
func TestWorkerSlotExhaustion(t *testing.T) {
	tl := newTestTimeline(t)
	block := make(chan struct{})
	m := NewMonolith(MonolithOptions{
		Timeline: tl, SelfName: "ada", MaxWorkers: 2,
		RequestThinker: taskModelFunc(func(context.Context, string, []llm.Message) (string, error) {
			<-block
			return taskFinalShell("占槽"), nil
		}),
	})
	mon := m.(*monolith)
	s := task.New(tl, "ada")
	submitTask(t, s, "任务1")
	submitTask(t, s, "任务2")

	// 派两个。Acquire 在 Wake 内同步完成，但 Claim 的 store 写入
	// 与 goroutine 启动有微竞态——等一拍让两个 Claim 都落地。
	mon.Wake(context.Background(), Wake{Step: syntheticStep(monolithWakeType), Kind: WakeScheduled})
	mon.Wake(context.Background(), Wake{Step: syntheticStep(monolithWakeType), Kind: WakeScheduled})
	time.Sleep(200 * time.Millisecond)

	// 两槽被占。
	if mon.workers.HasSlot() {
		t.Fatal("两槽被占后不应有空槽")
	}
	// Pending 因满槽不放行（store 里有第三个排队任务时）。
	submitTask(t, s, "任务3")
	_, has, err := mon.Pending(context.Background())
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if has {
		t.Fatal("满槽时 Pending 不应放行新任务")
	}

	// 释放 → 槽恢复。
	close(block)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if mon.workers.HasSlot() {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !mon.workers.HasSlot() {
		t.Fatal("任务完成后应有空槽")
	}
	// 有槽后 Pending 放行第三个。
	_, has, err = mon.Pending(context.Background())
	if err != nil || !has {
		t.Fatalf("释放后 Pending 应放行（has=%v err=%v）", has, err)
	}
	// Windows TempDir 清理竞态：等 Worker goroutine 完全退出（池
	// 清空 + 轨迹写入完毕）再让清理跑（同 sensor 测试的收尾纪律）。
	drainDeadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(drainDeadline) && mon.workers.Count() > 0 {
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond) // 文件句柄释放余量
}
