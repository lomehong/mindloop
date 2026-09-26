// quota.go 是模型调用尝试的配额账本——"重试计入调用预算"的落点。
// 每次真实请求尝试（首试与每次重试）都要过账，配额耗尽返回
// ErrBudgetExceeded，不作为瞬时错误继续重试。配额经 context 传递：
// 一次任务 attempt 一份账本，不碰共享 client 字段，并发调用不串账。
package llm

import (
	"context"
	"errors"
	"sync"
)

// ErrBudgetExceeded 是调用配额耗尽的哨兵错误：预算阻止的是新请求，
// 不是已建立的流。
var ErrBudgetExceeded = errors.New("llm: 模型调用预算已耗尽")

// CallQuota 是并发安全的调用尝试计数器。
type CallQuota struct {
	mu        sync.Mutex
	remaining int
}

// NewCallQuota 构造一个允许 n 次请求尝试的配额。
func NewCallQuota(n int) *CallQuota { return &CallQuota{remaining: n} }

type callQuotaKey struct{}

// WithCallQuota 把调用配额挂到 ctx 上；未挂配额的 ctx 视为不限。
func WithCallQuota(ctx context.Context, q *CallQuota) context.Context {
	return context.WithValue(ctx, callQuotaKey{}, q)
}

// TakeCall 从 ctx 上的配额消费一次调用尝试，供请求路径在每次
// 实际尝试前调用；ctx 无配额时返回 nil（不限）。
func TakeCall(ctx context.Context) error {
	q, _ := ctx.Value(callQuotaKey{}).(*CallQuota)
	if q == nil {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.remaining <= 0 {
		return ErrBudgetExceeded
	}
	q.remaining--
	return nil
}
