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
	AssistantTypes: []string{"final", "reasoning"},
	ExcludeFields:  []string{"exit_code", "exec_ms", "killed", "workdir", "task"},
}

// Options 配置一次运行。零值字段取默认。
type Options struct {
	// Timeline 是运行所在的轨迹（通常刚由 traj new 创建）。
	Timeline *traj.Timeline
	// Thinker 是模型调用方。
	Thinker Thinker
	// Task 是本轮任务的描述，作为 prompt 步骤落盘。
	Task string
	// SystemPrompt 覆盖默认系统提示（测试用）。
	SystemPrompt string
	// WorkDir 是脚本工作目录；默认 <轨迹目录>/runs/<runid8>。
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
}

// Result 是一次成功完成的运行。
type Result struct {
	Final      string
	Iterations int
	RunID      string
	WorkDir    string
}

// finalFileName 是 FINAL 哨兵文件的固定名字。
const finalFileName = ".mindloop_final"

// Run 执行循环直到 FINAL、失速或轮次耗尽。
func Run(ctx context.Context, opts Options) (Result, error) {
	if opts.Timeline == nil {
		return Result{}, errors.New("runner: 缺少 Timeline")
	}
	if opts.Thinker == nil {
		return Result{}, errors.New("runner: 缺少 Thinker")
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
	logf := opts.Progress

	// 运行头：run 步骤的 step_id 就是本次运行的 id——所有后续
	// 步骤盖 run_id 章（Headlong 的 run_id 溯源规则）。
	header := traj.NewStep("run")
	taskBrief := opts.Task
	if r := []rune(taskBrief); len(r) > 200 {
		taskBrief = string(r[:200]) + "…"
	}
	header.Fields["task"] = taskBrief
	if opts.LaunchedBy != "" {
		header.Fields["launched_by"] = opts.LaunchedBy
	}
	runID := header.StepID
	if err := opts.Timeline.Append(ctx, header); err != nil {
		return Result{}, fmt.Errorf("runner: 写运行头: %w", err)
	}

	// 工作目录随轨迹走：产物与日志同处一个目录树。
	workDir := opts.WorkDir
	if workDir == "" {
		workDir = filepath.Join(opts.Timeline.Dir, "runs", runID[:8])
	}
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return Result{}, fmt.Errorf("runner: 建工作目录: %w", err)
	}

	promptStep := traj.NewStep("prompt")
	promptStep.Fields["run_id"] = runID
	if opts.LaunchedBy != "" {
		promptStep.Fields["launched_by"] = opts.LaunchedBy
	}
	promptStep.Fields["content"] = opts.Task
	if err := opts.Timeline.Append(ctx, promptStep); err != nil {
		return Result{}, fmt.Errorf("runner: 写任务: %w", err)
	}

	finalPath := filepath.Join(workDir, finalFileName)
	// MINDLOOP_EXE 让沙箱里的脚本能调用本 CLI（mem add 写记忆、
	// traj append 落步骤）——agent 与人用同一套工具，这是组合性
	// 的落点。os.Executable 失败时省略该变量。
	exeEnv := []string{"MINDLOOP_RUN_ID=" + runID, "MINDLOOP_WORKDIR=" + workDir}
	if exe, err := os.Executable(); err == nil {
		exeEnv = append(exeEnv, "MINDLOOP_EXE="+exe)
	}
	// appendStep 落盘带 run_id 章的步骤。ctx 用 WithoutCancel：
	// 思考失败/失速/轮次耗尽这些终局审计步骤恰恰发生在取消之后，
	// 用已取消的 ctx 会让 Append 必败，关键事实永远落不了盘。
	// 写失败不静默——有 logf 就喊出来。
	appendStep := func(typ, content string, extra map[string]any) error {
		s := traj.NewStep(typ)
		s.Fields["run_id"] = runID
		if opts.LaunchedBy != "" {
			s.Fields["launched_by"] = opts.LaunchedBy
		}
		if content != "" {
			s.Fields["content"] = content
		}
		for k, v := range extra {
			s.Fields[k] = v
		}
		err := opts.Timeline.Append(context.WithoutCancel(ctx), s)
		if err != nil && logf != nil {
			logf("runner: 步骤 %q 落盘失败: %v", typ, err)
		}
		return err
	}

	consecutiveFails := 0
	lastFailCmd, lastFailExit := "", 0

	for iteration := 1; iteration <= opts.MaxIterations; iteration++ {
		if logf != nil {
			logf("── 第 %d/%d 轮 ──", iteration, opts.MaxIterations)
		}

		// 上下文每轮从日志重渲：日志是唯一事实源。
		steps, err := opts.Timeline.Steps()
		if err != nil {
			return Result{RunID: runID, WorkDir: workDir}, fmt.Errorf("runner: 读轨迹: %w", err)
		}
		msgs := make([]llm.Message, 0, len(steps))
		for _, m := range prompt.Render(steps, renderOptions) {
			msgs = append(msgs, llm.Message{Role: string(m.Role), Content: m.Content})
		}

		text, err := opts.Thinker.Think(ctx, opts.SystemPrompt, msgs)
		if err != nil {
			_ = appendStep("error", "思考失败: "+err.Error(), nil)
			return Result{RunID: runID, WorkDir: workDir}, fmt.Errorf("runner: 思考: %w", err)
		}
		if err := appendStep("reasoning", text, nil); err != nil {
			return Result{RunID: runID, WorkDir: workDir}, fmt.Errorf("runner: 写推理: %w", err)
		}

		ext := extractCode(text)
		if ext.Notice != "" && logf != nil {
			logf("提示模型: %s", ext.Notice)
		}

		// 上一次的哨兵必须清掉：FINAL 属于"本轮是否完成"，不清理
		// 会把上一轮的完成泄漏到这一轮。
		_ = os.Remove(finalPath)

		res, err := sandbox.Run(ctx, sandbox.Request{
			Script:         ext.Code,
			Dir:            workDir,
			FinalPath:      finalPath,
			Env:            exeEnv,
			Timeout:        opts.Timeout,
			IdleTimeout:    opts.IdleTimeout,
			MaxOutputBytes: opts.MaxOutputBytes,
			MemLimitBytes:  opts.MemLimitBytes,
		})
		if err != nil {
			_ = appendStep("error", "执行失败: "+err.Error(), nil)
			return Result{RunID: runID, WorkDir: workDir}, fmt.Errorf("runner: 执行: %w", err)
		}

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
		extra := map[string]any{
			"exit_code": res.ExitCode,
			"exec_ms":   res.Duration.Milliseconds(),
		}
		if res.KillReason != "" {
			extra["killed"] = res.KillReason
			out.WriteString(fmt.Sprintf("\n〔执行被终止: %s〕", res.KillReason))
		}
		if err := appendStep("shell-output", strings.TrimSpace(out.String()), extra); err != nil {
			return Result{RunID: runID, WorkDir: workDir}, fmt.Errorf("runner: 写输出: %w", err)
		}
		if logf != nil {
			logf("exit=%d %s 输出 %d 字节", res.ExitCode, res.Duration.Round(time.Millisecond), len(res.Stdout))
		}

		// FINAL 副作用检测：文件在，任务就完成了。
		if res.FinalSet {
			data, err := os.ReadFile(finalPath)
			if err != nil {
				return Result{RunID: runID, WorkDir: workDir}, fmt.Errorf("runner: 读 FINAL: %w", err)
			}
			final := strings.TrimSpace(string(data))
			if err := appendStep("final", final, nil); err != nil {
				return Result{RunID: runID, WorkDir: workDir}, fmt.Errorf("runner: 写 FINAL: %w", err)
			}
			return Result{Final: final, Iterations: iteration, RunID: runID, WorkDir: workDir}, nil
		}

		// 失速守卫：连续失败计数；同一命令以同一退出码失败两次
		// 直接判死——模型在原地打转（Headlong 的 repeat guard）。
		failed := res.ExitCode != 0 || res.KillReason != ""
		if failed {
			consecutiveFails++
			if ext.Code == lastFailCmd && res.ExitCode == lastFailExit {
				_ = appendStep("error", fmt.Sprintf("同一命令连续两次失败（exit %d），中止。", res.ExitCode), nil)
				return Result{RunID: runID, WorkDir: workDir}, ErrStalled
			}
			if consecutiveFails >= opts.StallLimit {
				_ = appendStep("error", fmt.Sprintf("连续 %d 次失败，中止。", consecutiveFails), nil)
				return Result{RunID: runID, WorkDir: workDir}, ErrStalled
			}
			lastFailCmd, lastFailExit = ext.Code, res.ExitCode
		} else {
			consecutiveFails = 0
			lastFailCmd, lastFailExit = "", 0
		}
	}

	_ = appendStep("error", fmt.Sprintf("轮次耗尽（%d）。", opts.MaxIterations), nil)
	return Result{RunID: runID, WorkDir: workDir}, ErrMaxIterations
}
