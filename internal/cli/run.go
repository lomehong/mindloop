package cli

import (
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"mindloop/internal/llm"
	"mindloop/internal/mind"
	"mindloop/internal/obs"
	"mindloop/internal/policy"
	"mindloop/internal/runner"
	"mindloop/internal/sandbox"
	"mindloop/internal/traj"
)

// errStalledExit / errMaxIterExit：运行的真实结局——先自行输出
// 诊断，再以退出码 3 结束。
func (c *CLI) runFinishedWithOutcome(res runner.Result, t *traj.Timeline, err error) error {
	fmt.Fprintf(c.stderr, "mindloop: %v（运行记录见 %s）\n", err, t.Path)
	if res.WorkDir != "" {
		fmt.Fprintf(c.stderr, "mindloop: 工作目录 %s 保留现场\n", res.WorkDir)
	}
	return exitError{code: 3}
}

func (c *CLI) newRunCmd() *cobra.Command {
	var maxIterP, stallP int
	var timeoutP, idleP time.Duration
	var maxOutP int64
	var workdirP string
	cmd := &cobra.Command{
		Use:   `run <traj> "任务"`,
		Short: "单次运行循环：模型写 bash → 沙箱执行 → 记录 → FINAL",
		Long: `单次任务运行。最终答案写 stdout，过程进度写 stderr，
每一步（prompt/reasoning/shell-output/final）都作为步骤落进轨迹。

退出码：0 完成；3 失速或轮次耗尽（运行的真实结局）。

执行授权（MINDLOOP_EXEC_POLICY=ask|trusted|deny）：
  ask（缺省）：脚本执行前展示正文、工作目录与归属，并等待批准
  （mindloop approve <traj> <hash> 批准，加 --deny 拒绝）；
  trusted：直接执行；deny：禁止脚本执行（模型仍可思考）。
  注意：trusted 是"启动授权与工作目录约定"，不是操作系统访问隔离——
  bash 仍以当前用户权限运行，可能访问本用户可访问的文件与网络。`,
		Example: `  mindloop run <id> "统计本目录下 go 文件的总行数"
  mindloop run <id> "清点当前目录" --max-iterations 4`,
		Args: exactArgs(2, `用法: mindloop run <traj> "任务" [flags]`),
		RunE: func(cmd *cobra.Command, args []string) error {
			t, err := traj.Load(args[0])
			if err != nil {
				return c.fail(err)
			}
			// 统一执行授权：CLI run 与心智场景共用 policy.Gate
			// （runner.BeforeExecute 的唯一实现面）。
			gate := policy.NewGate(policy.Dir(t.Dir), policy.ModeFromEnv())
			gate.Logger = func(format string, args ...any) {
				fmt.Fprintf(c.stderr, format+"\n", args...)
			}
			client, err := llm.FromEnv()
			if err != nil {
				return c.fail(err)
			}
			client.OnDone = obs.UsageRecorder(t.Dir, client.Model, client.Provider, func(f string, a ...any) {
				fmt.Fprintf(c.stderr, "· "+f+"\n", a...)
			})
			fmt.Fprintf(c.stderr, "供应商=%s 模型=%s\n", client.Provider, client.Model)
			res, err := runner.Run(c.ctx, runner.Options{
				Timeline:       t,
				Thinker:        mind.LLMThinker{Client: client},
				Task:           args[1],
				MaxIterations:  maxIterP,
				StallLimit:     stallP,
				Timeout:        timeoutP,
				IdleTimeout:    idleP,
				MaxOutputBytes: maxOutP,
				WorkDir:        workdirP,
				BeforeExecute:  gate.Authorize,
				Progress: func(format string, args ...any) {
					fmt.Fprintf(c.stderr, format+"\n", args...)
				},
			})
			if err != nil {
				if errors.Is(err, runner.ErrStalled) || errors.Is(err, runner.ErrMaxIterations) {
					return c.runFinishedWithOutcome(res, t, err)
				}
				return c.fail(err)
			}
			fmt.Fprintf(c.stderr, "完成：%d 轮，run=%s\n", res.Iterations, res.RunID)
			fmt.Fprintln(c.stdout, res.Final)
			return nil
		},
	}
	fs := cmd.Flags()
	fs.IntVar(&maxIterP, "max-iterations", 12, "轮次上限")
	fs.IntVar(&stallP, "stall-limit", 3, "连续失败上限")
	fs.DurationVar(&timeoutP, "timeout", sandbox.DefaultTimeout, "单轮总时长上限")
	fs.DurationVar(&idleP, "idle-timeout", sandbox.DefaultIdleTimeout, "单轮空闲上限")
	fs.Int64Var(&maxOutP, "max-output", sandbox.DefaultMaxOutputBytes, "单轮输出保留上限（字节）")
	fs.StringVar(&workdirP, "workdir", "", "工作目录（默认在轨迹目录下的 runs/）")
	return cmd
}
