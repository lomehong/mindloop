package mind

import (
	"context"
	"strings"
	"testing"

	"github.com/lomehong/mindloop/internal/llm"
	"github.com/lomehong/mindloop/internal/traj"
)

// recordingThinker 记录每次调用的 system 提示，返回固定短句。
type recordingThinker struct{ systems []string }

func (rt *recordingThinker) Think(_ context.Context, system string, _ []llm.Message) (string, error) {
	rt.systems = append(rt.systems, system)
	return "收到", nil
}

// TestResponderIMSourceInjectsDiscipline：P0-5 词限纪律按 source 键控——
// IM 信道（wecom*）注入 ≤100 字纪律段，CLI/终端（chat/空）不注入。
func TestResponderIMSourceInjectsDiscipline(t *testing.T) {
	makeResponder := func(t *testing.T) (*Responder, *traj.Timeline, *recordingThinker) {
		t.Helper()
		t.Setenv("MINDLOOP_HOME", t.TempDir())
		tl, err := traj.Create(context.Background(), "responder-channel-test")
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		th := &recordingThinker{}
		r := NewResponder(ResponderOptions{
			Timeline: tl, Thinker: th, SelfName: "ada", Persona: "You are ada.",
		}).(*Responder)
		return r, tl, th
	}
	appendInbound := func(t *testing.T, tl *traj.Timeline, source string) traj.Step {
		t.Helper()
		s := traj.NewStep(traj.TypeMessage)
		s.Fields["from"] = "operator"
		s.Fields["to"] = "ada"
		s.Fields["content"] = "在吗"
		if source != "" {
			s.Fields["source"] = source
		}
		if err := tl.Append(context.Background(), s); err != nil {
			t.Fatal(err)
		}
		return s
	}

	t.Run("wecom 注入纪律段", func(t *testing.T) {
		r, tl, th := makeResponder(t)
		trig := appendInbound(t, tl, "wecom")
		out := r.Wake(context.Background(), Wake{Step: trig, Kind: WakeStep})
		if !strings.Contains(out.Note, "已回复") {
			t.Fatalf("outcome = %+v", out)
		}
		if len(th.systems) == 0 || !strings.Contains(th.systems[0], "IM 即时通讯信道") {
			t.Fatalf("wecom 来源应注入词限纪律段，systems=%v", th.systems)
		}
		if !strings.Contains(th.systems[0], "100") {
			t.Fatalf("纪律段应含 100 字上限: %q", th.systems[0])
		}
	})

	t.Run("chat 不注入", func(t *testing.T) {
		r, tl, th := makeResponder(t)
		trig := appendInbound(t, tl, "chat")
		out := r.Wake(context.Background(), Wake{Step: trig, Kind: WakeStep})
		if !strings.Contains(out.Note, "已回复") {
			t.Fatalf("outcome = %+v", out)
		}
		if len(th.systems) == 0 || strings.Contains(th.systems[0], "IM 即时通讯信道") {
			t.Fatalf("chat 来源不应注入纪律段，systems=%v", th.systems)
		}
	})
}
