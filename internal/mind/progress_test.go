package mind

import (
	"context"
	"strings"
	"testing"
	"time"

	"mindloop/internal/traj"
)

// captureThinker 复用 extension_test.go 的同名助手（记录系统提示）。

// TestResponderProgressDigestInjectsWorkSummary：responder 上下文
// 注入近期工作摘要——ada 实测被问进度两次回答「没有任何命令输出」，
// 根因是上下文只有消息类步骤，对 monolith 的工作成果全盲。
func TestResponderProgressDigestInjectsWorkSummary(t *testing.T) {
	tl := newMsgTimeline(t)

	// 工作痕迹：action + shell-output（应进摘要）；prompt/run（应被
	// 过滤）；空正文簿记（应被过滤）；对话消息（属历史，不进摘要）。
	action := traj.NewStep(traj.TypeAction)
	action.Fields["content"] = "完成了锁重构与并发评审"
	if err := tl.Append(context.Background(), action); err != nil {
		t.Fatal(err)
	}
	out := traj.NewStep(traj.TypeShellOutput)
	out.Fields["content"] = "PASS 42 tests\nFAIL 0"
	if err := tl.Append(context.Background(), out); err != nil {
		t.Fatal(err)
	}
	run := traj.NewStep(traj.TypeRun)
	run.Fields["task"] = "这个不该出现在摘要里"
	if err := tl.Append(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	prompt := traj.NewStep(traj.TypePrompt)
	prompt.Fields["content"] = "任务原文也不该出现"
	if err := tl.Append(context.Background(), prompt); err != nil {
		t.Fatal(err)
	}
	empty := traj.NewStep(traj.TypeFork) // 无正文：不进摘要
	if err := tl.Append(context.Background(), empty); err != nil {
		t.Fatal(err)
	}
	msg := traj.NewStep(traj.TypeMessage)
	msg.Fields["from"] = "operator"
	msg.Fields["to"] = "ada"
	msg.Fields["content"] = "进度如何了？"
	if err := tl.Append(context.Background(), msg); err != nil {
		t.Fatal(err)
	}

	thinker := &captureThinker{}
	r := NewResponder(ResponderOptions{
		Timeline: tl,
		Thinker:  thinker,
		SelfName: "ada",
	})
	res := r.(*Responder)
	if _, err := res.compose(context.Background(), "operator", "进度如何了？", nil); err != nil {
		t.Fatalf("compose: %v", err)
	}

	sys := thinker.got()
	if !strings.Contains(sys, "[action] 完成了锁重构与并发评审") {
		t.Fatalf("action 步骤未进工作摘要:\n%s", sys)
	}
	if !strings.Contains(sys, "[shell-output] PASS 42 tests") {
		t.Fatalf("shell-output 步骤未进工作摘要:\n%s", sys)
	}
	if !strings.Contains(sys, "真实工作记录") || !strings.Contains(sys, "据此如实回答") {
		t.Fatalf("摘要缺少如实回答声明:\n%s", sys)
	}
	if strings.Contains(sys, "不该出现") || strings.Contains(sys, "[run]") || strings.Contains(sys, "[prompt]") {
		t.Fatalf("簿记步骤泄漏进了摘要:\n%s", sys)
	}
}

// TestResponderProgressDigestEmptyKeepsPromptUntouched：没有任何
// 可摘要步骤时，系统提示必须与旧行为逐字节一致（摘要段缺席）。
func TestResponderProgressDigestEmptyKeepsPromptUntouched(t *testing.T) {
	tl := newMsgTimeline(t)
	inboundMessage(t, tl, "operator", "在吗") // 只有对话消息：不进摘要

	thinker := &captureThinker{}
	r := NewResponder(ResponderOptions{Timeline: tl, Thinker: thinker, SelfName: "ada"})
	res := r.(*Responder)
	if _, err := res.compose(context.Background(), "operator", "在吗", nil); err != nil {
		t.Fatalf("compose: %v", err)
	}
	if strings.Contains(thinker.got(), "Work digest") {
		t.Fatalf("无可摘要步骤时不应注入摘要段:\n%s", thinker.got())
	}
	if !strings.Contains(thinker.got(), "You are also the voice of the agent") {
		t.Fatalf("系统提示模板缺失:\n%s", thinker.got())
	}
}

// TestMonolithReportsUnfinishedWorkOnMaxIterations：轮次耗尽（没有
// FINAL）时，最后 reasoning 的实质内容必须作为消息投递给 operator
// ——ada 实测评审做完但轮次耗尽，成果静默丢失。
func TestMonolithReportsUnfinishedWorkOnMaxIterations(t *testing.T) {
	tl := newMsgTimeline(t)
	th := NewMonolith(MonolithOptions{
		Timeline: tl,
		Thinker: &scriptThinker{responses: []string{
			"echo work-in-progress", // 无 fence 无 FINAL：轮次打满
			"echo work-in-progress",
		}},
		MaxIterations: 2,
		SelfName:      "ada",
		Backoff:       &BackoffPolicy{Base: 2 * time.Second, Max: time.Minute, ThoughtCap: 30 * time.Second},
	})
	th.Wake(context.Background(), Wake{Step: syntheticStep(monolithWakeType), Kind: WakeWatchdog})

	steps, err := tl.Steps()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range steps {
		if s.Type != traj.TypeMessage {
			continue
		}
		from, _ := s.Field("from")
		to, _ := s.Field("to")
		if from != "ada" || to != "operator" {
			continue
		}
		content, _ := s.Field("content")
		if strings.Contains(content, "轮次耗尽") && strings.Contains(content, "work-in-progress") {
			found = true
		}
	}
	if !found {
		t.Fatal("轮次耗尽后未投递最后阶段的思考摘要")
	}
}

// TestMonolithNoReportWhenFinalProduced：正常完成（有 FINAL）不应
// 产生工作摘要消息——报告只服务轮次耗尽路径。
func TestMonolithNoReportWhenFinalProduced(t *testing.T) {
	tl := newMsgTimeline(t)
	th := NewMonolith(MonolithOptions{
		Timeline: tl,
		Thinker: &scriptThinker{responses: []string{
			fence(`FINAL="顺利完成"`),
		}},
		SelfName: "ada",
	})
	th.Wake(context.Background(), Wake{Step: syntheticStep(monolithWakeType), Kind: WakeScheduled})

	steps, _ := tl.Steps()
	for _, s := range steps {
		if s.Type != traj.TypeMessage {
			continue
		}
		if to, _ := s.Field("to"); to == "operator" {
			t.Fatal("正常完成不应投递轮次耗尽摘要")
		}
	}
}

// TestMonolithReportTruncatesTo500Runes：长产出裁到 ~500 rune。
func TestMonolithReportTruncatesTo500Runes(t *testing.T) {
	tl := newMsgTimeline(t)
	long := "echo " + strings.Repeat("x", 600)
	th := NewMonolith(MonolithOptions{
		Timeline: tl,
		Thinker: &scriptThinker{responses: []string{
			long,
			long,
		}},
		MaxIterations: 2,
		SelfName:      "ada",
		Backoff:       &BackoffPolicy{Base: 2 * time.Second, Max: time.Minute, ThoughtCap: 30 * time.Second},
	})
	th.Wake(context.Background(), Wake{Step: syntheticStep(monolithWakeType), Kind: WakeWatchdog})

	steps, _ := tl.Steps()
	found := false
	for _, s := range steps {
		if s.Type != traj.TypeMessage {
			continue
		}
		if to, _ := s.Field("to"); to != "operator" {
			continue
		}
		content, _ := s.Field("content")
		body := content[strings.Index(content, "\n")+1:] // 跳过说明行
		if n := len([]rune(body)); n > 501 {
			t.Fatalf("摘要正文 %d rune，应 ≤ 500+省略号", n)
		}
		found = true
	}
	if !found {
		t.Fatal("长产出未投递")
	}
}
