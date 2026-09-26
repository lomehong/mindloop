package mind

import (
	"context"
	"strconv"
	"sync"
	"time"

	"mindloop/internal/task"
	"mindloop/internal/traj"
)

// Responder 是持久思考者：盖过协议章的入站消息在恢复窗口内补答、
// 窗口外以 no-reply 收据关闭、失败留下 reply-failed 事实——"消息
// 是否被处理"始终是日志事实，不是内存状态。旧历史（无章）永远
// 不被触碰：恢复不是"补写历史"，只兑现新协议对送达消息的承诺。
var _ durableThinker = (*Responder)(nil)

// 状态收据的取值：message 步骤（source=reply-status，reply_to 指向
// 原消息）复用了"带 reply_to 即已处理"的 answered 守卫语义——收据
// 落盘即事实，任何路径都不会对同一条消息再度出手。
const (
	replyStatusSource  = "reply-status"
	replyStatusNoReply = "no-reply"
	replyStatusFailed  = "reply-failed"
)

// protocolVersionField 是当前协议章的字符串形态（轨迹字段是 JSON
// 数字，Field 取回后是它的十进制文本）。
var protocolVersionField = strconv.Itoa(task.ProtocolVersion)

// chatScan 是对话恢复的增量账本：cursor 只消费文件新追加的完整行
// （归零语义内建——重写/截断时重放并由 offset 回退检测重置），
// handled 记已关闭的入站消息（被回复或被收据清算），suppressed 是
// 进程内的重试抑制（落盘双失败时的防线），pending 按日志序保存
// 尚未关闭的协议消息。全部字段由 mu 保护：Pending/Recover 在调度
// 主循环，noteHandled/suppress 在 worker goroutine。
type chatScan struct {
	mu         sync.Mutex
	cursor     *traj.Cursor
	handled    map[string]bool
	suppressed map[string]bool
	pending    []traj.Step
}

// isProtocolInbound 报告 step 是否是盖过协议章的入站消息：只有这类
// 消息参与恢复与待办。任务提交（message_kind=task）是任务队列的
// 事实；未知协议版本不由本版本处理（防御：不碰不认识的形状）。
func (r *Responder) isProtocolInbound(s traj.Step) bool {
	if s.Type != traj.TypeMessage {
		return false
	}
	version, _ := s.Field("protocol_version")
	if version != protocolVersionField {
		return false
	}
	if kind, _ := s.Field("message_kind"); kind == "task" {
		return false
	}
	to, _ := s.Field("to")
	from, _ := s.Field("from")
	return to == r.opts.SelfName && from != "" && from != r.opts.SelfName
}

// expired 报告消息是否超出恢复窗口。时间戳不可解析时不视为过期
// ——与投递路径的陈旧守卫同一判据：宁可回复也不静默吞掉。
func (r *Responder) expired(ts string) bool {
	t, err := time.Parse(traj.TimeFormat, ts)
	return err == nil && time.Since(t) > r.opts.RecallWindow
}

// Recover 在调度器持锁启动时清算：过期未处理的协议消息写 no-reply
// 收据，窗口内的留给 Pending 补答。收据写失败的消息保留在待办里
// （后续投递路径会重试）——启动不因一条收据写失败而失败。
func (r *Responder) Recover(ctx context.Context) error {
	r.scan.mu.Lock()
	defer r.scan.mu.Unlock()
	if err := r.scanNewLocked(); err != nil {
		return err
	}
	kept := make([]traj.Step, 0, len(r.scan.pending))
	for _, msg := range r.scan.pending {
		if r.scan.handled[msg.StepID] || r.scan.suppressed[msg.StepID] {
			continue
		}
		if !r.expired(msg.TS) {
			kept = append(kept, msg)
			continue
		}
		if err := r.writeStatusRecord(ctx, msg, replyStatusNoReply, "消息已超过恢复窗口，未回复"); err != nil {
			kept = append(kept, msg)
			continue
		}
		r.scan.handled[msg.StepID] = true
	}
	r.scan.pending = kept
	return nil
}

// Pending 返回最旧的未处理协议消息：持久事实是唯一判据——心跳
// 有没有通知都无所谓，停机期间的提交与超载丢弃的待办都会被看见。
func (r *Responder) Pending(ctx context.Context) (Wake, bool, error) {
	r.scan.mu.Lock()
	defer r.scan.mu.Unlock()
	if err := r.scanNewLocked(); err != nil {
		return Wake{}, false, err
	}
	for len(r.scan.pending) > 0 {
		head := r.scan.pending[0]
		if r.scan.handled[head.StepID] || r.scan.suppressed[head.StepID] {
			r.scan.pending = r.scan.pending[1:]
			continue
		}
		break
	}
	if len(r.scan.pending) == 0 {
		return Wake{}, false, nil
	}
	return Wake{Kind: WakeStep, Step: r.scan.pending[0]}, true, nil
}

// scanNewLocked 只消费文件新追加的行并对账。锁必须由调用方持有。
// offset 回退（文件被截断/重写）意味着重放：账本随之重建，避免
// 把重放内容重复入账。
func (r *Responder) scanNewLocked() error {
	// 只补建缺失的账本，不覆盖已有记录：suppress/noteHandled 可能
	// 先于首次扫描发生（首次扫描前就收到 Wake 的路径）——覆盖会
	// 抹掉抑制记录，让消息重新面对模型。
	if r.scan.cursor == nil {
		r.scan.cursor = traj.NewCursor(r.opts.Timeline.Path)
	}
	if r.scan.handled == nil {
		r.scan.handled = map[string]bool{}
	}
	if r.scan.suppressed == nil {
		r.scan.suppressed = map[string]bool{}
	}
	before := r.scan.cursor.Offset()
	steps, err := r.scan.cursor.ReadNew()
	if err != nil {
		return err
	}
	if r.scan.cursor.Offset() < before {
		r.scan.handled = map[string]bool{}
		r.scan.suppressed = map[string]bool{}
		r.scan.pending = nil
	}
	for _, s := range steps {
		if s.Type != traj.TypeMessage {
			continue
		}
		if rt, ok := s.Field("reply_to"); ok {
			r.scan.handled[rt] = true
		}
		if r.isProtocolInbound(s) {
			r.scan.pending = append(r.scan.pending, s)
		}
	}
	return nil
}

// noteHandled 把一条入站消息记入已关闭——Wake 路径在处理成功后
// 立即入账，省掉一次"下一个心跳才追上"的空转。
func (r *Responder) noteHandled(id string) {
	r.scan.mu.Lock()
	defer r.scan.mu.Unlock()
	if r.scan.handled == nil {
		r.scan.handled = map[string]bool{}
	}
	r.scan.handled[id] = true
}

// suppress 在进程内抑制重试：收据写不出去（磁盘故障）时，Pending
// 不再把同一条消息交回给模型——防重试轰炸的最后一道内存防线。
// 重启即失效：若磁盘恢复，恢复路径会给它一次新的机会。
func (r *Responder) suppress(id string) {
	r.scan.mu.Lock()
	defer r.scan.mu.Unlock()
	if r.scan.suppressed == nil {
		r.scan.suppressed = map[string]bool{}
	}
	r.scan.suppressed[id] = true
}

// writeStatusRecord 落一条状态收据（Durable：收据是跨重启事实）。
// 不碰内存账本：调用方决定如何入账（Recover 持锁 / Wake 走
// writeStatus）。
func (r *Responder) writeStatusRecord(ctx context.Context, msg traj.Step, state, content string) error {
	from, _ := msg.Field("from")
	s := traj.NewStep(traj.TypeMessage)
	s.Fields["from"] = r.opts.SelfName
	s.Fields["to"] = from
	s.Fields["source"] = replyStatusSource
	s.Fields["state"] = state
	s.Fields["reply_to"] = msg.StepID
	s.Fields["launched_by"] = r.Name()
	s.Fields["content"] = content
	return r.opts.Timeline.AppendWithOptions(ctx, s, traj.AppendOptions{Durable: true})
}

// writeStatus 落收据并立即入账（Wake 路径）。
func (r *Responder) writeStatus(ctx context.Context, msg traj.Step, state, content string) error {
	if err := r.writeStatusRecord(ctx, msg, state, content); err != nil {
		return err
	}
	r.noteHandled(msg.StepID)
	return nil
}
