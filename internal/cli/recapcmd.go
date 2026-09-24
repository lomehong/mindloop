package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"mindloop/internal/llm"
	"mindloop/internal/recap"
	"mindloop/internal/traj"
)

func (c *CLI) newRecapCmd() *cobra.Command {
	var cachedP, flushP bool
	var maxP int
	cmd := &cobra.Command{
		Use:   "recap <traj>",
		Short: "情节摘要：把日志切窗并缓存为分层上下文的粗层",
		Long: `把日志按确定性规则（时间间隔/步数/字节）切成情节窗口，
逐窗摘要并缓存（盖 model + prompt_version 章）。边界前缀稳定——
已摘要的历史永不重算，追加步骤只增量补齐尾窗。

monolith 唤醒时的上下文 = 人生分集摘要（本命令的缓存，粗层）
+ 原文尾窗（prompt 渲染，细层）。`,
		Example: `  mindloop recap <id>                # 补齐缺失摘要并渲染
  mindloop recap <id> --cached       # 只渲染，零模型调用
  mindloop recap <id> --flush        # 连尾窗一起摘要`,
		Args: exactArgs(1, "用法: mindloop recap <traj> [--cached] [--flush] [--max N]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			t, err := traj.Load(args[0])
			if err != nil {
				return c.fail(err)
			}
			if !cachedP {
				client, err := llm.FromEnv()
				if err != nil {
					return c.fail(err)
				}
				client.OnDone = usageRecorder(t.Dir)
				u := &recap.Updater{
					Timeline: t,
					Thinker:  llmThinker{c: client},
					Flush:    flushP,
				}
				rep, err := u.Update(c.ctx)
				if err != nil {
					return c.fail(err)
				}
				fmt.Fprintf(c.stderr, "recap: %d 个闭合窗口，命中 %d，新增摘要 %d，成本阀跳过 %d\n",
					rep.Windows, rep.Cached, rep.Summarized, rep.SkippedCost)
			}
			life, err := recap.RenderLife(t.Dir, maxP)
			if err != nil {
				return c.fail(err)
			}
			if life == "" {
				fmt.Fprintln(c.stdout, "（暂无情节摘要——去掉 --cached 让摘要器先跑一遍）")
			} else {
				fmt.Fprint(c.stdout, life)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&cachedP, "cached", false, "只渲染已有摘要，不调用模型")
	cmd.Flags().BoolVar(&flushP, "flush", false, "把尾窗也摘要（否则尾窗增长到闭窗条件才处理）")
	cmd.Flags().IntVar(&maxP, "max", 0, "渲染最近 N 幕（0 = 全部）")
	return cmd
}
