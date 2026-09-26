package mind

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"mindloop/internal/task"
	"mindloop/internal/traj"
)

func newMsgTimeline(t *testing.T) *traj.Timeline {
	t.Helper()
	t.Setenv("MINDLOOP_HOME", t.TempDir())
	tl, err := traj.Create(context.Background(), "postmessage-test")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return tl
}

// TestPostMessageFieldContract：PostMessage 产出的 message 步骤必须
// 精确携带 from/to/source/content 四个字段——responder 的定向过滤
// （to == SelfName）、调度器的 FIFO 分类、对话历史组装都建立在这个
// 形状上；字段名漂移是静默失效。协议章（protocol_version）是第五个
// 结构字段：恢复窗口只处理盖章消息，旧历史永远不被补答。
func TestPostMessageFieldContract(t *testing.T) {
	tl := newMsgTimeline(t)
	if err := PostMessage(tl, "operator", "ada", "chat", "你好"); err != nil {
		t.Fatalf("PostMessage: %v", err)
	}
	steps, err := tl.Steps()
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 2 { // 头行 + 消息
		t.Fatalf("步骤数 = %d，应为 2", len(steps))
	}
	s := steps[1]
	if s.Type != traj.TypeMessage {
		t.Fatalf("类型 = %q，应为 %q", s.Type, traj.TypeMessage)
	}
	for k, want := range map[string]string{
		"from":             "operator",
		"to":               "ada",
		"source":           "chat",
		"content":          "你好",
		"protocol_version": strconv.Itoa(task.ProtocolVersion),
	} {
		if got, _ := s.Field(k); got != want {
			t.Fatalf("字段 %s = %q，应为 %q", k, got, want)
		}
	}
}

// TestPostMessageOnceIdempotent：client_message_id 是消息落盘幂等键——
// 同 (from, cid) 同载荷重发返回原步骤且不重复落盘；同键不同内容返回
// ErrMessageConflict（不静默吞掉客户端载荷不一致）；不同 from 用同键
// 不冲突（幂等域是 (from, cid)）；空键不参与查重、不落盘该字段。
func TestPostMessageOnceIdempotent(t *testing.T) {
	tl := newMsgTimeline(t)
	first, err := PostMessageOnce(tl, "you", "ada", "chat", "你好", "cm-1")
	if err != nil {
		t.Fatalf("PostMessageOnce: %v", err)
	}
	if cid, _ := first.Field("client_message_id"); cid != "cm-1" {
		t.Fatalf("落盘步骤应携带 client_message_id，得到 %q", cid)
	}
	again, err := PostMessageOnce(tl, "you", "ada", "chat", "你好", "cm-1")
	if err != nil {
		t.Fatalf("同键同载荷重发应幂等: %v", err)
	}
	if again.StepID != first.StepID {
		t.Fatalf("重发应返回原步骤: %s != %s", again.StepID, first.StepID)
	}
	if _, err := PostMessageOnce(tl, "you", "ada", "chat", "改了内容", "cm-1"); !errors.Is(err, ErrMessageConflict) {
		t.Fatalf("同键不同载荷应为 ErrMessageConflict，得到 %v", err)
	}
	if _, err := PostMessageOnce(tl, "someone", "ada", "chat", "你好", "cm-1"); err != nil {
		t.Fatalf("不同 from 的同 cid 不应冲突: %v", err)
	}
	for i := 0; i < 2; i++ { // 空 cid：两次都真实落盘
		if _, err := PostMessageOnce(tl, "you", "ada", "chat", "无键", ""); err != nil {
			t.Fatal(err)
		}
	}
	steps, err := tl.Steps()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, s := range steps {
		if s.Type != traj.TypeMessage {
			continue
		}
		if cid, ok := s.Field("client_message_id"); ok {
			if cid == "" {
				t.Fatal("空 cid 不应落盘 client_message_id 字段")
			}
		}
		c, _ := s.Field("content")
		if c == "你好" || c == "无键" {
			n++
		}
	}
	if n != 4 { // 你好×2（cm-1 一条 + someone 一条）+ 无键×2
		t.Fatalf("落盘消息数 = %d，应为 4", n)
	}
}

// TestThinkerNamesMatchesBundled：权威名单与 NewMonolith/NewResponder
// 的实际注册名逐一一致——新增思考者忘了同步清单，这里立刻红。
func TestThinkerNamesMatchesBundled(t *testing.T) {
	names := ThinkerNames()
	want := []string{
		NewMonolith(MonolithOptions{}).Name(),
		NewResponder(ResponderOptions{}).Name(),
	}
	if len(names) != len(want) {
		t.Fatalf("ThinkerNames = %v，与实际注册名 %v 数量不符", names, want)
	}
	seen := map[string]bool{}
	for i, n := range names {
		if n == "" {
			t.Fatal("名单含空名")
		}
		if seen[n] {
			t.Fatalf("名单含重复项: %v", names)
		}
		seen[n] = true
		if n != want[i] {
			t.Fatalf("ThinkerNames[%d] = %q，实际注册名 %q", i, n, want[i])
		}
	}
}
