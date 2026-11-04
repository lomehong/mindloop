// Worker 池：任务并发执行的槽位管理（Super-agent 论文第 2 节双层
// 架构的 mindloop 落地，2026-11-04 用户拍板「根据论文实现设计」）。
//
// 论文的双层分工在 mindloop 的映射：
//
//	Router = monolith（领取→派工→立即释放，继续响应事件/评估/决定）
//	Worker = runner.Run 异步执行体（各自独立的 LLM 会话——runner 的
//	上下文从任务文本构建，不读轨迹历史，并发任务互不污染；步骤
//	以 run_id 分组追加到同一轨迹 = 论文 Worker 的隔离 + 汇报）
//
// 槽位语义：至多 MaxWorkers 个任务同时在跑；满槽时 Pending 不再
// 放行新任务（排队等待，与论文 Router 的批处理队列对应）。任务
// 终态落盘即释放槽位（settleTask 的调用点）。
package mind

import (
	"sync"
)

// workerPool 跟踪在飞任务占用的槽位。零值不可用，走 newWorkerPool。
type workerPool struct {
	mu       sync.Mutex
	max      int
	inFlight map[string]bool // task ID → 占用中
}

func newWorkerPool(max int) *workerPool {
	if max < 1 {
		max = 1 // 缺省 1 = 同步串行（既有行为；>=2 才开启异步并发）
	}
	return &workerPool{max: max, inFlight: make(map[string]bool)}
}

// Acquire 尝试为 taskID 占一个槽位；满槽或已占用返回 false。
func (p *workerPool) Acquire(taskID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.inFlight[taskID] || len(p.inFlight) >= p.max {
		return false
	}
	p.inFlight[taskID] = true
	return true
}

// Release 释放 taskID 的槽位（幂等）。
func (p *workerPool) Release(taskID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.inFlight, taskID)
}

// Count 返回当前在飞数（面板/观测用）。
func (p *workerPool) Count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.inFlight)
}

// HasSlot 报告是否还有空槽。
func (p *workerPool) HasSlot() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.inFlight) < p.max
}
