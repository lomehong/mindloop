package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mindloop/internal/llm"
	"mindloop/internal/traj"
)

type functionThinker func(context.Context, string, []llm.Message) (string, error)

func (f functionThinker) Think(ctx context.Context, system string, msgs []llm.Message) (string, error) {
	return f(ctx, system, msgs)
}

func TestRunTaskFinalEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, response, kind string
		outputs              int
	}{
		{"model", `FINAL="模型答案"`, "model-final", 0},
		{"shell", fence(`echo evidence; FINAL="模型答案"`), "shell-final", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.outputs > 0 {
				requireBash(t)
			}
			tl := newTestTimeline(t)
			res, err := Run(context.Background(), Options{Timeline: tl, Thinker: &fakeThinker{responses: []string{tc.response}}, Task: "当前完整任务", TaskID: "task-one", Attempt: 2, RunID: "preallocated-run-one"})
			if err != nil {
				t.Fatal(err)
			}
			if res.RunID != "preallocated-run-one" || res.Final != "模型答案" || res.FinalKind != tc.kind {
				t.Fatalf("结果关联错误: %+v", res)
			}
			steps, err := tl.Steps()
			if err != nil {
				t.Fatal(err)
			}
			byID := map[string]traj.Step{}
			var outputs int
			for _, step := range steps[1:] {
				byID[step.StepID] = step
				for field, want := range map[string]string{"run_id": res.RunID, "task_id": "task-one", "attempt": "2"} {
					if got, _ := step.Field(field); got != want {
						t.Fatalf("步骤 %s 的 %s=%q，应为 %q", step.Type, field, got, want)
					}
				}
				if step.Type == traj.TypeRun && step.StepID != res.RunID {
					t.Fatal("运行头没有采用预分配 ID")
				}
				if step.Type == traj.TypeShellOutput {
					outputs++
				}
			}
			if outputs != tc.outputs || len(res.EvidenceStepIDs) != tc.outputs+1 {
				t.Fatalf("证据不完整: %+v, outputs=%d", res, outputs)
			}
			for i, id := range res.EvidenceStepIDs {
				step, ok := byID[id]
				if !ok {
					t.Fatalf("证据未落盘: %s", id)
				}
				wantType := traj.TypeShellOutput
				if i == len(res.EvidenceStepIDs)-1 {
					wantType = traj.TypeFinal
				}
				if step.Type != wantType {
					t.Fatalf("错误证据: %+v", step)
				}
				if step.Type == traj.TypeFinal {
					if kind, _ := step.Field("result_kind"); kind != tc.kind {
						t.Fatalf("FINAL 类型=%q", kind)
					}
				}
			}
		})
	}
}

func TestRunRejectsCanceledLateModelResult(t *testing.T) {
	for _, response := range []string{`FINAL="迟归成功"`, fence(`echo unsafe > late.txt; FINAL="迟归成功"`)} {
		t.Run(response, func(t *testing.T) {
			tl := newTestTimeline(t)
			dir := t.TempDir()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			thinker := functionThinker(func(context.Context, string, []llm.Message) (string, error) { cancel(); return response, nil })
			res, err := Run(ctx, Options{Timeline: tl, Thinker: thinker, Task: "任务", WorkDir: dir})
			if !errors.Is(err, context.Canceled) || res.Final != "" {
				t.Fatalf("迟归被采纳: %+v %v", res, err)
			}
			if _, err := os.Stat(filepath.Join(dir, "late.txt")); !os.IsNotExist(err) {
				t.Fatalf("取消后启动了脚本: %v", err)
			}
			steps, _ := tl.Steps()
			for _, step := range steps {
				if step.Type == traj.TypeFinal {
					t.Fatal("取消后写成功事实")
				}
			}
		})
	}
}

func TestRunExecutionGatePreventsSideEffects(t *testing.T) {
	for _, cancelApproval := range []bool{false, true} {
		t.Run(map[bool]string{false: "拒绝", true: "批准迟归"}[cancelApproval], func(t *testing.T) {
			tl := newTestTimeline(t)
			dir := t.TempDir()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			denied := errors.New("未批准")
			var got Execution
			script := `echo forbidden > gate.txt; FINAL="完成"`
			res, err := Run(ctx, Options{Timeline: tl, Thinker: &fakeThinker{responses: []string{fence(script)}}, Task: "任务", WorkDir: dir, TaskID: "task-one", Attempt: 1, RunID: "preallocated-run-one", BeforeExecute: func(_ context.Context, req Execution) error {
				got = req
				if cancelApproval {
					cancel()
					return nil
				}
				return denied
			}})
			want := denied
			if cancelApproval {
				want = context.Canceled
			}
			if !errors.Is(err, want) || res.Final != "" {
				t.Fatalf("授权入口失效: %+v %v", res, err)
			}
			if got.Script != script || got.WorkDir != dir || got.RunID != "preallocated-run-one" || got.TaskID != "task-one" || got.Attempt != 1 {
				t.Fatalf("审批对象不完整: %+v", got)
			}
			if _, err := os.Stat(filepath.Join(dir, "gate.txt")); !os.IsNotExist(err) {
				t.Fatalf("未批准却执行: %v", err)
			}
		})
	}
}

func TestRunTaskContextDoesNotIncludeOtherRequests(t *testing.T) {
	tl := newTestTimeline(t)
	for _, typ := range []string{traj.TypeMessage, traj.TypePrompt, traj.TypeReasoning, traj.TypeShellOutput, traj.TypeFinal} {
		step := traj.NewStep(typ)
		step.Fields["content"] = "别的任务或聊天：不应执行"
		step.Fields["run_id"] = "old-run"
		if err := tl.Append(context.Background(), step); err != nil {
			t.Fatal(err)
		}
	}
	var input string
	thinker := functionThinker(func(_ context.Context, _ string, msgs []llm.Message) (string, error) {
		for _, msg := range msgs {
			input += msg.Content
		}
		return `FINAL="本任务结果"`, nil
	})
	_, err := Run(context.Background(), Options{Timeline: tl, Thinker: thinker, Task: "当前完整要求", TaskID: "current-task", Attempt: 1, RunID: "current-run-one"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(input, "当前完整要求") || strings.Contains(input, "别的任务或聊天") {
		t.Fatalf("上下文串线: %s", input)
	}
}

func TestRunRejectsInvalidTaskCorrelationBeforeWriting(t *testing.T) {
	for _, tc := range []struct {
		run, task string
		attempt   int
	}{
		{"../escape", "task", 1}, {"run", "task", 0}, {"run", "", 1}, {"", "task", 1},
	} {
		t.Run(tc.run+tc.task, func(t *testing.T) {
			tl := newTestTimeline(t)
			_, err := Run(context.Background(), Options{Timeline: tl, Thinker: &fakeThinker{responses: []string{`FINAL="wrong"`}}, RunID: tc.run, TaskID: tc.task, Attempt: tc.attempt})
			if err == nil {
				t.Fatal("接受了无效关联")
			}
			steps, _ := tl.Steps()
			if len(steps) != 1 {
				t.Fatal("无效请求污染了轨迹")
			}
		})
	}
}
