// Package task 从根轨迹中的版本化事实投影显式委托，不将历史聊天视为执行授权。
package task

import "errors"

const ProtocolVersion = 1
const eventType = "task-event"

type State string

const (
	Queued           State = "queued"
	Running          State = "running"
	AwaitingApproval State = "awaiting_approval"
	Canceling        State = "canceling"
	Succeeded        State = "succeeded"
	Failed           State = "failed"
	Canceled         State = "canceled"
	Interrupted      State = "interrupted"
	BudgetExceeded   State = "budget_exceeded"
)

var (
	ErrInvalid    = errors.New("task: 请求字段无效")
	ErrConflict   = errors.New("task: 幂等键已用于不同请求")
	ErrNotFound   = errors.New("task: 任务不存在")
	ErrTransition = errors.New("task: 状态或运行代次不允许此操作")
	ErrBusy       = errors.New("task: 已有行动任务占用执行槽")
	ErrNoQueued   = errors.New("task: 没有排队任务")
	ErrCorrupt    = errors.New("task: 轨迹事实损坏，拒绝推断或追加任务状态")
)

type Submission struct {
	From            string `json:"from"`
	ClientMessageID string `json:"client_message_id"`
	Content         string `json:"content"`
	SourceStepID    string `json:"source_step_id,omitempty"`
}

type Task struct {
	ID         string `json:"task_id"`
	IdentityID string `json:"identity_id"`
	Submission
	State           State    `json:"status"`
	Attempt         int      `json:"attempt"`
	RunID           string   `json:"run_id,omitempty"`
	CreatedAt       string   `json:"created_at"`
	UpdatedAt       string   `json:"updated_at"`
	Reason          string   `json:"reason,omitempty"`
	Result          string   `json:"result,omitempty"`
	ResultKind      string   `json:"result_kind,omitempty"`
	EvidenceStepIDs []string `json:"evidence_step_ids,omitempty"`
	Events          []Event  `json:"events"`
}

// Event 保存状态变化及幂等控制命令的收据；旧 attempt 的证据不会被覆盖。
type Event struct {
	StepID          string   `json:"step_id"`
	TS              string   `json:"ts"`
	TaskID          string   `json:"task_id"`
	State           State    `json:"status"`
	Attempt         int      `json:"attempt"`
	RunID           string   `json:"run_id,omitempty"`
	Reason          string   `json:"reason,omitempty"`
	Result          string   `json:"result,omitempty"`
	ResultKind      string   `json:"result_kind,omitempty"`
	EvidenceStepIDs []string `json:"evidence_step_ids,omitempty"`
	Operation       string   `json:"operation"`
	Actor           string   `json:"actor,omitempty"`
	RequestID       string   `json:"request_id,omitempty"`
	BaseAttempt     int      `json:"base_attempt"`
}
