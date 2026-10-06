package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/lomehong/mindloop/internal/identity"
	"github.com/lomehong/mindloop/internal/policy"
	"github.com/lomehong/mindloop/internal/traj"
)

// approve 是执行授权控制面的用户入口：列出等待批准的脚本、批准或
// 拒绝。目标解析兼容两种入口——身份名（心智场景，根轨迹在身份
// 目录下）与全局轨迹 id/前缀（run 场景）。
func (c *CLI) newApproveCmd() *cobra.Command {
	var denyP bool
	cmd := &cobra.Command{
		Use:   "approve <traj> [脚本哈希前缀]",
		Short: "批准或拒绝等待执行的脚本（执行策略的控制面）",
		Long: `MINDLOOP_EXEC_POLICY=auto（缺省）或 ask 时，需要人过目的
脚本在执行前展示正文、工作目录与任务/运行归属，并等待明确批准
（auto 只拦删除/外发发布/提权/凭据/系统改动，见
internal/policy/approval_class.go；ask 拦一切非只读脚本）。

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
			tl, dir, err := approvalTarget(args[0])
			if err != nil {
				return c.fail(err)
			}
			if len(args) == 1 {
				return c.printPendingApprovals(dir)
			}
			if err := policy.Decide(dir, args[1], !denyP); err != nil {
				return c.fail(err)
			}
			// 味觉落轨迹：决定文件是瞬态的（gate 消费即删），持久
			// 证据自己写。失败只提示——决定本身已生效。
			signal := "approve"
			if denyP {
				signal = "deny"
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := traj.AppendTasteStep(ctx, tl, signal, "", args[1]); err != nil {
				fmt.Fprintf(c.stderr, "⚠ 味觉归因落盘失败（决定已生效）: %v\n", err)
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

// approvalTarget 解析授权控制面的轨迹与目录：先按身份名（心智场景），
// 再按全局轨迹 id/前缀（run 场景）。味觉归因需要轨迹写位，所以
// 返回 Timeline 本体。
func approvalTarget(target string) (*traj.Timeline, string, error) {
	if id, err := identity.Load(target); err == nil {
		return id.Timeline, policy.Dir(id.Timeline.Dir), nil
	}
	if tl, err := traj.Load(target); err == nil {
		return tl, policy.Dir(tl.Dir), nil
	}
	return nil, "", fmt.Errorf("没有匹配 %q 的身份或轨迹——身份用 mindloop identity list 查看；轨迹 id 由 mindloop run 的 <traj> 参数给出", target)
}

// printPendingApprovals 渲染待批脚本：哈希前缀、归属、有效期、风险
// 与完整正文——"执行前展示正文"是 ask 策略的定义。
func (c *CLI) printPendingApprovals(dir string) error {
	pending, err := policy.ListPending(dir)
	if err != nil {
		return c.fail(err)
	}
	if len(pending) == 0 {
		fmt.Fprintln(c.stdout, "没有等待批准的脚本（auto/ask 策略下需要人过目的脚本会在这里等待）")
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
