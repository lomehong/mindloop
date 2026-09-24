package cli

import (
	"errors"
	"fmt"
	"os"
	"sort"

	"github.com/spf13/cobra"

	"mindloop/internal/identity"
)

func (c *CLI) newIdentityCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "identity",
		Short: "身份管理：一个身份就是一个目录（persona + 记忆 + 轨迹）",
	}
	cmd.AddCommand(
		c.newIdentityCreateCmd(),
		c.newIdentityListCmd(),
		c.newIdentityRemoveCmd(),
	)
	return cmd
}

func (c *CLI) newIdentityCreateCmd() *cobra.Command {
	var personaFile string
	cmd := &cobra.Command{
		Use:   "create <名字>",
		Short: "创建身份（目录 + 默认 persona + 根轨迹）",
		Args:  exactArgs(1, "用法: mindloop identity create <名字> [--persona-file F]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := identity.Create(c.ctx, args[0])
			if err != nil {
				return c.fail(err)
			}
			if personaFile != "" {
				data, err := os.ReadFile(personaFile)
				if err != nil {
					return c.fail(err)
				}
				if err := os.WriteFile(id.Dir+"/persona.md", data, 0o644); err != nil {
					return c.fail(err)
				}
			}
			fmt.Fprintf(c.stdout, "身份 %s 已创建\n  目录: %s\n  根轨迹: %s\n", id.Name, id.Dir, id.Timeline.ID)
			return nil
		},
	}
	cmd.Flags().StringVar(&personaFile, "persona-file", "", "从文件读取自定义 persona（默认用模板）")
	return cmd
}

func (c *CLI) newIdentityListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "列出全部身份",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			names, err := identity.List()
			if err != nil {
				return c.fail(err)
			}
			sort.Strings(names)
			for _, n := range names {
				fmt.Fprintf(c.stdout, "%s\n", n)
			}
			return nil
		},
	}
}

// newIdentityRemoveCmd：删除身份目录（轨迹 + 记忆 + 元数据）。
// 不变量：心智根（含 .env 配置）**永远不被触碰**——这是事故教训
// 钉下的硬约束，identity.Remove 与本命令的注释双重声明。
func (c *CLI) newIdentityRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <名字>",
		Short: "删除一个身份（仅该身份的目录，心智根/.env 不动）",
		Args:  exactArgs(1, "用法: mindloop identity remove <名字>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := identity.Remove(args[0]); err != nil {
				if errors.Is(err, identity.ErrNotFound) {
					return exitError{code: 1, err: fmt.Errorf("身份 %s 不存在", args[0])}
				}
				return c.fail(err)
			}
			fmt.Fprintf(c.stdout, "已删除身份 %s（心智根/.env 未动）\n", args[0])
			return nil
		},
	}
}
