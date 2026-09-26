package mind

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"mindloop/internal/llm"
	"mindloop/internal/mem"
	"mindloop/internal/prompt"
	"mindloop/internal/traj"
)

// ResponderSystemPrompt 是 responder 的系统提示。与 monolith 的
// 分工遵循 Headlong 的同一条线：monolith 负责"行动"，responder
// 负责"说话"——它只做一次模型调用（无代理循环），延迟最低。
const ResponderSystemTemplate = `%s

You are also the voice of the agent: when a human message arrives, you compose the reply. Reply in the human's language, in a tone that fits your persona, briefly and directly. 普通聊天不是执行授权。如果需要运行命令或检查文件，请说明尚未创建任务，并引导用户使用“交给 Agent 执行”或 task submit；不得承诺已接单、正在执行或稍后自动回报。

History entries are prefixed with [timestamps] — that is metadata, not content. Never start your reply with a timestamp or copy any history formatting into your reply. Reply with plain spoken text only.`

// Responder 是对话回复思考者：拾取对身份的 message 步骤，组装
// 对话历史，一次模型调用产出回复，写回轨迹（reply_to 盖章）。
// 它同时是持久思考者：恢复账本见 chatdurable.go。
type Responder struct {
	opts ResponderOptions
	scan chatScan
}

// ResponderOptions 配置 responder。
type ResponderOptions struct {
	Timeline *traj.Timeline
	Thinker  runnerThinker
	// SelfName 是身份名；只回复 to == SelfName 的消息。
	SelfName string
	// Persona 是人格文本（注入系统提示）。
	Persona string
	// MaxHistory 是随回复附带的对话条数（默认 20）。
	MaxHistory int
	// ContextBudget 是单次回复的输入文本字节预算（0 取
	// prompt.DefaultContextBudget）；persona/规则与最新用户消息
	// 为受保护内容（超限报错），工作摘要与历史吃剩余。
	ContextBudget int
	// SummaryBudget 是工作摘要段（progressDigest）的字节上限
	// （0 取 prompt.DefaultSummaryBudget）。
	SummaryBudget int
	// MemDir 是记忆目录。非空时把与本次消息 BM25 相关的记忆注入
	// 系统提示（与 monolith 同款：线索而非已验证事实，ID 与来源
	// 步骤供追溯）——responder 此前对记忆全盲，agent 会在对话里
	// 否认已经写进记忆库的工作。
	MemDir string
	// MemoryBytes 是相关记忆段的字节上限（0 取
	// prompt.DefaultMemoryBudget）。
	MemoryBytes int
	// RecallWindow 之外的旧消息不再视为待回复（rewind 兜底，
	// 默认 15 分钟）。
	RecallWindow time.Duration
	// Streaming 开启回复流式旁路：回复生成期间把累积全文写进
	// <Timeline.Dir>/stream/<reply_to>.txt，并在 <RunLockDir>/replying
	// 维护状态文件（web SSE 端点据此实时推送）。最终 message 步骤
	// 照旧一次性落轨迹——旁路是瞬态投影，轨迹仍是唯一事实源。
	// 装配层按 MINDLOOP_STREAM 折算（!=0 即开，缺省开启）；关掉时
	// 完全不写旁路文件。
	Streaming bool
	// StreamFn 是流式补全的调用面：与 Think 同参，多一个 onDelta
	// 增量回调（逐段回调全文增量，读方以累积全量为准）。装配层从
	// llm.Client.CompleteStream 适配（见 cli/mindsetup.go）；nil =
	// 无流式能力，Streaming 开着也回退一次性补全。
	StreamFn func(ctx context.Context, system string, msgs []llm.Message, onDelta func(string)) (string, error)
	// MinDwell 是旁路文件的最小可观测驻留：快生成器（echo、缓存
	// 命中、短回复）的旁路存活可能短于读方（web SSE 200ms 轮询）
	// 的采样周期，流式协议会静默失效——finish 时不足额则补足再删。
	// 只延迟清理、不延迟回复落盘（顺序锚不变）。0 取默认 1s；负数
	// 禁用驻留（测试）。
	MinDwell time.Duration
}

// runnerThinker 是 responder 对模型调用的需求面：单次补全。
type runnerThinker interface {
	Think(ctx context.Context, system string, msgs []llm.Message) (string, error)
}

// NewResponder 构造 responder 思考者。
func NewResponder(opts ResponderOptions) Thinker {
	if opts.MaxHistory <= 0 {
		opts.MaxHistory = 20
	}
	if opts.RecallWindow <= 0 {
		opts.RecallWindow = 15 * time.Minute
	}
	switch {
	case opts.MinDwell == 0:
		opts.MinDwell = defaultReplyDwell
	case opts.MinDwell < 0:
		opts.MinDwell = 0 // 负数：禁用驻留（测试直通）
	}
	return &Responder{opts: opts}
}

func (r *Responder) Name() string { return responderName }

func (r *Responder) Subscriptions() Subscription {
	// 只订阅人类消息；回复步骤带 launched_by=responder，天然被
	// 本守卫挡住。不设 Watchdog：responder 是纯被动的，无事不做。
	return Subscription{Types: []string{traj.TypeMessage}, TriggerSelf: false}
}

func (r *Responder) Wake(ctx context.Context, w Wake) Outcome {
	step := w.Step
	if step.Type != traj.TypeMessage {
		return Outcome{}
	}
	if kind, _ := step.Field("message_kind"); kind == "task" {
		return Outcome{}
	}
	from, _ := step.Field("from")
	to, _ := step.Field("to")
	// 只回复对我的、不是我说的消息（防御：单流多身份时互不打扰）。
	if to != r.opts.SelfName || from == r.opts.SelfName || from == "" {
		return Outcome{}
	}
	// answered 守卫：同一入站消息绝不回第二次——Headlong 的
	// "三条回复一个你在吗" 事故的三层守卫里，日志事实是主防线。
	if r.answered(step.StepID) {
		return Outcome{Note: "已回复过，跳过"}
	}
	// 陈旧消息守卫：冷启动重放或停机积压太久的话不追答——
	// "一个月前的你在吗"不需要回答。新协议消息同时留下 no-reply
	// 收据：过期不会停留在"未决"，也不再被任何路径追答。
	if r.expired(step.TS) {
		if r.isProtocolInbound(step) {
			if err := r.writeStatus(ctx, step, replyStatusNoReply, "消息已超过恢复窗口，未回复"); err != nil {
				return Outcome{Note: "消息过于陈旧，跳过（no-reply 收据落盘失败: " + err.Error() + "）"}
			}
		}
		return Outcome{Note: "消息过于陈旧，跳过"}
	}
	content, _ := step.Field("content")

	// 流式旁路：建立（截断式）+ replying 状态。返回 nil（开关关、
	// 无 StreamFn、reply_to 不合规或建立失败）则退化为一次性补全
	// ——旁路是体验增强，绝不挡住回复本身。
	var stream *replyStream
	if r.opts.Streaming && r.opts.StreamFn != nil {
		stream = beginReplyStream(r.opts.Timeline.Dir, step.StepID, r.opts.MinDwell)
	}

	reply, err := r.compose(ctx, from, content, stream)
	if err != nil {
		stream.discard() // LLM 失败：旁路与状态一起消失
		note := "回复失败: " + err.Error()
		if r.isProtocolInbound(step) {
			// 持久化失败事实：收据即"不再重试"的日志依据，
			// 重启后 answered 守卫据此拦截；收据写不出去则内存
			// 抑制（取消不是失败事实——停机重启后仍应补答）。
			switch mErr := r.writeStatus(ctx, step, replyStatusFailed, note); {
			case mErr == nil:
				note += "（已记录失败事实，不自动重试）"
			case ctx.Err() == nil:
				r.suppress(step.StepID)
			}
		}
		return Outcome{Note: note}
	}
	stream.seal() // 全文定稿：关闭旁路句柄，等轨迹落盘后移除

	// 回复落盘：message 步骤 + reply_to 盖章 + launched_by=responder
	// （调度器据此不把回复喂回给 responder 自己）。
	s := traj.NewStep(traj.TypeMessage)
	s.Fields["from"] = r.opts.SelfName
	s.Fields["to"] = from
	s.Fields["source"] = "chat"
	s.Fields["reply_to"] = step.StepID
	s.Fields["launched_by"] = r.Name()
	s.Fields["content"] = reply
	if err := r.opts.Timeline.Append(ctx, s); err != nil {
		stream.discard()
		if r.isProtocolInbound(step) && ctx.Err() == nil {
			// 停机取消不算失败事实：重启恢复还会补答这条消息。
			r.suppress(step.StepID)
		}
		return Outcome{Note: "回复落盘失败: " + err.Error()}
	}
	// 轨迹已落盘才移除旁路：读方看到旁路消失（done）后 invalidate
	// 查询，必能拿到正式消息——顺序是 done 事件正确性的锚。
	stream.finish()
	r.noteHandled(step.StepID)
	return Outcome{Note: fmt.Sprintf("已回复 %s", from)}
}

// answered 扫描日志查找已盖 reply_to 章的回复——"消息是否已被
// 回答"是日志事实而非内存状态，重启后依然成立。
func (r *Responder) answered(triggerStepID string) bool {
	steps, err := r.opts.Timeline.Steps()
	if err != nil {
		return false // 读不出日志时宁可重试也不静默吞掉
	}
	for _, s := range steps {
		if s.Type != traj.TypeMessage {
			continue
		}
		if rt, ok := s.Field("reply_to"); ok && rt == triggerStepID {
			return true
		}
	}
	return false
}

// history 汇聚我与人类来访者的最近对话：人类来访者 = 所有对身份
// 说过话的 from——网页的 you 与 CLI 的 operator 是同一操作员的
// 不同入口，按日志序并入同一条对话流（双向消息都算）。按 from
// 精确匹配会让跨来源的对话互相不可见：agent 在 CLI 里对昨天在
// 网页里发生的事失忆。
func (r *Responder) history(person string, max int) []llm.Message {
	steps, err := r.opts.Timeline.Steps()
	if err != nil {
		return nil
	}
	// 人类来访者集合：所有入站（to == 我）的发送者 + 当前对话对象
	// （防御：对象还没说过话时依然成立）。任务提交是委托不是对话，
	// 不参与归并。
	humans := map[string]bool{person: true}
	for _, s := range steps {
		if s.Type != traj.TypeMessage {
			continue
		}
		if kind, _ := s.Field("message_kind"); kind == "task" {
			continue
		}
		from, _ := s.Field("from")
		if to, _ := s.Field("to"); to == r.opts.SelfName && from != "" && from != r.opts.SelfName {
			humans[from] = true
		}
	}
	var msgs []llm.Message
	for _, s := range steps {
		if s.Type != traj.TypeMessage {
			continue
		}
		if kind, _ := s.Field("message_kind"); kind == "task" {
			continue
		}
		// 状态收据是元事实（过期/失败），不是对话内容——混入历史
		// 会让模型模仿"系统收据"腔调。
		if src, _ := s.Field("source"); src == replyStatusSource {
			continue
		}
		from, _ := s.Field("from")
		to, _ := s.Field("to")
		var role string
		switch {
		case from == r.opts.SelfName && humans[to]:
			role = "assistant"
		case from != "" && from != r.opts.SelfName && to == r.opts.SelfName:
			role = "user"
		default:
			continue
		}
		content, _ := s.Field("content")
		// 日志即 API：ts 是外部输入（手工编辑/jq 重写的日志可能是
		// 任意字符串），不校验就切片会在调度器 goroutine 里 panic。
		ts := strings.ReplaceAll(s.TS, "T", " ")
		if len(ts) >= 19 {
			ts = ts[:19]
		}
		msgs = append(msgs, llm.Message{Role: role, Content: fmt.Sprintf("[%s] %s", ts, content)})
	}
	if len(msgs) > max {
		msgs = msgs[len(msgs)-max:]
	}
	return msgs
}

// relatedMemories 用本次入站消息检索记忆库，渲染成"- [type id]
// summary"行串（行格式与 monolith 共用）。查询取人话本身：用户问
// 什么就召回什么。检索失败或命中为空返回空串——记忆是增强，绝不
// 挡住回复。
func (r *Responder) relatedMemories(inbound string) string {
	if r.opts.MemDir == "" {
		return ""
	}
	hits, err := mem.Store{Dir: r.opts.MemDir}.Search(inbound, 3)
	if err != nil {
		return ""
	}
	var b strings.Builder
	for _, h := range hits {
		b.WriteString("\n- ")
		b.WriteString(memoryHitLine(h))
	}
	return b.String()
}

// compose 组装对话并做模型调用。stream 非 nil 时走流式：增量经
// onDelta 落进旁路文件，返回值仍是拼接后的完整全文——旁路只是
// 投影，全文的处理（去元数据前缀、落轨迹）与一次性补全完全一致。
//
// 上下文经预算账本组装：persona/规则与最新用户消息受保护（超限
// 报错，绝不静默截掉人话），工作摘要与相关记忆先按各自上限裁剪
// （低优先），历史再吃剩余——空余预算回流历史。
func (r *Responder) compose(ctx context.Context, person, inbound string, stream *replyStream) (string, error) {
	// 归因随 ctx 走到模型调用收尾：聊天开销与任务/唤醒分开记账。
	ctx = llm.WithAttrib(ctx, llm.Attrib{Thinker: r.Name(), Phase: "chat"})
	system := fmt.Sprintf(ResponderSystemTemplate, r.opts.Persona)
	budget := prompt.NewBudget(r.opts.ContextBudget)
	if err := budget.TakeProtected("system", system); err != nil {
		return "", err
	}
	if err := budget.TakeProtected("user", inbound); err != nil {
		return "", err
	}
	system += budget.TakeCapped("digest", r.progressDigest(), summaryCap(r.opts.SummaryBudget))
	if seg := budget.TakeCapped("memory", r.relatedMemories(inbound), memoryCap(r.opts.MemoryBytes)); seg != "" {
		// 与 monolith 同一口径：BM25 命中是线索而非已验证事实。
		system += "\n\n相关记忆（BM25 检索，线索而非已验证事实；ID 与来源步骤供追溯）：" + seg
	}
	msgs := fitMessages(r.history(person, r.opts.MaxHistory), budget.Remaining())
	msgs = append(msgs, llm.Message{Role: "user", Content: inbound})
	if stream != nil {
		text, err := r.opts.StreamFn(ctx, system, msgs, stream.appendDelta)
		if err != nil {
			return "", err
		}
		return stripMetaPrefix(strings.TrimSpace(text)), nil
	}
	text, err := r.opts.Thinker.Think(ctx, system, msgs)
	if err != nil {
		return "", err
	}
	return stripMetaPrefix(strings.TrimSpace(text)), nil
}

// summaryCap 折算低优先文本段的上限（0 = 默认，负值同）。
func summaryCap(v int) int {
	if v <= 0 {
		return prompt.DefaultSummaryBudget
	}
	return v
}

// fitMessages 丢弃最老的历史消息直到总量落在 limit 字节内——预算
// 装不下全部历史时丢最久远的回合，最近的对话与人话绝不丢。
func fitMessages(msgs []llm.Message, limit int) []llm.Message {
	total := 0
	for _, m := range msgs {
		total += len(m.Content)
	}
	drop := 0
	for drop < len(msgs) && total > limit {
		total -= len(msgs[drop].Content)
		drop++
	}
	return msgs[drop:]
}

// metaPrefixRe 匹配模型回写的时间戳元数据前缀——即使有系统提示，
// 历史里的 "[ts] content" 格式仍可能被模仿（Headlong 的模型曾
// 学会回写自己的 token 元数据）。输出侧再剥一次。
var metaPrefixRe = regexp.MustCompile(
	`^\[\d{4}-\d{2}-\d{2}[ T]\d{2}:\d{2}:\d{2}(?:\.\d+)?Z?\]\s*`)

func stripMetaPrefix(s string) string {
	return metaPrefixRe.ReplaceAllString(s, "")
}

// progressDigest 渲染「近期工作摘要」：最近至多 15 条非对话步骤
// （message/prompt/run 除外——它们要么已在历史里、要么是簿记），
// 每条一行 `[type] 首行excerpt · 相对时间`，注入系统提示尾部。
// responder 的上下文原本对 monolith 的工作成果全盲，ada 实测被问
// 进度时两次回答「没有任何命令输出」——摘要显式声明这是真实工作
// 记录，被问进度必须据此如实回答。无可摘要步骤返回空串（系统提示
// 与历史行为完全不变）。
func (r *Responder) progressDigest() string {
	steps, err := r.opts.Timeline.Steps()
	if err != nil {
		return ""
	}
	const maxLines, excerptRunes = 15, 100
	var lines []string
	for i := len(steps) - 1; i >= 0 && len(lines) < maxLines; i-- {
		s := steps[i]
		switch s.Type {
		case traj.TypeMessage, traj.TypePrompt, traj.TypeRun, traj.TypeTrajectory:
			continue
		}
		content, _ := s.Field("content")
		excerpt := firstLine(content)
		if excerpt == "" {
			continue // 无正文的簿记步骤（fork 等）不进摘要
		}
		line := fmt.Sprintf("[%s] %s", s.Type, truncateRunes(excerpt, excerptRunes))
		if age := relAge(s.TS); age != "" {
			line += " · " + age
		}
		lines = append([]string{line}, lines...) // 新的在上
	}
	if len(lines) == 0 {
		return ""
	}
	return "\n\n## Work digest（近期工作记录）\n" +
		"这是你（monolith）近期的真实工作记录（最新在上），被问进度时必须据此如实回答：\n" +
		strings.Join(lines, "\n")
}

// firstLine 取多行文本的首行（裁剪在调用方做）。
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// relAge 把步骤时间戳渲染成粗粒度中文相对时间（"" = 不可解析）。
func relAge(ts string) string {
	t, err := time.Parse(traj.TimeFormat, ts)
	if err != nil {
		return ""
	}
	switch d := time.Since(t); {
	case d < time.Minute:
		return "刚刚"
	case d < time.Hour:
		return fmt.Sprintf("%d 分钟前", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d 小时前", int(d.Hours()))
	default:
		return fmt.Sprintf("%d 天前", int(d.Hours()/24))
	}
}
