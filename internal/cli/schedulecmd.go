package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/lomehong/mindloop/internal/identity"
	"github.com/lomehong/mindloop/internal/schedule"
)

// schedule 命令组是身份日程的用户入口：schedule.json 是唯一事实
// （用户可直接编辑，心智热加载），CLI 只做读写与呈现——到点动作
// 由心智心跳保证，CLI 不替它执行节拍。
func (c *CLI) newScheduleCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "schedule",
		Short: "身份日程：定时提交任务（at）、周期执行命令（every）或周期巡检（every+task）",
		Long: `身份级时间表 <身份目录>/schedule.json，由心智心跳驱动：到点由
代码保证，不依赖模型自觉。边界：进程不在线则不执行；重启时 at 条目
在 48h 回看窗口内错过的有效触发会幂等补提交，exec/巡检不回补。

条目 = 触发器（every 或 at）+ 动作（exec 或 task）：
  every+exec   周期执行命令——命令在沙箱里跑，不叫醒模型、零 API 成本；
  at+task      每天本地时刻提交任务，由心智领取执行（幂等键含计划
               时刻，同一天只一次）；
  every+task   周期巡检任务——定期叫醒心智检查一件事，无事不报
               （走请求档，成本低）；at+exec 不支持。

quiet 窗口（"HH:MM-HH:MM"，允许跨午夜）只约束 task 条目：at 条目
到点落在窗口内则顺延到窗口终点；巡检条目窗口内直接蒸发（错过的
检查不补）；exec 条目不受 quiet 限制。`,
	}
	cmd.AddCommand(
		c.newScheduleListCmd(),
		c.newScheduleAddCmd(),
		c.newScheduleRemoveCmd(),
		c.newScheduleRunCmd(),
	)
	return cmd
}

// validateScheduleItem 用解析器自身校验单个条目：文件里坏条目跳过
// 并警告，但用户刚输入的东西必须显式报错——不能静默丢弃。
func validateScheduleItem(item schedule.Item) (schedule.Parsed, error) {
	body, err := json.Marshal(schedule.File{Items: []schedule.Item{item}})
	if err != nil {
		return schedule.Parsed{}, err
	}
	parsed, warns, err := schedule.Parse(body)
	if err != nil {
		return schedule.Parsed{}, err
	}
	if len(parsed) == 0 {
		if len(warns) > 0 {
			return schedule.Parsed{}, errors.New(warns[0])
		}
		return schedule.Parsed{}, errors.New("条目无效")
	}
	return parsed[0], nil
}

// describeScheduleEntry 把条目渲染成一句话（add 的回显）。
func describeScheduleEntry(p schedule.Parsed) string {
	var desc string
	switch p.Kind {
	case schedule.KindExec:
		desc = fmt.Sprintf("每 %s 执行 `%s`（超时 %s）", p.Every, oneLineLocal(p.Item.Exec, 80), p.Timeout)
	case schedule.KindPatrol:
		desc = fmt.Sprintf("每 %s 提交巡检任务「%s」（无事不报）", p.Every, oneLineLocal(p.Item.Task, 80))
	default:
		when := fmt.Sprintf("每天 %02d:%02d", p.AtMinute/60, p.AtMinute%60)
		if p.HasDays {
			when = "每周 " + schedule.TriggerText(p)
		}
		desc = fmt.Sprintf("%s 提交任务「%s」", when, oneLineLocal(p.Item.Task, 80))
	}
	desc += schedule.QuietText(p)
	if !p.Enabled {
		desc += "（已禁用）"
	}
	return desc
}

// warnScheduleTakeEffect 给出生效路径的提示：心智在跑则下一拍热
// 加载（≤5 秒），没跑则保存即事实、启动后生效。
func (c *CLI) warnScheduleTakeEffect(id *identity.Identity) {
	if mindRunning(id.Timeline.Dir) {
		fmt.Fprintln(c.stdout, "心智在运行：条目将在数秒内热加载生效。")
		return
	}
	fmt.Fprintf(c.stderr, "⚠ 心智没有在运行——条目已保存，启动后生效（mindloop chat %s）\n", id.Name)
}

// scheduleRuntimeFor 按装配语义构造一个日程运行时（同一路径、同一
// 任务提交通道）——CLI 子命令直接使用；日志走文件，不注入终端。
func (c *CLI) scheduleRuntimeFor(id *identity.Identity) *schedule.Runtime {
	_, _, extraEnv, _ := identityExtension(id)
	return assembleSchedule(id, extraEnv, nil)
}

// scheduleEntryView 是 list --json 的条目形态：解析结果 + 状态投影
// 合并（机器消费面）。
type scheduleEntryView struct {
	ID           string `json:"id"`
	Enabled      bool   `json:"enabled"`
	Kind         string `json:"kind"`
	Trigger      string `json:"trigger"`
	Action       string `json:"action"`
	NextRun      string `json:"next_run,omitempty"`
	LastRun      string `json:"last_run,omitempty"`
	LastExitCode int    `json:"last_exit_code,omitempty"`
	LastError    string `json:"last_error,omitempty"`
	LastNote     string `json:"last_note,omitempty"`
}

// printScheduleEntry 打印一个条目的两行形态：条目行 + 状态细节行。
func (c *CLI) printScheduleEntry(p schedule.Parsed, st schedule.ItemState, now time.Time) {
	fmt.Fprintf(c.stdout, "%-16s %-12s %s\n", p.Item.ID, schedule.TriggerText(p), schedule.ActionText(p))
	var bits []string
	if !p.Enabled {
		bits = append(bits, "已禁用")
	}
	if next := schedule.NextText(p, st, now); next != "" {
		bits = append(bits, "下次 "+next)
	}
	if st.LastRun == "" {
		bits = append(bits, "尚未执行")
	} else {
		last := "上次 " + st.LastRun
		switch {
		case st.LastError != "":
			last += "（失败: " + oneLineLocal(st.LastError, 80) + "）"
		case st.LastNote != "":
			last += "（" + oneLineLocal(st.LastNote, 80) + "）"
		default:
			last += fmt.Sprintf("（exit %d）", st.LastExitCode)
		}
		bits = append(bits, last)
	}
	fmt.Fprintf(c.stdout, "  %s\n", strings.Join(bits, "  "))
}

func (c *CLI) newScheduleListCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "list <身份名>",
		Short: "列出日程条目（含上次执行与下次触发）",
		Args:  exactArgs(1, "用法: mindloop schedule list <身份名> [--json]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := c.loadIdentity(args[0])
			if err != nil {
				return c.fail(err)
			}
			schedulePath, _, statePath := schedulePaths(id)
			parsed, warns, err := schedule.Load(schedulePath)
			if err != nil {
				return c.fail(fmt.Errorf("日程文件解析失败（%s）: %w", schedulePath, err))
			}
			for _, w := range warns {
				fmt.Fprintf(c.stderr, "⚠ %s\n", w)
			}
			state := schedule.LoadState(statePath)
			now := time.Now()
			if jsonOut {
				views := make([]scheduleEntryView, 0, len(parsed))
				for _, p := range parsed {
					st := state.Items[p.Item.ID]
					views = append(views, scheduleEntryView{
						ID: p.Item.ID, Enabled: p.Enabled, Kind: string(p.Kind),
						Trigger: schedule.TriggerText(p), Action: schedule.ActionText(p),
						NextRun: schedule.NextText(p, st, now),
						LastRun: st.LastRun, LastExitCode: st.LastExitCode,
						LastError: st.LastError, LastNote: st.LastNote,
					})
				}
				return c.printTaskJSON(views)
			}
			if len(parsed) == 0 {
				fmt.Fprintf(c.stdout, "（没有日程。用 mindloop schedule add %s '<条目 JSON>' 添加）\n", id.Name)
				return nil
			}
			for _, p := range parsed {
				c.printScheduleEntry(p, state.Items[p.Item.ID], now)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "输出条目数组 JSON（含状态投影）")
	return cmd
}

func (c *CLI) newScheduleAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <身份名> <条目 JSON>",
		Short: "添加或替换一个日程条目（同 id 整条替换）",
		Long: `条目 JSON 与 schedule.json 的 items 元素同构，例如：
  {"id":"daily","at":"21:00","task":"读取今天的采样并写晚报 {{date}}"}
  {"id":"sample","every":"2m","exec":"pwsh -NoProfile -File sample.ps1"}
  {"id":"ci","every":"30m","task":"检查 CI，无新事项直接完成不汇报","quiet":"23:00-08:00"}

触发器（every/at）与动作（exec/task）各恰好一个；id 允许字母数字
与 -_.，≤64；task 模板里的 {{date}} 渲染为当天本地日期。`,
		Example: `  mindloop schedule add ada '{"id":"daily","at":"21:00","task":"写晚报 {{date}}"}'
  mindloop schedule add ada '{"id":"sample","every":"2m","exec":"pwsh -NoProfile -File sample.ps1"}'
  mindloop schedule add ada '{"id":"ci","every":"30m","task":"检查 CI，无事直接完成","quiet":"23:00-08:00"}'`,
		Args: exactArgs(2, `用法: mindloop schedule add <身份名> '<条目 JSON>'`),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := c.loadIdentity(args[0])
			if err != nil {
				return c.fail(err)
			}
			var item schedule.Item
			if err := json.Unmarshal([]byte(args[1]), &item); err != nil {
				return usageErr("条目 JSON 解析失败: " + err.Error())
			}
			p, err := validateScheduleItem(item)
			if err != nil {
				return usageErr(err.Error())
			}
			schedulePath, _, _ := schedulePaths(id)
			file, err := schedule.LoadFile(schedulePath)
			if err != nil {
				return c.fail(err)
			}
			replaced := false
			for i := range file.Items {
				if file.Items[i].ID == item.ID {
					file.Items[i] = item
					replaced = true
					break
				}
			}
			if !replaced {
				file.Items = append(file.Items, item)
			}
			if err := schedule.SaveFile(schedulePath, file); err != nil {
				return c.fail(err)
			}
			verb := "已添加"
			if replaced {
				verb = "已替换"
			}
			fmt.Fprintf(c.stdout, "%s日程条目 %s：%s\n", verb, p.Item.ID, describeScheduleEntry(p))
			c.warnScheduleTakeEffect(id)
			return nil
		},
	}
	return cmd
}

func (c *CLI) newScheduleRemoveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remove <身份名> <条目 id>",
		Short: "删除一个日程条目",
		Args:  exactArgs(2, "用法: mindloop schedule remove <身份名> <条目 id>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := c.loadIdentity(args[0])
			if err != nil {
				return c.fail(err)
			}
			schedulePath, _, _ := schedulePaths(id)
			file, err := schedule.LoadFile(schedulePath)
			if err != nil {
				return c.fail(err)
			}
			kept := file.Items[:0]
			removed := false
			for _, it := range file.Items {
				if it.ID == args[1] {
					removed = true
					continue
				}
				kept = append(kept, it)
			}
			if !removed {
				return c.fail(fmt.Errorf("没有条目 %q（mindloop schedule list %s 查看）", args[1], id.Name))
			}
			file.Items = kept
			if err := schedule.SaveFile(schedulePath, file); err != nil {
				return c.fail(err)
			}
			fmt.Fprintf(c.stdout, "已删除日程条目 %s\n", args[1])
			c.warnScheduleTakeEffect(id)
			return nil
		},
	}
	return cmd
}

func (c *CLI) newScheduleRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run <身份名> <条目 id>",
		Short: "立即执行一个条目（exec 同步执行；task 按今天提交）",
		Long: `手动触发一次，不改变调度节拍：
  - exec：同步在沙箱里执行并返回结果（超时按条目配置）；
  - task：按到点触发相同的幂等键提交——同一天不会因手动+自动
    产生两个任务（已提交过则返回既有任务）。`,
		Args: exactArgs(2, "用法: mindloop schedule run <身份名> <条目 id>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := c.loadIdentity(args[0])
			if err != nil {
				return c.fail(err)
			}
			schedulePath, _, _ := schedulePaths(id)
			parsed, warns, err := schedule.Load(schedulePath)
			if err != nil {
				return c.fail(fmt.Errorf("日程文件解析失败（%s）: %w", schedulePath, err))
			}
			for _, w := range warns {
				fmt.Fprintf(c.stderr, "⚠ %s\n", w)
			}
			var found *schedule.Parsed
			for i := range parsed {
				if parsed[i].Item.ID == args[1] {
					found = &parsed[i]
					break
				}
			}
			if found == nil {
				return c.fail(fmt.Errorf("没有条目 %q（mindloop schedule list %s 查看）", args[1], id.Name))
			}
			if !found.Enabled {
				fmt.Fprintf(c.stderr, "⚠ 条目 %s 已禁用；仍按手动触发执行\n", found.Item.ID)
			}
			rt := c.scheduleRuntimeFor(id)
			if found.Kind == schedule.KindExec {
				out := rt.ExecuteNow(c.ctx, *found)
				fmt.Fprintf(c.stdout, "条目 %s 手动执行：退出码 %d，用时 %s\n", found.Item.ID, out.ExitCode, out.Duration.Round(time.Millisecond))
				if out.Err != nil {
					if out.Detail != "" {
						fmt.Fprintln(c.stderr, out.Detail)
					}
					return c.fail(fmt.Errorf("执行失败: %v", out.Err))
				}
				return nil
			}
			now := time.Now()
			note, err := rt.SubmitNow(c.ctx, *found, now)
			if err != nil {
				return taskErr(err)
			}
			key := schedule.DateKey(found.Item.ID, schedule.PlannedFor(*found, now))
			if note != "" {
				fmt.Fprintf(c.stdout, "已提交任务（%s）：%s\n", key, note)
			} else {
				fmt.Fprintf(c.stdout, "已提交任务（%s）\n", key)
			}
			c.warnOffline(id, "任务已排队")
			return nil
		},
	}
	return cmd
}
