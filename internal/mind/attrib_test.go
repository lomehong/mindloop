package mind

import (
	"context"
	"errors"
	"strings"
	"testing"

	"mindloop/internal/llm"
	"mindloop/internal/task"
	"mindloop/internal/traj"
)

// TestExecuteTaskAttribution：显式任务的模型调用归因——task/run/attempt
// 来自任务记录，thinker/phase 标明"monolith 的委托执行"。
func TestExecuteTaskAttribution(t *testing.T) {
	tl := newTestTimeline(t)
	s := task.New(tl, "ada")
	submitted := submitTask(t, s, "attrib-task")

	var attrs []llm.Attrib
	m := NewMonolith(MonolithOptions{Timeline: tl, SelfName: "ada",
		Thinker: taskModelFunc(func(context.Context, string, []llm.Message) (string, error) {
			return "", errors.New("显式任务不应使用思考档")
		}),
		RequestThinker: taskModelFunc(func(ctx context.Context, _ string, _ []llm.Message) (string, error) {
			attrs = append(attrs, llm.AttribFrom(ctx))
			return `FINAL="归因探针完成"`, nil
		}),
	})
	m.Wake(context.Background(), Wake{Step: syntheticStep(monolithWakeType), Kind: WakeScheduled})

	got := readTask(t, s, submitted.ID)
	if len(attrs) == 0 {
		t.Fatal("任务执行未发起模型调用")
	}
	a := attrs[0]
	if a.Task != submitted.ID || a.Run != got.RunID || a.Attempt != 1 {
		t.Fatalf("任务归因 = %+v（want task=%s run=%s attempt=1）", a, submitted.ID, got.RunID)
	}
	if a.Thinker != "monolith" || a.Phase != "task" {
		t.Fatalf("thinker/phase = %+v", a)
	}
}

// TestWakeAttribution：自主唤醒的模型调用归因——phase=wake、wake=原因、
// thinker=monolith；无任务时 task/attempt 保持空。
func TestWakeAttribution(t *testing.T) {
	tl := newTestTimeline(t)
	var attrs []llm.Attrib
	m := NewMonolith(MonolithOptions{Timeline: tl, SelfName: "ada",
		Thinker: taskModelFunc(func(ctx context.Context, _ string, _ []llm.Message) (string, error) {
			attrs = append(attrs, llm.AttribFrom(ctx))
			return "", errors.New("探针主动失败")
		}),
	})
	m.Wake(context.Background(), Wake{Step: syntheticStep(monolithWakeType), Kind: WakeScheduled})

	if len(attrs) == 0 {
		t.Fatal("唤醒未发起模型调用")
	}
	a := attrs[0]
	if a.Thinker != "monolith" || a.Phase != "wake" || a.Wake != "scheduled spontaneity" {
		t.Fatalf("唤醒归因 = %+v", a)
	}
	if a.Task != "" || a.Attempt != 0 {
		t.Fatalf("自主唤醒不应带任务归因: %+v", a)
	}
}

// TestResponderChatAttribution：聊天回复的模型调用归因——thinker=responder、
// phase=chat（用量台账据此区分聊天与任务/唤醒的开销）。
func TestResponderChatAttribution(t *testing.T) {
	t.Setenv("MINDLOOP_HOME", t.TempDir())
	tl, err := traj.Create(context.Background(), "responder-attrib")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	var attrs []llm.Attrib
	r := NewResponder(ResponderOptions{
		Timeline: tl,
		Thinker: taskModelFunc(func(ctx context.Context, _ string, _ []llm.Message) (string, error) {
			attrs = append(attrs, llm.AttribFrom(ctx))
			return "收到。", nil
		}),
		SelfName: "ada",
		Persona:  "You are ada.",
	}).(*Responder)

	trig := inboundMessage(t, tl, "operator", "在吗？")
	if out := r.Wake(context.Background(), Wake{Step: trig, Kind: WakeStep}); !strings.Contains(out.Note, "已回复") {
		t.Fatalf("outcome = %+v", out)
	}
	if len(attrs) == 0 {
		t.Fatal("回复未发起模型调用")
	}
	a := attrs[0]
	if a.Thinker != "responder" || a.Phase != "chat" {
		t.Fatalf("聊天归因 = %+v", a)
	}
}
