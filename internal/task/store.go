package task

import (
	"context"
	"encoding/json"
	"mindloop/internal/traj"
)

// Store 仅持有身份根轨迹句柄，进程内不缓存任务事实。
type Store struct {
	tl       *traj.Timeline
	identity string
}

func New(tl *traj.Timeline, identity string) *Store { return &Store{tl: tl, identity: identity} }

func (s *Store) Submit(ctx context.Context, req Submission) (Task, error) {
	if !validSubmission(req) {
		return Task{}, ErrInvalid
	}
	return s.transaction(ctx, func(p *projection) (Task, error) {
		if old := p.submitted[requestKey{req.From, req.ClientMessageID}]; old != nil {
			if old.Submission != req {
				return Task{}, ErrConflict
			}
			return *old, nil
		}
		step := traj.NewStep(traj.TypeMessage)
		step.Fields = map[string]any{"protocol_version": ProtocolVersion, "message_kind": "task", "task_id": step.StepID,
			"client_message_id": req.ClientMessageID, "from": req.From, "to": s.identity, "source": "task", "content": req.Content}
		if req.SourceStepID != "" {
			step.Fields["source_step_id"] = req.SourceStepID
		}
		if err := s.tl.AppendWithOptions(ctx, step, traj.AppendOptions{Durable: true}); err != nil {
			return Task{}, err
		}
		return submittedTask(step, s.identity, req), nil
	})
}
func (s *Store) List(ctx context.Context) ([]Task, error) {
	p, err := s.read(ctx, false)
	if err != nil {
		return nil, err
	}
	tasks := make([]Task, 0, len(p.ordered))
	for _, task := range p.ordered {
		tasks = append(tasks, *task)
	}
	return tasks, nil
}
func (s *Store) Get(ctx context.Context, id string) (Task, error) {
	p, err := s.read(ctx, false)
	if err != nil {
		return Task{}, err
	}
	task := p.byID[id]
	if task == nil {
		return Task{}, ErrNotFound
	}
	return *task, nil
}
func (s *Store) Claim(ctx context.Context, runID string) (Task, error) {
	if !validKey(runID) {
		return Task{}, ErrInvalid
	}
	return s.transaction(ctx, func(p *projection) (Task, error) {
		for _, task := range p.ordered {
			if active(task.State) {
				return Task{}, ErrBusy
			}
		}
		for _, task := range p.ordered {
			if task.State == Queued {
				return s.writeEvent(ctx, task, Event{State: Running, Attempt: task.Attempt, BaseAttempt: task.Attempt, RunID: runID, Operation: "claim"})
			}
		}
		return Task{}, ErrNoQueued
	})
}
func (s *Store) Advance(ctx context.Context, id string, ev Event) (Task, error) {
	return s.transaction(ctx, func(p *projection) (Task, error) {
		task := p.byID[id]
		if task == nil {
			return Task{}, ErrNotFound
		}
		ev.Operation = "advance"
		ev.Actor = ""
		ev.RequestID = ""
		ev.BaseAttempt = ev.Attempt
		return s.writeEvent(ctx, task, ev)
	})
}
func (s *Store) Cancel(ctx context.Context, id string, attempt int, actor, requestID string) (Task, error) {
	return s.command(ctx, id, attempt, actor, requestID, "cancel")
}
func (s *Store) Retry(ctx context.Context, id string, attempt int, actor, requestID string) (Task, error) {
	return s.command(ctx, id, attempt, actor, requestID, "retry")
}

// Recover 只能在取得身份运行锁、确认旧执行已退出后调用。
func (s *Store) Recover(ctx context.Context) error {
	_, err := s.transaction(ctx, func(p *projection) (Task, error) {
		for _, task := range p.ordered {
			if !active(task.State) {
				continue
			}
			if _, err := s.writeEvent(ctx, task, Event{State: Interrupted, Attempt: task.Attempt, BaseAttempt: task.Attempt, RunID: task.RunID, Operation: "recover", Reason: "进程重启导致执行中断；需要人工确认重试，旧批准失效"}); err != nil {
				return Task{}, err
			}
		}
		return Task{}, nil
	})
	return err
}

func (s *Store) command(ctx context.Context, id string, attempt int, actor, requestID, operation string) (Task, error) {
	if !validKey(actor) || !validKey(requestID) || attempt < 1 {
		return Task{}, ErrInvalid
	}
	return s.transaction(ctx, func(p *projection) (Task, error) {
		if old, ok := p.commands[requestKey{actor, requestID}]; ok {
			if old.TaskID != id || old.Operation != operation || old.BaseAttempt != attempt {
				return Task{}, ErrConflict
			}
			return *p.byID[id], nil
		}
		task := p.byID[id]
		if task == nil {
			return Task{}, ErrNotFound
		}
		ev := Event{State: Canceling, Attempt: attempt, BaseAttempt: attempt, RunID: task.RunID, Actor: actor, RequestID: requestID, Operation: operation, Reason: "用户请求取消，等待执行退出"}
		if operation == "retry" {
			ev.State = Queued
			ev.Attempt = attempt + 1
			ev.RunID = ""
			ev.Reason = "用户确认重试"
		} else if task.State == Queued || task.State == Canceled {
			ev.State = Canceled
			ev.Reason = "任务已取消，未启动执行"
		}
		return s.writeEvent(ctx, task, ev)
	})
}

// 锁序固定：任务锁 → 轨迹锁；读改写整体受跨进程任务锁保护。
func (s *Store) transaction(ctx context.Context, fn func(*projection) (Task, error)) (Task, error) {
	if s.tl == nil || s.tl.Path == "" || !validKey(s.identity) {
		return Task{}, ErrInvalid
	}
	timeout := s.tl.LockTimeout
	if timeout <= 0 {
		timeout = traj.DefaultLockTimeout
	}
	release, err := traj.AcquireDirLock(ctx, s.tl.Path+".tasks.lock", timeout)
	if err != nil {
		return Task{}, err
	}
	defer release()
	p, err := s.read(ctx, true)
	if err != nil {
		return Task{}, err
	}
	if err := ctx.Err(); err != nil {
		return Task{}, err
	}
	return fn(p)
}

func (s *Store) writeEvent(ctx context.Context, task *Task, ev Event) (Task, error) {
	if err := validateTransition(task, ev); err != nil {
		return Task{}, err
	}
	step := traj.NewStep(eventType)
	ev.StepID = step.StepID
	ev.TS = step.TS
	ev.TaskID = task.ID
	data, err := json.Marshal(ev)
	if err != nil {
		return Task{}, err
	}
	if err := json.Unmarshal(data, &step.Fields); err != nil {
		return Task{}, err
	}
	step.Fields["protocol_version"] = ProtocolVersion
	if err := s.tl.AppendWithOptions(ctx, step, traj.AppendOptions{Durable: true}); err != nil {
		return Task{}, err
	}
	apply(task, ev)
	return *task, nil
}
