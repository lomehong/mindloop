// Mandate 盖章（dsh-superagent 原型域模型的授权凭证对应物，
// 2026-11-04）：任务派工时在轨迹落一条不可变的授权快照——记录
// 「当时凭什么让它做」（from 信道 + 执行策略 + 派工时刻），审计
// 能回答“这个任务是在什么授权环境下开始的”。与 agentguard 互补：
// guard 堵「不该写的面」（否定），mandate 记「该做的授权链」（肯定）。
package mind

import (
	"context"
	"os"

	"github.com/lomehong/mindloop/internal/task"
	"github.com/lomehong/mindloop/internal/traj"
)

// stampMandate 落授权快照步骤（best-effort：失败不影响派工——
// mandate 是审计增强，不是执行门禁）。
func (m *monolith) stampMandate(item task.Task) {
	s := traj.NewStep("mandate")
	s.Fields["task_id"] = item.ID
	s.Fields["from"] = item.From
	s.Fields["attempt"] = item.Attempt
	s.Fields["launched_by"] = m.Name()
	if policy := os.Getenv("MINDLOOP_EXEC_POLICY"); policy != "" {
		s.Fields["exec_policy"] = policy
	} else {
		s.Fields["exec_policy"] = "auto(缺省)"
	}
	// 上下文使用 WithoutCancel：mandate 落盘是审计事实，唤醒 ctx
	// 结束也必须落。
	_ = m.opts.Timeline.Append(context.WithoutCancel(context.Background()), s)
}
