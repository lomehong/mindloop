package mind

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"mindloop/internal/llm"
	"mindloop/internal/task"
	"mindloop/internal/traj"
)

// failingThinker 的每次补全都失败——compose 失败事实的测试模型。
type failingThinker struct{}

func (f *failingThinker) Think(context.Context, string, []llm.Message) (string, error) {
	return "", errors.New("假模型不可用")
}

// newChatResponder 在临时轨迹上构造 SelfName=ada 的 responder——
// 聊天持久化恢复测试的统一基底。
func newChatResponder(t *testing.T, thinker runnerThinker) (*Responder, *traj.Timeline) {
	t.Helper()
	tl := newTestTimeline(t)
	r := NewResponder(ResponderOptions{
		Timeline: tl, Thinker: thinker, SelfName: "ada", Persona: "You are ada.",
	}).(*Responder)
	return r, tl
}

// newProtocolInbound 构造一条盖过协议章的入站消息（不落盘；调用方
// 可先改 TS 等信封字段再 appendTraj）。
func newProtocolInbound(from, to, content string) traj.Step {
	s := traj.NewStep(traj.TypeMessage)
	s.Fields["protocol_version"] = task.ProtocolVersion
	s.Fields["from"] = from
	s.Fields["to"] = to
	s.Fields["source"] = "chat"
	s.Fields["content"] = content
	return s
}

// legacyInbound 落一条旧历史消息（无协议章）——恢复不得触碰。
func legacyInbound(t *testing.T, tl *traj.Timeline, ts, content string) traj.Step {
	t.Helper()
	s := traj.NewStep(traj.TypeMessage)
	s.TS = ts
	s.Fields["from"] = "operator"
	s.Fields["to"] = "ada"
	s.Fields["content"] = content
	return appendTraj(t, tl, s)
}

func appendTraj(t *testing.T, tl *traj.Timeline, s traj.Step) traj.Step {
	t.Helper()
	if err := tl.Append(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	return s
}

// chatReply 写一条普通回复（非状态收据）并盖 reply_to 章。
func chatReply(t *testing.T, tl *traj.Timeline, target string) traj.Step {
	t.Helper()
	s := traj.NewStep(traj.TypeMessage)
	s.Fields["from"] = "ada"
	s.Fields["to"] = "operator"
	s.Fields["source"] = "chat"
	s.Fields["reply_to"] = target
	s.Fields["content"] = "回复"
	return appendTraj(t, tl, s)
}

// findStatus 返回指向 target 的状态收据（source=reply-status）。
func findStatus(t *testing.T, tl *traj.Timeline, target string) (traj.Step, bool) {
	t.Helper()
	steps, err := tl.Steps()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range steps {
		if src, _ := s.Field("source"); src != "reply-status" {
			continue
		}
		if rt, _ := s.Field("reply_to"); rt == target {
			return s, true
		}
	}
	return traj.Step{}, false
}

// countStatusMarks 统计指向 target 的状态收据条数（幂等断言用）。
func countStatusMarks(t *testing.T, tl *traj.Timeline, target string) int {
	t.Helper()
	steps, err := tl.Steps()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, s := range steps {
		if src, _ := s.Field("source"); src != "reply-status" {
			continue
		}
		if rt, _ := s.Field("reply_to"); rt == target {
			n++
		}
	}
	return n
}

// findReply 返回指向 target 的普通回复内容（不含状态收据）。
func findReply(tl *traj.Timeline, target string) (string, bool) {
	steps, err := tl.Steps()
	if err != nil {
		return "", false
	}
	for _, s := range steps {
		if src, _ := s.Field("source"); src == "reply-status" {
			continue
		}
		if rt, _ := s.Field("reply_to"); rt == target {
			c, _ := s.Field("content")
			return c, true
		}
	}
	return "", false
}

// countReplies 统计普通回复（带 reply_to、非状态收据）总数。
func countReplies(tl *traj.Timeline) int {
	steps, err := tl.Steps()
	if err != nil {
		return -1
	}
	n := 0
	for _, s := range steps {
		if _, ok := s.Field("reply_to"); !ok {
			continue
		}
		if src, _ := s.Field("source"); src == "reply-status" {
			continue
		}
		n++
	}
	return n
}

// TestResponderPendingFindsOldestUnrepliedProtocolMessage：持久待办
// 只发现"盖过协议章、未处理"的消息，按日志序最旧优先；旧历史
// （无章）不参与恢复补答；处理完一条轮下一条，全部处理完后为空。
func TestResponderPendingFindsOldestUnrepliedProtocolMessage(t *testing.T) {
	r, tl := newChatResponder(t, &cannedThinker{reply: "收到"})
	legacyInbound(t, tl, traj.NowString(), "旧时代的消息")
	first := appendTraj(t, tl, newProtocolInbound("operator", "ada", "第一条"))
	second := appendTraj(t, tl, newProtocolInbound("operator", "ada", "第二条"))

	wake, ok, err := r.Pending(context.Background())
	if err != nil || !ok {
		t.Fatalf("Pending = %v, %v", ok, err)
	}
	if wake.Kind != WakeStep || wake.Step.StepID != first.StepID {
		t.Fatalf("应先恢复最旧的盖章消息: kind=%v step=%s", wake.Kind, wake.Step.StepID)
	}
	// Pending 只发现不消费：消息被处理前重复查询返回同一条——
	// “取出即丢”会让投递失败变成静默丢失。
	wake2, ok2, err2 := r.Pending(context.Background())
	if err2 != nil || !ok2 || wake2.Step.StepID != first.StepID {
		t.Fatalf("未处理期间应稳定返回同一条: %+v %v %v", wake2, ok2, err2)
	}
	chatReply(t, tl, first.StepID)
	wake, ok, err = r.Pending(context.Background())
	if err != nil || !ok || wake.Step.StepID != second.StepID {
		t.Fatalf("第一条处理后应轮到第二条: %+v %v %v", wake, ok, err)
	}
	chatReply(t, tl, second.StepID)
	if _, ok, err := r.Pending(context.Background()); err != nil || ok {
		t.Fatalf("全部处理后不应再有待办: %v %v", ok, err)
	}
}

// TestResponderPendingIgnoresTaskSubmissions：显式委托是任务队列的
// 事实，不是聊天待办——恢复补答绝不能把它变成"聊天回复"。
func TestResponderPendingIgnoresTaskSubmissions(t *testing.T) {
	r, tl := newChatResponder(t, &cannedThinker{reply: "收到"})
	submitTask(t, task.New(tl, "ada"), "not-chat")
	if _, ok, err := r.Pending(context.Background()); err != nil || ok {
		t.Fatalf("显式委托不得进入聊天待办: %v %v", ok, err)
	}
}

// TestResponderRecoverMarksExpiredNoReply：恢复只补答窗口内的新协议
// 消息；窗口外的写 no-reply 持久标记；无章旧历史完全不被触碰；
// 重复恢复幂等（已有收据不重写）。
func TestResponderRecoverMarksExpiredNoReply(t *testing.T) {
	r, tl := newChatResponder(t, &cannedThinker{reply: "收到"})
	stale := newProtocolInbound("operator", "ada", "过期的新协议消息")
	stale.TS = "2026-01-01T00:00:00.000Z"
	appendTraj(t, tl, stale)
	fresh := appendTraj(t, tl, newProtocolInbound("operator", "ada", "窗口内的消息"))
	legacyStale := legacyInbound(t, tl, "2026-01-01T00:00:00.000Z", "过期的旧历史")

	if err := r.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	mark, ok := findStatus(t, tl, stale.StepID)
	if !ok {
		t.Fatal("过期盖章消息未写 no-reply 标记")
	}
	if state, _ := mark.Field("state"); state != "no-reply" {
		t.Fatalf("state = %q", state)
	}
	if from, _ := mark.Field("from"); from != "ada" {
		t.Fatalf("标记 from = %q", from)
	}
	if to, _ := mark.Field("to"); to != "operator" {
		t.Fatalf("标记 to = %q", to)
	}
	if by, _ := mark.Field("launched_by"); by != "responder" {
		t.Fatalf("标记 launched_by = %q", by)
	}
	if content, _ := mark.Field("content"); content == "" {
		t.Fatal("标记缺少人类可读说明")
	}
	if _, ok := findStatus(t, tl, fresh.StepID); ok {
		t.Fatal("窗口内消息不应被清算")
	}
	if _, ok := findStatus(t, tl, legacyStale.StepID); ok {
		t.Fatal("无章旧历史不得被标记")
	}
	// 窗口内的消息留给 Pending 补答，且只返回它。
	wake, ok, err := r.Pending(context.Background())
	if err != nil || !ok || wake.Step.StepID != fresh.StepID {
		t.Fatalf("恢复后应补答窗口内消息: %+v %v %v", wake, ok, err)
	}
	if err := r.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := countStatusMarks(t, tl, stale.StepID); got != 1 {
		t.Fatalf("重复恢复写了 %d 条标记", got)
	}
}

// TestResponderWakeStaleMarksNoReply：投递路径的陈旧守卫对新协议
// 消息留下 no-reply 事实——过期不会停留在"未决"，也不会被追答；
// 收据即事实，重复投递被 answered 守卫拦住。
func TestResponderWakeStaleMarksNoReply(t *testing.T) {
	r, tl := newChatResponder(t, &cannedThinker{reply: "不应送达"})
	stale := newProtocolInbound("operator", "ada", "一个月前的你在吗")
	stale.TS = "2026-01-01T00:00:00.000Z"
	appendTraj(t, tl, stale)

	out := r.Wake(context.Background(), Wake{Step: stale, Kind: WakeStep})
	if !strings.Contains(out.Note, "陈旧") {
		t.Fatalf("outcome = %+v", out)
	}
	if _, ok := findReply(tl, stale.StepID); ok {
		t.Fatal("陈旧消息被追答")
	}
	mark, ok := findStatus(t, tl, stale.StepID)
	if !ok {
		t.Fatal("陈旧收据未落盘")
	}
	if state, _ := mark.Field("state"); state != "no-reply" {
		t.Fatalf("state = %q", state)
	}
	if _, ok, _ := r.Pending(context.Background()); ok {
		t.Fatal("已清算的消息不应再进入待办")
	}
	if out := r.Wake(context.Background(), Wake{Step: stale, Kind: WakeStep}); !strings.Contains(out.Note, "跳过") {
		t.Fatalf("重复投递应被 answered 守卫拦住: %+v", out)
	}
}

// TestResponderFailureRecordsFactAndSuppressesRetry：compose 失败写
// reply-failed 持久事实；失败事实抑制自动重试（防重试轰炸）；无章
// 旧消息保持原行为（只有 Note，不写标记）。
func TestResponderFailureRecordsFactAndSuppressesRetry(t *testing.T) {
	r, tl := newChatResponder(t, &failingThinker{})
	failed := appendTraj(t, tl, newProtocolInbound("operator", "ada", "这条会失败"))
	out := r.Wake(context.Background(), Wake{Step: failed, Kind: WakeStep})
	if !strings.Contains(out.Note, "回复失败") {
		t.Fatalf("outcome = %+v", out)
	}
	mark, ok := findStatus(t, tl, failed.StepID)
	if !ok {
		t.Fatal("compose 失败未写失败事实")
	}
	if state, _ := mark.Field("state"); state != "reply-failed" {
		t.Fatalf("state = %q", state)
	}
	if _, ok, _ := r.Pending(context.Background()); ok {
		t.Fatal("失败事实后不得自动重试")
	}
	legacy := legacyInbound(t, tl, traj.NowString(), "旧消息失败不写标记")
	_ = r.Wake(context.Background(), Wake{Step: legacy, Kind: WakeStep})
	if _, ok := findStatus(t, tl, legacy.StepID); ok {
		t.Fatal("无章旧消息不得被写标记")
	}
}

// TestResponderCanceledDeliveryIsRetryable：停机取消导致的回复落盘
// 失败不是失败事实——重启恢复必须还能补答这条消息（不能抑制）。
func TestResponderCanceledDeliveryIsRetryable(t *testing.T) {
	r, tl := newChatResponder(t, &cannedThinker{reply: "回复"})
	m := appendTraj(t, tl, newProtocolInbound("operator", "ada", "停机时送达"))
	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	out := r.Wake(cctx, Wake{Step: m, Kind: WakeStep})
	if !strings.Contains(out.Note, "落盘失败") {
		t.Fatalf("outcome = %+v", out)
	}
	wake, ok, err := r.Pending(context.Background())
	if err != nil || !ok || wake.Step.StepID != m.StepID {
		t.Fatalf("停机取消不应留下抑制: %+v %v %v", wake, ok, err)
	}
}

// TestResponderSuppressedMessageNotPending：进程内抑制（落盘双失败
// 时的最后防线）让 Pending 跳过该消息，不面对锁超时这类环境故障的
// 注入也能钉住抑制的可见效果。
func TestResponderSuppressedMessageNotPending(t *testing.T) {
	r, tl := newChatResponder(t, &cannedThinker{reply: "收到"})
	m := appendTraj(t, tl, newProtocolInbound("operator", "ada", "将被抑制"))
	r.suppress(m.StepID)
	if _, ok, err := r.Pending(context.Background()); err != nil || ok {
		t.Fatalf("抑制中的消息不应进入待办: %v %v", ok, err)
	}
}

// TestResponderHistoryExcludesReplyStatus：状态收据是元事实，不得
// 混入对话历史——否则模型会在后续回复里模仿"系统收据"腔调。
func TestResponderHistoryExcludesReplyStatus(t *testing.T) {
	r, tl := newChatResponder(t, &cannedThinker{reply: "回复"})
	chatReply(t, tl, "unused-target")
	mark := traj.NewStep(traj.TypeMessage)
	mark.Fields["from"], mark.Fields["to"] = "ada", "operator"
	mark.Fields["source"] = "reply-status"
	mark.Fields["state"] = "no-reply"
	mark.Fields["reply_to"] = "some-old-message"
	mark.Fields["content"] = "系统状态收据不应进历史"
	appendTraj(t, tl, mark)
	msgs := r.history("operator", 20)
	if len(msgs) == 0 {
		t.Fatal("普通对话应从历史中可见")
	}
	for _, msg := range msgs {
		if strings.Contains(msg.Content, "系统状态收据") {
			t.Fatalf("状态收据混入对话历史: %+v", msg)
		}
	}
}

// TestDispatcherRecoversChatWithinWindow：端到端恢复语义——窗口内
// 的盖章消息在启动后被补答；窗口外的被标记 no-reply 而非追答；
// 旧历史（无章）完全不被触碰。
func TestDispatcherRecoversChatWithinWindow(t *testing.T) {
	tl := newTestTimeline(t)
	stale := newProtocolInbound("operator", "ada", "停机前太久")
	stale.TS = "2026-01-01T00:00:00.000Z"
	appendTraj(t, tl, stale)
	fresh := appendTraj(t, tl, newProtocolInbound("operator", "ada", "等待补答"))
	legacy := legacyInbound(t, tl, traj.NowString(), "旧历史")
	r := NewResponder(ResponderOptions{
		Timeline: tl, Thinker: &cannedThinker{reply: "补答"}, SelfName: "ada",
	}).(*Responder)
	_, cancel, done := startTaskDispatcher(t, tl, r)
	waitFor(t, 5*time.Second, func() bool {
		c, ok := findReply(tl, fresh.StepID)
		return ok && strings.Contains(c, "补答")
	})
	if _, ok := findStatus(t, tl, stale.StepID); !ok {
		t.Fatal("过期消息未在恢复时清算")
	}
	if _, ok := findReply(tl, stale.StepID); ok {
		t.Fatal("过期消息被追答")
	}
	if _, ok := findReply(tl, legacy.StepID); ok {
		t.Fatal("旧历史被补答")
	}
	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("调度器退出: %v", err)
	}
}

// TestDispatcherKeepsEveryPendingChatMessage：运行中连发 20 条（超出
// 16 条 FIFO 上限）——被内存队列丢弃的消息必须由持久待办重新发现，
// 全部得到回复且不重复。
func TestDispatcherKeepsEveryPendingChatMessage(t *testing.T) {
	tl := newTestTimeline(t)
	r := NewResponder(ResponderOptions{
		Timeline: tl, Thinker: &cannedThinker{reply: "收到"}, SelfName: "ada",
	}).(*Responder)
	d, cancel, done := startTaskDispatcher(t, tl, r)
	sentinel := appendTraj(t, tl, newProtocolInbound("operator", "ada", "哨兵"))
	waitFor(t, 10*time.Second, func() bool { _, ok := findReply(tl, sentinel.StepID); return ok })

	var sent []traj.Step
	for i := 0; i < 20; i++ {
		sent = append(sent, appendTraj(t, tl, newProtocolInbound("operator", "ada", fmt.Sprintf("第 %d 条", i))))
	}
	waitFor(t, 30*time.Second, func() bool {
		for _, m := range sent {
			if _, ok := findReply(tl, m.StepID); !ok {
				return false
			}
		}
		return true
	})
	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("调度器退出: %v", err)
	}
	if !d.WaitIdle(5 * time.Second) {
		t.Fatal("未收尾")
	}
	if got := countReplies(tl); got != 21 {
		t.Fatalf("回复总数 = %d，应为 21（超载不丢、不重复）", got)
	}
}
