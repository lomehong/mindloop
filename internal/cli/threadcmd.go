package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/lomehong/mindloop/internal/thread"
)

// newThreadCmd 是话题级工作线索的工具面（thread 包文档钉死的分层）：
// task 是强承诺（状态机），thread 是弱线索（话题指针）。心智经
// `mindloop thread` 自己开线/推进/收线——与 mem add 同款"自组合"；
// wakeTask 把开放线索注入唤醒上下文，指代解析（"那个事""进度怎么样"）
// 靠线索清单在眼前。
func (c *CLI) newThreadCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "thread",
		Short: "话题级工作线索：开线/推进/收线（心智自组合）",
		Long: `开放线索 = 还没到任务量级但不能忘的话题。与 task 的分工：
task 是强承诺（领取/状态机/幂等提交），thread 是话题指针（上下文）。
wakeTask 会把未完结线索注入唤醒提示——"进度怎么样""那个事"这类
指代由此解析。

  thread add <身份名> "标题"              开一条线索
  thread update <身份名> <id前缀> --note "进展" --status parked
  thread list <身份名> [--all]           开放线索（--all 含已完结）`,
	}
	cmd.AddCommand(c.newThreadAddCmd(), c.newThreadUpdateCmd(), c.newThreadListCmd())
	return cmd
}

func (c *CLI) newThreadAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add <身份名> <标题>",
		Short: "开一条新线索（open）",
		Args:  exactArgs(2, "用法: mindloop thread add <身份名> <标题>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := c.loadIdentity(args[0])
			if err != nil {
				return c.fail(err)
			}
			t, err := thread.Add(id.Dir, args[1])
			if err != nil {
				return c.fail(err)
			}
			fmt.Fprintf(c.stdout, "已开线索 %s：%s\n", t.ID, t.Title)
			return nil
		},
	}
}

func (c *CLI) newThreadUpdateCmd() *cobra.Command {
	var noteP, statusP string
	cmd := &cobra.Command{
		Use:   "update <身份名> <id前缀>",
		Short: "更新线索的状态与备注（id 支持唯一前缀）",
		Args:  exactArgs(2, "用法: mindloop thread update <身份名> <id前缀> [--note 进展] [--status open|parked|done]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := c.loadIdentity(args[0])
			if err != nil {
				return c.fail(err)
			}
			var status thread.Status
			switch strings.TrimSpace(statusP) {
			case "":
			case "open":
				status = thread.Open
			case "parked":
				status = thread.Parked
			case "done":
				status = thread.Done
			default:
				return c.fail(fmt.Errorf("--status 只允许 open/parked/done"))
			}
			t, err := thread.Update(id.Dir, args[1], status, noteP)
			if err != nil {
				return c.fail(err)
			}
			fmt.Fprintf(c.stdout, "已更新 %s：%s（%s）\n", t.ID, t.Title, t.Status)
			return nil
		},
	}
	cmd.Flags().StringVar(&noteP, "note", "", "最近进展一行（≤120 字节；空 = 不更新）")
	cmd.Flags().StringVar(&statusP, "status", "", "open/parked/done（空 = 不更新状态）")
	return cmd
}

func (c *CLI) newThreadListCmd() *cobra.Command {
	var allP bool
	cmd := &cobra.Command{
		Use:   "list <身份名>",
		Short: "列出线索（默认只列未完结）",
		Args:  exactArgs(1, "用法: mindloop thread list <身份名> [--all]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := c.loadIdentity(args[0])
			if err != nil {
				return c.fail(err)
			}
			items, err := thread.Load(id.Dir)
			if err != nil {
				return c.fail(err)
			}
			if m := thread.Manifest(id.Dir, time.Now()); m != "" && !allP {
				fmt.Fprintln(c.stdout, strings.TrimPrefix(m, "开放线索（进行中的话题/工作，用户问\"进度怎么样\"\"那个事\"指的就是这些；新消息若延续某条线索，用 thread update 更新它）："))
				return nil
			}
			if len(items) == 0 {
				fmt.Fprintln(c.stdout, "（没有线索——thread add 开一条）")
				return nil
			}
			for _, t := range items {
				note := ""
				if t.Note != "" {
					note = "：" + t.Note
				}
				fmt.Fprintf(c.stdout, "%s %-8s %s%s\n", t.ID, t.Status, t.Title, note)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&allP, "all", false, "包含已完结线索")
	return cmd
}
