package mind

import (
	"context"
	"testing"

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
// 形状上；字段名漂移是静默失效。
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
		"from":    "operator",
		"to":      "ada",
		"source":  "chat",
		"content": "你好",
	} {
		if got, _ := s.Field(k); got != want {
			t.Fatalf("字段 %s = %q，应为 %q", k, got, want)
		}
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
