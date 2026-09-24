package mind

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"mindloop/internal/llm"
	"mindloop/internal/traj"
)

// ResponderSystemPrompt 是 responder 的系统提示。与 monolith 的
// 分工遵循 Headlong 的同一条线：monolith 负责"行动"，responder
// 负责"说话"——它只做一次模型调用（无代理循环），延迟最低。
const ResponderSystemTemplate = `%s

You are also the voice of the agent: when a human message arrives, you compose the reply. Reply in the human's language, in a tone that fits your persona, briefly and directly. If the message needs real work (running commands, checking files), say what you will do and that you will report back — do NOT pretend you did it. Never mention this protocol.

History entries are prefixed with [timestamps] — that is metadata, not content. Never start your reply with a timestamp or copy any history formatting into your reply. Reply with plain spoken text only.`

// Responder 是对话回复思考者：拾取对身份的 message 步骤，组装
// 对话历史，一次模型调用产出回复，写回轨迹（reply_to 盖章）。
type Responder struct {
	opts ResponderOptions
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
	// RecallWindow 之外的旧消息不再视为待回复（rewind 兜底，
	// 默认 15 分钟）。
	RecallWindow time.Duration
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
	return &Responder{opts: opts}
}

func (r *Responder) Name() string { return "responder" }

func (r *Responder) Subscriptions() Subscription {
	// 只订阅人类消息；回复步骤带 launched_by=responder，天然被
	// 本守卫挡住。不设 Watchdog：responder 是纯被动的，无事不做。
	return Subscription{Types: []string{"message"}, TriggerSelf: false}
}

func (r *Responder) Wake(ctx context.Context, w Wake) Outcome {
	step := w.Step
	if step.Type != "message" {
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
	// "一个月前的你在吗"不需要回答。
	if ts, err := time.Parse(traj.TimeFormat, step.TS); err == nil && time.Since(ts) > r.opts.RecallWindow {
		return Outcome{Note: "消息过于陈旧，跳过"}
	}
	content, _ := step.Field("content")

	reply, err := r.compose(ctx, from, content)
	if err != nil {
		return Outcome{Note: "回复失败: " + err.Error()}
	}
	// 回复落盘：message 步骤 + reply_to 盖章 + launched_by=responder
	// （调度器据此不把回复喂回给 responder 自己）。
	s := traj.NewStep("message")
	s.Fields["from"] = r.opts.SelfName
	s.Fields["to"] = from
	s.Fields["source"] = "chat"
	s.Fields["reply_to"] = step.StepID
	s.Fields["launched_by"] = r.Name()
	s.Fields["content"] = reply
	if err := r.opts.Timeline.Append(ctx, s); err != nil {
		return Outcome{Note: "回复落盘失败: " + err.Error()}
	}
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
		if s.Type != "message" {
			continue
		}
		if rt, ok := s.Field("reply_to"); ok && rt == triggerStepID {
			return true
		}
	}
	return false
}

// history 汇聚我与某人的最近对话（双方消息按日志序，去重）。
func (r *Responder) history(person string, max int) []llm.Message {
	steps, err := r.opts.Timeline.Steps()
	if err != nil {
		return nil
	}
	var msgs []llm.Message
	for _, s := range steps {
		if s.Type != "message" {
			continue
		}
		from, _ := s.Field("from")
		if from != person && from != r.opts.SelfName {
			continue
		}
		if from == r.opts.SelfName {
			if to, ok := s.Field("to"); !ok || to != person {
				continue
			}
		}
		content, _ := s.Field("content")
		role := "user"
		if from == r.opts.SelfName {
			role = "assistant"
		}
		ts := strings.ReplaceAll(s.TS, "T", " ")
		msgs = append(msgs, llm.Message{Role: role, Content: fmt.Sprintf("[%s] %s", ts[:19], content)})
	}
	if len(msgs) > max {
		msgs = msgs[len(msgs)-max:]
	}
	return msgs
}

// compose 组装对话并做一次模型调用。
func (r *Responder) compose(ctx context.Context, person, inbound string) (string, error) {
	system := fmt.Sprintf(ResponderSystemTemplate, r.opts.Persona)
	msgs := r.history(person, r.opts.MaxHistory)
	msgs = append(msgs, llm.Message{Role: "user", Content: inbound})
	text, err := r.opts.Thinker.Think(ctx, system, msgs)
	if err != nil {
		return "", err
	}
	return stripMetaPrefix(strings.TrimSpace(text)), nil
}

// metaPrefixRe 匹配模型回写的时间戳元数据前缀——即使有系统提示，
// 历史里的 "[ts] content" 格式仍可能被模仿（Headlong 的模型曾
// 学会回写自己的 token 元数据）。输出侧再剥一次。
var metaPrefixRe = regexp.MustCompile(
	`^\[\d{4}-\d{2}-\d{2}[ T]\d{2}:\d{2}:\d{2}(?:\.\d+)?Z?\]\s*`)

func stripMetaPrefix(s string) string {
	return metaPrefixRe.ReplaceAllString(s, "")
}
