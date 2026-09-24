package mind

import (
	"context"
	"fmt"
	"strings"
	"time"

	"mindloop/internal/ids"
	"mindloop/internal/mem"
	"mindloop/internal/recap"
	"mindloop/internal/runner"
	"mindloop/internal/traj"
)

// MonolithSystemPrompt 是 monolith 的系统提示：每次唤醒决定并执行
// 下一步。英文面向模型；FINAL="IDLE" 是"无事可做"的协议值。
const MonolithSystemPrompt = `You are the monolith — the persistent mind of an agent. You have just been woken up.

Each wake, you look at the recent stream of your life (rendered after the system prompt) and do the NEXT useful thing. Not a plan. One concrete step, executed now.

How to decide:
- If a human message or unfinished business needs action: act on it now.
- If you have an ongoing project: advance it one small verifiable step.
- If there is something worth learning or recording: do it in your work directory.
- If there is genuinely nothing worth doing: set FINAL="IDLE" without running commands.

How to act: output exactly ONE bash code block per turn (bash, "set -e" — on Windows the shell is Git Bash). Its output returns to you. When your step is done, set FINAL to a one-line summary of what you actually did (past tense, evidence-backed). Never claim success without output proving it.

Never use interactive commands. One wake is not the place to finish everything — the dispatcher will wake you again.`

// MonolithOptions 配置 monolith 思考者。
type MonolithOptions struct {
	Timeline *traj.Timeline
	Thinker  runner.Thinker
	// Backoff 是闲置回退策略（nil 取默认）。
	Backoff *BackoffPolicy
	// MaxIterations 是每次唤醒内部的轮次上限（默认 8）：一次唤醒
	// 本身就是一次完整的运行循环（Headlong："each wakeup is
	// itself a shellm run"）。
	MaxIterations int
	// Persona 是人格文本，注入每次唤醒的系统提示。
	Persona string
	// MemDir 非空时，唤醒提示会带上与近期思维流相关的记忆
	// （BM25 检索，最多 3 条）。
	MemDir string
	// Timeout / IdleTimeout / MaxOutputBytes 透传给每轮执行。
	Timeout        time.Duration
	IdleTimeout    time.Duration
	MaxOutputBytes int64
	// Watchdog 是调度器合成唤醒的间隔（0 取默认 5 分钟）。
	Watchdog time.Duration
	// EnableRecap 开启分层上下文：每次唤醒先补齐缺失的情节摘要
	//（每次至多 2 条，成本阀），并把"人生分集"作为上下文粗层。
	EnableRecap bool
	// RequestThinker 是请求档思考者（可选；nil = 单档）。反应式
	// 唤醒——外部步骤触发（观察/合并，通常是 responder 报告人类
	// 来话或外部产物）——用它；自发的定时/watchdog 空唤醒用
	// Thinker（思考档）。双模型分层来自 Headlong 2026-09-23 的
	// 实测：成熟身份的空闲唤醒占唤醒总数的绝大多数，分层后安静
	// 日的模型开销降 70-80%。用量台账按客户端记录模型名，两档
	// 在 llm-usage.jsonl 里天然可区分。
	RequestThinker runner.Thinker
	// SetLogger 注入日志回调（recap 进度）。
	SetLogger func(format string, args ...any)
}

// monolith 实现 Thinker。回退状态（层级 + 驻留计数）留在实例里：
// 调度器保证同一时刻至多一次唤醒在跑（busy 标志），无需并发保护。
type monolith struct {
	opts  MonolithOptions
	level int
	ticks int // 当前层级的空唤醒驻留计数（Hold dwell）
}

// NewMonolith 构造 monolith 思考者。
func NewMonolith(opts MonolithOptions) Thinker {
	if opts.MaxIterations <= 0 {
		opts.MaxIterations = 8
	}
	return &monolith{opts: opts}
}

func (m *monolith) Name() string { return "monolith" }

func (m *monolith) Subscriptions() Subscription {
	// 订阅外部产物与合成唤醒。人类 message 归 responder（分工：
	// monolith 行动，responder 说话）；不订阅自己产出的任何类型
	// ——包括 classify 写下的 action（测试钉死这条不变量）。
	// launched_by 守卫是第二道防线；Watchdog 是活性兜底：哪怕
	// 预约机制整个失灵，也会被周期性唤醒——活性由调度器保证，
	// 不靠 thinker 自身代码路径。
	watchdog := m.opts.Watchdog
	if watchdog <= 0 {
		watchdog = 5 * time.Minute
	}
	return Subscription{
		Types:       []string{"observation", "merge", "monolith-wake"},
		TriggerSelf: false,
		Watchdog:    watchdog,
	}
}

func (m *monolith) Wake(ctx context.Context, w Wake) Outcome {
	reactive := w.Kind == WakeStep && w.Step.Type == "message"
	reason := "scheduled spontaneity"
	switch w.Kind {
	case WakeWatchdog:
		reason = "watchdog keep-alive check"
	case WakeStep:
		reason = fmt.Sprintf("step %s (%s)", w.Step.Type, ids.Short(w.Step.StepID, 8))
	}

	start := time.Now()
	// 分层上下文的补全：唤醒前先把积压的情节摘要补掉（每次至多
	// 2 条，成本阀）。摘要失败不阻塞唤醒——粗层缺失只是上下文
	// 变薄，不是停机理由。
	if m.opts.EnableRecap {
		u := &recap.Updater{
			Timeline:     m.opts.Timeline,
			Thinker:      m.opts.Thinker,
			MaxSummaries: 2,
		}
		if rep, err := u.Update(ctx); err != nil {
			if m.opts.SetLogger != nil {
				m.opts.SetLogger("recap 更新失败（继续唤醒）: %v", err)
			}
		} else if rep.Summarized > 0 && m.opts.SetLogger != nil {
			m.opts.SetLogger("recap 新增 %d 条情节摘要", rep.Summarized)
		}
	}
	// 双模型分层：反应式唤醒（外部步骤触发）走请求档，自发的
	// 定时/watchdog 唤醒走思考档。未配请求档时两档同一。
	thinker := m.opts.Thinker
	if w.Kind == WakeStep && m.opts.RequestThinker != nil {
		thinker = m.opts.RequestThinker
	}
	res, err := runner.Run(ctx, runner.Options{
		Timeline:       m.opts.Timeline,
		Thinker:        thinker,
		Task:           m.wakeTask(reason, w),
		SystemPrompt:   m.systemPrompt(),
		LaunchedBy:     m.Name(),
		MaxIterations:  m.opts.MaxIterations,
		Timeout:        m.opts.Timeout,
		IdleTimeout:    m.opts.IdleTimeout,
		MaxOutputBytes: m.opts.MaxOutputBytes,
	})

	class, summary := m.classify(ctx, res, err)
	policy := backoffOrDefault(m.opts.Backoff)
	m.level, m.ticks = policy.Advance(m.level, m.ticks, class, reactive)
	delay := policy.Delay(m.level, class == ClassThoughtOnly)
	note := fmt.Sprintf("%s → 下次唤醒 %v 后（level %d，耗时 %s）",
		summary, delay, m.level, time.Since(start).Round(time.Second))
	return Outcome{WantWake: true, NextWakeIn: delay, Note: note}
}

// classify 是工作探测（work probe）：FINAL=IDLE 或运行失败 →
// ClassIdle；非 IDLE 的 final 是 monolith 的持久结论——作为
// action 步骤落盘（"做过什么"是日志里的事实，也是探针的证据）并
// 归类 ClassWork。
func (m *monolith) classify(ctx context.Context, res runner.Result, err error) (WakeClass, string) {
	if err != nil {
		return ClassIdle, fmt.Sprintf("运行异常: %v", err)
	}
	final := strings.TrimSpace(res.Final)
	if strings.EqualFold(final, "IDLE") {
		return ClassIdle, "无事可做"
	}
	s := traj.NewStep("action")
	s.Fields["run_id"] = res.RunID
	s.Fields["launched_by"] = m.Name()
	s.Fields["content"] = final
	// WithoutCancel：结论落盘是审计事实，Ctrl+C 之后也必须落——
	// 用已取消的 ctx 会让 Append 必败，失败详情也不该被吞掉。
	if aerr := m.opts.Timeline.Append(context.WithoutCancel(ctx), s); aerr != nil {
		return ClassThoughtOnly, fmt.Sprintf("结论落盘失败: %v", aerr)
	}
	return ClassWork, fmt.Sprintf("完成一步（%d 轮）: %s", res.Iterations, traj.OneLine(final, 80))
}

// systemPrompt 组装人格与循环协议。人格在前——它是"你是谁"，
// 协议是"你怎么做"。
func (m *monolith) systemPrompt() string {
	if m.opts.Persona == "" {
		return MonolithSystemPrompt
	}
	return m.opts.Persona + "\n\n---\n\n" + MonolithSystemPrompt
}

// wakeTask 构造唤醒任务：唤醒原因 + 人生分集（粗层，recap 缓存）
// + 相关记忆（BM25 检索近期思维流对记忆库的关联）+ 自组合提示
// （agent 用同一套 CLI 写自己的记忆——工具同时是它的和人的）。
func (m *monolith) wakeTask(reason string, w Wake) string {
	var b strings.Builder
	fmt.Fprintf(&b, "你在 %s 被唤醒（原因: %s）。回顾下方的近期思维流，决定并执行下一步。无事可做时 FINAL=\"IDLE\"。",
		traj.NowString(), reason)
	if m.opts.EnableRecap {
		if life, err := recap.RenderLife(m.opts.Timeline.Dir, 20); err == nil && life != "" {
			b.WriteString("\n\n" + life)
		}
	}
	if m.opts.MemDir != "" {
		if related := m.relatedMemories(w); len(related) > 0 {
			b.WriteString("\n\n相关记忆：")
			for _, r := range related {
				b.WriteString("\n- " + r)
			}
		}
		b.WriteString("\n\n持久化重要事实：在 bash 里运行 \"$MINDLOOP_EXE\" mem add --type fact \"内容\"（见 mindloop mem --help）。")
	}
	return b.String()
}

// relatedMemories 用近期思维流的文本作为查询，检索记忆库。
func (m *monolith) relatedMemories(w Wake) []string {
	store := mem.Store{Dir: m.opts.MemDir}
	steps, err := m.opts.Timeline.Steps()
	if err != nil {
		return nil
	}
	var query strings.Builder
	n := 0
	for i := len(steps) - 1; i >= 0 && n < 6; i-- {
		if c, ok := steps[i].Field("content"); ok && c != "" {
			query.WriteString(c)
			query.WriteString("\n")
			n++
		}
	}
	if w.Step.Type == "message" {
		if c, ok := w.Step.Field("content"); ok {
			query.WriteString(c)
		}
	}
	hits, err := store.Search(query.String(), 3)
	if err != nil {
		return nil
	}
	var out []string
	for _, h := range hits {
		out = append(out, fmt.Sprintf("[%s] %s", h.Type, h.Summary))
	}
	return out
}

func backoffOrDefault(p *BackoffPolicy) BackoffPolicy {
	if p == nil {
		return BackoffPolicy{}.normalized()
	}
	return p.normalized()
}

func oneLine(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", "\\n")
	r := []rune(s)
	if len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}
