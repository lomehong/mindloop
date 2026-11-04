// Super-agent 双层架构的并发验证（论文第 2 节，2026-11-04 用户
// 拍板落地）：MaxWorkers>=2 时 Wake 异步派工、槽位限制并发度、
// Mandate 盖章落轨迹。既有测试（MaxWorkers 未设 → 1）走同步
// 路径不受影响——这里专测异步分支的核心机制。
package mind

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lomehong/mindloop/internal/llm"
	"github.com/lomehong/mindloop/internal/task"
	"github.com/lomehong/mindloop/internal/traj"
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

// TestTrueParallelForkExecution：v2 fork 并行的核心验证——两个
// Worker 的模型调用必须重叠。模型进入后阻塞在 release 上，只有
// 两个都进入才放行；若执行被串行化（v1 的 workerMu 语义），第二
// 个调用永远进不来，超时即失败。完成后核验三件事：双任务终态、
// fork 子轨迹存在（runner 步骤与根隔离）、merge 步骤回到根轨迹
// （Worker 向 Router 汇报）。
func TestTrueParallelForkExecution(t *testing.T) {
	tl := newTestTimeline(t)

	entered := make(chan struct{}, 2) // 模型进入信号（缓冲 2：Worker 不因发送阻塞）
	release := make(chan struct{})    // 两个都进入后统一放行
	m := NewMonolith(MonolithOptions{
		Timeline: tl, SelfName: "ada", MaxWorkers: 2,
		RequestThinker: taskModelFunc(func(context.Context, string, []llm.Message) (string, error) {
			entered <- struct{}{}
			<-release // 挂起直到验证方确认"两个都在跑"
			return taskFinalShell("并行任务"), nil
		}),
	})
	mon := m.(*monolith)
	s := task.New(tl, "ada")
	first := submitTask(t, s, "并行A")
	second := submitTask(t, s, "并行B")

	mon.Wake(context.Background(), Wake{Step: syntheticStep(monolithWakeType), Kind: WakeScheduled})
	mon.Wake(context.Background(), Wake{Step: syntheticStep(monolithWakeType), Kind: WakeScheduled})

	// 并行判定：两个模型调用都在 release 之前进入。
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-time.After(15 * time.Second):
			t.Fatal("两个 Worker 未并发进入模型——执行被串行化（fork 并行失效）")
		}
	}
	close(release)

	// 等双任务终态（fork 隔离下 store 投影不应再撕裂）。
	deadline := time.Now().Add(30 * time.Second)
	var a, b task.Task
	for time.Now().Before(deadline) {
		a, _ = s.Get(context.Background(), first.ID)
		b, _ = s.Get(context.Background(), second.ID)
		if terminalState(a.State) && terminalState(b.State) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if a.State != task.Succeeded || b.State != task.Succeeded {
		t.Fatalf("任务终态: %s / %s（期望双双 Succeeded）", a.State, b.State)
	}

	// merge 回根：每个 Worker 完成后在根轨迹落一条 TypeMerge。
	steps, err := tl.Steps()
	if err != nil {
		t.Fatalf("读根轨迹: %v", err)
	}
	merges := 0
	for _, st := range steps {
		if st.Type == traj.TypeMerge {
			merges++
		}
	}
	if merges != 2 {
		t.Fatalf("根轨迹应有 2 条 merge 步骤（Worker 汇报），实际 %d", merges)
	}

	// fork 子轨迹存在：与根轨迹同目录的 worker- 前缀目录。
	forks := 0
	entries, err := os.ReadDir(filepath.Dir(tl.Dir))
	if err != nil {
		t.Fatalf("读轨迹根目录: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() && containsWorkerSlug(e.Name()) {
			forks++
		}
	}
	if forks != 2 {
		t.Fatalf("应有 2 条 worker fork 子轨迹，实际 %d", forks)
	}

	// Windows TempDir 清理竞态：等 Worker goroutine 完全退出。
	drainDeadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(drainDeadline) && mon.workers.Count() > 0 {
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
}

// containsWorkerSlug 判断轨迹目录名是否带 worker- slug。DirName 的
// 格式是 "<id短前缀>-<slug>"，slug 本身以 "worker-" 开头。
func containsWorkerSlug(name string) bool {
	_, slug, ok := strings.Cut(name, "-")
	return ok && strings.HasPrefix(slug, "worker-")
}
