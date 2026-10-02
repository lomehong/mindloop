package cli

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"mindloop/internal/identity"
	"mindloop/internal/obs"
	"mindloop/internal/task"
	"mindloop/internal/traj"
)

func (c *CLI) newStatsCmd() *cobra.Command {
	var identityP string
	var daysP int
	var idleWindowP time.Duration
	cmd := &cobra.Command{
		Use:   "stats",
		Short: "评估基线：调用/token 分账、空转唤醒率、任务完成率",
		Long: `从用量台账（usage/llm-usage.jsonl）与任务系统派生三个基线
指标——视图皆派生，不新增任何存储：

  分账    调用与 token 按唤醒/阶段/模型分组——自发行为 vs 响应
          人类各烧多少；
  空转率  watchdog 合成唤醒的思考轮中，窗口内未跟随新运行的占比
          （台账级代理；"有效 FINAL"的精确判定待计划步骤）；
  任务    终态任务的成功占比与在途数。

不带 --identity 时聚合全部身份。窗口按台账时间戳过滤。`,
		Example: `  mindloop stats
  mindloop stats --identity ada
  mindloop stats --days 30 --idle-window 2m`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.runStats(identityP, daysP, idleWindowP)
		},
	}
	fs := cmd.Flags()
	fs.StringVar(&identityP, "identity", "", "只看单个身份")
	fs.IntVar(&daysP, "days", 7, "统计窗口（天，按台账时间戳）")
	fs.DurationVar(&idleWindowP, "idle-window", 90*time.Second, "空转判定：唤醒后多久内出现新运行算干活")
	return cmd
}

func (c *CLI) runStats(identityName string, days int, idleWindow time.Duration) error {
	names := []string{identityName}
	if identityName == "" {
		list, err := identity.List()
		if err != nil {
			return c.fail(err)
		}
		if len(list) == 0 {
			fmt.Fprintln(c.stdout, "还没有身份——跑 mindloop init 创建演示身份。")
			return nil
		}
		names = list
	}

	windowStart := time.Now().AddDate(0, 0, -days)
	fmt.Fprintf(c.stdout, "评估基线（近 %d 天，空转窗口 %s）\n\n", days, idleWindow)

	var allRows []obs.UsageRow
	shown := 0
	for _, name := range names {
		id, err := identity.Load(name)
		if err != nil {
			fmt.Fprintf(c.stdout, "== %s：加载失败（%v）==\n\n", name, err)
			continue
		}
		rows, err := obs.LoadUsage(filepath.Join(id.Dir, "usage", "llm-usage.jsonl"))
		if err != nil {
			return c.fail(err)
		}
		filtered := rowsInWindow(rows, windowStart)
		allRows = append(allRows, filtered...)

		st := obs.Derive(filtered, idleWindow)
		c.printIdentityStats(name, st, c.taskCompletion(id), obs.LoadAdmission(id.Dir))
		shown++
	}

	if shown > 1 {
		fmt.Fprintln(c.stdout, "―― 合计 ――")
		st := obs.Derive(allRows, idleWindow)
		c.printTotals(st)
	}
	return nil
}

// rowsInWindow 按台账时间戳过滤窗口；时间戳坏/空的行保留——统计
// 可以不精确到秒，但不能静默丢行。
func rowsInWindow(rows []obs.UsageRow, windowStart time.Time) []obs.UsageRow {
	var out []obs.UsageRow
	for _, r := range rows {
		if r.TS == "" {
			out = append(out, r)
			continue
		}
		ts, err := time.Parse(traj.TimeFormat, r.TS)
		if err != nil || !ts.Before(windowStart) {
			out = append(out, r)
		}
	}
	return out
}

func (c *CLI) printIdentityStats(name string, st obs.Stats, tc *taskCompletionStats, ad obs.AdmissionStatus) {
	fmt.Fprintf(c.stdout, "== %s ==\n", name)
	c.printTotals(st)
	fmt.Fprintf(c.stdout, "  按唤醒  %s\n", bucketLine(st.ByWake))
	fmt.Fprintf(c.stdout, "  按阶段  %s\n", bucketLine(st.ByPhase))
	fmt.Fprintf(c.stdout, "  按模型  %s\n", bucketLine(st.ByModel))
	if rate, ok := st.IdleRate(); ok {
		fmt.Fprintf(c.stdout, "  空转率  watchdog 唤醒 %d 轮，%d 轮未跟随运行（%.0f%%）※台账级代理\n",
			st.WatchdogWakes, st.IdleNoRun, rate*100)
	} else {
		fmt.Fprintln(c.stdout, "  空转率  （窗口内无 watchdog 唤醒）")
	}
	if tc == nil {
		fmt.Fprintln(c.stdout, "  任务    （无任务记录）")
	} else {
		fmt.Fprintf(c.stdout, "  任务    %s\n", tc.summary())
	}
	c.printBudget(ad)
	fmt.Fprintln(c.stdout)
}

// printBudget 预算与熔断现状：上限设置了才显示预算行；熔断冷却
// 无论预算与否都要喊出来。
func (c *CLI) printBudget(ad obs.AdmissionStatus) {
	if ad.DailyLimit > 0 || ad.SelfLimit > 0 {
		line := fmt.Sprintf("  预算    今日 %d", ad.UsedToday)
		if ad.DailyLimit > 0 {
			line += fmt.Sprintf("/%d", ad.DailyLimit)
		}
		if ad.SelfLimit > 0 {
			line += fmt.Sprintf("（自发 %d/%d）", ad.SelfUsedToday, ad.SelfLimit)
		}
		line += " tokens"
		if ad.CoolingUntil != "" {
			line += "｜⚠ 熔断冷却中"
		}
		fmt.Fprintln(c.stdout, line)
		return
	}
	if ad.CoolingUntil != "" {
		fmt.Fprintf(c.stdout, "  准入    ⚠ 熔断冷却中（至 %s）\n", ad.CoolingUntil)
	}
}

func (c *CLI) printTotals(st obs.Stats) {
	fmt.Fprintf(c.stdout, "  调用    %d 次（错误 %d）｜prompt %d｜completion %d tokens\n",
		st.Calls, st.Errors, st.PromptTokens, st.CompletionTokens)
}

// bucketLine 把分账桶渲染成一行：watchdog 40 次/5.0k tok、manual 16 次/…
func bucketLine(buckets map[string]*obs.Bucket) string {
	if len(buckets) == 0 {
		return "（无）"
	}
	parts := make([]string, 0, len(buckets))
	for _, name := range sortedStringKeys(buckets) {
		b := buckets[name]
		label := fmt.Sprintf("%s %d 次", name, b.Calls)
		if b.Errors > 0 {
			label += fmt.Sprintf("（错 %d）", b.Errors)
		}
		label += fmt.Sprintf("/%s tok", humanTokens(b.PromptTokens+b.CompletionTokens))
		parts = append(parts, label)
	}
	return strings.Join(parts, "、")
}

// humanTokens 粗粒度可读化：千位以上按 k 记——分账看的是量级。
func humanTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

func sortedStringKeys[M any](m map[string]M) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// taskCompletionStats 任务完成率的派生值。
type taskCompletionStats struct {
	terminal    int
	succeeded   int
	failed      int
	canceled    int
	interrupted int
	budget      int
	active      int
}

func (s *taskCompletionStats) summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "终态 %d（成 %d", s.terminal, s.succeeded)
	if s.failed > 0 {
		fmt.Fprintf(&b, " / 败 %d", s.failed)
	}
	if s.canceled > 0 {
		fmt.Fprintf(&b, " / 取消 %d", s.canceled)
	}
	if s.interrupted > 0 {
		fmt.Fprintf(&b, " / 中断 %d", s.interrupted)
	}
	if s.budget > 0 {
		fmt.Fprintf(&b, " / 超预算 %d", s.budget)
	}
	fmt.Fprintf(&b, "），在途 %d", s.active)
	if s.terminal > 0 {
		fmt.Fprintf(&b, "，完成率 %.0f%%", float64(s.succeeded)/float64(s.terminal)*100)
	}
	return b.String()
}

// taskCompletion 从任务系统派生完成率；无任务记录返回 nil。
func (c *CLI) taskCompletion(id *identity.Identity) *taskCompletionStats {
	tasks, err := task.New(id.Timeline, id.Name).List(c.ctx)
	if err != nil || len(tasks) == 0 {
		return nil
	}
	s := &taskCompletionStats{}
	for _, tk := range tasks {
		switch tk.State {
		case task.Succeeded:
			s.terminal++
			s.succeeded++
		case task.Failed:
			s.terminal++
			s.failed++
		case task.Canceled:
			s.terminal++
			s.canceled++
		case task.Interrupted:
			s.terminal++
			s.interrupted++
		case task.BudgetExceeded:
			s.terminal++
			s.budget++
		default:
			s.active++
		}
	}
	return s
}
