package mind

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"mindloop/internal/llm"
	"mindloop/internal/recap"
	"mindloop/internal/task"
	"mindloop/internal/traj"
)

type taskModelFunc func(context.Context, string, []llm.Message) (string, error)

func (f taskModelFunc) Think(ctx context.Context, system string, msgs []llm.Message) (string, error) {
	return f(ctx, system, msgs)
}

func submitTask(t *testing.T, s *task.Store, key string) task.Task {
	t.Helper()
	got, err := s.Submit(context.Background(), task.Submission{From: "operator", ClientMessageID: key, Content: "完整要求-" + key})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func readTask(t *testing.T, s *task.Store, id string) task.Task {
	t.Helper()
	got, err := s.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestMonolithTaskPersistsCorrelatedResult(t *testing.T) {
	tl := newTestTimeline(t)
	s := task.New(tl, "ada")
	first, second := submitTask(t, s, "first"), submitTask(t, s, "second")
	calls := 0
	m := NewMonolith(MonolithOptions{Timeline: tl, SelfName: "ada",
		Thinker: taskModelFunc(func(context.Context, string, []llm.Message) (string, error) {
			return "", errors.New("显式任务不应使用思考档")
		}),
		RequestThinker: taskModelFunc(func(_ context.Context, _ string, msgs []llm.Message) (string, error) {
			calls++
			want, forbidden := first.Content, second.Content
			if calls == 2 {
				want, forbidden = forbidden, want
			}
			var input string
			for _, msg := range msgs {
				input += msg.Content
			}
			if !strings.Contains(input, want) || strings.Contains(input, forbidden) {
				t.Errorf("任务上下文串线: %s", input)
			}
			return fmt.Sprintf("FINAL=\"结果-%d\"", calls), nil
		}),
	})
	for i, request := range []task.Task{first, second} {
		m.Wake(context.Background(), Wake{Step: syntheticStep(monolithWakeType), Kind: WakeScheduled})
		got := readTask(t, s, request.ID)
		if got.State != task.Succeeded || got.Result != fmt.Sprintf("结果-%d", i+1) || got.ResultKind != "model-final" || got.RunID == "" || len(got.EvidenceStepIDs) != 1 {
			t.Fatalf("结果未落任务事实: %+v", got)
		}
		steps, err := tl.Steps()
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, step := range steps {
			if step.StepID != got.EvidenceStepIDs[0] {
				continue
			}
			tid, _ := step.Field("task_id")
			rid, _ := step.Field("run_id")
			found = step.Type == traj.TypeFinal && tid == got.ID && rid == got.RunID
		}
		if !found {
			t.Fatal("结果证据未指向对应 task/run")
		}
	}
	if calls != 2 {
		t.Fatalf("显式任务调用次数=%d", calls)
	}
}

func startTaskDispatcher(t *testing.T, tl *traj.Timeline, thinker Thinker) (*Dispatcher, context.CancelFunc, <-chan error) {
	t.Helper()
	lock, owned, err := TryRunLock(tl)
	if err != nil || !owned {
		t.Fatalf("运行锁: %v", err)
	}
	d := NewDispatcher(tl, 5*time.Millisecond)
	d.Register(thinker)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runOwned(d, ctx, lock) }()
	t.Cleanup(func() { cancel(); d.WaitIdle(time.Second); lock.Release() })
	return d, cancel, done
}

func TestDispatcherRecoversOnlyQueuedTasks(t *testing.T) {
	for _, state := range []task.State{task.Running, task.AwaitingApproval, task.Canceling} {
		t.Run(string(state), func(t *testing.T) {
			tl := newTestTimeline(t)
			s := task.New(tl, "ada")
			old := submitTask(t, s, "old")
			if _, err := s.Claim(context.Background(), "old-run"); err != nil {
				t.Fatal(err)
			}
			if state != task.Running {
				if _, err := s.Advance(context.Background(), old.ID, task.Event{State: state, Attempt: 1, RunID: "old-run"}); err != nil {
					t.Fatal(err)
				}
			}
			queued := submitTask(t, s, "queued-before-start")
			var calls atomic.Int32
			m := NewMonolith(MonolithOptions{Timeline: tl, SelfName: "ada", Watchdog: time.Hour,
				Thinker: taskModelFunc(func(context.Context, string, []llm.Message) (string, error) {
					calls.Add(1)
					return `FINAL="恢复完成"`, nil
				}),
			})
			d, cancel, done := startTaskDispatcher(t, tl, m)
			waitFor(t, 3*time.Second, func() bool { return readTask(t, s, queued.ID).State == task.Succeeded })
			cancel()
			<-done
			if !d.WaitIdle(time.Second) {
				t.Fatal("未收尾")
			}
			if got := readTask(t, s, old.ID); got.State != task.Interrupted || got.Attempt != 1 {
				t.Fatalf("中断任务被重放: %+v", got)
			}
			if calls.Load() != 1 {
				t.Fatalf("多执行了恢复任务: %d", calls.Load())
			}
		})
	}
}

func TestDispatcherDrainsOneHundredPersistedTasks(t *testing.T) {
	tl := newTestTimeline(t)
	s := task.New(tl, "ada")
	for i := 0; i < 100; i++ {
		submitTask(t, s, fmt.Sprintf("offline-%d", i))
	}
	var calls atomic.Int32
	m := NewMonolith(MonolithOptions{Timeline: tl, SelfName: "ada", Watchdog: time.Hour,
		Thinker: taskModelFunc(func(context.Context, string, []llm.Message) (string, error) {
			calls.Add(1)
			return `FINAL="已完成"`, nil
		}),
	})
	d, cancel, done := startTaskDispatcher(t, tl, m)
	waitFor(t, 15*time.Second, func() bool {
		all, err := s.List(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range all {
			if item.State != task.Succeeded {
				return false
			}
		}
		return len(all) == 100
	})
	cancel()
	<-done
	if !d.WaitIdle(time.Second) {
		t.Fatal("未收尾")
	}
	if calls.Load() != 100 {
		t.Fatalf("持久队列重复或丢失执行: %d", calls.Load())
	}
}

func TestTaskCancelWaitsForLateModelExit(t *testing.T) {
	tl := newTestTimeline(t)
	s := task.New(tl, "ada")
	first, second := submitTask(t, s, "cancel"), submitTask(t, s, "next")
	started, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	m := NewMonolith(MonolithOptions{Timeline: tl, SelfName: "ada", Watchdog: time.Hour,
		Thinker: taskModelFunc(func(ctx context.Context, _ string, _ []llm.Message) (string, error) {
			if calls.Add(1) == 1 {
				close(started)
				<-ctx.Done()
				close(canceled)
				<-release
			}
			return `FINAL="迟归成功不能覆盖取消"`, nil
		}),
	})
	d, stop, done := startTaskDispatcher(t, tl, m)
	t.Cleanup(func() { close(release) })
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("未领取任务")
	}
	if _, err := s.Cancel(context.Background(), first.ID, 1, "operator", "cancel-once"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("取消未传播到模型")
	}
	if got := readTask(t, s, first.ID); got.State != task.Canceling {
		t.Fatalf("尚未退出却声称停止: %+v", got)
	}
	if got := readTask(t, s, second.ID); got.State != task.Queued {
		t.Fatalf("旧执行占槽期间启动了新任务: %+v", got)
	}
	if d.WaitIdle(20 * time.Millisecond) {
		t.Fatal("不服从取消的执行被当作已退出")
	}
	release <- struct{}{}
	waitFor(t, 3*time.Second, func() bool { return readTask(t, s, second.ID).State == task.Succeeded })
	stop()
	<-done
	d.WaitIdle(time.Second)
	if got := readTask(t, s, first.ID); got.State != task.Canceled || got.Result != "" {
		t.Fatalf("迟归结果覆盖取消: %+v", got)
	}
}

func TestMonolithTaskFailureHasTerminalFact(t *testing.T) {
	for _, mode := range []string{"failure", "panic", "iterations"} {
		t.Run(mode, func(t *testing.T) {
			tl := newTestTimeline(t)
			s := task.New(tl, "ada")
			req := submitTask(t, s, mode)
			m := NewMonolith(MonolithOptions{Timeline: tl, SelfName: "ada", MaxIterations: 1,
				Thinker: taskModelFunc(func(context.Context, string, []llm.Message) (string, error) {
					switch mode {
					case "panic":
						panic("假模型 panic")
					case "failure":
						return "", errors.New("假模型失败")
					}
					return "没有 FINAL", nil
				}),
			})
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("任务 panic 未保存失败状态: %v", r)
					}
				}()
				m.Wake(context.Background(), Wake{Kind: WakeScheduled})
			}()
			got := readTask(t, s, req.ID)
			if got.State != task.Failed || got.Reason == "" {
				t.Fatalf("任务失败不可查询: %+v", got)
			}
		})
	}
}

func TestAutonomousContextExcludesChatTasksAndLegacyRecap(t *testing.T) {
	tl := newTestTimeline(t)
	for _, typ := range []string{traj.TypeMessage, traj.TypePrompt, traj.TypeReasoning, traj.TypeShellOutput, traj.TypeFinal, traj.TypeAction} {
		s := traj.NewStep(typ)
		s.Fields["content"] = "不可再次执行的旧要求"
		if typ == traj.TypeMessage {
			s.Fields["from"], s.Fields["to"] = "operator", "ada"
		}
		if err := tl.Append(context.Background(), s); err != nil {
			t.Fatal(err)
		}
	}
	s := task.New(tl, "ada")
	old := submitTask(t, s, "interrupted")
	if _, err := s.Claim(context.Background(), "old-run"); err != nil {
		t.Fatal(err)
	}
	if err := s.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	observation := traj.NewStep(traj.TypeObservation)
	observation.Fields["content"] = "可用的独立观察"
	if err := tl.Append(context.Background(), observation); err != nil {
		t.Fatal(err)
	}
	cache := recap.Cache{Path: filepath.Join(tl.Dir, "recap", "episodes.jsonl")}
	if err := cache.Append(context.Background(), recap.Episode{Start: 0, End: 10, Title: "旧摘要", Summary: "不可再次执行的旧要求", PromptVersion: 1}); err != nil {
		t.Fatal(err)
	}
	var input string
	m := NewMonolith(MonolithOptions{Timeline: tl, SelfName: "ada", EnableRecap: true,
		Thinker: taskModelFunc(func(_ context.Context, system string, msgs []llm.Message) (string, error) {
			input += system
			for _, msg := range msgs {
				input += msg.Content
			}
			return `FINAL="IDLE"`, nil
		}),
	})
	m.Wake(context.Background(), Wake{Kind: WakeScheduled})
	if strings.Contains(input, "不可再次执行的旧要求") || strings.Contains(input, old.Content) {
		t.Fatalf("自主上下文重放了未授权要求: %s", input)
	}
	if !strings.Contains(input, "可用的独立观察") {
		t.Fatal("自主上下文丢失了允许的观察")
	}
}

// TestRunRejectsDurableRecoveryWithoutRunOwned：注册持久思考者后，
// 普通 Run 必须在恢复之前就被拒绝——没有身份运行权就没有恢复许可，
// 更不能先改任务事实再报错。
func TestRunRejectsDurableRecoveryWithoutRunOwned(t *testing.T) {
	tl := newTestTimeline(t)
	s := task.New(tl, "ada")
	old := submitTask(t, s, "left-running")
	if _, err := s.Claim(context.Background(), "old-run"); err != nil {
		t.Fatal(err)
	}
	d := NewDispatcher(tl, time.Millisecond)
	d.Register(NewMonolith(MonolithOptions{Timeline: tl, SelfName: "ada"}))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := d.Run(ctx)
	if err == nil || !strings.Contains(err.Error(), "RunOwned") {
		t.Fatalf("无运行权的持久调度未被拒绝: %v", err)
	}
	if got := readTask(t, s, old.ID); got.State != task.Running {
		t.Fatalf("被拒启动产生了恢复副作用: %+v", got)
	}
}

func TestResponderDoesNotReplyToTaskSubmission(t *testing.T) {
	r, tl := newResponder(t)
	req := submitTask(t, task.New(tl, "ada"), "not-chat")
	steps, err := tl.Steps()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range steps {
		if s.StepID == req.ID {
			r.Wake(context.Background(), Wake{Kind: WakeStep, Step: s})
		}
	}
	if r.answered(req.ID) {
		t.Fatal("显式委托被普通聊天回复，造成虚假接单")
	}
	if got := r.history("operator", 20); len(got) != 0 {
		t.Fatalf("任务内容混入普通聊天: %+v", got)
	}
}
