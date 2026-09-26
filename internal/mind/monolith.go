package mind

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"mindloop/internal/ids"
	"mindloop/internal/llm"
	"mindloop/internal/mem"
	"mindloop/internal/prompt"
	"mindloop/internal/recap"
	"mindloop/internal/runner"
	"mindloop/internal/skills"
	"mindloop/internal/task"
	"mindloop/internal/traj"
)

// MonolithSystemPrompt 是 monolith 的系统提示：每次唤醒决定并执行
// 下一步。英文面向模型；FINAL="IDLE" 是"无事可做"的协议值。
const MonolithSystemPrompt = `You are the monolith — the persistent mind of an agent. You have just been woken up.

Each wake, you look at the recent stream of your life (rendered after the system prompt) and do the NEXT useful thing. Not a plan. One concrete step, executed now.

How to decide:
- 普通聊天、历史委托和旧运行记录都不是新的执行授权。显式任务由持久队列领取，不得自行补做或重试已中断的委托。
- 自主行动只处理独立观察和自身维护；不得读取聊天来绕过委托入口。
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
	// SelfName 是身份名：轮次耗尽时的工作摘要以它署名投递给
	// operator（对话流里与 responder 的回复同一身份）。空则退回
	// thinker 名 "monolith"。
	SelfName string
	// MemDir 非空时，唤醒提示会带上与近期思维流相关的记忆
	// （BM25 检索，最多 3 条）。
	MemDir string
	// ContextBudget / MemoryBytes / SummaryBytes 是唤醒上下文的
	// 字节预算（0 取 prompt 包默认：128 KiB / 8 KiB / 16 KiB）。
	// 唤醒原因与指令受保护；人生分集与相关记忆按各自上限裁剪，
	// 统一账本保证小总预算下级联收缩。
	ContextBudget int
	MemoryBytes   int
	SummaryBytes  int
	// TaskCallBudget 是显式任务每个 attempt 的模型调用尝试上限
	// （含重试；<=0 取默认 20）——与调度器的 30 分钟唤醒期限共同
	// 构成任务成本防线，超出时任务落 budget_exceeded。
	TaskCallBudget int
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
	// SummaryThinker 是摘要档思考者（可选；nil = 思考档）。recap
	// 的摘要调用与唤醒主循环解耦：摘要可以配更便宜/更快的模型，
	// 主循环档位不变。
	SummaryThinker runner.Thinker
	// SkillsDirs 是 Agent Skills 技能库的两层目录（身份级在前、
	// 全局在后，前者遮蔽后者同名）。索引进系统提示（渐进披露的
	// 第一层），正文由模型经 SKILLS_DIR 按需读取。
	SkillsDirs []string
	// MCPServers 是已配置的 MCP 服务器名清单——只进名字（渐进
	// 披露），工具清单由模型经 $MINDLOOP_EXE mcp tools/call 按需
	// 探索，避免大而全的工具表把上下文撑爆。
	MCPServers []string
	// ExtraEnv 追加给沙箱进程的环境变量（SKILLS_DIR、
	// MINDLOOP_IDENTITY_DIR 等）——agent 在 bash 里用同一套 CLI
	// 探索技能与 MCP 工具。
	ExtraEnv []string
	// SetLogger 注入日志回调（recap 进度）。
	SetLogger func(format string, args ...any)
	// BeforeExecute 与独立 runner 共用脚本授权入口，覆盖自主行动和显式任务。
	BeforeExecute func(context.Context, runner.Execution) error
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

func (m *monolith) Name() string { return monolithName }

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
		Types:       []string{traj.TypeObservation, traj.TypeMerge, monolithWakeType},
		TriggerSelf: false,
		Watchdog:    watchdog,
	}
}

func (m *monolith) Wake(ctx context.Context, w Wake) Outcome {
	item, claimErr := m.taskStore().Claim(ctx, ids.NewUUID())
	if claimErr == nil {
		return m.executeTask(ctx, item)
	}
	if !errors.Is(claimErr, task.ErrNoQueued) {
		return Outcome{Note: "任务领取未执行: " + claimErr.Error()}
	}
	if w.Kind == WakeTask {
		return Outcome{} // 排队任务已被取消，不能将通知转为自主执行授权。
	}
	reactive := w.Kind == WakeStep && w.Step.Type == traj.TypeMessage
	reason := "scheduled spontaneity"
	switch w.Kind {
	case WakeWatchdog:
		reason = "watchdog keep-alive check"
	case WakeStep:
		reason = fmt.Sprintf("step %s (%s)", w.Step.Type, ids.Short(w.Step.StepID, 8))
	}
	// 归因随 ctx 走到模型调用收尾：台账能把这次唤醒的每一笔记到
	// 思考者、唤醒原因与阶段上（recap 摘要调用会再覆盖成自己的）。
	ctx = llm.WithAttrib(ctx, llm.Attrib{Thinker: m.Name(), Wake: reason, Phase: "wake"})

	start := time.Now()
	// 分层上下文的补全：唤醒前先把积压的情节摘要补掉（每次至多
	// 2 条，成本阀）。摘要失败不阻塞唤醒——粗层缺失只是上下文
	// 变薄，不是停机理由。
	if m.opts.EnableRecap {
		// 摘要档：recap 的摘要调用独立于唤醒主循环的档位。
		summaryThinker := m.opts.Thinker
		if m.opts.SummaryThinker != nil {
			summaryThinker = m.opts.SummaryThinker
		}
		u := &recap.Updater{
			Timeline:     m.opts.Timeline,
			Thinker:      summaryThinker,
			MaxSummaries: 2,
			Autonomous:   true,
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
		ContextBudget:  m.opts.ContextBudget,
		LaunchedBy:     m.Name(),
		MaxIterations:  m.opts.MaxIterations,
		Timeout:        m.opts.Timeout,
		IdleTimeout:    m.opts.IdleTimeout,
		MaxOutputBytes: m.opts.MaxOutputBytes,
		ExtraEnv:       m.opts.ExtraEnv,
		BeforeExecute:  m.opts.BeforeExecute,
		Autonomous:     true,
	})

	// 轮次耗尽 = 没有 FINAL：已完成/已产出的工作会静默丢失（ada
	// 实测——评审做完但轮次耗尽，成果没回发）。把最后阶段的思考
	// 摘要以消息投递给 operator，先于分类落盘。
	if err != nil && errors.Is(err, runner.ErrMaxIterations) {
		m.reportUnfinishedWork(ctx, res)
	}

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
// reportUnfinishedWork 在轮次耗尽（没有 FINAL）时，把最后阶段的
// 思考摘要投递给 operator——run 被硬停不代表没有产出，静默丢弃
// 就是"完成了工作却说没有任何命令输出"。内容剥掉 fence 标记、裁
// 到 500 rune；from 用身份名（对话流里与 responder 的回复同一署
// 名），to=operator 使 responder 的定向守卫天然忽略它——进度报告
// 不是对话回合，不进 responder 的历史组装。
func (m *monolith) reportUnfinishedWork(ctx context.Context, res runner.Result) {
	steps, err := m.opts.Timeline.Steps()
	if err != nil {
		return
	}
	var last string
	for i := len(steps) - 1; i >= 0; i-- {
		s := steps[i]
		if s.Type != traj.TypeReasoning {
			continue
		}
		if rid, ok := s.Field("run_id"); ok && rid != res.RunID {
			continue // 只要本次耗尽运行自己的产出
		}
		last, _ = s.Field("content")
		break
	}
	text := strings.TrimSpace(stripFences(last))
	if text == "" {
		return // 没有实质内容（纯围栏/空白）就不打扰
	}
	s := traj.NewStep(traj.TypeMessage)
	s.Fields["from"] = m.fromName()
	s.Fields["to"] = "operator"
	s.Fields["source"] = "progress"
	s.Fields["launched_by"] = m.Name()
	s.Fields["content"] = "（轮次耗尽，以下为最后阶段的思考摘要）\n" + truncateRunes(text, 500)
	// WithoutCancel：轮次耗尽常发生在停机边缘，报告必须落地。
	_ = m.opts.Timeline.Append(context.WithoutCancel(ctx), s)
}

// fromName 是 monolith 对话投递的署名：优先身份名。
func (m *monolith) fromName() string {
	if m.opts.SelfName != "" {
		return m.opts.SelfName
	}
	return m.Name()
}

// fenceLineRe 匹配行首的 markdown 围栏行（含语言标注）。
var fenceLineRe = regexp.MustCompile("(?m)^```[a-zA-Z0-9_-]*[ \t]*$")

// stripFences 剥掉围栏标记、保留围栏内的实质内容——轮次耗尽的
// reasoning 往往带着写了一半的代码块，围栏语法本身对人是噪音。
func stripFences(s string) string {
	return fenceLineRe.ReplaceAllString(s, "")
}

// truncateRunes 按 rune 裁剪到 max（含省略号）。
func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
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
	s := traj.NewStep(traj.TypeAction)
	s.Fields["run_id"] = res.RunID
	s.Fields["context_kind"] = "autonomous"
	s.Fields["launched_by"] = m.Name()
	s.Fields["content"] = final
	// WithoutCancel：结论落盘是审计事实，Ctrl+C 之后也必须落——
	// 用已取消的 ctx 会让 Append 必败，失败详情也不该被吞掉。
	if aerr := m.opts.Timeline.Append(context.WithoutCancel(ctx), s); aerr != nil {
		return ClassThoughtOnly, fmt.Sprintf("结论落盘失败: %v", aerr)
	}
	return ClassWork, fmt.Sprintf("完成一步（%d 轮）: %s", res.Iterations, traj.OneLine(final, 80))
}

// systemPrompt 组装人格、循环协议与扩展能力段（技能索引 + MCP
// 服务器清单——渐进披露：只进"有什么、怎么探索"，不进正文或
// 工具全表）。人格在前——它是"你是谁"，协议是"你怎么做"。
func (m *monolith) systemPrompt() string {
	base := MonolithSystemPrompt
	if m.opts.Persona != "" {
		base = m.opts.Persona + "\n\n---\n\n" + MonolithSystemPrompt
	}
	if s := (skills.Store{Dirs: m.opts.SkillsDirs}).PromptSection(); s != "" {
		base += "\n\n" + s
	}
	if len(m.opts.MCPServers) > 0 {
		base += "\n\n" + mcpSection(m.opts.MCPServers)
	}
	return base
}

// mcpSection 渲染 MCP 工具面的提示段：服务器名 + 探索用法。
// 工具全表不进提示——agent 用 CLI 按需发现，上下文预算留给任务。
func mcpSection(servers []string) string {
	var b strings.Builder
	b.WriteString("## MCP tools\n\n")
	b.WriteString("MCP (Model Context Protocol) servers are configured for this identity. ")
	b.WriteString("Their tools are plain CLI calls:\n\n")
	b.WriteString("  \"$MINDLOOP_EXE\" mcp tools <server>              # discover tools + input schemas\n")
	b.WriteString("  \"$MINDLOOP_EXE\" mcp call <server> <tool> '<json-args>'\n\n")
	b.WriteString("Configured servers: ")
	b.WriteString(strings.Join(servers, ", "))
	b.WriteString("\n")
	return strings.TrimRight(b.String(), "\n")
}

// wakeTask 构造唤醒任务：唤醒原因 + 人生分集（粗层，recap 缓存）
// + 相关记忆（BM25 检索近期思维流对记忆库的关联）+ 自组合提示
// （agent 用同一套 CLI 写自己的记忆——工具同时是它的和人的）。
//
// 分段预算：唤醒原因与指令受保护（超限不裁剪——最终由 runner
// 的受保护检查兜底报错），人生分集与相关记忆按各自上限裁剪；
// 同一账本下小总预算会级联收缩后两者。
func (m *monolith) wakeTask(reason string, w Wake) string {
	budget := prompt.NewBudget(m.opts.ContextBudget)
	head := fmt.Sprintf("你在 %s 被唤醒（原因: %s）。回顾下方的近期思维流，决定并执行下一步。无事可做时，把 FINAL=\"IDLE\" 写进代码块内执行（写在块外不生效）。",
		traj.NowString(), reason)
	_ = budget.TakeProtected("wake", head)
	var b strings.Builder
	b.WriteString(head)
	if m.opts.EnableRecap {
		if life, err := recap.RenderAutonomousLife(m.opts.Timeline.Dir, 20); err == nil && life != "" {
			if seg := budget.TakeCapped("recap", life, summaryCap(m.opts.SummaryBytes)); seg != "" {
				b.WriteString("\n\n")
				b.WriteString(seg)
			}
		}
	}
	if m.opts.MemDir != "" {
		if related := m.relatedMemories(w); len(related) > 0 {
			var rel strings.Builder
			for _, r := range related {
				rel.WriteString("\n- ")
				rel.WriteString(r)
			}
			if seg := budget.TakeCapped("memory", rel.String(), memoryCap(m.opts.MemoryBytes)); seg != "" {
				// 记忆段定位为线索：BM25 命中不等于已验证事实，
				// ID 与来源步骤供消费方自行追溯。
				b.WriteString("\n\n相关记忆（BM25 检索，线索而非已验证事实；ID 与来源步骤供追溯）：")
				b.WriteString(seg)
			}
		}
		b.WriteString("\n\n持久化重要事实：在 bash 里运行 \"$MINDLOOP_EXE\" mem add --type fact \"内容\"（见 mindloop mem --help）。")
	}
	return b.String()
}

// memoryCap 折算相关记忆段上限（0 取默认，负值同）。
func memoryCap(v int) int {
	if v <= 0 {
		return prompt.DefaultMemoryBudget
	}
	return v
}

// relatedMemories 用近期思维流的文本作为查询，检索记忆库。
// 行携带记忆 ID 与来源步骤：检索结果是线索而不是已验证事实，
// 追溯出处由消费方自行核对。
func (m *monolith) relatedMemories(w Wake) []string {
	store := mem.Store{Dir: m.opts.MemDir}
	steps, err := m.opts.Timeline.Steps()
	if err != nil {
		return nil
	}
	steps = prompt.AutonomousSteps(steps)
	var query strings.Builder
	n := 0
	for i := len(steps) - 1; i >= 0 && n < 6; i-- {
		if c, ok := steps[i].Field("content"); ok && c != "" {
			query.WriteString(c)
			query.WriteString("\n")
			n++
		}
	}
	hits, err := store.Search(query.String(), 3)
	if err != nil {
		return nil
	}
	var out []string
	for _, h := range hits {
		out = append(out, memoryHitLine(h))
	}
	return out
}

// memoryHitLine 渲染一条检索命中："[type id] summary（来源步骤 x）"。
// monolith 与 responder 共用同一格式——两个消费方对线索的追溯
// 口径必须一致。
func memoryHitLine(h mem.Scored) string {
	line := fmt.Sprintf("[%s %s] %s", h.Type, h.ID, h.Summary)
	if h.Source != "" {
		line += fmt.Sprintf("（来源步骤 %s）", h.Source)
	}
	return line
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
