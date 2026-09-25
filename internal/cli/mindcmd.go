package cli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"mindloop/internal/config"
	"mindloop/internal/identity"
	"mindloop/internal/llm"
	"mindloop/internal/mind"
	"mindloop/internal/traj"
)

// mindRun 前台运行一个身份的心智（无人格化交互，适合当服务/后台
// 守护）。与人对话请用 mindloop chat ada。
func (c *CLI) newMindRunCmd() *cobra.Command {
	var pollP, watchdogP, idleBaseP, idleMaxP, thoughtCapP time.Duration
	var idleHoldP int
	var maxIterP int
	cmd := &cobra.Command{
		Use:   "run <身份名>",
		Short: "启动常驻心智（前台守护；与人对话请用 mind chat）",
		Long: `调度器跟踪根轨迹：人类消息经 mind say 注入，responder 负责回复，
monolith 负责自主行动；闲置时指数回退（每级驻留 --idle-hold 次空
唤醒再加深），活性由调度器的 watchdog 与自发性预约共同保证。
同一身份同时只允许一个调度器（运行锁）。

双模型分层：设置 MINDLOOP_REQUEST_MODEL 后，反应式唤醒（人类来话、
外部产物）走请求档模型，自发的空闲唤醒走便宜的思考档。`,
		Example: `  mindloop mind run ada --watchdog 5m
  mindloop mind run ada --idle-base 10s --idle-max 2m --idle-hold 3`,
		Args: exactArgs(1, "用法: mindloop mind run <身份名> [flags]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := c.loadIdentity(args[0])
			if err != nil {
				return c.fail(err)
			}
			// 身份级 .env：模型等配置按身份覆盖。加载顺序保证优先级
			// 为 显式环境变量 > 身份 .env > 全局 ~/.mindloop/.env
			// （LoadEnv 不覆盖已存在的进程环境变量，全局 .env 已在
			// CLI 启动时并入）。
			if err := config.LoadEnv(filepath.Join(id.Dir, ".env")); err != nil {
				fmt.Fprintf(c.stderr, "⚠ 身份 .env 加载失败: %v\n", err)
			}
			lock, owned, err := mind.TryRunLock(id.Timeline)
			if err != nil {
				return c.fail(err)
			}
			if !owned {
				return c.fail(fmt.Errorf("心智 %s 已在运行（另一个窗口或 chat 会话）", id.Name))
			}
			defer lock.Release()
			// 接管前消费上次会话残留的停机标志。
			if err := clearStopFlag(id); err != nil {
				c.mindLog("消费残留停机标志失败: %v", err)
			}

			policy := mind.BackoffPolicy{Base: idleBaseP, Max: idleMaxP, ThoughtCap: thoughtCapP, Hold: idleHoldP}
			stack, err := c.assembleMindStack(id, mindStackOpts{
				clientFactory: llm.FromEnv,
				poll:          pollP,
				watchdog:      watchdogP,
				backoff:       &policy,
				maxIterations: maxIterP,
				logger:        c.mindLog,
			})
			if err != nil {
				return c.fail(err)
			}
			fmt.Fprintf(c.stderr, "心智 %s 启动（Ctrl+C 停机）。与它对话: mindloop chat ada 或 mindloop mind say ada \"...\"\n", id.Name)
			runErr := stack.dispatcher.Run(c.ctx)
			// 在途思考收尾再释放运行锁（defer lock.Release() 在函数
			// 返回时执行）——进程退出把跑一半的 bash 与落盘硬切，
			// 正是"优雅停机"承诺要避免的。
			if !stack.dispatcher.WaitIdle(15 * time.Second) {
				fmt.Fprintln(c.stderr, "⚠ 15 秒内思考未全部收尾，放弃等待")
			}
			if runErr != nil && !errors.Is(runErr, mind.ErrStopRequested) {
				if errors.Is(runErr, context.Canceled) {
					return nil
				}
				return c.fail(runErr)
			}
			return nil
		},
	}
	fs := cmd.Flags()
	fs.DurationVar(&pollP, "poll", 200*time.Millisecond, "轨迹轮询间隔")
	fs.DurationVar(&watchdogP, "watchdog", 5*time.Minute, "watchdog 合成唤醒间隔")
	fs.DurationVar(&idleBaseP, "idle-base", 5*time.Second, "回退基础延迟")
	fs.DurationVar(&idleMaxP, "idle-max", 5*time.Minute, "回退封顶")
	fs.DurationVar(&thoughtCapP, "thought-cap", time.Minute, "思考型唤醒的回退封顶")
	fs.IntVar(&idleHoldP, "idle-hold", 3, "每级驻留的空唤醒次数（dwell，0 取默认 3）")
	fs.IntVar(&maxIterP, "max-iterations", 8, "每次唤醒的内部轮次上限")
	return cmd
}

// mindSay 是人类对身份说话的入口（Headlong 的 `ada hello` 语义）：
// 默认等待 ada 的回复并打印。消息与回复都是日志事实——正在运行的
// 心智哪怕在另一个终端，这里也能等到回复。
func (c *CLI) newMindSayCmd() *cobra.Command {
	var waitP time.Duration
	var noWaitP bool
	cmd := &cobra.Command{
		Use:   `say <身份名> "内容"`,
		Short: "对身份说一句话，等待并打印它的回复",
		Example: `  mindloop mind say ada "你好"
  mindloop mind say ada "跑个长任务" --wait 5m
  mindloop mind say ada "不用回" --no-wait`,
		Args: exactArgs(2, `用法: mindloop mind say <身份名> "内容" [--wait 90s|--no-wait]`),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := c.loadIdentity(args[0])
			if err != nil {
				return c.fail(err)
			}
			if err := mind.PostMessage(id.Timeline, "operator", id.Name, "cli", args[1]); err != nil {
				return c.fail(err)
			}
			// 回执 id：取刚落盘的最后一步（PostMessage 只回错误）。
			// 缺了它 --no-wait 少打印回执、等待路径退化为超时——
			// 都不是静默错误，可接受。
			last, lerr := id.Timeline.LastStep()
			if lerr != nil {
				return c.fail(lerr)
			}
			triggerStepID := last.StepID

			if noWaitP {
				fmt.Fprintf(c.stdout, "已投递（step %s）\n", triggerStepID)
				return nil
			}
			// 心智没在跑：投递了也没人回，直说并给出出路。
			// 只读探测——say 是旁观者，不该创建或偷取运行锁。
			if !mindRunning(id.Timeline.Dir) {
				fmt.Fprintln(c.stderr, "⚠ 心智没有在运行——消息已记录，但不会有人回复。")
				fmt.Fprintln(c.stderr, "  先启动: mindloop chat ada（或 mindloop mind run ada）")
				return exitError{code: 1}
			}
			fmt.Fprintf(c.stderr, "已投递，等待 %s 回复（%v 内）……\n", id.Name, waitP)
			reply, _, err := waitReply(id, triggerStepID, waitP)
			if err != nil {
				return c.fail(err)
			}
			if reply == "" {
				return exitError{code: 1, err: fmt.Errorf("%v 内未收到回复（心智可能在忙，稍后用 mindloop mind history ada 查看）", waitP)}
			}
			fmt.Fprintf(c.stdout, "%s> %s\n", id.Name, reply)
			return nil
		},
	}
	cmd.Flags().DurationVar(&waitP, "wait", 90*time.Second, "等待回复的时长")
	cmd.Flags().BoolVar(&noWaitP, "no-wait", false, "投递后立即返回，不等回复")
	return cmd
}

// waitReply 轮询日志，等待盖了 reply_to 章的回复，返回
// (回复内容, 回复步骤 id)。step_id 供调用方去重（tail 可能已打印）。
//
// 主体走 cursor 增量读（NewCursorAtEnd + ReadNew）：90 秒等待窗
// 只读 1 次全量 + 每次轮询读增量，长身份不再每 300ms 全文件解析。
func waitReply(id *identity.Identity, triggerStepID string, timeout time.Duration) (string, string, error) {
	find := func(steps []traj.Step) (string, string, bool) {
		for i := len(steps) - 1; i >= 0; i-- {
			s := steps[i]
			if s.Type != traj.TypeMessage {
				continue
			}
			if rt, ok := s.Field("reply_to"); ok && rt == triggerStepID {
				content, _ := s.Field("content")
				return content, s.StepID, true
			}
		}
		return "", "", false
	}
	// 起步先全扫一次：捕获"游标建立前恰好已落盘"的即时回复
	// （responder 快到不可能，但等待路径宁可多读一次不可瞎）。
	if steps, err := id.Timeline.Steps(); err == nil {
		if content, stepID, ok := find(steps); ok {
			return content, stepID, nil
		}
	}
	cursor := traj.NewCursorAtEnd(id.Timeline.Path)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		steps, err := cursor.ReadNew()
		if err == nil {
			if content, stepID, ok := find(steps); ok {
				return content, stepID, nil
			}
		}
		// 读失败不放弃：cursor 的偏移没有前进，下轮重试同一段。
		time.Sleep(300 * time.Millisecond)
	}
	return "", "", nil
}

// newMindStopCmd：一条命令优雅停机——投递停机标志，调度器在
// 下一次心跳（200ms 内）收尾退出，在途思考不被腰斩。
func (c *CLI) newMindStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop <身份名>",
		Short: "优雅停机正在运行的心智",
		Args:  exactArgs(1, "用法: mindloop mind stop <身份名>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := c.loadIdentity(args[0])
			if err != nil {
				return c.fail(err)
			}
			if err := mind.RequestStop(id.Timeline); err != nil {
				return c.fail(err)
			}
			// 等待退出用只读探测：等待方偷锁会跟正在收尾的调度器
			// 竞争属主文件。
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				if !mindRunning(id.Timeline.Dir) {
					fmt.Fprintf(c.stdout, "心智 %s 已停机\n", id.Name)
					return nil
				}
				time.Sleep(200 * time.Millisecond)
			}
			if pid := mindOwnerPID(id.Timeline.Dir); pid > 0 {
				return c.fail(fmt.Errorf("等待停机超时（调度器可能卡在长任务上；属主 pid=%d，必要时可 taskkill /PID %d /F 强制结束）", pid, pid))
			}
			return c.fail(fmt.Errorf("等待停机超时（调度器可能卡在长任务上；可从运行锁 owner.json 查属主 pid 后强制结束）"))
		},
	}
}

// newMindHistoryCmd 是对话的只读视图（交互式对话在 mind chat）。
func (c *CLI) newMindHistoryCmd() *cobra.Command {
	var nP int
	var personP string
	cmd := &cobra.Command{
		Use:   "history <身份名>",
		Short: "查看身份的对话记录",
		Args:  exactArgs(1, "用法: mindloop mind history <身份名> [--person P] [-n N]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := c.loadIdentity(args[0])
			if err != nil {
				return c.fail(err)
			}
			steps, err := id.Timeline.Steps()
			if err != nil {
				return c.fail(err)
			}
			type line struct{ ts, from, to, content string }
			var out []line
			for _, s := range steps {
				if s.Type != traj.TypeMessage {
					continue
				}
				from, _ := s.Field("from")
				to, _ := s.Field("to")
				if personP != "" && from != personP && to != personP {
					continue
				}
				content, _ := s.Field("content")
				out = append(out, line{s.TS, from, to, content})
			}
			if nP > 0 && len(out) > nP {
				out = out[len(out)-nP:]
			}
			for _, l := range out {
				fmt.Fprintf(c.stdout, "[%s] %s → %s: %s\n",
					strings.Replace(l.ts, "T", " ", 1), l.from, l.to, traj.OneLine(l.content, 160))
			}
			return nil
		},
	}
	cmd.Flags().IntVarP(&nP, "num", "n", 30, "最近 N 条（0 = 全部）")
	cmd.Flags().StringVar(&personP, "person", "", "只看与某人的对话")
	return cmd
}

// oneLineLocal 压成单行供终端实时输出：换行转空格（比 traj.OneLine
// 的 "\n" 字面转义更省横向空间）+ 按 rune 截断。
func oneLineLocal(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	r := []rune(s)
	if len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}

func (c *CLI) newMindStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status <身份名>",
		Short: "身份与最近活动的概览",
		Args:  exactArgs(1, "用法: mindloop mind status <身份名>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := c.loadIdentity(args[0])
			if err != nil {
				return c.fail(err)
			}
			// 只读探测：status 是旁观命令，不建锁不偷锁。
			running := "未运行（mindloop chat ada 启动）"
			if mindRunning(id.Timeline.Dir) {
				running = "运行中"
			}
			steps, err := id.Timeline.Steps()
			if err != nil {
				return c.fail(err)
			}
			fmt.Fprintf(c.stdout, "身份: %s\n状态: %s\n根轨迹: %s\n步骤数: %d\n", id.Name, running, id.Timeline.Path, len(steps))
			tail := steps
			if len(tail) > 5 {
				tail = tail[len(tail)-5:]
			}
			for _, s := range tail {
				printPretty(c.stdout, s, false)
			}
			return nil
		},
	}
}
