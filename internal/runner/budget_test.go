package runner

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"mindloop/internal/prompt"
)

// 受保护内容（系统提示 + 当前任务）超限时必须在发起模型调用前
// 明确报错——绝不静默截掉用户要求。
func TestRunRejectsProtectedContextOverflow(t *testing.T) {
	tl := newTestTimeline(t)
	thinker := &fakeThinker{responses: []string{fence("FINAL=\"never\"")}}
	_, err := Run(context.Background(), Options{
		Timeline:      tl,
		Thinker:       thinker,
		Task:          strings.Repeat("任", 20), // 60 字节
		SystemPrompt:  strings.Repeat("S", 200),
		ContextBudget: 100,
	})
	var oe *prompt.OverflowError
	if !errors.As(err, &oe) {
		t.Fatalf("受保护内容超限应返回 *prompt.OverflowError，得到: %v", err)
	}
	if thinker.calls != 0 {
		t.Fatalf("超限时不应发起模型调用，calls=%d", thinker.calls)
	}
}

// 历史渲染吃剩余预算：第一轮 2000 字节输出，第二轮上下文必须被
// 限制在 总预算 − 受保护段 之内。
func TestRunRendersHistoryWithinRemainingBudget(t *testing.T) {
	requireBash(t)
	tl := newTestTimeline(t)
	thinker := &fakeThinker{responses: []string{
		fence("printf '%2000s' '' | tr ' ' 'x'"),
		fence("FINAL=\"done\""),
	}}
	res, err := Run(context.Background(), Options{
		Timeline:      tl,
		Thinker:       thinker,
		Task:          "T",
		SystemPrompt:  "S",
		ContextBudget: 1000,
		IdleTimeout:   2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Final != "done" {
		t.Fatalf("final = %q", res.Final)
	}
	total := 0
	for _, m := range thinker.lastMsgs {
		total += len(m.Content)
	}
	if total == 0 {
		t.Fatal("第二轮应带历史消息")
	}
	if total > 1000-1-1 {
		t.Fatalf("历史渲染 %d 字节超出剩余预算 %d", total, 1000-1-1)
	}
}

// 预算充足（默认 128 KiB）时近期历史完整回流。
func TestRunHistoryFlowsWhenBudgetIsLarge(t *testing.T) {
	requireBash(t)
	tl := newTestTimeline(t)
	token := "budget-flow-token-8123"
	thinker := &fakeThinker{responses: []string{
		fence("echo " + token),
		fence("FINAL=\"ok\""),
	}}
	_, err := Run(context.Background(), Options{
		Timeline:    tl,
		Thinker:     thinker,
		Task:        "T",
		IdleTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var found bool
	for _, m := range thinker.lastMsgs {
		if strings.Contains(m.Content, token) {
			found = true
		}
	}
	if !found {
		t.Fatal("预算充足时第一轮输出应回流进第二轮上下文")
	}
}
