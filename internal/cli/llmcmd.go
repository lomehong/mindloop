package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/lomehong/mindloop/internal/config"
	"github.com/lomehong/mindloop/internal/llm"
	"github.com/lomehong/mindloop/internal/obs"
	"github.com/lomehong/mindloop/internal/robot"
)

// llm 命令组是模型准入状态的显式管理入口。准入守卫（obs.Guard）
// 在连续失败后自动熔断冷却，冷却窗口结束后自动路径只放行一次
// 探测；这里提供人工确认供应商已恢复后的显式恢复。
func (c *CLI) newLlmCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "llm",
		Short: "模型准入状态：熔断冷却的显式恢复 + 视觉冒烟",
		Long: `模型调用连续 3 次失败进入 5 分钟冷却，冷却结束后只放行一次
探测；冷却期内新请求被拒绝（显式任务失败、可重试）。确认供应商
已恢复后用 resume 显式清零，立即恢复放行，无需等待冷却结束。

vision 子命令是字面模态的端到端冒烟：真实截屏 → 图片消息 → 真实
模型回答——验证当前配置的模型是否支持图片输入（不支持会得到
供应商的明确报错，心智运行时会自动去图降档）。`,
	}
	cmd.AddCommand(c.newLlmResumeCmd(), c.newLlmVisionCmd())
	return cmd
}

// newLlmVisionCmd：截屏 + 图片消息 + 真实补全——视觉回路的
// 显式验证入口（也供人工直接"问一眼屏幕"）。
func (c *CLI) newLlmVisionCmd() *cobra.Command {
	var question string
	cmd := &cobra.Command{
		Use:     "vision <identity>",
		Short:   "看一眼屏幕并问模型（截屏 → 图片消息 → 真实回答）",
		Example: `  mindloop llm vision ada --question "屏幕上有哪些窗口？"`,
		Args:    exactArgs(1, "用法: mindloop llm vision <identity> [--question 问题]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := c.loadIdentity(args[0])
			if err != nil {
				return c.fail(err)
			}
			if question == "" {
				question = "用一两句话描述这张屏幕截图：有哪些窗口、大致在做什么。"
			}
			title, ok := robot.ForegroundTitle()
			if ok && robot.IsSensitiveTitle(title) {
				return c.fail(fmt.Errorf("llm vision: 前台窗口 %q 命中敏感词——拒绝截取", title))
			}
			png, info, err := robot.ScreenshotPNG()
			if err != nil {
				return c.fail(err)
			}
			fitted, mime, err := robot.FitForVision(png, llm.MaxImageBytes)
			if err != nil {
				return c.fail(err)
			}
			// 身份 .env 合入进程环境（不覆盖已有键），FromEnv 同一条
			// 键链——CLI 实际使用与心智运行时同一构造路径。
			if err := config.LoadEnv(filepath.Join(id.Dir, ".env")); err != nil {
				return c.fail(err)
			}
			client, err := llm.FromEnv()
			if err != nil {
				return c.fail(err)
			}
			ctx, cancel := context.WithTimeout(c.ctx, 60*time.Second)
			defer cancel()
			start := time.Now()
			answer, err := client.Complete(ctx, "你是屏幕视觉助手。用户给你一张桌面截图，据实回答，不要编造看不清的内容。",
				[]llm.Message{{
					Role:    "user",
					Content: fmt.Sprintf("%s\n（截图 %dx%d%s）", question, info.Width, info.Height, map[bool]string{true: "，前台=" + title, false: ""}[ok]),
					Images:  []llm.Image{llm.NewImage(mime, fitted)},
				}})
			if err != nil {
				return c.fail(fmt.Errorf("llm vision: 模型调用失败（模型可能不支持图片输入）: %w", err))
			}
			fmt.Fprintf(c.stdout, "截图 %dx%d（%d 字节，%s）→ %s（%s）\n\n%s\n",
				info.Width, info.Height, len(fitted), mime, client.Model, time.Since(start).Round(time.Millisecond), answer)
			return nil
		},
	}
	cmd.Flags().StringVar(&question, "question", "", "要问的问题（默认描述屏幕）")
	return cmd
}

func (c *CLI) newLlmResumeCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "resume <identity>",
		Short:   "显式恢复：清除连续失败计数，模型请求立即放行",
		Example: `  mindloop llm resume ada`,
		Args:    exactArgs(1, "用法: mindloop llm resume <identity>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := c.loadIdentity(args[0])
			if err != nil {
				return c.fail(err)
			}
			old, err := obs.ClearHealth(id.Dir)
			if err != nil {
				return c.fail(err)
			}
			if old.ConsecutiveErrors == 0 {
				fmt.Fprintln(c.stdout, "无熔断记录——无需恢复。")
				return nil
			}
			fmt.Fprintf(c.stdout, "熔断状态已清除：连续失败 %d 次", old.ConsecutiveErrors)
			if old.LastErrorAt != "" {
				fmt.Fprintf(c.stdout, "（最后失败于 %s）", old.LastErrorAt)
			}
			fmt.Fprintln(c.stdout, "，模型请求立即放行。")
			return nil
		},
	}
}
