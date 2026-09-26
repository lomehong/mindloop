package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"mindloop/internal/identity"
	"mindloop/internal/policy"
	"mindloop/internal/traj"
)

// approve 是执行授权控制面的用户入口：列出等待批准的脚本、批准或
// 拒绝。目标解析兼容两种入口——身份名（心智场景，根轨迹在身份
// 目录下）与全局轨迹 id/前缀（run 场景）。
func (c *CLI) newApproveCmd() *cobra.Command {
	var denyP bool
	cmd := &cobra.Command{
		Use:   "approve <traj> [脚本哈希前缀]",
		Short: "批准或拒绝等待执行的脚本（ask 策略的控制面）",
		Long: `MINDLOOP_EXEC_POLICY=ask（缺省）时，模型生成的脚本在执行前
展示正文、工作目录与任务/运行归属，并等待明确批准。

无哈希: 列出全部待批脚本（含完整正文与风险提示）。
有哈希: 批准该脚本；--deny 拒绝。哈希可用唯一前缀（至少 4 位）。

<traj> 可以是身份名（mindloop mind run / chat 场景）或轨迹
id/前缀（mindloop run 场景）。

边界: 本命令控制的是"启动授权与工作目录约定"，不是操作系统访问隔离——
批准后 bash 仍以当前用户权限运行，可能访问本用户可访问的文件与网络。`,
		Example: `  mindloop approve ada                 # 列出 ada 的待批脚本
  mindloop approve ada 3f2a1b7c        # 批准（哈希前缀）
  mindloop approve ada 3f2a1b7c --deny # 拒绝`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := approvalDir(args[0])
			if err != nil {
				return c.fail(err)
			}
			if len(args) == 1 {
				return c.printPendingApprovals(dir)
			}
			if err := policy.Decide(dir, args[1], !denyP); err != nil {
				return c.fail(err)
			}
			verb := "已批准"
			if denyP {
				verb = "已拒绝"
			}
			fmt.Fprintf(c.stdout, "%s脚本 %s（等待方将消费该决定）\n", verb, args[1])
			return nil
		},
	}
	cmd.Flags().BoolVar(&denyP, "deny", false, "拒绝执行（而不是批准）")
	return cmd
}

// approvalDir 解析授权控制面目录：先按身份名（心智场景），再按全局
// 轨迹 id/前缀（run 场景）。都不匹配时给出可执行的下一步。
func approvalDir(target string) (string, error) {
	if id, err := identity.Load(target); err == nil {
		return policy.Dir(id.Timeline.Dir), nil
	}
	if tl, err := traj.Load(target); err == nil {
		return policy.Dir(tl.Dir), nil
	}
	return "", fmt.Errorf("没有匹配 %q 的身份或轨迹——身份用 mindloop identity list 查看；轨迹 id 由 mindloop run 的 <traj> 参数给出", target)
}

// printPendingApprovals 渲染待批脚本：哈希前缀、归属、有效期、风险
// 与完整正文——"执行前展示正文"是 ask 策略的定义。
func (c *CLI) printPendingApprovals(dir string) error {
	pending, err := policy.ListPending(dir)
	if err != nil {
		return c.fail(err)
	}
	if len(pending) == 0 {
		fmt.Fprintln(c.stdout, "没有等待批准的脚本（MINDLOOP_EXEC_POLICY=ask 时脚本执行前会在这里等待）")
		return nil
	}
	fmt.Fprintf(c.stdout, "待批脚本 %d 个：\n", len(pending))
	for i, p := range pending {
		fmt.Fprintf(c.stdout, "\n[%d] %s\n", i+1, shortHash(p.Hash))
		fmt.Fprintf(c.stdout, "    工作目录: %s\n", p.WorkDir)
		if p.TaskID != "" {
			fmt.Fprintf(c.stdout, "    任务: %s（attempt %d）\n", p.TaskID, p.Attempt)
		}
		fmt.Fprintf(c.stdout, "    运行: %s\n", p.RunID)
		fmt.Fprintf(c.stdout, "    有效期至 %s\n", p.Expires.Local().Format("15:04:05"))
		for _, r := range p.Risks {
			fmt.Fprintf(c.stdout, "    ⚠ %s\n", r)
		}
		fmt.Fprintf(c.stdout, "    ── 脚本正文 ──\n%s\n", p.Script)
	}
	return nil
}

// shortHash 是展示用的哈希前缀（与 policy 内部口径一致）。
func shortHash(hash string) string {
	if len(hash) > 12 {
		return hash[:12]
	}
	return hash
}
