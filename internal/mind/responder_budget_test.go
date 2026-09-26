package mind

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"mindloop/internal/prompt"
	"mindloop/internal/traj"
)

// newBudgetResponder 装配可直接调 compose 的 responder。
func newBudgetResponder(t *testing.T, mutate func(*ResponderOptions)) (*Responder, *traj.Timeline, *captureThinker) {
	t.Helper()
	t.Setenv("MINDLOOP_HOME", t.TempDir())
	tl, err := traj.Create(context.Background(), "responder-budget-test")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	ct := &captureThinker{}
	opts := ResponderOptions{
		Timeline: tl,
		Thinker:  ct,
		SelfName: "ada",
		Persona:  "You are ada.",
	}
	if mutate != nil {
		mutate(&opts)
	}
	return NewResponder(opts).(*Responder), tl, ct
}

// 受保护段（persona/规则 + 最新用户消息）超限时，compose 必须在
// 调用模型之前明确报错——绝不静默截掉用户的话。
func TestComposeRejectsProtectedOverflow(t *testing.T) {
	r, _, ct := newBudgetResponder(t, func(o *ResponderOptions) {
		o.Persona = strings.Repeat("P", 500)
		o.ContextBudget = 100
	})
	_, err := r.compose(context.Background(), "operator", "你好", nil)
	var oe *prompt.OverflowError
	if !errors.As(err, &oe) {
		t.Fatalf("persona 超限应返回 *prompt.OverflowError，得到: %v", err)
	}
	if ct.gotCalls() != 0 {
		t.Fatalf("超限时不应调用模型，calls=%d", ct.gotCalls())
	}
}

// 历史吃剩余预算：装不下的旧回合被丢弃，最新的用户消息绝不丢。
// （模板+persona 固定开销约 626 字节，预算 1000 给历史留出余量。）
func TestComposeHistoryTrimmedToRemainingBudget(t *testing.T) {
	r, tl, ct := newBudgetResponder(t, func(o *ResponderOptions) {
		o.ContextBudget = 1000
	})
	for i := 0; i < 5; i++ {
		s := traj.NewStep(traj.TypeMessage)
		s.Fields["from"] = "operator"
		s.Fields["to"] = "ada"
		s.Fields["content"] = strings.Repeat("h", 300)
		if err := tl.Append(context.Background(), s); err != nil {
			t.Fatal(err)
		}
	}
	_, err := r.compose(context.Background(), "operator", "最新消息", nil)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	msgs := ct.gotMsgs()
	if len(msgs) == 0 {
		t.Fatal("受保护的最新用户消息丢失")
	}
	last := msgs[len(msgs)-1]
	if last.Content != "最新消息" {
		t.Fatalf("最后一条 = %q，应为最新用户消息", last.Content)
	}
	historyBytes := 0
	for _, m := range msgs[:len(msgs)-1] {
		historyBytes += len(m.Content)
	}
	remain := 1000 - len(ct.got()) - len("最新消息")
	if historyBytes > remain {
		t.Fatalf("历史 %d 字节超出剩余预算 %d（system=%d 字节）", historyBytes, remain, len(ct.got()))
	}
	if historyBytes == 0 {
		t.Fatal("剩余预算应能容纳最近至少一回合")
	}
}

// 工作摘要段受独立上限约束：摘要过长时裁剪到 SummaryBudget，
// 不挤压历史与人话。
func TestComposeDigestCappedBySummaryBudget(t *testing.T) {
	r, tl, ct := newBudgetResponder(t, func(o *ResponderOptions) {
		o.SummaryBudget = 200
	})
	for i := 0; i < 3; i++ {
		s := traj.NewStep(traj.TypeReasoning)
		s.Fields["content"] = strings.Repeat("z", 300)
		if err := tl.Append(context.Background(), s); err != nil {
			t.Fatal(err)
		}
	}
	_, err := r.compose(context.Background(), "operator", "hi", nil)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	base := fmt.Sprintf(ResponderSystemTemplate, "You are ada.")
	digest := strings.TrimPrefix(ct.got(), base)
	if digest == "" {
		t.Fatal("system 应包含工作摘要段")
	}
	if len(digest) > 200 {
		t.Fatalf("摘要段 %d 字节超出上限 200", len(digest))
	}
	if !strings.Contains(digest, "Work digest") {
		t.Fatalf("摘要段头部丢失: %q", digest)
	}
}
