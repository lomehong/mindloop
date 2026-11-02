package mind

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lomehong/mindloop/internal/llm"
	"github.com/lomehong/mindloop/internal/task"
	"github.com/lomehong/mindloop/internal/traj"
)

// advisorFake 记录调用并返回预设输出。
type advisorFake struct {
	reply string
	calls int
}

func (a *advisorFake) Think(_ context.Context, _ string, _ []llm.Message) (string, error) {
	a.calls++
	return a.reply, nil
}

func newAdvisorEnv(t *testing.T) (*traj.Timeline, *task.Store) {
	t.Helper()
	t.Setenv("MINDLOOP_HOME", t.TempDir())
	tl, err := traj.Create(context.Background(), "advisor-test")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return tl, task.New(tl, "ada")
}

// seedRunning 提交任务并领取（置 running）。
func seedRunning(t *testing.T, store *task.Store, content string) task.Task {
	t.Helper()
	item, err := store.Submit(context.Background(), task.Submission{From: "operator", ClientMessageID: time.Now().Format("150405.000000000"), Content: content})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	claimed, err := store.Claim(context.Background(), "run-advisor-1")
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if claimed.ID != item.ID {
		t.Fatalf("Claim 领取了别的任务: %s != %s", claimed.ID, item.ID)
	}
	return item
}

func stepsOfAlerts(t *testing.T, tl *traj.Timeline) []traj.Step {
	t.Helper()
	steps, _ := tl.Steps()
	var out []traj.Step
	for _, s := range steps {
		if s.Type == traj.TypeAlert {
			out = append(out, s)
		}
	}
	return out
}

func TestAdvisorDriftWritesAlert(t *testing.T) {
	tl, store := newAdvisorEnv(t)
	item := seedRunning(t, store, "修复登录页的空指针崩溃")
	// 一条盖 task 章的南辕北辙步骤。
	act := traj.NewStep(traj.TypeAction)
	act.Fields["task_id"] = item.ID
	act.Fields["content"] = "开始重构数据库迁移脚本（与登录页无关）"
	_ = tl.Append(context.Background(), act)

	fake := &advisorFake{reply: `{"verdict":"drift","reason":"执行轨迹在重构数据库迁移，与登录页空指针修复目标无关"}`}
	a := NewAdvisor(AdvisorOptions{Timeline: tl, Thinker: fake, SelfName: "ada"})
	out := a.Wake(context.Background(), Wake{Kind: WakeWatchdog})

	if !strings.Contains(out.Note, "drift") {
		t.Fatalf("outcome 应报告 drift: %+v", out)
	}
	alerts := stepsOfAlerts(t, tl)
	if len(alerts) != 1 {
		t.Fatalf("应落 1 条 alert，得 %d", len(alerts))
	}
	al := alerts[0]
	if k, _ := al.Field("kind"); k != "task-drift" {
		t.Fatalf("kind = %q", k)
	}
	if tid, _ := al.Field("task_id"); tid != item.ID {
		t.Fatalf("task_id = %q", tid)
	}
	if c, _ := al.Field("content"); !strings.Contains(c, "cancel") {
		t.Fatalf("告警应说明 cancel 权在操作员: %q", c)
	}
}

func TestAdvisorOnTrackQuiet(t *testing.T) {
	tl, store := newAdvisorEnv(t)
	seedRunning(t, store, "写周报")
	fake := &advisorFake{reply: `{"verdict":"on-track","reason":"正在汇总数据"}`}
	a := NewAdvisor(AdvisorOptions{Timeline: tl, Thinker: fake, SelfName: "ada"})
	out := a.Wake(context.Background(), Wake{Kind: WakeWatchdog})
	if !strings.Contains(out.Note, "on-track") {
		t.Fatalf("outcome: %+v", out)
	}
	if alerts := stepsOfAlerts(t, tl); len(alerts) != 0 {
		t.Fatalf("on-track 不应告警，得 %d 条", len(alerts))
	}
}

func TestAdvisorNoActiveTaskZeroCalls(t *testing.T) {
	tl, _ := newAdvisorEnv(t)
	fake := &advisorFake{reply: "{}"}
	a := NewAdvisor(AdvisorOptions{Timeline: tl, Thinker: fake, SelfName: "ada"})
	a.Wake(context.Background(), Wake{Kind: WakeWatchdog})
	if fake.calls != 0 {
		t.Fatalf("无在途任务应零模型调用，得 %d", fake.calls)
	}
	// 不可解析输出保守判 on-track。
	fake2 := &advisorFake{reply: "我觉得挺好的"}
	tl2, store2 := newAdvisorEnv(t)
	seedRunning(t, store2, "随便什么任务")
	a2 := NewAdvisor(AdvisorOptions{Timeline: tl2, Thinker: fake2, SelfName: "ada"})
	if out := a2.Wake(context.Background(), Wake{Kind: WakeWatchdog}); !strings.Contains(out.Note, "on-track") {
		t.Fatalf("不可解析应保守 on-track: %+v", out)
	}
}
