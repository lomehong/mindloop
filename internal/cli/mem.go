package cli

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"mindloop/internal/mem"
)

// resolveMemStore 从身份名解析记忆目录。记忆随身份走：
// <identities>/<name>/memories。无身份时报错——记忆必须属于某个
// 身份，全局记忆会让两个身份的记忆互相污染。
func (c *CLI) resolveMemStore(name string) (mem.Store, error) {
	if name == "" {
		return mem.Store{}, fmt.Errorf("mem: 需要 --identity <身份名>")
	}
	id, err := c.loadIdentity(name)
	if err != nil {
		return mem.Store{}, err
	}
	return mem.Store{Dir: filepath.Join(id.Dir, "memories")}, nil
}

func (c *CLI) newMemCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mem",
		Short: "身份的文件记忆库（markdown + BM25 检索）",
		Long: `记忆是 markdown 文件（frontmatter + 正文），可 cat、可 grep、
可 git。检索是本地 BM25（ASCII 词元 + CJK 二元组切词），零 API 成本。

沙箱里的 agent 用 $MINDLOOP_EXE mem add 自己写记忆——工具同时是
它的和人的。`,
	}
	cmd.AddCommand(
		c.newMemAddCmd(),
		c.newMemListCmd(),
		c.newMemSearchCmd(),
		c.newMemReviseCmd(),
		c.newMemInvalidateCmd(),
		c.newMemForgetCmd(),
	)
	return cmd
}

const memTypeList = "fact|belief|value|preference|todo|objective|person|note"

func (c *CLI) newMemAddCmd() *cobra.Command {
	var identityName, typ string
	cmd := &cobra.Command{
		Use:   `add --identity <名> --type <类型> "内容"`,
		Short: "写入一条记忆",
		Example: `  mindloop mem add --identity ada --type fact "操作员的名字是张伟"
  $MINDLOOP_EXE mem add --identity ada --type todo "记得复查部署"`,
		Args: exactArgs(1, `用法: mindloop mem add --identity <名> --type <类型> "内容"`),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := c.resolveMemStore(identityName)
			if err != nil {
				return c.fail(err)
			}
			a, err := store.AddWith(c.ctx, typ, args[0], mem.AddOpts{})
			if err != nil {
				return c.fail(err)
			}
			if a.Duplicate {
				fmt.Fprintf(c.stdout, "已存在相同内容 %s（未重复写入）\n", a.Memory.ID)
				return nil
			}
			fmt.Fprintf(c.stdout, "%s  %s\n", a.Memory.ID, a.Memory.Summary)
			// 冲突候选进 stderr：stdout 保持机器可读（首字段 = id）。
			if len(a.Conflicts) > 0 {
				fmt.Fprintf(c.stderr, "⚠ 与 %d 条已有记忆高度相似（如需修正请用 mem revise <id> \"新内容\"）：\n", len(a.Conflicts))
				for _, cf := range a.Conflicts {
					fmt.Fprintf(c.stderr, "  %.2f  %s  %-10s %s\n", cf.Similarity, cf.Memory.ID, cf.Memory.Type, cf.Memory.Summary)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&identityName, "identity", "", "身份名（必填）")
	cmd.Flags().StringVar(&typ, "type", "note", "记忆类型: "+memTypeList)
	return cmd
}

func (c *CLI) newMemListCmd() *cobra.Command {
	var identityName string
	var n int
	cmd := &cobra.Command{
		Use:   "list --identity <名>",
		Short: "列出记忆（新的在前）",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := c.resolveMemStore(identityName)
			if err != nil {
				return c.fail(err)
			}
			all, bad, err := store.ListDetailed()
			if err != nil {
				return c.fail(err)
			}
			// 坏记忆文件喊出来——静默跳过会让记忆"悄悄变少"。
			for _, e := range bad {
				fmt.Fprintf(c.stderr, "⚠ %v\n", e)
			}
			if n > 0 && len(all) > n {
				all = all[:n]
			}
			for _, m := range all {
				line := fmt.Sprintf("%s  %-10s %s", m.ID, m.Type, m.Summary)
				if m.Status != "" && m.Status != mem.StatusActive {
					line += "  [" + m.Status + "]"
				}
				fmt.Fprintln(c.stdout, line)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&identityName, "identity", "", "身份名（必填）")
	cmd.Flags().IntVarP(&n, "num", "n", 20, "最近 N 条（0 = 全部）")
	return cmd
}

func (c *CLI) newMemSearchCmd() *cobra.Command {
	var identityName string
	var k int
	cmd := &cobra.Command{
		Use:     `search --identity <名> "查询"`,
		Short:   "BM25 检索记忆",
		Example: `  mindloop mem search --identity ada "操作员偏好" -k 3`,
		Args:    exactArgs(1, `用法: mindloop mem search --identity <名> "查询" [-k N]`),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := c.resolveMemStore(identityName)
			if err != nil {
				return c.fail(err)
			}
			hits, err := store.Search(args[0], k)
			if err != nil {
				return c.fail(err)
			}
			for _, h := range hits {
				fmt.Fprintf(c.stdout, "%6.2f  %s  %-10s %s\n", h.Score, h.ID, h.Type, h.Summary)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&identityName, "identity", "", "身份名（必填）")
	cmd.Flags().IntVarP(&k, "top", "k", 5, "返回前 K 条")
	return cmd
}

func (c *CLI) newMemReviseCmd() *cobra.Command {
	var identityName string
	cmd := &cobra.Command{
		Use:     `revise --identity <名> <记忆id> "新内容"`,
		Short:   "修订一条记忆（旧版本标记为被替代，保留在盘上可追溯）",
		Example: `  mindloop mem revise --identity ada a1b2c3d4 "操作员搬到了上海"`,
		Args:    exactArgs(2, `用法: mindloop mem revise --identity <名> <记忆id> "新内容"`),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := c.resolveMemStore(identityName)
			if err != nil {
				return c.fail(err)
			}
			a, err := store.Revise(c.ctx, args[0], args[1])
			if err != nil {
				return c.fail(err)
			}
			fmt.Fprintf(c.stdout, "%s  %s（替代 %s）\n", a.Memory.ID, a.Memory.Summary, args[0])
			return nil
		},
	}
	cmd.Flags().StringVar(&identityName, "identity", "", "身份名（必填）")
	return cmd
}

func (c *CLI) newMemInvalidateCmd() *cobra.Command {
	var identityName string
	cmd := &cobra.Command{
		Use:     "invalidate --identity <名> <记忆id>",
		Short:   "失效一条记忆（保留文件供审计，退出检索与被引用）",
		Example: `  mindloop mem invalidate --identity ada a1b2c3d4`,
		Args:    exactArgs(1, "用法: mindloop mem invalidate --identity <名> <记忆id>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := c.resolveMemStore(identityName)
			if err != nil {
				return c.fail(err)
			}
			if err := store.Invalidate(c.ctx, args[0]); err != nil {
				return c.fail(err)
			}
			fmt.Fprintf(c.stdout, "已失效 %s\n", args[0])
			return nil
		},
	}
	cmd.Flags().StringVar(&identityName, "identity", "", "身份名（必填）")
	return cmd
}

func (c *CLI) newMemForgetCmd() *cobra.Command {
	var identityName string
	cmd := &cobra.Command{
		Use:   "forget --identity <名> <记忆id>",
		Short: "删除一条记忆",
		Args:  exactArgs(1, "用法: mindloop mem forget --identity <名> <记忆id>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := c.resolveMemStore(identityName)
			if err != nil {
				return c.fail(err)
			}
			if err := store.Forget(args[0]); err != nil {
				return c.fail(err)
			}
			fmt.Fprintf(c.stdout, "已删除 %s\n", args[0])
			return nil
		},
	}
	cmd.Flags().StringVar(&identityName, "identity", "", "身份名（必填）")
	return cmd
}
