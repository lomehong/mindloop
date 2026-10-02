package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/spf13/cobra"

	"github.com/lomehong/mindloop/internal/identity"
	"github.com/lomehong/mindloop/internal/snapshot"
	"github.com/lomehong/mindloop/internal/traj"
)

// snapshotsFromEnv 读 MINDLOOP_SNAPSHOT（"0" 关闭运行级快照，其余
// 含未设置即开启）。runner.Options.Snapshots 的 wiring 侧取值。
func snapshotsFromEnv() bool { return os.Getenv("MINDLOOP_SNAPSHOT") != "0" }

// runIDRe 是可进入文件系统的运行 id 形态（与 runner 的 validRunID
// 同一约束）——undo 的参数直接拼快照目录名，必须先过这道闸。
var runIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func (c *CLI) newUndoCmd() *cobra.Command {
	var yesP, forceP bool
	var identityP, becauseP string
	cmd := &cobra.Command{
		Use:   "undo <traj> [run-id]",
		Short: "撤销一次运行的文件效果（恢复到执行前的工作目录）",
		Long: `把一次运行对工作目录的改动恢复到执行前：非只读脚本执行
前后 runner 各拍一份清单，本命令按差分变更集恢复——改写的取回旧
内容、新增的删除、删除的找回。

<traj> 是轨迹 id（或唯一前缀）；身份的根轨迹用 --identity <名字>
定位。不带 run-id 时列出该轨迹的全部快照。边界（如实声明）：快照
只覆盖运行工作目录（<轨迹>/runs/<run_id>）之内——脚本写目录外的
行为不在恢复范围；超限大文件（>4MB）只记录不备份，恢复时如实报告
"不可自动恢复"。

--because 归因（味觉证据面，perception.md Phase 4）：撤销的原因
决定它在 S 阈值校准里的权重——只有 proposal-redundant（提案本身
多余）算"阈值过紧"的证据；execution-failed/changed-mind 与感知
无关。因果混淆是校准的大敌，选因是顺手的一步。`,
		Example: `  mindloop undo runner-demo              # 列出可撤销的运行
  mindloop undo runner-demo run-01       # 恢复指定运行（会先确认）
  mindloop undo runner-demo run-01 --yes
  mindloop undo --identity ada run-01 --yes --because proposal-redundant`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.runUndo(args, identityP, yesP, forceP, becauseP)
		},
	}
	fs := cmd.Flags()
	fs.BoolVar(&yesP, "yes", false, "跳过确认")
	fs.BoolVar(&forceP, "force", false, "对已撤销过的快照强制再恢复一次")
	fs.StringVar(&identityP, "identity", "", "用身份的根轨迹定位（替代 <traj>，此时 <traj> 位置填 run-id）")
	fs.StringVar(&becauseP, "because", "", "撤销归因（味觉证据面）: proposal-redundant|execution-failed|changed-mind")
	return cmd
}

// undoCauses 是撤销归因的封闭词表：proposal-redundant 是唯一的
// "阈值过紧"证据（校准语义见 newUndoCmd 的 Long）。
var undoCauses = map[string]bool{
	"proposal-redundant": true,
	"execution-failed":   true,
	"changed-mind":       true,
}

func (c *CLI) runUndo(args []string, identityName string, yes, force bool, because string) error {
	if because != "" && !undoCauses[because] {
		return usageErr("--because 只接受 proposal-redundant|execution-failed|changed-mind")
	}
	var tl *traj.Timeline
	var runArgs []string
	switch {
	case identityName != "":
		id, err := identity.Load(identityName)
		if err != nil {
			return c.fail(err)
		}
		tl = id.Timeline
		runArgs = args
	default:
		t, err := traj.Load(args[0])
		if err != nil {
			return c.fail(err)
		}
		tl = t
		runArgs = args[1:]
	}
	snapsDir := filepath.Join(tl.Dir, "snapshots")

	// 列表模式：run-id 缺省。
	if len(runArgs) == 0 {
		return c.undoList(snapsDir)
	}

	runID := runArgs[0]
	if !runIDRe.MatchString(runID) {
		return usageErr("运行 id 只允许字母数字与 - _（不接受路径片段）")
	}
	dir := filepath.Join(snapsDir, runID)
	changes, err := snapshot.Changes(dir)
	if err != nil {
		return c.fail(fmt.Errorf("没有可撤销的快照: %w", err))
	}
	if snapshot.Undone(dir) && !force {
		return c.fail(fmt.Errorf("运行 %s 已撤销过（重复恢复可能叠加回写）；确认要再来一次加 --force", runID))
	}
	if len(changes) == 0 {
		fmt.Fprintf(c.stdout, "运行 %s 没有改动任何文件，无需撤销。\n", runID)
		return nil
	}

	// 恢复前把变更集亮出来—— undo 是写操作，人得先看见要动什么。
	fmt.Fprintf(c.stdout, "运行 %s 的变更集（%d 项）:\n", runID, len(changes))
	for _, ch := range changes {
		fmt.Fprintf(c.stdout, "  %-8s %s\n", ch.Op, ch.Rel)
	}
	if !yes && !c.initConfirm("确认恢复到执行前?") {
		fmt.Fprintln(c.stdout, "已取消，未做任何修改")
		return nil
	}
	workDir := filepath.Join(tl.Dir, "runs", runID)
	skipped, err := snapshot.Restore(dir, workDir)
	if err != nil {
		return c.fail(err)
	}
	fmt.Fprintf(c.stdout, "已恢复 %d 项 ✓\n", len(changes)-len(skipped))
	if len(skipped) > 0 {
		fmt.Fprintf(c.stdout, "⚠ 以下文件超限未备份，无法自动恢复，请人工处理:\n")
		for _, rel := range skipped {
			fmt.Fprintf(c.stdout, "  %s\n", rel)
		}
	}
	// 味觉落轨迹（--because 提供时）：审批决定文件是瞬态的，归因
	// 必须自己落轨迹才是持久证据。落盘失败不回滚恢复本身——证据
	// 是增强，不是事务的一部分。
	if because != "" {
		s := traj.NewStep(traj.TypeTaste)
		s.Fields["signal"] = "undo"
		s.Fields["run"] = runID
		s.Fields["cause"] = because
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := tl.Append(ctx, s); err != nil {
			fmt.Fprintf(c.stdout, "⚠ 味觉归因落盘失败（恢复已完成）: %v\n", err)
		}
	}
	return nil
}

// undoList 列出轨迹的全部快照（含零变更与已撤销标记）。
func (c *CLI) undoList(snapsDir string) error {
	entries, err := os.ReadDir(snapsDir)
	if err != nil {
		fmt.Fprintln(c.stdout, "还没有任何快照（快照默认开启；MINDLOOP_SNAPSHOT=0 可关闭）。")
		return nil
	}
	count := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(snapsDir, e.Name())
		changes, chErr := snapshot.Changes(dir)
		marker := ""
		if snapshot.Undone(dir) {
			marker = "（已撤销）"
		}
		if chErr != nil {
			fmt.Fprintf(c.stdout, "  %s  ⚠ 变更集损坏（%v）%s\n", e.Name(), chErr, marker)
			count++
			continue
		}
		if len(changes) == 0 {
			fmt.Fprintf(c.stdout, "  %s  未改动文件 %s\n", e.Name(), marker)
		} else {
			fmt.Fprintf(c.stdout, "  %s  %d 项变更 %s\n", e.Name(), len(changes), marker)
			for _, ch := range changes {
				fmt.Fprintf(c.stdout, "      %-8s %s\n", ch.Op, ch.Rel)
			}
		}
		count++
	}
	if count == 0 {
		fmt.Fprintln(c.stdout, "还没有任何快照。")
	}
	fmt.Fprintf(c.stdout, "\n恢复: mindloop undo <traj> <run-id>\n")
	return nil
}
