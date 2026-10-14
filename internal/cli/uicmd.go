package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lomehong/mindloop/internal/mind"
	"github.com/lomehong/mindloop/internal/traj"
)

// newUICmd 是界面场景的工具面（scene.md §5.1）：模型经
// $MINDLOOP_EXE ui set 以"声明状态"的方式驱动仪表盘卡片面——幂等
// upsert（第一次是显示，再调是更新，--remove 即移除）。判重经轨迹
// replay（CLI 是独立进程，不复用 mind 的内存 Store；竞态双写的后果
// 只是重复的幂等步骤，投影端无碍）。
func (c *CLI) newUICmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ui",
		Short: "界面场景：声明仪表盘卡片面的当前状态（UI = f(scene)）",
		Long: `以幂等声明驱动仪表盘的卡片投影——core 只描述"此刻舞台上应该
有什么"，怎么渲染是投影端的事。卡片是呈现不是执行：set 不过审批门，
choice 卡的用户决策经 intent 回流才产生副作用。

  ui set <身份名> <id> --kind text --summary "…" --data '{"…"}'
  ui set <身份名> <id> --remove
  ui list <身份名>

kind 词汇表 v1：text（信息卡）/ progress（进度）/ choice（决策）/
metric（数值）。intent 是语义重要性：ambient（氛围）/ inform（一般，
缺省）/ confront（用户必须停下决策——同一时刻至多一张焦点卡）。
密度上限 8 张；data ≤16KB（合法 JSON）；summary ≤80 字节。
同 id 同内容的重复 set 不落轨迹（无变化不广播）。`,
	}
	cmd.AddCommand(c.newUISetCmd(), c.newUIListCmd())
	return cmd
}

func (c *CLI) newUISetCmd() *cobra.Command {
	var kindP, intentP, summaryP, dataP string
	var removeP bool
	var batchP, exitReasonP string
	cmd := &cobra.Command{
		Use:   "set <身份名> <id>",
		Short: "幂等声明一个界面卡片（--remove 移除）",
		Args:  exactArgs(2, "用法: mindloop ui set <身份名> <id>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := c.loadIdentity(args[0])
			if err != nil {
				return c.fail(err)
			}
			surfaceID := args[1]
			steps, err := id.Timeline.Steps()
			if err != nil {
				return c.fail(err)
			}
			store := mind.NewSceneStore()
			store.Replay(steps)

			// choreography 批号（theater.md §16.7）：同一编排事务的多张
			// 卡共享 batch_id，投影端单帧批量落位 + stagger 40ms。模型
			// 在一个 bash 块里连发多张卡时传同一 --batch 即可断言。
			batchFields := func(fields map[string]any) map[string]any {
				if batchP != "" {
					fields["batch_id"] = batchP
				}
				return fields
			}

			if removeP {
				changed := store.Remove(surfaceID)
				if !changed {
					fmt.Fprintf(c.stdout, "卡片 %s 不存在，无需移除\n", surfaceID)
					return nil
				}
				fields := map[string]any{"op": "remove", "id": surfaceID}
				// exit_reason 生命周期括号（theater.md §16.7）：ttl/
				// superseded/done/dismissed——投影端按 reason 选退场动效。
				if exitReasonP != "" {
					fields["exit_reason"] = exitReasonP
				}
				if err := appendSceneStep(id.Timeline, batchFields(fields)); err != nil {
					return c.fail(err)
				}
				fmt.Fprintf(c.stdout, "已移除卡片 %s\n", surfaceID)
				return nil
			}

			if kindP == "" {
				return c.fail(fmt.Errorf("--kind 必填（text/progress/choice/metric）"))
			}
			if strings.TrimSpace(summaryP) == "" {
				return c.fail(fmt.Errorf("--summary 必填（一行摘要，manifest 回注与降级占位共用）"))
			}
			var data json.RawMessage
			if dataP != "" {
				trimmed := []byte(dataP)
				if !json.Valid(trimmed) {
					return c.fail(fmt.Errorf("--data 不是合法 JSON"))
				}
				// canonical 化：键序稳定，判重才可靠。
				var buf any
				if err := json.Unmarshal(trimmed, &buf); err == nil {
					if normalized, merr := json.Marshal(buf); merr == nil {
						data = normalized
					} else {
						data = trimmed
					}
				} else {
					data = trimmed
				}
			} else if cur := store.Get(surfaceID); cur != nil && len(cur.Data) > 0 {
				// 省略 --data = 保留卡片原有数据（更新摘要/意图的常见
				// 形态不该被 shell 转义问题惩罚——真实用户反馈）。
				data = cur.Data
			}
			changed, err := store.Upsert(mind.Surface{
				ID: surfaceID, Kind: kindP, Intent: intentP,
				Summary: summaryP, Data: data,
			})
			if err != nil {
				return c.fail(err)
			}
			if !changed {
				fmt.Fprintf(c.stdout, "卡片 %s 无变化，不落轨迹\n", surfaceID)
				return nil
			}
			fields := map[string]any{
				"op": "upsert", "id": surfaceID, "kind": kindP,
				"summary": summaryP,
			}
			if intentP != "" {
				fields["intent"] = intentP
			}
			if len(data) > 0 {
				fields["data"] = string(data) // 扁平 fields 存标量：JSON 字符串化
			}
			if err := appendSceneStep(id.Timeline, batchFields(fields)); err != nil {
				return c.fail(err)
			}
			fmt.Fprintf(c.stdout, "已声明卡片 %s（%s）\n", surfaceID, kindP)
			return nil
		},
	}
	fs := cmd.Flags()
	fs.StringVar(&kindP, "kind", "", "卡片类型：text/progress/choice/metric")
	fs.StringVar(&intentP, "intent", "", "语义重要性：ambient/inform/confront（缺省 inform）")
	fs.StringVar(&summaryP, "summary", "", "一行摘要（manifest 回注与降级占位共用，≤80 字节）")
	fs.StringVar(&dataP, "data", "", "卡片数据（合法 JSON 字符串，≤16KB）")
	fs.BoolVar(&removeP, "remove", false, "移除该卡片")
	fs.StringVar(&batchP, "batch", "", "choreography 批号：同批多卡共享（投影端单帧批量落位+stagger）")
	fs.StringVar(&exitReasonP, "exit-reason", "", "remove 的生命周期括号：ttl/superseded/done/dismissed（投影端按 reason 选退场动效）")
	return cmd
}

func (c *CLI) newUIListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <身份名>",
		Short: "当前场景投影（调试面）",
		Args:  exactArgs(1, "用法: mindloop ui list <身份名>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := c.loadIdentity(args[0])
			if err != nil {
				return c.fail(err)
			}
			steps, err := id.Timeline.Steps()
			if err != nil {
				return c.fail(err)
			}
			store := mind.NewSceneStore()
			store.Replay(steps)
			if m := store.Manifest(); m != "" {
				fmt.Fprintln(c.stdout, m)
			} else {
				fmt.Fprintln(c.stdout, "当前无界面卡片")
			}
			return nil
		},
	}
}

// appendSceneStep 落一条 scene 步骤（-launched_by 无——scene 是
// 结构性步骤，不继承也不需要运行章）。
func appendSceneStep(tl *traj.Timeline, fields map[string]any) error {
	s := traj.NewStep(traj.TypeScene)
	for k, v := range fields {
		s.Fields[k] = v
	}
	return tl.Append(context.Background(), s)
}
