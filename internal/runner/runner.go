// Package runner 是持久心智的单次运行循环——shellm 的 run_loop 的
// Go 对应物，但只保留骨架：渲染上下文 → 思考 → 提取代码 → 沙箱
// 执行 → 记录 → 检查 FINAL。每一步都作为步骤落进轨迹日志，日志
// 是唯一事实源；上下文每轮从日志重渲，绝不在内存里私藏一份真相。
//
// 模型调用通过 Thinker 接口注入：internal/llm 提供真实现，测试
// 提供脚本化的假实现——循环的验证不依赖任何网络。
package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mindloop/internal/llm"
	"mindloop/internal/prompt"
	"mindloop/internal/sandbox"
	"mindloop/internal/traj"
)

// 哨兵错误。
var (
	// ErrStalled：连续失败达到 StallLimit，主动中止。
	ErrStalled = errors.New("runner: 连续失败达到上限，中止运行")
	// ErrMaxIterations：轮次耗尽仍未完成。
	ErrMaxIterations = errors.New("runner: 轮次耗尽仍未完成")
)

// Thinker 是运行循环对"下一轮思考"的抽象。
type Thinker interface {
	// Think 接收系统提示与已渲染的对话，返回模型原始文本。
	Think(ctx context.Context, system string, msgs []llm.Message) (string, error)
}

// 默认渲染参数：与 prompt 包的 Defaults 一致，另剔除执行元数据
// 字段——Headlong 的模型曾学会模仿自己的 token 元数据并写回上百次。
var renderOptions = prompt.Options{
	Tail:           40,
	Block:          10,
	AssistantTypes: []string{traj.TypeFinal, traj.TypeReasoning},
	ExcludeFields:  []string{"exit_code", "exec_ms", "killed", "workdir", "task"},
}

// Options 配置一次运行。零值字段取默认。
type Options struct {
	// 显式任务使用领取时预分配的 RunID；普通运行仍自行生成。
	RunID   string
	TaskID  string
	Attempt int
	// BeforeExecute 是所有脚本启动前的统一授权入口，等待期间不调用模型。
	BeforeExecute func(context.Context, Execution) error
	// Autonomous 排除聊天、委托以及没有来源标记的旧运行上下文。
	Autonomous bool
	// Timeline 是运行所在的轨迹（通常刚由 traj new 创建）。
	Timeline *traj.Timeline
	// Thinker 是模型调用方。
	Thinker Thinker
	// Task 是本轮任务的描述，作为 prompt 步骤落盘。
	Task string
	// SystemPrompt 覆盖默认系统提示（测试用）。
	SystemPrompt string
	// ContextBudget 是单次请求的输入文本字节预算（0 取
	// prompt.DefaultContextBudget）——输入防线，不等价于模型
	// token 窗口；受保护内容（系统提示 + 完整任务）超限时
	// Run 明确报错，不静默截断。
	ContextBudget int
	// WorkDir 是脚本工作目录；默认 <轨迹目录>/runs/<完整 runID>。
	WorkDir string
	// LaunchedBy 盖在每个写入步骤的 launched_by 字段上——调度器
	// 据此区分"思考者自己的产物"与"外部事件"，防自触发回路
	// （Headlong 的 trigger_self 语义）。
	LaunchedBy string
	// MaxIterations 是轮次上限（默认 12）。
	MaxIterations int
	// StallLimit 是连续失败上限（默认 3）。
	StallLimit int
	// Timeout / IdleTimeout / MaxOutputBytes / MemLimitBytes 透传
	// 给 sandbox。
	Timeout        time.Duration
	IdleTimeout    time.Duration
	MaxOutputBytes int64
	MemLimitBytes  uint64
	// Progress 是进度回调（人看的信息，进 stderr 而不是日志）。
	Progress func(format string, args ...any)
	// ExtraEnv 是追加给沙箱进程的 K=V 环境变量（SKILLS_DIR、
	// MINDLOOP_IDENTITY_DIR 等扩展面）——agent 在沙箱里用同一套
	// CLI 探索身份的扩展能力。
	ExtraEnv []string
}

// Result 是一次成功完成的运行。
type Result struct {
	FinalKind       string
	EvidenceStepIDs []string
	Final           string
	Iterations      int
	RunID           string
	WorkDir         string
}

// Execution 是待授权的具体脚本及其执行归属，不包含环境凭据。
type Execution struct {
	Script  string
	WorkDir string
	RunID   string
	TaskID  string
	Attempt int
}

// finalFileName 是 FINAL 哨兵文件的固定名字。
const finalFileName = ".mindloop_final"

// run 承载一次运行循环的会话状态。Run 的五类职责（建头/渲染/
// 思考/执行/记录）拆到各自的小方法里，状态经接收者传递而非闭包
// 捕获——每个方法都可独立读懂，主循环只剩骨架。
type run struct {
	opts Options
	logf func(format string, args ...any)

	runID     string
	workDir   string
	finalPath string
	exeEnv    []string

	evidence         []string
	consecutiveFails int
	lastFailCmd      string
	lastFailExit     int
	budget           *prompt.Budget
}

// Run 执行循环直到 FINAL、失速或轮次耗尽。
func Run(ctx context.Context, opts Options) (Result, error) {
	if opts.Timeline == nil {
		return Result{}, errors.New("runner: 缺少 Timeline")
	}
	if opts.Thinker == nil {
		return Result{}, errors.New("runner: 缺少 Thinker")
	}
	if (opts.TaskID != "" && (opts.Attempt < 1 || opts.RunID == "")) || (opts.TaskID == "" && opts.Attempt != 0) || (opts.RunID != "" && !validRunID(opts.RunID)) {
		return Result{}, errors.New("runner: 无效的任务/运行关联")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if opts.SystemPrompt == "" {
		opts.SystemPrompt = SystemPrompt
	}
	if opts.MaxIterations <= 0 {
		opts.MaxIterations = 12
	}
	if opts.StallLimit <= 0 {
		opts.StallLimit = 3
	}
	// 受保护段（系统提示 + 完整任务）先登记：超限属于配置错误，
	// 在写任何步骤、调任何模型之前就明确报错。
	budget := prompt.NewBudget(opts.ContextBudget)
	if err := budget.TakeProtected("system", opts.SystemPrompt); err != nil {
		return Result{}, fmt.Errorf("runner: %w", err)
	}
	if err := budget.TakeProtected("task", opts.Task); err != nil {
		return Result{}, fmt.Errorf("runner: %w", err)
	}
	r := &run{opts: opts, logf: opts.Progress, budget: budget}
	return r.start(ctx)
}

// start 建运行头、工作目录与任务步骤，然后进入主循环。
// 运行头的 step_id 就是本次运行的 id——所有后续步骤盖 run_id 章
// （Headlong 的 run_id 溯源规则）。
func (r *run) start(ctx context.Context) (Result, error) {
	header := traj.NewStep(traj.TypeRun)
	if r.opts.RunID != "" {
		header.StepID = r.opts.RunID
	}
	r.runID = header.StepID
	r.correlate(&header)
	taskBrief := r.opts.Task
	if runes := []rune(taskBrief); len(runes) > 200 {
		taskBrief = string(runes[:200]) + "…"
	}
	header.Fields["task"] = taskBrief
	if r.opts.LaunchedBy != "" {
		header.Fields["launched_by"] = r.opts.LaunchedBy
	}
	if err := r.opts.Timeline.AppendWithOptions(ctx, header, traj.AppendOptions{Durable: r.opts.TaskID != ""}); err != nil {
		return Result{}, fmt.Errorf("runner: 写运行头: %w", err)
	}

	// 工作目录随轨迹走：产物与日志同处一个目录树。
	r.workDir = r.opts.WorkDir
	if r.workDir == "" {
		r.workDir = filepath.Join(r.opts.Timeline.Dir, "runs", r.runID)
	}
	if err := os.MkdirAll(r.workDir, 0o755); err != nil {
		return Result{}, fmt.Errorf("runner: 建工作目录: %w", err)
	}
	r.finalPath = filepath.Join(r.workDir, finalFileName)

	// MINDLOOP_EXE 让沙箱里的脚本能调用本 CLI（mem add 写记忆、
	// traj append 落步骤）——agent 与人用同一套工具，这是组合性
	// 的落点。os.Executable 失败时省略该变量。
	r.exeEnv = []string{"MINDLOOP_RUN_ID=" + r.runID, "MINDLOOP_WORKDIR=" + r.workDir}
	if exe, err := os.Executable(); err == nil {
		r.exeEnv = append(r.exeEnv, "MINDLOOP_EXE="+exe)
	}
	r.exeEnv = append(r.exeEnv, r.opts.ExtraEnv...)

	promptStep := traj.NewStep(traj.TypePrompt)
	r.correlate(&promptStep)
	promptStep.Fields["content"] = r.opts.Task
	if err := r.opts.Timeline.AppendWithOptions(ctx, promptStep, traj.AppendOptions{Durable: r.opts.TaskID != ""}); err != nil {
		return Result{}, fmt.Errorf("runner: 写任务: %w", err)
	}

	return r.loop(ctx)
}

// appendStep 落盘带 run_id 章的步骤。ctx 用 WithoutCancel：思考
// 失败/失速/轮次耗尽这些终局审计步骤恰恰发生在取消之后，用已取消
// 的 ctx 会让 Append 必败，关键事实永远落不了盘。写失败不静默——
// 有 logf 就喊出来。
func (r *run) appendStep(ctx context.Context, typ, content string, extra map[string]any) error {
	s := traj.NewStep(typ)
	r.correlate(&s)
	if content != "" {
		s.Fields["content"] = content
	}
	for k, v := range extra {
		s.Fields[k] = v
	}
	writeCtx := context.WithoutCancel(ctx)
	if typ == traj.TypeFinal {
		writeCtx = ctx
	}
	err := r.opts.Timeline.AppendWithOptions(writeCtx, s, traj.AppendOptions{Durable: r.opts.TaskID != ""})
	if err == nil && (typ == traj.TypeShellOutput || typ == traj.TypeFinal) {
		r.evidence = append(r.evidence, s.StepID)
	}
	if err != nil && r.logf != nil {
		r.logf("runner: 步骤 %q 落盘失败: %v", typ, err)
	}
	return err
}

// renderMessages 每轮从日志重渲上下文——日志是唯一事实源，绝不
// 在内存私藏一份真相。
func (r *run) renderMessages() ([]llm.Message, error) {
	steps, err := r.opts.Timeline.Steps()
	if err != nil {
		return nil, err
	}
	if r.opts.TaskID != "" {
		filtered := make([]traj.Step, 0, len(steps))
		for _, s := range steps {
			rid, _ := s.Field("run_id")
			tid, _ := s.Field("task_id")
			if rid == r.runID && tid == r.opts.TaskID && s.Type != traj.TypeMessage {
				filtered = append(filtered, s)
			}
		}
		steps = filtered
	} else if r.opts.Autonomous {
		steps = prompt.AutonomousSteps(steps)
	}
	// 历史（含旧记忆/摘要等低优先材料）吃剩余预算；预算已被受
	// 保护段用满时不带历史（MaxBytes=0 在 prompt 里表示不限，
	// 不能当"零预算"用）。
	opts := renderOptions
	if remain := r.budget.Remaining(); remain > 0 {
		opts.MaxBytes = remain
	} else {
		return []llm.Message{}, nil
	}
	msgs := make([]llm.Message, 0, len(steps))
	for _, m := range prompt.Render(steps, opts) {
		msgs = append(msgs, llm.Message{Role: string(m.Role), Content: m.Content})
	}
	return msgs, nil
}

// execute 清掉上一轮哨兵后，把本轮代码放进沙箱执行。哨兵属于
// "本轮是否完成"——不清理会把上一轮的完成泄漏到这一轮。
func (r *run) execute(ctx context.Context, code string) (sandbox.Result, error) {
	if err := ctx.Err(); err != nil {
		return sandbox.Result{}, err
	}
	if r.opts.BeforeExecute != nil {
		if err := r.opts.BeforeExecute(ctx, Execution{Script: code, WorkDir: r.workDir, RunID: r.runID, TaskID: r.opts.TaskID, Attempt: r.opts.Attempt}); err != nil {
			return sandbox.Result{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return sandbox.Result{}, err
	}
	_ = os.Remove(r.finalPath)
	return sandbox.Run(ctx, sandbox.Request{
		Script:         code,
		Dir:            r.workDir,
		FinalPath:      r.finalPath,
		Env:            r.exeEnv,
		Timeout:        r.opts.Timeout,
		IdleTimeout:    r.opts.IdleTimeout,
		MaxOutputBytes: r.opts.MaxOutputBytes,
		MemLimitBytes:  r.opts.MemLimitBytes,
	})
}

func validRunID(id string) bool {
	if len(id) == 0 || len(id) > 128 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z') && !(c >= 'A' && c <= 'Z') && !(c >= '0' && c <= '9') && c != '-' && c != '_' {
			return false
		}
	}
	return true
}

func (r *run) correlate(s *traj.Step) {
	s.Fields["run_id"] = r.runID
	if r.opts.Autonomous {
		s.Fields["context_kind"] = "autonomous"
	}
	if r.opts.LaunchedBy != "" {
		s.Fields["launched_by"] = r.opts.LaunchedBy
	}
	if r.opts.TaskID != "" {
		s.Fields["task_id"] = r.opts.TaskID
		s.Fields["attempt"] = r.opts.Attempt
	}
}

func (r *run) finish(ctx context.Context, final, kind string, iteration int) (Result, error) {
	if err := r.appendStep(ctx, traj.TypeFinal, final, map[string]any{"result_kind": kind}); err != nil {
		return Result{RunID: r.runID, WorkDir: r.workDir}, fmt.Errorf("runner: 写 FINAL: %w", err)
	}
	return Result{Final: final, FinalKind: kind, EvidenceStepIDs: r.evidence, Iterations: iteration, RunID: r.runID, WorkDir: r.workDir}, nil
}

// outputStepContent 把执行结果拼成 shell-output 步骤的正文与
// 附加字段（exit_code/exec_ms/killed）。
func outputStepContent(ext extraction, res sandbox.Result) (string, map[string]any) {
	var out strings.Builder
	if ext.Notice != "" {
		out.WriteString("〔" + ext.Notice + "〕\n\n")
	}
	if strings.TrimSpace(res.Stdout) != "" {
		out.WriteString(res.Stdout)
	}
	if strings.TrimSpace(res.Stderr) != "" {
		out.WriteString("\n[stderr]\n" + res.Stderr)
	}
	if res.KillReason != "" {
		out.WriteString(fmt.Sprintf("\n〔执行被终止: %s〕", res.KillReason))
	}
	extra := map[string]any{
		"exit_code": res.ExitCode,
		"exec_ms":   res.Duration.Milliseconds(),
	}
	if res.KillReason != "" {
		extra["killed"] = res.KillReason
	}
	return strings.TrimSpace(out.String()), extra
}

// loop 是主循环骨架：渲染 → 思考 → 提取 → 执行 → 记录 → FINAL
// 或失速判定。
func (r *run) loop(ctx context.Context) (Result, error) {
	// 归因随 ctx 传到模型调用收尾：台账把每一笔记到任务运行代次上
	// （自主运行 TaskID 为空，只带 run）。
	ctx = llm.WithAttrib(ctx, llm.Attrib{Task: r.opts.TaskID, Run: r.runID, Attempt: r.opts.Attempt})
	for iteration := 1; iteration <= r.opts.MaxIterations; iteration++ {
		if err := ctx.Err(); err != nil {
			return Result{RunID: r.runID, WorkDir: r.workDir}, err
		}
		if r.logf != nil {
			r.logf("── 第 %d/%d 轮 ──", iteration, r.opts.MaxIterations)
		}

		msgs, err := r.renderMessages()
		if err != nil {
			return Result{RunID: r.runID, WorkDir: r.workDir}, fmt.Errorf("runner: 读轨迹: %w", err)
		}

		text, err := r.opts.Thinker.Think(ctx, r.opts.SystemPrompt, msgs)
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		if err != nil {
			_ = r.appendStep(ctx, traj.TypeError, "思考失败: "+err.Error(), nil)
			return Result{RunID: r.runID, WorkDir: r.workDir}, fmt.Errorf("runner: 思考: %w", err)
		}
		if err := r.appendStep(ctx, traj.TypeReasoning, text, nil); err != nil {
			return Result{RunID: r.runID, WorkDir: r.workDir}, fmt.Errorf("runner: 写推理: %w", err)
		}

		ext := extractCode(text)
		if ext.Notice != "" && r.logf != nil {
			r.logf("提示模型: %s", ext.Notice)
		}

		// 裸文本 FINAL= 声明（回复里没有任何代码块）：模型清楚表达
		// 了"任务完成+答案"，按成功语义直接采为终局——不进沙箱、
		// 不教学回灌，成果原样落 final 步骤回发（实测事故见
		// extract.go bareFinal）。
		if ext.Final != "" {
			if r.logf != nil {
				r.logf("识别到裸文本 FINAL= 声明，直接采为终局")
			}
			return r.finish(ctx, ext.Final, "model-final", iteration)
		}

		res, err := r.execute(ctx, ext.Code)
		if err != nil {
			_ = r.appendStep(ctx, traj.TypeError, "执行失败: "+err.Error(), nil)
			return Result{RunID: r.runID, WorkDir: r.workDir}, fmt.Errorf("runner: 执行: %w", err)
		}

		content, extra := outputStepContent(ext, res)
		if err := r.appendStep(ctx, traj.TypeShellOutput, content, extra); err != nil {
			return Result{RunID: r.runID, WorkDir: r.workDir}, fmt.Errorf("runner: 写输出: %w", err)
		}
		if r.logf != nil {
			r.logf("exit=%d %s 输出 %d 字节", res.ExitCode, res.Duration.Round(time.Millisecond), len(res.Stdout))
		}

		if err := ctx.Err(); err != nil {
			return Result{RunID: r.runID, WorkDir: r.workDir}, err
		}
		// FINAL 副作用检测：文件在，任务就完成了。
		if res.FinalSet {
			data, err := os.ReadFile(r.finalPath)
			if err != nil {
				return Result{RunID: r.runID, WorkDir: r.workDir}, fmt.Errorf("runner: 读 FINAL: %w", err)
			}
			final := strings.TrimSpace(string(data))
			return r.finish(ctx, final, "shell-final", iteration)
		}

		// 失速守卫：连续失败计数；同一命令以同一退出码失败两次
		// 直接判死——模型在原地打转（Headlong 的 repeat guard）。
		if failed := res.ExitCode != 0 || res.KillReason != ""; failed {
			r.consecutiveFails++
			if ext.Code == r.lastFailCmd && res.ExitCode == r.lastFailExit {
				_ = r.appendStep(ctx, traj.TypeError, fmt.Sprintf("同一命令连续两次失败（exit %d），中止。", res.ExitCode), nil)
				return Result{RunID: r.runID, WorkDir: r.workDir}, ErrStalled
			}
			if r.consecutiveFails >= r.opts.StallLimit {
				_ = r.appendStep(ctx, traj.TypeError, fmt.Sprintf("连续 %d 次失败，中止。", r.consecutiveFails), nil)
				return Result{RunID: r.runID, WorkDir: r.workDir}, ErrStalled
			}
			r.lastFailCmd, r.lastFailExit = ext.Code, res.ExitCode
		} else {
			r.consecutiveFails = 0
			r.lastFailCmd, r.lastFailExit = "", 0
		}
	}

	_ = r.appendStep(ctx, traj.TypeError, fmt.Sprintf("轮次耗尽（%d）。", r.opts.MaxIterations), nil)
	return Result{RunID: r.runID, WorkDir: r.workDir}, ErrMaxIterations
}
