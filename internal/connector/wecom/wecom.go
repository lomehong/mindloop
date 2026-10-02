// Package wecom 是企业微信智能机器人的 bridge 适配器：WS 长连接
// （无公网可部署）、回调入站、aibot_send_msg markdown 出站。
//
// 协议要点（docs/designs/connectors.md §7；字段按官方
// @wecom/aibot-node-sdk 线上实现校准，集中在 protocol.go）：
//   - 订阅：建连后发送 aibot_subscribe（bot_id + 长连接专用
//     secret）并等待回执——errcode!=0 计认证失败，连续 5 次
//     （SDK maxAuthFailureAttempts）判凭证不可用，终止并提示；
//   - 心跳：应用层 {cmd:"ping"} 帧（30s），连续 2 次无回执
//     （SDK maxMissedPong）判连接死亡，断开重连；
//   - 单连接互踢：aibot_event_callback 的 event.eventtype =
//     disconnected_event——以 ErrKicked 终止，不做双连竞争；
//   - 入站只处理文本（图片/语音不落轨迹）；幂等键 wecom:<msgid>，
//     保守假设服务端可能重推，重推由幂等键吸收；
//   - 出站统一 aibot_send_msg（chatid=userid 单聊），不用 req_id
//     回复通道——chatid 是无状态映射，与"路由即数据"吻合；前提是
//     用户先给机器人发过消息（解锁），首次使用引导见部署文档。
//     发送等待 errcode 回执（SDK 同款 10s 超时），失败交给出站泵
//     的重试退避。
package wecom

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lomehong/mindloop/internal/connector"
	"github.com/lomehong/mindloop/internal/connector/ws"
	"github.com/lomehong/mindloop/internal/mind"
	"github.com/lomehong/mindloop/internal/traj"
)

// ErrKicked 是单连接互踢的哨兵：收到 disconnected_event 返回它，
// 调用方据此退出进程（重启即自然让位，不与新连接竞争）。
var ErrKicked = errors.New("wecom: 连接被同 BotID 的新连接顶替")

// ErrAuthFailed 是凭证不可用的哨兵：连续认证失败达到上限（密钥错/
// 机器人被删）——重连无意义，终止并提示检查 BotID/Secret。
var ErrAuthFailed = errors.New("wecom: 认证持续失败，凭证可能无效")

// SDK 对齐的常量。
const (
	// authFailureLimit 是连续认证失败上限（SDK maxAuthFailureAttempts）。
	authFailureLimit = 5
	// maxMissedPong 是心跳无回执判死阈值（SDK maxMissedPong）。
	maxMissedPong = 2
	// maxMarkdownBytes 是出站单条 markdown 的字节阈值（官方上限
	// ~20480 字节，按设计的保守折扣取 90%）。
	maxMarkdownBytes = 18 * 1000
	// SourceName 是入站消息的 source 标记。
	SourceName = "wecom"
)

// wsConn 是传输层的最小接口：桥本体不依赖 ws 包的具体连接类型。
type wsConn interface {
	ReadMessage() (ws.Opcode, []byte, error)
	WriteMessage(ws.Opcode, []byte) error
	WritePing([]byte) error
	Close() error
}

// Options 配置 bridge。
type Options struct {
	// Timeline / Self 是根轨迹与身份名。
	Timeline *traj.Timeline
	Self     string
	// BotID / Secret 是企微智能机器人凭据（长连接专用 Secret）。
	BotID  string
	Secret string
	// Allow 是白名单 userid（缺失拒绝启动——fail-closed 红线）。
	Allow []string
	// StateDir 是出站泵游标目录（<身份>/connectors/）。
	StateDir string
	// Addr 是 WS 地址（默认官方网关；测试注入 httptest 地址）。
	Addr string
	// Dial 是连接函数（默认 ws.Dial；测试注入假服务端）。
	Dial func(ctx context.Context, addr string) (wsConn, error)
	// PingInterval 是应用层心跳周期（默认 30s，SDK 同款）。
	PingInterval time.Duration
	// ApproveTo 是审批卡的投递地址（缺省白名单第一人）。
	ApproveTo string
	// StreamInterval 是流式旁路轮询/节流周期（默认 500ms）。
	StreamInterval time.Duration
	// ApprovalInterval 是待批目录轮询周期（默认 2s）。
	ApprovalInterval time.Duration
	// AckTimeout 是订阅/心跳/发送回执的等待上限（默认 10s，SDK
	// requestTimeout 同款）。
	AckTimeout time.Duration
	// ReconnectBackoff 是重连退避起点（默认 1s，指数翻倍封顶 30s；
	// 测试注入小值加速）。
	ReconnectBackoff time.Duration
	// Logger 注入诊断。
	Logger func(format string, args ...any)
}

// Bridge 是企微智能机器人 bridge。
type Bridge struct {
	opts    Options
	allow   map[string]bool
	dial    func(ctx context.Context, addr string) (wsConn, error)
	pingIn  time.Duration
	backoff time.Duration // 重连退避
	ackWait time.Duration

	// pending 是等待回执的请求（req_id → 回执通道）：订阅、心跳、
	// 发送共用一套对账——响应帧无 cmd，只能按 req_id 归位。
	mu      sync.Mutex
	pending map[string]chan parsedFrame
	conn    wsConn // 当前连接（重连时替换；出站投递按需取用）

	// authFails 是跨连接的连续认证失败计数（成功即清零）。
	authFails atomic.Int64

	// 流式与会话（StateDir 持久化；丢失即降级，不影响正确性）。
	sessions *sessionStore
	tokens   *tokenStore
}

// New 构造 bridge；凭据或白名单缺失拒绝启动（fail-closed）。
func New(opts Options) (*Bridge, error) {
	if opts.Timeline == nil {
		return nil, errors.New("wecom: 缺少根轨迹")
	}
	if strings.TrimSpace(opts.BotID) == "" || strings.TrimSpace(opts.Secret) == "" {
		return nil, errors.New("wecom: 缺少 WECOM_BOT_ID / WECOM_BOT_SECRET")
	}
	if strings.TrimSpace(opts.StateDir) == "" {
		// 游标文件必须落在身份目录下——空值会让它落在进程工作目录
		// 的相对路径上，跨身份/跨进程互相覆盖（实测事故形态）。
		return nil, errors.New("wecom: 缺少 StateDir（出站游标目录）")
	}
	if len(opts.Allow) == 0 {
		return nil, errors.New("wecom: 缺少 WECOM_ALLOW 白名单（红线：无白名单不启动）")
	}
	if opts.Addr == "" {
		opts.Addr = "wss://openws.work.weixin.qq.com"
	}
	if opts.Dial == nil {
		opts.Dial = dialReal
	}
	if opts.PingInterval <= 0 {
		opts.PingInterval = 30 * time.Second
	}
	if opts.AckTimeout <= 0 {
		opts.AckTimeout = 10 * time.Second
	}
	if opts.ReconnectBackoff <= 0 {
		opts.ReconnectBackoff = time.Second
	}
	if opts.StreamInterval <= 0 {
		opts.StreamInterval = 500 * time.Millisecond
	}
	if opts.ApprovalInterval <= 0 {
		opts.ApprovalInterval = 2 * time.Second
	}
	allow := make(map[string]bool, len(opts.Allow))
	for _, uid := range opts.Allow {
		uid = strings.TrimSpace(uid)
		if uid != "" {
			allow[uid] = true
		}
	}
	if len(allow) == 0 {
		return nil, errors.New("wecom: WECOM_ALLOW 白名单为空")
	}
	// 出站游标目录必须存在：cursor.Save 用 os.WriteFile，不建父
	// 目录——目录缺失时保存静默失败，重启后从 EOF 起步，桥停机
	// 期间的主动汇报会全部漏发（实测发现）。
	if err := os.MkdirAll(opts.StateDir, 0o755); err != nil {
		return nil, fmt.Errorf("wecom: 创建游标目录失败: %w", err)
	}
	return &Bridge{
		opts:     opts,
		allow:    allow,
		dial:     opts.Dial,
		pingIn:   opts.PingInterval,
		backoff:  opts.ReconnectBackoff,
		ackWait:  opts.AckTimeout,
		pending:  map[string]chan parsedFrame{},
		sessions: newSessionStore(filepath.Join(opts.StateDir, "wecom-sessions.json")),
		tokens:   newTokenStore(filepath.Join(opts.StateDir, "wecom-tokens.json")),
	}, nil
}

// Addresses 是出站泵的认领地址集（白名单 userid 一一映射）。
func (b *Bridge) Addresses() []string {
	out := make([]string, 0, len(b.allow))
	for uid := range b.allow {
		out = append(out, "wecom:"+uid)
	}
	return out
}

// Run 运行 bridge 直到 ctx 取消、互踢或凭证判死：出站泵在后台
// goroutine，主循环是入站（断线指数退避重连、重连即重新订阅）。
func (b *Bridge) Run(ctx context.Context) error {
	// 派生内部 ctx：任何终止路径（互踢/判错/外部取消）都先取消它，
	// 泵才会退出——否则 Run 在等泵收尾时自锁（实测挂死）。
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	ctx = runCtx
	// 出站泵：认领 to=wecom:<白名单 userid> 的回复与主动汇报。
	pump, err := connector.NewOutbound(connector.OutboundOptions{
		Path:       b.opts.Timeline.Path,
		CursorPath: filepath.Join(b.opts.StateDir, "wecom-outbound.cursor"),
		Self:       b.opts.Self,
		Addresses:  b.Addresses(),
		Deliver:    &sender{b: b},
		Skip: func(s traj.Step) bool {
			// 会话跟踪到的回复（reply_to 命中已跟踪入站步骤）完全归
			// 流式泵所有：增量 + finish 终稿 + 失败兜底发送都在流式
			// 泵内闭环——两条路径赛跑会产生重复推送（实测事故）。
			// 未跟踪（桥重启丢会话）的回复仍走出站泵。
			if rt, ok := s.Field("reply_to"); ok && rt != "" {
				return b.sessions.has(rt)
			}
			return false
		},
		Logger: b.logf,
	})
	if err != nil {
		return err
	}
	pumpDone := make(chan struct{})
	go func() {
		defer close(pumpDone)
		if err := pump.Run(ctx); err != nil && ctx.Err() == nil {
			b.logf("出站泵退出: %v", err)
		}
	}()
	// 流式回复泵：req_id 凭证来自入站回调的会话跟踪；丢失即降级
	// 为出站泵的主动推送。
	go b.runStreamer(ctx, b.opts.StreamInterval)
	// 审批卡 watcher：policy 待批目录 → button_interaction 卡 →
	// 点击回调落决策（手机上完成审批闭环）。
	go b.runApprovals(ctx, b.opts.ApprovalInterval)
	// Run 返回前等泵收尾：泵的退出路径落盘游标——不等会让下一任
	// 桥在游标落盘前启动，续读变"首启"，重启窗口的主动汇报漏发。
	// defer LIFO：此处必须显式先 cancel（内部 ctx）再等——否则
	// 顶部的 defer cancel 要等本 defer 返回后才跑，本 defer 会等
	// 一个永不退出的泵，自锁（实测挂死）。
	defer func() {
		cancel()
		<-pumpDone
	}()

	for {
		if err := b.runOnce(ctx); err != nil {
			return err
		}
		// runOnce 正常返回 = 连接断开：退避重连。
		b.logf("连接断开，%s 后重连", b.backoff)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(b.backoff):
		}
		b.backoff *= 2
		if cap30s := 30 * time.Second; b.backoff > cap30s {
			b.backoff = cap30s
		}
	}
}

// runOnce 是一次连接生命周期：拨号 → 订阅（等认证回执）→ 心跳与
// 读循环。返回 nil 表示连接断开（可重连）；返回 ErrKicked /
// ErrAuthFailed / ctx 错误表示终止。
func (b *Bridge) runOnce(ctx context.Context) error {
	conn, err := b.dial(ctx, b.opts.Addr)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		b.logf("拨号失败: %v", err)
		return nil
	}
	b.setConn(conn)
	defer func() {
		b.setConn(nil)
		_ = conn.Close()
	}()

	// 读循环先行：认证回执也要经它进来——"先等回执再起读循环"
	// 会自己把自己饿死（回执没有读者）。
	frames := make(chan []byte, 16)
	readErr := make(chan error, 1)
	go func() {
		defer close(frames)
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				b.logf("reader 退出: %v", err)
				readErr <- err
				return
			}
			frames <- data
		}
	}()

	// 认证订阅：等回执、计失败。SDK 语义——errcode!=0 计一次连续
	// 失败（上限 5 判凭证不可用），成功即清零。等待内嵌在帧循环里
	//（authCh 与读循环同一 select），不阻塞帧的接收。
	authReq := newReqID(cmdSubscribe)
	authCh := make(chan parsedFrame, 1)
	b.setPending(authReq, authCh)
	defer b.dropPending(authReq)
	if err := conn.WriteMessage(ws.OpText, subscribeFrame(b.opts.BotID, b.opts.Secret, authReq)); err != nil {
		b.logf("订阅发送失败: %v", err)
		return nil
	}
	authed := false
	for !authed {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-readErr:
			if !errors.Is(err, ws.ErrClosed) {
				b.logf("读取断开: %v", err)
			}
			return nil
		case data, ok := <-frames:
			if !ok {
				b.logf("读循环退出（连接关闭）")
				return nil
			}
			parsed, perr := parseFrame(data)
			if perr != nil {
				b.logf("无法解析的帧（忽略）: %v", perr)
				continue
			}
			if parsed.IsResponse {
				if parsed.ReqID == authReq {
					authCh <- parsed
				} else {
					b.settleResponse(parsed)
				}
				continue
			}
			b.logf("认证前收到业务帧 %s（忽略）", parsed.Cmd)
		case f := <-authCh:
			switch {
			case f.Errcode != 0:
				n := b.authFails.Add(1)
				b.logf("认证失败（第 %d 次，errcode=%d）: %s", n, f.Errcode, f.Errmsg)
				if n >= authFailureLimit {
					return fmt.Errorf("%w（连续 %d 次，errcode=%d: %s）——请检查 WECOM_BOT_ID / WECOM_BOT_SECRET",
						ErrAuthFailed, n, f.Errcode, f.Errmsg)
				}
				return nil
			default:
				b.authFails.Store(0)
				authed = true
				b.logf("认证成功")
			}
		}
	}

	tick := time.NewTicker(b.pingIn)
	defer tick.Stop()
	var missedPong atomic.Int64
	b.backoff = b.opts.ReconnectBackoff // 建连成功：重置退避
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-readErr:
			if !errors.Is(err, ws.ErrClosed) {
				b.logf("读取断开: %v", err)
			}
			return nil
		case <-tick.C:
			// 应用层心跳：连续 maxMissedPong 次无回执判连接死亡
			//（回执等待在后台 goroutine，绝不阻塞读循环）。
			if missedPong.Load() >= maxMissedPong {
				b.logf("连续 %d 次心跳无回执，判定连接死亡", missedPong.Load())
				return nil
			}
			reqID := newReqID(cmdPing)
			if err := conn.WriteMessage(ws.OpText, pingFrame(reqID)); err != nil {
				b.logf("心跳发送失败: %v", err)
				return nil
			}
			go func() {
				f, err := b.awaitResponse(ctx, reqID)
				if err != nil || f.Errcode != 0 {
					missedPong.Add(1)
				} else {
					missedPong.Store(0)
				}
			}()
		case data, ok := <-frames:
			if !ok {
				b.logf("读循环退出（连接关闭）")
				return nil
			}
			parsed, err := parseFrame(data)
			if err != nil {
				b.logf("无法解析的帧（忽略）: %v", err)
				continue
			}
			if parsed.IsResponse {
				b.settleResponse(parsed) // 订阅/心跳/发送的回执按 req_id 归位
				continue
			}
			if err := b.handleFrame(ctx, parsed); err != nil {
				return err
			}
		}
	}
}

// handleFrame 分发一帧业务帧：消息回调入站、互踢事件终止；其余
// 命令忽略（enter_chat 等事件）。返回错误即终止 Run（目前只有互踢）。
func (b *Bridge) handleFrame(ctx context.Context, f parsedFrame) error {
	switch f.Cmd {
	case cmdMsgCallback:
		var body callbackBody
		if len(f.Body) > 0 {
			if err := json.Unmarshal(f.Body, &body); err != nil {
				b.logf("回调载荷解析失败（忽略）: %v", err)
				return nil
			}
		}
		b.handleCallback(ctx, f.ReqID, body)
	case cmdEventCallback:
		var ev eventBody
		if len(f.Body) > 0 {
			if err := json.Unmarshal(f.Body, &ev); err != nil {
				b.logf("事件载荷解析失败（忽略）: %v", err)
				return nil
			}
		}
		switch ev.Event.EventType {
		case eventTypeDisconnected:
			b.logf("%v", ErrKicked)
			return ErrKicked
		case eventTypeCard:
			b.handleCardClick(ctx, f, ev)
		default:
			b.logf("忽略事件 %s", ev.Event.EventType)
		}
	default:
		b.logf("忽略命令 %s", f.Cmd)
	}
	return nil
}

// handleCallback 处理一条入站回调：白名单（fail-closed）→ 文本
// 提取 → write-then-ack（先落盘；WS 推送无确认通道，重推由幂等键
// 吸收——设计 §4.4 确认矩阵的企微行）。
func (b *Bridge) handleCallback(ctx context.Context, reqID string, body callbackBody) {
	userid := body.From.Userid
	if !b.allow[userid] {
		b.logf("白名单外用户 %q 的消息已丢弃（不落轨迹）", userid)
		return
	}
	content := strings.TrimSpace(body.Text.Content)
	if content == "" || body.Msgid == "" {
		b.logf("跳过非文本或无 msgid 的回调")
		return
	}
	// 幂等键 = wecom:<msgid>：服务端重推同一 msgid 时同键同内容
	// 返回原步骤；冲突（同键不同内容）记日志不重试。
	step, err := mind.PostMessageOnce(b.opts.Timeline, "wecom:"+userid,
		b.opts.Self, SourceName, content, "wecom:"+body.Msgid)
	if err == nil {
		// 会话跟踪：回复步骤以 reply_to 引用入站 step_id，流式泵
		// 凭这里记录的 req_id 走被动通道（丢失即降级主动推送）。
		b.sessions.put(step.StepID, session{ReqID: reqID, Userid: userid})
	}
	if errors.Is(err, mind.ErrMessageConflict) {
		b.logf("msgid %s 内容冲突（丢弃，不重试）", body.Msgid)
		return
	}
	if err != nil {
		// 落盘失败：服务端可能重推（保守假设），届时幂等键兜底；
		// 本帧丢弃不阻塞读循环。
		b.logf("入站落盘失败（msgid %s）: %v", body.Msgid, err)
		return
	}
}

// sender 适配出站泵：to=wecom:<userid> → aibot_send_msg（chatid
// 无状态映射），长文按渠道阈值分段，每段等 errcode 回执——失败
// 交给出站泵的重试退避，桥本体不做自己的等待队列。
type sender struct{ b *Bridge }

func (s *sender) Send(ctx context.Context, to, text string) error {
	c := s.b.currentConn()
	if c == nil {
		return errors.New("wecom: 连接未就绪")
	}
	userid := strings.TrimPrefix(to, "wecom:")
	for _, seg := range connector.Segment(text, maxMarkdownBytes) {
		reqID := newReqID(cmdSendMsg)
		frame := sendMsgFrame(userid, seg, reqID)
		if err := c.WriteMessage(ws.OpText, frame); err != nil {
			s.b.logf("Send 写帧失败: %v", err)
			return err
		}
		resp, err := s.b.awaitResponse(ctx, reqID)
		if err != nil {
			return fmt.Errorf("wecom: 发送回执缺失: %w", err)
		}
		if resp.Errcode != 0 {
			return fmt.Errorf("wecom: 发送失败（errcode=%d）: %s", resp.Errcode, resp.Errmsg)
		}
	}
	return nil
}

// setPending / dropPending 是 awaitResponse 的手动形态：认证等待
// 内嵌在帧循环的 select 里，需要先登记再进循环。
func (b *Bridge) setPending(reqID string, ch chan parsedFrame) {
	b.mu.Lock()
	b.pending[reqID] = ch
	b.mu.Unlock()
}

func (b *Bridge) dropPending(reqID string) {
	b.mu.Lock()
	delete(b.pending, reqID)
	b.mu.Unlock()
}

// awaitResponse 登记 req_id 并等待回执（超时/ctx 取消即失败）。
func (b *Bridge) awaitResponse(ctx context.Context, reqID string) (parsedFrame, error) {
	ch := make(chan parsedFrame, 1)
	b.mu.Lock()
	b.pending[reqID] = ch
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.pending, reqID)
		b.mu.Unlock()
	}()
	select {
	case f := <-ch:
		return f, nil
	case <-time.After(b.ackWait):
		return parsedFrame{}, errors.New("等待回执超时")
	case <-ctx.Done():
		return parsedFrame{}, ctx.Err()
	}
}

// settleResponse 把一帧回执按 req_id 归位；无主回执静默丢弃。
func (b *Bridge) settleResponse(f parsedFrame) {
	b.mu.Lock()
	ch, ok := b.pending[f.ReqID]
	b.mu.Unlock()
	if ok {
		ch <- f
	}
}

func (b *Bridge) setConn(c wsConn) {
	b.mu.Lock()
	b.conn = c
	b.mu.Unlock()
}

func (b *Bridge) currentConn() wsConn {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.conn
}

func (b *Bridge) logf(format string, args ...any) {
	if b.opts.Logger != nil {
		b.opts.Logger(format, args...)
	}
}

// dialReal 是默认连接函数（官方网关 wss）。
func dialReal(ctx context.Context, addr string) (wsConn, error) {
	return ws.Dial(ctx, addr)
}
