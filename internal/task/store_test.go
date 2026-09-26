package task

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"mindloop/internal/traj"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	tl, err := traj.CreateAt(context.Background(), t.TempDir(), "tasks")
	if err != nil {
		t.Fatal(err)
	}
	return New(tl, "ada")
}
func submit(t *testing.T, s *Store, key string) Task {
	t.Helper()
	task, err := s.Submit(context.Background(), Submission{From: "operator", ClientMessageID: key, Content: "完整的任务要求"})
	if err != nil {
		t.Fatal(err)
	}
	return task
}
func getTask(t *testing.T, s *Store, id string) Task {
	t.Helper()
	task, err := s.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func TestSubmissionIsSingleFactAndSurvivesLostResponse(t *testing.T) {
	s := testStore(t)
	first := submit(t, s, "request-1")
	if first.ID == "" || first.State != Queued || first.Attempt != 1 {
		t.Fatalf("task=%+v", first)
	}
	steps, err := s.tl.Steps()
	if err != nil || len(steps) != 2 {
		t.Fatalf("steps=%v err=%v", steps, err)
	}
	fact := steps[1]
	if fact.Type != traj.TypeMessage || fact.StepID != first.ID {
		t.Fatalf("fact=%+v", fact)
	}
	for key, want := range map[string]string{"protocol_version": "1", "message_kind": "task", "task_id": first.ID, "client_message_id": "request-1", "from": "operator", "to": "ada", "content": "完整的任务要求"} {
		if got, _ := fact.Field(key); got != want {
			t.Errorf("%s=%q want=%q", key, got, want)
		}
	}
	reopened := New(s.tl, "ada")
	replay := submit(t, reopened, "request-1")
	if replay.ID != first.ID {
		t.Fatalf("响应丢失后创建了第二个任务: %+v", replay)
	}
	steps, _ = s.tl.Steps()
	if len(steps) != 2 {
		t.Fatalf("重复写入了事实: %d", len(steps))
	}
}

func TestSubmissionKeyScopeAndConflicts(t *testing.T) {
	s := testStore(t)
	first := submit(t, s, "same-id")
	for _, change := range []Submission{
		{From: "operator", ClientMessageID: "same-id", Content: "不同内容"},
		{From: "operator", ClientMessageID: "same-id", Content: "完整的任务要求", SourceStepID: "another-message"},
	} {
		if _, err := s.Submit(context.Background(), change); !errors.Is(err, ErrConflict) {
			t.Fatalf("同键异载荷未冲突: %v", err)
		}
	}
	otherSender, err := s.Submit(context.Background(), Submission{From: "other", ClientMessageID: "same-id", Content: "完整的任务要求"})
	if err != nil || otherSender.ID == first.ID {
		t.Fatalf("发起人未隔离: %+v %v", otherSender, err)
	}
	otherRequest := submit(t, s, "different-id")
	if otherRequest.ID == first.ID {
		t.Fatal("相同文本不同请求被合并")
	}
	otherIdentity := submit(t, testStore(t), "same-id")
	if otherIdentity.ID == first.ID {
		t.Fatal("身份间任务被合并")
	}
}

func TestOneHundredSubmissionsAreQueryableWithoutDispatcher(t *testing.T) {
	s := testStore(t)
	for i := 0; i < 100; i++ {
		submit(t, s, fmt.Sprintf("request-%d", i))
	}
	all, err := New(s.tl, "ada").List(context.Background())
	if err != nil || len(all) != 100 {
		t.Fatalf("tasks=%d err=%v", len(all), err)
	}
	for _, task := range all {
		if task.State != Queued {
			t.Fatalf("停机提交丢失: %+v", task)
		}
	}
}

func TestConcurrentDuplicateSubmissionsAcrossHandles(t *testing.T) {
	s := testStore(t)
	var wg sync.WaitGroup
	results := make(chan Task, 20)
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			task, err := New(s.tl, "ada").Submit(context.Background(), Submission{From: "operator", ClientMessageID: "shared", Content: "要求"})
			results <- task
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	id := ""
	for task := range results {
		if id == "" {
			id = task.ID
		}
		if task.ID != id {
			t.Fatal("并发重复创建")
		}
	}
	all, err := s.List(context.Background())
	if err != nil || len(all) != 1 {
		t.Fatalf("tasks=%d err=%v", len(all), err)
	}
}

func TestClaimCancelRetryAndStaleCompletion(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	first := submit(t, s, "first")
	second := submit(t, s, "second")
	running, err := s.Claim(ctx, "run-1")
	if err != nil || running.ID != first.ID || running.State != Running {
		t.Fatalf("claim=%+v %v", running, err)
	}
	if _, err := s.Claim(ctx, "run-overlap"); !errors.Is(err, ErrBusy) {
		t.Fatalf("重叠领取: %v", err)
	}
	cancel, err := s.Cancel(ctx, first.ID, 1, "operator", "cancel-1")
	if err != nil || cancel.State != Canceling {
		t.Fatalf("cancel=%+v %v", cancel, err)
	}
	if _, err := s.Advance(ctx, first.ID, Event{Attempt: 1, RunID: "run-1", State: Succeeded, Result: "迟归成功"}); !errors.Is(err, ErrTransition) {
		t.Fatalf("取消中提前成功: %v", err)
	}
	if _, err := s.Retry(ctx, first.ID, 1, "operator", "too-early"); !errors.Is(err, ErrTransition) {
		t.Fatalf("尚未停止却重试: %v", err)
	}
	if _, err := s.Advance(ctx, first.ID, Event{Attempt: 1, RunID: "run-1", State: Canceled, Reason: "执行已退出"}); err != nil {
		t.Fatal(err)
	}
	retried, err := s.Retry(ctx, first.ID, 1, "operator", "retry-1")
	if err != nil || retried.Attempt != 2 || retried.State != Queued || retried.RunID != "" {
		t.Fatalf("retry=%+v %v", retried, err)
	}
	replay, err := s.Retry(ctx, first.ID, 1, "operator", "retry-1")
	if err != nil || replay.Attempt != 2 {
		t.Fatalf("重复重试=%+v %v", replay, err)
	}
	if _, err := s.Cancel(ctx, second.ID, 1, "operator", "retry-1"); !errors.Is(err, ErrConflict) {
		t.Fatalf("控制幂等键冲突未检测: %v", err)
	}
	if _, err := s.Advance(ctx, first.ID, Event{Attempt: 1, RunID: "run-1", State: Succeeded, Result: "旧 attempt"}); !errors.Is(err, ErrTransition) {
		t.Fatalf("迟归结果被采纳: %v", err)
	}
	if task := getTask(t, s, first.ID); task.Result != "" || len(task.Events) != 5 {
		t.Fatalf("历史/结果错误: %+v", task)
	}
}

func TestRecoveryOnlyInterruptsActiveTasks(t *testing.T) {
	for _, state := range []State{Queued, Running, AwaitingApproval, Canceling, Succeeded, Failed, Canceled, Interrupted, BudgetExceeded} {
		t.Run(string(state), func(t *testing.T) {
			ctx := context.Background()
			s := testStore(t)
			task := submit(t, s, "one")
			if state != Queued {
				if _, err := s.Claim(ctx, "run-1"); err != nil {
					t.Fatal(err)
				}
				if state == Canceled {
					if _, err := s.Cancel(ctx, task.ID, 1, "operator", "cancel"); err != nil {
						t.Fatal(err)
					}
				}
				if state != Running {
					ev := Event{Attempt: 1, RunID: "run-1", State: state, Reason: "测试原因", Result: "完成结果", ResultKind: "model-final", EvidenceStepIDs: []string{"final-step"}}
					if _, err := s.Advance(ctx, task.ID, ev); err != nil {
						t.Fatal(err)
					}
				}
			}
			restarted := New(s.tl, "ada")
			if err := restarted.Recover(ctx); err != nil {
				t.Fatal(err)
			}
			got := getTask(t, restarted, task.ID)
			want := state
			if state == Running || state == AwaitingApproval || state == Canceling {
				want = Interrupted
			}
			if got.State != want || got.Attempt != 1 {
				t.Fatalf("recovered=%+v want=%s", got, want)
			}
			before := len(got.Events)
			if err := restarted.Recover(ctx); err != nil {
				t.Fatal(err)
			}
			if len(getTask(t, restarted, task.ID).Events) != before {
				t.Fatal("重复恢复追加了状态")
			}
			if state == Succeeded {
				if got.Result != "完成结果" || got.ResultKind != "model-final" || len(got.EvidenceStepIDs) != 1 {
					t.Fatalf("结果丢失: %+v", got)
				}
				if _, err := restarted.Retry(ctx, task.ID, 1, "operator", "retry"); !errors.Is(err, ErrTransition) {
					t.Fatalf("成功任务被重试: %v", err)
				}
			}
			if want != Queued {
				if _, err := restarted.Claim(ctx, "new-run"); !errors.Is(err, ErrNoQueued) {
					t.Fatalf("重启自动重放: %v", err)
				}
			}
		})
	}
}

func TestLegacyChatNeverBecomesTaskAndInvalidWritesFailClosed(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	legacy := traj.NewStep(traj.TypeMessage)
	legacy.Fields["content"] = "帮我执行"
	legacy.Fields["task_id"] = "legacy"
	if err := s.tl.Append(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	all, err := s.List(ctx)
	if err != nil || len(all) != 0 {
		t.Fatalf("旧消息转成任务: %+v %v", all, err)
	}
	for _, req := range []Submission{{}, {From: "operator", ClientMessageID: "id", Content: "  "}, {From: "operator", Content: "任务"}} {
		if _, err := s.Submit(ctx, req); !errors.Is(err, ErrInvalid) {
			t.Fatalf("坏请求被接受: %v", err)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.Submit(canceled, Submission{From: "operator", ClientMessageID: "id", Content: "任务"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("取消请求成功: %v", err)
	}
	f, err := os.OpenFile(s.tl.Path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(`{"type":"message"`)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Submit(ctx, Submission{From: "operator", ClientMessageID: "id", Content: "任务"}); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("半行后继续写导致任务丢失: %v", err)
	}
	missing := New(&traj.Timeline{ID: "missing", Dir: t.TempDir(), Path: filepath.Join(t.TempDir(), "missing.jsonl")}, "ada")
	if _, err := missing.Submit(ctx, Submission{From: "operator", ClientMessageID: "id", Content: "任务"}); err == nil {
		t.Fatal("不存在的轨迹接收成功")
	}
}
