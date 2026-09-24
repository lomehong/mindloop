package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"mindloop/internal/identity"
	"mindloop/internal/llm"
	"mindloop/internal/mind"
	"mindloop/internal/obs"
	"mindloop/internal/traj"
)

// mindRun 前台运行一个身份的心智（无人格化交互，适合当服务/后台
// 守护）。与人对话请用 mindloop chat ada。
func (c *CLI) newMindRunCmd() *cobra.Command {
	var pollP, watchdogP, idleBaseP, idleMaxP, thoughtCapP time.Duration
	var maxIterP int
	cmd := &cobra.Command{
		Use:   "run <身份名>",
		Short: "启动常驻心智（前台守护；与人对话请用 mind chat）",
		Long: `调度器跟踪根轨迹：人类消息经 mind say 注入，responder 负责回复，
monolith 负责自主行动；闲置时指数回退，活性由调度器的 watchdog 与
自发性预约共同保证。同一身份同时只允许一个调度器（运行锁）。`,
		Example: `  mindloop mind run ada --watchdog 5m
  mindloop mind run ada --idle-base 10s --idle-max 2m`,
		Args: exactArgs(1, "用法: mindloop mind run <身份名> [flags]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := c.loadIdentity(args[0])
			if err != nil {
				return c.fail(err)
			}
			lock, owned, err := mind.TryRunLock(id.Timeline)
			if err != nil {
				return c.fail(err)
			}
			if !owned {
				return c.fail(fmt.Errorf("心智 %s 已在运行（另一个窗口或 chat 会话）", id.Name))
			}
			defer lock.Release()

			client, err := llm.FromEnv()
			if err != nil {
				return c.fail(err)
			}
			// 观测面：用量台账 + 健康标记（<身份目录>/ 下）。
			client.OnDone = obs.UsageRecorder(id.Dir, client.Model, client.Provider)

			policy := mind.BackoffPolicy{Base: idleBaseP, Max: idleMaxP, ThoughtCap: thoughtCapP}
			dispatcher := mind.NewDispatcher(id.Timeline, pollP)
			dispatcher.SetLogger(c.mindLog)
			persona, err := id.Persona()
			if err != nil {
				return c.fail(err)
			}
			dispatcher.Register(mind.NewMonolith(mind.MonolithOptions{
				Timeline:      id.Timeline,
				Thinker:       llmThinker{c: client},
				Backoff:       &policy,
				MaxIterations: maxIterP,
				Watchdog:      watchdogP,
				Persona:       persona,
				MemDir:        id.Dir + "/memories",
				EnableRecap:   true,
				SetLogger:     c.mindLog,
			}))
			dispatcher.Register(mind.NewResponder(mind.ResponderOptions{
				Timeline: id.Timeline,
				Thinker:  llmThinker{c: client},
				SelfName: id.Name,
				Persona:  persona,
			}))
			fmt.Fprintf(c.stderr, "心智 %s 启动（Ctrl+C 停机）。与它对话: mindloop chat ada 或 mindloop mind say ada \"...\"\n", id.Name)
			if err := dispatcher.Run(c.ctx); err != nil && !errors.Is(err, mind.ErrStopRequested) {
				if errors.Is(err, context.Canceled) {
					return nil
				}
				return c.fail(err)
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
			s := traj.NewStep("message")
			s.Fields["from"] = "operator"
			s.Fields["to"] = id.Name
			s.Fields["source"] = "cli"
			s.Fields["content"] = args[1]
			if err := id.Timeline.Append(c.ctx, s); err != nil {
				return c.fail(err)
			}

			if noWaitP {
				fmt.Fprintf(c.stdout, "已投递（step %s）\n", s.StepID)
				return nil
			}
			// 心智没在跑：投递了也没人回，直说并给出出路。
			lock, owned, err := mind.TryRunLock(id.Timeline)
			if err != nil {
				return c.fail(err)
			}
			if owned {
				lock.Release()
				fmt.Fprintln(c.stderr, "⚠ 心智没有在运行——消息已记录，但不会有人回复。")
				fmt.Fprintln(c.stderr, "  先启动: mindloop chat ada（或 mindloop mind run ada）")
				return exitError{code: 1}
			}
			fmt.Fprintf(c.stderr, "已投递，等待 %s 回复（%v 内）……\n", id.Name, waitP)
			reply, _, err := waitReply(id, s.StepID, waitP)
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
func waitReply(id *identity.Identity, triggerStepID string, timeout time.Duration) (string, string, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		steps, err := id.Timeline.Steps()
		if err == nil {
			for i := len(steps) - 1; i >= 0; i-- {
				s := steps[i]
				if s.Type != "message" {
					continue
				}
				if rt, ok := s.Field("reply_to"); ok && rt == triggerStepID {
					content, _ := s.Field("content")
					return content, s.StepID, nil
				}
			}
		}
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
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				lock, owned, err := mind.TryRunLock(id.Timeline)
				if err != nil {
					return c.fail(err)
				}
				if owned {
					lock.Release()
					fmt.Fprintf(c.stdout, "心智 %s 已停机\n", id.Name)
					return nil
				}
				time.Sleep(200 * time.Millisecond)
			}
			return c.fail(fmt.Errorf("等待停机超时（调度器可能卡在长任务上，可 taskkill /F /IM mindloop.exe 强制结束）"))
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
				if s.Type != "message" {
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
					replaceFirstT(l.ts), l.from, l.to, oneLineLocal(l.content, 160))
			}
			return nil
		},
	}
	cmd.Flags().IntVarP(&nP, "num", "n", 30, "最近 N 条（0 = 全部）")
	cmd.Flags().StringVar(&personP, "person", "", "只看与某人的对话")
	return cmd
}

func replaceFirstT(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == 'T' {
			return s[:i] + " " + s[i+1:]
		}
	}
	return s
}

func oneLineLocal(s string, max int) string {
	s = replaceNewlines(s)
	r := []rune(s)
	if len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}

func replaceNewlines(s string) string {
	out := []rune(s)
	for i, r := range out {
		if r == '\n' {
			out[i] = ' '
		}
	}
	return string(out)
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
			lock, owned, err := mind.TryRunLock(id.Timeline)
			if err != nil {
				return c.fail(err)
			}
			running := "未运行（mindloop chat ada 启动）"
			if !owned {
				running = "运行中"
			} else {
				lock.Release()
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
