// Package cli 是 mindloop 的命令行层，基于 spf13/cobra：命令树、
// 帮助生成、未知命令建议（"did you mean"）、flag 与位置参数混排
// 都交给 cobra；本包负责把领域包（traj/prompt/llm/mem/...）接成
// 命令，并维护三条进程级约定：
//
//   - 退出码：0 成功；1 运行失败；2 用法错误；3 run 的失速/轮次
//     耗尽（运行的真实结局，不是内部错误）。
//   - stdout 只放数据，stderr 放诊断——两者由 main 注入，整层
//     可不经子进程测试。
//   - <home>/.env 在启动时加载，显式环境变量优先。
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"mindloop/internal/config"
	"mindloop/internal/traj"
)

// CLI 聚拢一次命令调用的环境。
type CLI struct {
	ctx    context.Context
	stdout io.Writer
	stderr io.Writer
}

// exitError 携带进程退出码。err 为 nil 时 Execute 不再重复打印
// （命令已自行输出了诊断）。
type exitError struct {
	code int
	err  error
}

func (e exitError) Error() string {
	if e.err != nil {
		return e.err.Error()
	}
	return ""
}

// fail 把运行失败包装为退出码 1。
func (c *CLI) fail(err error) error { return exitError{code: 1, err: err} }

// usageErr 把用法错误包装为退出码 2（中文用法提示由调用方给出）。
func usageErr(usage string) error { return exitError{code: 2, err: errors.New(usage)} }

// Execute 调度一次命令行调用，返回进程退出码。
func Execute(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	c := &CLI{ctx: ctx, stdout: stdout, stderr: stderr}
	// .env 持久配置：读 <home>/.env，显式环境变量优先。加载失败
	// 不阻止运行（配置缺失会在需要它的命令处报错）。
	_ = config.LoadHome(traj.Home())

	root := c.newRootCmd()
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)
	// 错误与用法由 Execute 统一输出，避免 cobra 默认的英文堆叠。
	root.SilenceUsage = true
	root.SilenceErrors = true
	// completion 子命令藏起来——它属于 shell 集成，不该占帮助首页。
	root.CompletionOptions.HiddenDefaultCmd = true

	if err := root.Execute(); err != nil {
		var ee exitError
		if errors.As(err, &ee) {
			if ee.err != nil {
				fmt.Fprintf(stderr, "mindloop: %v\n", ee.err)
			}
			return ee.code
		}
		if errors.Is(err, context.Canceled) {
			return 0 // Ctrl+C 不是错误
		}
		// 走到这里的是 cobra 层的错误（未知命令/未知旗标）——
		// cobra 的建议文本（did you mean）会一并带出。
		fmt.Fprintf(stderr, "mindloop: %v\n", err)
		fmt.Fprintln(stderr, "运行 'mindloop --help' 查看用法。")
		return 2
	}
	return 0
}

func (c *CLI) newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "mindloop",
		Short: "跨平台的持久化 agent 框架（Windows 一等公民）",
		Long: `mindloop — 跨平台的持久化 agent 框架

以追加式 JSONL 轨迹日志为唯一事实源：一个持久身份（identity）带着
常驻心智（mind）持续思考——模型写 bash，在沙箱中执行（Windows 下
为 Job Object 原生沙箱，无需 Docker），一切落进可审计的日志；人类
经 mind say 与它对话，记忆经 mem 累积，经历经 recap 分层。

快速开始:
  1) 编辑 ~\.mindloop\.env 填入模型（完整参考 .env.example）:
       MINDLOOP_MODEL=glm-5
       MINDLOOP_API_KEY=你的key
  2) mindloop identity create ada
  3) mindloop chat ada        # 启动心智并开始对话
     你> 介绍一下你自己
     ada> 我是 ada ……`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       Version,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	root.SetVersionTemplate("mindloop v{{.Version}}\n")
	root.AddCommand(
		c.newRootChatCmd(),
		c.newTrajCmd(),
		c.newPromptCmd(),
		c.newRunCmd(),
		c.newRecapCmd(),
		c.newMindCmd(),
		c.newIdentityCmd(),
		c.newMemCmd(),
		c.newWebCmd(),
		c.newTailfCmd(),
		c.newVersionCmd(),
	)
	return root
}

// Version 是 CLI 的单一版本号来源——root 命令的 --version 与
// version 子命令共用，避免双处硬编码漂移。
const Version = "0.6.0"

func (c *CLI) newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "打印版本",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintf(c.stdout, "mindloop v%s（日志/渲染/运行循环/心智/记忆/recap/观测）\n", Version)
			return nil
		},
	}
}
