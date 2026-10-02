package task

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/lomehong/mindloop/internal/traj"
)

type requestKey struct{ actor, id string }
type projection struct {
	ordered   []*Task
	byID      map[string]*Task
	submitted map[requestKey]*Task
	commands  map[requestKey]Event
}

func validKey(value string) bool {
	return strings.TrimSpace(value) != "" && len(value) <= 256 && !strings.ContainsAny(value, "\x00\r\n")
}
func validSubmission(req Submission) bool {
	return validKey(req.From) && validKey(req.ClientMessageID) && strings.TrimSpace(req.Content) != "" && (req.SourceStepID == "" || validKey(req.SourceStepID))
}
func active(state State) bool {
	return state == Running || state == AwaitingApproval || state == Canceling
}
func retryable(state State) bool {
	return state == Failed || state == Canceled || state == Interrupted || state == BudgetExceeded
}

// validateTransition 既约束写入，也校验重建，UI 无权根据日志中任意一行推断成功。
func validateTransition(task *Task, ev Event) error {
	if ev.BaseAttempt != task.Attempt {
		return ErrTransition
	}
	switch ev.Operation {
	case "retry":
		if retryable(task.State) && ev.State == Queued && ev.Attempt == task.Attempt+1 && ev.RunID == "" {
			return nil
		}
	case "claim":
		if task.State == Queued && ev.State == Running && ev.Attempt == task.Attempt && validKey(ev.RunID) {
			return nil
		}
	case "cancel":
		if ev.Attempt != task.Attempt || ev.RunID != task.RunID {
			break
		}
		if (task.State == Queued || task.State == Canceled) && ev.State == Canceled {
			return nil
		}
		if active(task.State) && ev.State == Canceling {
			return nil
		}
	case "recover":
		if active(task.State) && ev.State == Interrupted && ev.Attempt == task.Attempt && ev.RunID == task.RunID {
			return nil
		}
	case "advance":
		if ev.Attempt != task.Attempt || ev.RunID != task.RunID || ev.RunID == "" {
			break
		}
		switch task.State {
		case Running:
			if ev.State == AwaitingApproval || ev.State == Canceling || ev.State == Succeeded || ev.State == Failed || ev.State == Interrupted || ev.State == BudgetExceeded {
				return nil
			}
		case AwaitingApproval:
			if ev.State == Running || ev.State == Canceling || ev.State == Failed || ev.State == Interrupted || ev.State == BudgetExceeded {
				return nil
			}
		case Canceling:
			if ev.State == Canceled || ev.State == Interrupted {
				return nil
			}
		}
	}
	return ErrTransition
}

func apply(task *Task, ev Event) {
	task.State, task.Attempt, task.RunID = ev.State, ev.Attempt, ev.RunID
	task.UpdatedAt, task.Reason = ev.TS, ev.Reason
	task.Result, task.ResultKind, task.EvidenceStepIDs = ev.Result, ev.ResultKind, ev.EvidenceStepIDs
	task.Events = append(task.Events, ev)
}
func submittedTask(step traj.Step, identity string, req Submission) Task {
	return Task{ID: step.StepID, IdentityID: identity, Submission: req, State: Queued, Attempt: 1, CreatedAt: step.TS, UpdatedAt: step.TS,
		Events: []Event{{StepID: step.StepID, TS: step.TS, TaskID: step.StepID, State: Queued, Attempt: 1, Operation: "submit"}}}
}

// read 无锁获取完整前缀：追加是单次原子写（O_APPEND 单次 Write、NTFS 的
// FILE_APPEND_DATA 亦然），读者永远只见完整行——轨迹锁只为多步骤写
// 定序，读不参与定序。曾在轨迹锁下读取：任务执行期间监督轮询与调度
// 心跳的全量重放把锁占成常态，写步骤的锁等待被饿死（演练实测 runner
// 写推理 5 秒超时被判 failed）。读-改-写的一致性由调用方的任务锁保证
// （transaction 全程持任务锁）。损坏/半行仍然明确拒绝，不能丢弃幂等
// 事实后接收第二次提交。
// syncExisting 确认上次响应丢失或 Sync 失败后可见的事实已落盘，再返回幂等成功。
func (s *Store) read(ctx context.Context, syncExisting bool) (*projection, error) {
	if s.tl == nil || s.tl.Path == "" || !validKey(s.identity) {
		return nil, ErrInvalid
	}
	flags := os.O_RDONLY
	if syncExisting {
		flags = os.O_RDWR
	}
	f, err := os.OpenFile(s.tl.Path, flags, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if syncExisting {
		if err := f.Sync(); err != nil {
			return nil, err
		}
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || data[len(data)-1] != '\n' {
		return nil, ErrCorrupt
	}
	p := &projection{byID: map[string]*Task{}, submitted: map[requestKey]*Task{}, commands: map[requestKey]Event{}}
	for n, line := range bytes.Split(data, []byte{'\n'}) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		step, err := traj.ParseStep(line)
		if err != nil || !json.Valid(line) {
			return nil, fmt.Errorf("%w: 第 %d 行", ErrCorrupt, n+1)
		}
		if n == 0 && (step.Type != traj.TypeTrajectory || step.StepID != s.tl.ID) {
			return nil, ErrCorrupt
		}
		version, _ := step.Field("protocol_version")
		kind, _ := step.Field("message_kind")
		if version == "" {
			continue
		}
		if step.Type != eventType && (step.Type != traj.TypeMessage || kind != "task") {
			continue
		}
		if version != "1" {
			return nil, fmt.Errorf("%w: 不支持的任务协议 %s", ErrCorrupt, version)
		}
		if _, err := time.Parse(time.RFC3339Nano, step.TS); err != nil {
			return nil, ErrCorrupt
		}
		if step.Type == traj.TypeMessage {
			var req Submission
			if err := json.Unmarshal(line, &req); err != nil || !validSubmission(req) {
				return nil, ErrCorrupt
			}
			taskID, _ := step.Field("task_id")
			to, _ := step.Field("to")
			key := requestKey{req.From, req.ClientMessageID}
			if taskID != step.StepID || to != s.identity || !validKey(taskID) || p.byID[taskID] != nil || p.submitted[key] != nil {
				return nil, ErrCorrupt
			}
			task := submittedTask(step, s.identity, req)
			p.ordered = append(p.ordered, &task)
			p.byID[task.ID] = &task
			p.submitted[key] = &task
			continue
		}
		var ev Event
		if err := json.Unmarshal(line, &ev); err != nil {
			return nil, ErrCorrupt
		}
		task := p.byID[ev.TaskID]
		if task == nil || validateTransition(task, ev) != nil {
			return nil, fmt.Errorf("%w: 任务事件 %s", ErrCorrupt, step.StepID)
		}
		if ev.Operation == "claim" {
			for _, other := range p.ordered {
				if active(other.State) {
					return nil, ErrCorrupt
				}
			}
		}
		if ev.Operation == "cancel" || ev.Operation == "retry" {
			key := requestKey{ev.Actor, ev.RequestID}
			if !validKey(ev.Actor) || !validKey(ev.RequestID) {
				return nil, ErrCorrupt
			}
			if _, exists := p.commands[key]; exists {
				return nil, ErrCorrupt
			}
			p.commands[key] = ev
		}
		apply(task, ev)
	}
	return p, nil
}
