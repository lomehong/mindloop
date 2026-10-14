package task

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

// cloneProjection 返回投影的完全独立深拷贝。read 的返回值会被调用方
// 就地改写（transaction 的 writeEvent 直接 apply 进返回的投影），而
// 缓存投影是 Store 的私有状态——共享任何指针（Task、Events 底层数组）
// 都会污染缓存。深拷贝是 O(任务数+事件数)，与全量重放的 O(行数)
// 不在一个量级，这正是缓存的意义。
func cloneProjection(src *projection) *projection {
	dst := &projection{
		ordered:   make([]*Task, 0, len(src.ordered)),
		byID:      make(map[string]*Task, len(src.byID)),
		submitted: make(map[requestKey]*Task, len(src.submitted)),
		commands:  make(map[requestKey]Event, len(src.commands)),
	}
	for _, t := range src.ordered {
		cp := cloneTask(t)
		dst.ordered = append(dst.ordered, cp)
		dst.byID[cp.ID] = cp
	}
	for k, t := range src.submitted {
		dst.submitted[k] = dst.byID[t.ID] // ordered 里的同一个对象——保持同一性
	}
	for k, ev := range src.commands {
		dst.commands[k] = cloneEvent(ev)
	}
	return dst
}

func cloneTask(t *Task) *Task {
	cp := *t
	cp.Events = make([]Event, len(t.Events))
	for i, ev := range t.Events {
		cp.Events[i] = cloneEvent(ev)
	}
	cp.EvidenceStepIDs = cloneStrings(t.EvidenceStepIDs)
	cp.Deliverables = cloneStrings(t.Deliverables)
	return &cp
}

func cloneEvent(ev Event) Event {
	ev.EvidenceStepIDs = cloneStrings(ev.EvidenceStepIDs)
	ev.Deliverables = cloneStrings(ev.Deliverables)
	return ev
}

func cloneStrings(s []string) []string {
	if s == nil {
		return nil
	}
	out := make([]string, len(s))
	copy(out, s)
	return out
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
	if ev.Deliverables != nil {
		task.Deliverables = ev.Deliverables
	}
	task.Events = append(task.Events, ev)
}
func submittedTask(step traj.Step, identity string, req Submission) Task {
	return Task{ID: step.StepID, IdentityID: identity, Submission: req, State: Queued, Attempt: 1, CreatedAt: step.TS, UpdatedAt: step.TS,
		Events: []Event{{StepID: step.StepID, TS: step.TS, TaskID: step.StepID, State: Queued, Attempt: 1, Operation: "submit"}}}
}

// read 获取完整前缀：追加是单次原子写（O_APPEND 单次 Write、NTFS 的
// FILE_APPEND_DATA 亦然），读者永远只见完整行——轨迹锁只为多步骤写
// 定序，读不参与定序。曾在轨迹锁下读取：任务执行期间监督轮询与调度
// 心跳的全量重放把锁占成常态，写步骤的锁等待被饿死（演练实测 runner
// 写推理 5 秒超时被判 failed）。读-改-写的一致性由调用方的任务锁保证
// （transaction 全程持任务锁）。损坏/半行仍然明确拒绝，不能丢弃幂等
// 事实后接收第二次提交。
// syncExisting 确认上次响应丢失或 Sync 失败后可见的事实已落盘，再返回幂等成功。
//
// # 投影缓存（2026-10-13 审查立项）
//
// 全量重放是 O(行数)：任务面每次 List/Get/事务都重放整条根轨迹，
// 任务量与历史一起涨（实测 40 个任务让一个完整思考循环花 1.8s，
// task 包单测 400s+）——轨迹越长，持任务锁的事务越堵。缓存以
// traj.FileIdentity（卷序列号+文件索引 / dev-ino）为键：
//
//   - 身份一致且 consumed ≤ 当前大小 → 从 consumed 起增量 apply，
//     已消费前缀不再解析；
//   - 身份变化（原位重写、收缩重建）→ 全量重放重建缓存；
//   - 任何错误出口（坏行、撕裂尾、ctx 取消）不推进缓存——下次
//     重新读到同一批字节，行为与无缓存的全量重放完全一致；
//   - 缓存投影是 Store 的私有状态，read 返回深拷贝：transaction 的
//     writeEvent 会就地 apply 返回的投影，共享指针会污染缓存。
func (s *Store) read(ctx context.Context, syncExisting bool) (*projection, error) {
	// 并发读者限次重试：另一进程正在追加时，本进程的 ReadFile 可能
	// 观察到半行或半写内容（Windows 实证：WriteFile 字节落盘与长度
	// 可见之间有窗口，读到的行可以是撕裂尾或非法 JSON）——ErrCorrupt
	// 类错误等一拍重读即完整；写入方真崩溃留下的持久损坏重试耗尽后
	// 依旧拒绝（ErrCorrupt 契约不变：拒绝推断、不推进缓存）。
	const attempts = 4
	var lastErr error
	for i := 0; i < attempts; i++ {
		p, err := s.readOnce(ctx, syncExisting)
		if err == nil || !errors.Is(err, ErrCorrupt) {
			return p, err
		}
		lastErr = err
		time.Sleep(time.Duration(50*(i+1)) * time.Millisecond)
	}
	return nil, lastErr
}

func (s *Store) readOnce(ctx context.Context, syncExisting bool) (*projection, error) {
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
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := fi.Size()
	fid := traj.FileIdentity(f)

	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	// 增量路径：身份一致、前缀未收缩，且要么有新增字节（正常追加），
	// 要么 mtime 未动（文件无任何变化）。mtime 变了却没有新增字节
	// = 同长度原地重写——缓存必须重建（fileID 抓不到这种形态：
	// 同一 inode 的重写不改变卷序列号/文件索引）。残余边界：粗
	// mtime 精度的文件系统上极快连续的重写可能漏检——traj 的
	// cursor 接受同样的边界，这里与它对齐。
	usable := fid != "" && s.cache != nil && s.cache.fileID == fid &&
		s.cache.consumed <= size &&
		(size > s.cache.consumed || fi.ModTime().Equal(s.cache.modTime))
	if usable {
		work := cloneProjection(s.cache.proj)
		if s.cache.consumed < size {
			data, err := readRange(f, s.cache.consumed, size)
			if err != nil {
				return nil, err
			}
			// 撕裂尾：与全量重放"文件末行无换行即拒绝"同一条纪律。
			if len(data) == 0 || data[len(data)-1] != '\n' {
				return nil, fmt.Errorf("%w: 字节 %d 起的追加无完整行尾", ErrCorrupt, s.cache.consumed)
			}
			if err := applyData(ctx, work, data, false, s.cache.lines, s.tl.ID, s.identity); err != nil {
				return nil, err // work 是克隆，缓存未被触碰
			}
			s.cache = &projectionCache{
				fileID: fid, consumed: size, modTime: fi.ModTime(),
				lines: s.cache.lines + countLines(data), proj: cloneProjection(work),
			}
		}
		// 返回值交给调用方改写（writeEvent 就地 apply），必须是缓存
		// 之外的独立副本——work 本身。
		return work, nil
	}
	// 全量路径：无缓存可用（新 Store）、身份变化（原位重写/收缩）。
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || data[len(data)-1] != '\n' {
		return nil, ErrCorrupt
	}
	p := &projection{byID: map[string]*Task{}, submitted: map[requestKey]*Task{}, commands: map[requestKey]Event{}}
	if err := applyData(ctx, p, data, true, 0, s.tl.ID, s.identity); err != nil {
		return nil, err
	}
	if fid != "" {
		s.cache = &projectionCache{
			fileID: fid, consumed: size, modTime: fi.ModTime(),
			lines: countLines(data), proj: cloneProjection(p),
		}
	} else {
		s.cache = nil // 文件身份不可读：下次也走全量
	}
	return p, nil
}

// readRange 读取 [off, end) 的字节。end 是本次观察到的文件大小——
// O_APPEND 追加对文件末尾原子，观察之后的新增留给下一次增量。
func readRange(f *os.File, off, end int64) ([]byte, error) {
	buf := make([]byte, end-off)
	n, err := f.ReadAt(buf, off)
	if err != nil && err != io.EOF {
		return nil, err
	}
	return buf[:n], nil
}

func countLines(data []byte) int {
	n := 0
	for _, b := range data {
		if b == '\n' {
			n++
		}
	}
	return n
}

// applyData 把一段以换行结尾的完整行数据 apply 进投影。
// checkHeader 控制首行轨迹头校验（全量重放的第 0 行；增量前缀已在
// 缓存构建时校验过）。lineBase 是错误报告里"第 N 行"的绝对行号
// 基数（缓存已消费的行数），坏行报位置才对得上人眼里的行号。
// headerID/identity 与 read 的调用方同源（轨迹头 StepID 与身份名）。
func applyData(ctx context.Context, p *projection, data []byte, checkHeader bool, lineBase int, headerID, identity string) error {
	for n, line := range bytes.Split(data, []byte{'\n'}) {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		step, err := traj.ParseStep(line)
		if err != nil || !json.Valid(line) {
			return fmt.Errorf("%w: 第 %d 行", ErrCorrupt, lineBase+n+1)
		}
		if checkHeader && n == 0 && (step.Type != traj.TypeTrajectory || step.StepID != headerID) {
			return ErrCorrupt
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
			return fmt.Errorf("%w: 不支持的任务协议 %s", ErrCorrupt, version)
		}
		if _, err := time.Parse(time.RFC3339Nano, step.TS); err != nil {
			return ErrCorrupt
		}
		if step.Type == traj.TypeMessage {
			var req Submission
			if err := json.Unmarshal(line, &req); err != nil || !validSubmission(req) {
				return ErrCorrupt
			}
			taskID, _ := step.Field("task_id")
			to, _ := step.Field("to")
			key := requestKey{req.From, req.ClientMessageID}
			if taskID != step.StepID || to != identity || !validKey(taskID) || p.byID[taskID] != nil || p.submitted[key] != nil {
				return ErrCorrupt
			}
			task := submittedTask(step, identity, req)
			p.ordered = append(p.ordered, &task)
			p.byID[task.ID] = &task
			p.submitted[key] = &task
			continue
		}
		var ev Event
		if err := json.Unmarshal(line, &ev); err != nil {
			return ErrCorrupt
		}
		task := p.byID[ev.TaskID]
		if task == nil || validateTransition(task, ev) != nil {
			return fmt.Errorf("%w: 任务事件 %s", ErrCorrupt, step.StepID)
		}
		if ev.Operation == "claim" {
			for _, other := range p.ordered {
				if active(other.State) {
					return ErrCorrupt
				}
			}
		}
		if ev.Operation == "cancel" || ev.Operation == "retry" {
			key := requestKey{ev.Actor, ev.RequestID}
			if !validKey(ev.Actor) || !validKey(ev.RequestID) {
				return ErrCorrupt
			}
			if _, exists := p.commands[key]; exists {
				return ErrCorrupt
			}
			p.commands[key] = ev
		}
		apply(task, ev)
	}
	return nil
}
