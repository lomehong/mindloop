package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"mindloop/internal/obs"
)

// llm 命令组是模型准入状态的显式管理入口。准入守卫（obs.Guard）
// 在连续失败后自动熔断冷却，冷却窗口结束后自动路径只放行一次
// 探测；这里提供人工确认供应商已恢复后的显式恢复。
func (c *CLI) newLlmCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "llm",
		Short: "模型准入状态：熔断冷却的显式恢复",
		Long: `模型调用连续 3 次失败进入 5 分钟冷却，冷却结束后只放行一次
探测；冷却期内新请求被拒绝（显式任务失败、可重试）。确认供应商
已恢复后用 resume 显式清零，立即恢复放行，无需等待冷却结束。`,
	}
	cmd.AddCommand(c.newLlmResumeCmd())
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
