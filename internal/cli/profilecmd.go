package cli

import (
	"fmt"
	"strings"

	"github.com/lomehong/mindloop/internal/profile"
	"github.com/spf13/cobra"
)

// newProfileCmd 是操作员画像的工具面（常驻蒸馏层）：心智经
// `mindloop profile set` 自维护（mem add 同款自组合），每条属性带
// 证据（来源轨迹步骤）；wakeTask/responder 把画像注入系统提示。
func (c *CLI) newProfileCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "profile",
		Short: "操作员画像：稳定特征的蒸馏层（常驻上下文）",
		Long: `画像属性是"总是该在上下文里"的长期特征（沟通风格/技术领域/
偏好/角色）——与 mem 的分工：mem 是可检索层（BM25 命中才出现），
画像是常驻层（每次唤醒/回复都在）。属性带证据（来源轨迹步骤），
蒸馏结论与最新对话冲突时以对话为准。

  profile set <身份名> <key> <value> [--evidence stepID]
  profile list <身份名>
  profile remove <身份名> <key>`,
	}
	cmd.AddCommand(c.newProfileSetCmd(), c.newProfileListCmd(), c.newProfileRemoveCmd())
	return cmd
}

func (c *CLI) newProfileSetCmd() *cobra.Command {
	var evidenceP string
	cmd := &cobra.Command{
		Use:   "set <身份名> <key> <value>",
		Short: "插入或更新一个画像属性（同 key 覆盖）",
		Args:  exactArgs(3, "用法: mindloop profile set <身份名> <key> <value>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := c.loadIdentity(args[0])
			if err != nil {
				return c.fail(err)
			}
			attr, err := profile.Set(id.Dir, args[1], args[2], evidenceP)
			if err != nil {
				return c.fail(err)
			}
			fmt.Fprintf(c.stdout, "已写入画像：%s = %s\n", attr.Key, attr.Value)
			return nil
		},
	}
	cmd.Flags().StringVar(&evidenceP, "evidence", "", "证据来源（轨迹步骤 ID，可选）")
	return cmd
}

func (c *CLI) newProfileListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <身份名>",
		Short: "列出全部画像属性",
		Args:  exactArgs(1, "用法: mindloop profile list <身份名>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := c.loadIdentity(args[0])
			if err != nil {
				return c.fail(err)
			}
			if m := profile.Manifest(id.Dir); m != "" {
				fmt.Fprintln(c.stdout, strings.TrimPrefix(m, "用户画像（长期特征蒸馏，背景信息——据此调整语气与详略；与最新事实冲突时以对话为准）："))
			} else {
				fmt.Fprintln(c.stdout, "（画像为空——profile set 写入属性）")
			}
			return nil
		},
	}
}

func (c *CLI) newProfileRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <身份名> <key>",
		Short: "删除一个画像属性（不存在则安静）",
		Args:  exactArgs(2, "用法: mindloop profile remove <身份名> <key>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := c.loadIdentity(args[0])
			if err != nil {
				return c.fail(err)
			}
			if !profile.Remove(id.Dir, args[1]) {
				fmt.Fprintf(c.stdout, "属性 %s 不存在\n", args[1])
				return nil
			}
			fmt.Fprintf(c.stdout, "已移除画像属性 %s\n", args[1])
			return nil
		},
	}
}
