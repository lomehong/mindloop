package mind

import (
	"context"
	"strings"
	"testing"

	"mindloop/internal/llm"
	"mindloop/internal/traj"
)

// cannedThinker 对每次调用返回固定文本——responder 的单次补全。
type cannedThinker struct{ reply string }

func (c *cannedThinker) Think(ctx context.Context, system string, msgs []llm.Message) (string, error) {
	return c.reply, nil
}

func newResponder(t *testing.T) (*Responder, *traj.Timeline) {
	t.Helper()
	t.Setenv("MINDLOOP_HOME", t.TempDir())
	tl, err := traj.Create(context.Background(), "responder-test")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	r := NewResponder(ResponderOptions{
		Timeline: tl,
		Thinker:  &cannedThinker{reply: "你好！我看到了你的消息。"},
		SelfName: "ada",
		Persona:  "You are ada.",
	}).(*Responder)
	return r, tl
}

func inboundMessage(t *testing.T, tl *traj.Timeline, from, content string) traj.Step {
	t.Helper()
	s := traj.NewStep("message")
	s.Fields["from"] = from
	s.Fields["to"] = "ada"
	s.Fields["content"] = content
	if err := tl.Append(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestResponderRepliesWithHistoryAndStamp(t *testing.T) {
	r, tl := newResponder(t)

	// 既有对话：操作员先说过一句，ada 回过一句。
	old := traj.NewStep("message")
	old.Fields["from"] = "operator"
	old.Fields["to"] = "ada"
	old.Fields["content"] = "我们上次聊到什么了"
	_ = tl.Append(context.Background(), old)
	historyRe := traj.NewStep("message")
	historyRe.Fields["from"] = "ada"
	historyRe.Fields["to"] = "operator"
	historyRe.Fields["content"] = "聊到 headlong 的记忆设计"
	_ = tl.Append(context.Background(), historyRe)

	// 新消息到来
	trig := inboundMessage(t, tl, "operator", "在吗？")
	out := r.Wake(context.Background(), Wake{Step: trig, Kind: WakeStep})

	if !strings.Contains(out.Note, "已回复") {
		t.Fatalf("outcome = %+v", out)
	}
	// 回复已落盘：message + reply_to 盖章 + launched_by
	steps, _ := tl.Steps()
	var reply *traj.Step
	for i := range steps {
		if rt, ok := steps[i].Field("reply_to"); ok && rt == trig.StepID {
			reply = &steps[i]
		}
	}
	if reply == nil {
		t.Fatal("找不到盖 reply_to 章的回复步骤")
	}
	if got, _ := reply.Field("from"); got != "ada" {
		t.Fatalf("回复 from = %q", got)
	}
	if c, _ := reply.Field("content"); !strings.Contains(c, "看到了") {
		t.Fatalf("回复内容 = %q", c)
	}
	// 历史注入：假 thinker 的 system 含 persona（通过最后一轮调用
	// 验证历史条数——回复消息总数 3 条 + 新入站 1 条）
	if len(r.history("operator", 20)) != 4 {
		t.Fatalf("对话历史条数 = %d，应为 4", len(r.history("operator", 20)))
	}
}

func TestResponderAnsweredGuard(t *testing.T) {
	r, tl := newResponder(t)
	trig := inboundMessage(t, tl, "operator", "你在吗？")
	if out := r.Wake(context.Background(), Wake{Step: trig, Kind: WakeStep}); !strings.Contains(out.Note, "已回复") {
		t.Fatalf("第一次应回复: %+v", out)
	}
	// 同一条消息再次投递（调度器重放/重复投递）：answered 守卫
	// 必须拦下——绝不对"你在吗"回两次。
	out := r.Wake(context.Background(), Wake{Step: trig, Kind: WakeStep})
	if !strings.Contains(out.Note, "跳过") {
		t.Fatalf("answered 守卫失效: %+v", out)
	}
	// 日志里只有一条回复
	steps, _ := tl.Steps()
	replies := 0
	for _, s := range steps {
		if _, ok := s.Field("reply_to"); ok {
			replies++
		}
	}
	if replies != 1 {
		t.Fatalf("回复条数 = %d，应为 1", replies)
	}
}

func TestResponderIgnoresForeignAndSelf(t *testing.T) {
	r, tl := newResponder(t)
	// 发给别人的消息
	other := traj.NewStep("message")
	other.Fields["from"] = "operator"
	other.Fields["to"] = "bob"
	_ = tl.Append(context.Background(), other)
	if out := r.Wake(context.Background(), Wake{Step: other, Kind: WakeStep}); out.Note != "" {
		t.Fatalf("发给别人的消息不应回复: %+v", out)
	}
	// 自己说的话
	self := traj.NewStep("message")
	self.Fields["from"] = "ada"
	self.Fields["to"] = "operator"
	_ = tl.Append(context.Background(), self)
	if out := r.Wake(context.Background(), Wake{Step: self, Kind: WakeStep}); out.Note != "" {
		t.Fatalf("自己的消息不应回复: %+v", out)
	}
}

func TestResponderStaleMessageSkipped(t *testing.T) {
	r, tl := newResponder(t)
	old := traj.NewStep("message")
	old.TS = "2026-01-01T00:00:00.000Z" // 遥远的过去
	old.Fields["from"] = "operator"
	old.Fields["to"] = "ada"
	old.Fields["content"] = "一个月前的你在吗"
	_ = tl.Append(context.Background(), old)
	out := r.Wake(context.Background(), Wake{Step: old, Kind: WakeStep})
	if !strings.Contains(out.Note, "陈旧") {
		t.Fatalf("陈旧消息应跳过: %+v", out)
	}
}

func TestResponderSubscriptionsNeverSelfTrigger(t *testing.T) {
	r, _ := newResponder(t)
	for _, typ := range r.Subscriptions().Types {
		if typ == "message" && r.Subscriptions().TriggerSelf {
			t.Fatal("responder 不应 trigger_self")
		}
	}
}
