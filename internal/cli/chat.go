package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"mindloop/internal/config"
	"mindloop/internal/identity"
	"mindloop/internal/llm"
	"mindloop/internal/mind"
	"mindloop/internal/traj"
)

func (c *CLI) newMindCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mind <身份名>",
		Short: "持久心智：对话、运行、停机、状态",
	}
	cmd.AddCommand(
		c.newMindChatCmd(),
		c.newMindRunCmd(),
		c.newMindSayCmd(),
		c.newMindStopCmd(),
		c.newMindStatusCmd(),
		c.newMindHistoryCmd(),
	)
	return cmd
}

// newRootChatCmd 是根级快捷入口：mindloop chat ada 与
// mindloop mind chat ada 等价——与人对话是这个产品最高频的动作，
// 值得一个顶级位置。
func (c *CLI) newRootChatCmd() *cobra.Command {
	cmd := c.newMindChatCmd()
	cmd.Use = "chat <身份名>"
	cmd.Short = "与身份交互对话（= mind chat，推荐入口）"
	return cmd
}

// newChatClient 构造模型客户端；配置不可用（未配置、providers.json
// 损坏等）时降级为 echo 占位并给出醒目提示——"先能对话，再谈
// 智能"比直接报错更符合预期。
func (c *CLI) newChatClient(id *identity.Identity) *llm.Client {
	client, err := thinkClient(id.Dir)
	if err != nil {
		fmt.Fprintf(c.stderr, "⚠ %v\n", err)
		fmt.Fprintf(c.stderr, "  编辑 ~\\.mindloop\\.env 或在配置页添加提供商档案；%s 处于占位模式，回复为固定内容\n", id.Name)
		return &llm.Client{Provider: llm.ProviderEcho}
	}
	return client
}

func (c *CLI) newMindChatCmd() *cobra.Command {
	var watchdog time.Duration
	cmd := &cobra.Command{
		Use:   `chat <身份名>`,
		Short: "与身份交互对话（心智没在跑会自动启动；推荐入口）",
		Long: `一条命令完成一切：心智没在跑就自动启动（本窗口接管，
退出时心智优雅停机），已在跑就只接入对话。输入消息回车即发送，
ada 的回复与它干活的进度实时显示。

会话内命令: /exit 或 exit 退出。

提示符:
  你>  等你打字——消息回车后这条提示符消失
  …    ada 自己醒了或还在处理——你可以开始打字`,
		Example: `  mindloop chat ada
  你> 介绍一下你自己
  ada> 我是 ada……`,
		Args: exactArgs(1, "用法: mindloop chat <身份名>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.runChat(args[0], watchdog)
		},
	}
	cmd.Flags().DurationVar(&watchdog, "watchdog", 5*time.Minute, "watchdog 合成唤醒间隔")
	return cmd
}

// promptMode 描述当前终端里提示符的语义：
//   - promptYou:      期待用户输入（"你> "）
//   - promptWorking:  心智自己醒了或还在处理（"…"）
//
// 切换时机：
//   - 用户按回车后  → promptWorking（不再假设他马上打字）
//   - 异步 ada 输出  → promptWorking（让他看到是谁在说话）
//   - 手动 Show()    → 沿用当前 mode（让用户继续输入或观察）
//
// 两种 mode 各自画对应提示符——消除"系统自动触发的提示符也长成
// 你>"的语义歧义（这一版 UX 反馈的真实来源）。
type promptMode int

const (
	promptYou promptMode = iota
	promptWorking
)

var (
	promptForYou     = "你> "
	promptForWorking = "…"
)

// promptWriter 管理提示符与异步输出共占一个终端的问题：异步输出
// 到达时先折行、输出完按当前 mode 补画提示符。首版发布的真实反馈：
// 提示符被异步输出顶掉 → 用户面对空行不知道能不能继续输入。
type promptWriter struct {
	mu      sync.Mutex
	w       io.Writer
	mode    promptMode
	pending bool
}

func newPromptWriter(w io.Writer, mode promptMode) *promptWriter {
	return &promptWriter{w: w, mode: mode}
}

// Set 切换提示符语义（消息发出/收到回复时调用）。
func (p *promptWriter) Set(mode promptMode) {
	p.mu.Lock()
	p.mode = mode
	p.mu.Unlock()
}

// Mode 返回当前 mode（供输入循环查询，刚发的消息把 mode 切成
// working，这样心跳到达后才会看到"…"而非"你>"）。
func (p *promptWriter) Mode() promptMode {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.mode
}

// Show 画出当前 mode 的提示符。
func (p *promptWriter) Show() {
	p.mu.Lock()
	p.pending = true
	if p.mode == promptYou {
		fmt.Fprint(p.w, promptForYou)
	} else {
		fmt.Fprint(p.w, promptForWorking)
	}
	p.mu.Unlock()
}

// InputConsumed 在用户按过回车后调用：终端已换行，提示符状态复位。
func (p *promptWriter) InputConsumed() {
	p.mu.Lock()
	p.pending = false
	p.mu.Unlock()
}

// Linef 打印一行异步输出；若提示符挂起则先折行，打印完按当前 mode
// 补回提示符。
func (p *promptWriter) Linef(format string, args ...any) {
	p.mu.Lock()
	if p.pending {
		fmt.Fprintln(p.w)
		p.pending = false
	}
	fmt.Fprintf(p.w, format+"\n", args...)
	p.mu.Unlock()
	p.Show()
}

// Plainf 打印不带提示符联动的普通行（横幅、退出语）。
func (p *promptWriter) Plainf(format string, args ...any) {
	p.mu.Lock()
	if p.pending {
		fmt.Fprintln(p.w)
		p.pending = false
	}
	fmt.Fprintf(p.w, format+"\n", args...)
	p.mu.Unlock()
}

// Finish 收尾：若提示符还挂着，折行把光标还给 shell。
func (p *promptWriter) Finish() {
	p.mu.Lock()
	if p.pending {
		fmt.Fprintln(p.w)
		p.pending = false
	}
	p.mu.Unlock()
}

// Pending 报告提示符是否还挂着。异步输出（回复/行动/错误）经
// Linef 落屏后必然挂着提示符——因此"挂着"意味着窗口内有过输出；
// 静默兜底计时器据此决定要不要告警。
func (p *promptWriter) Pending() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pending
}

// silenceGrace 是消息发出后等待任何动静的兜底窗口：到点仍无回复
// 或动态时，用户面对的是一行空白——需要一句说明和可以继续输入的
// 提示符。
const silenceGrace = 30 * time.Second

// armSilenceFallback 启动静默兜底计时：到点若提示符未挂起（窗口内
// 确实没有任何输出落屏），打印说明并恢复"工作态"提示符。判据方向
// 不能反——回复到达必走 Linef→Show 把提示符挂起，若以"挂起"为
// 触发条件，每条正常回复之后都会弹一条"无回复"假警报（线上实际
// 事故）。d 参数化供测试注入短窗口。
func armSilenceFallback(term *promptWriter, d time.Duration) *time.Timer {
	return time.AfterFunc(d, func() {
		if term.Pending() {
			return // 提示符挂着 = 已有输出落屏，不是静默
		}
		term.Plainf("（%d 秒未见回复或动态；可能仍在处理，可直接继续输入，Ctrl+C 退出）", int(d.Seconds()))
		term.Show()
	})
}

// replyLine 渲染一条回复的终端显示行：署名 + 全文原样（含换行）。
// 回复是对话的核心交付物，不截断、不压平——400 字截断曾把 444 字
// 的回复截成"……命…"丢给用户；行动/错误等辅助动态仍走一行摘要。
func replyLine(name, content string) string {
	return name + "> " + content
}

// runChat 交互对话主循环。
func (c *CLI) runChat(name string, watchdog time.Duration) error {
	id, err := c.loadIdentity(name)
	if err != nil {
		return c.fail(err)
	}
	// 身份级 .env：模型等配置按身份覆盖（显式环境变量 > 身份
	// .env > 全局 .env，LoadEnv 不覆盖已存在的进程环境变量）。
	if err := config.LoadEnv(filepath.Join(id.Dir, ".env")); err != nil {
		fmt.Fprintf(c.stderr, "⚠ 身份 .env 加载失败: %v\n", err)
	}
	term := newPromptWriter(c.stderr, promptYou)

	// 调度器的流水账（收到唤醒/已回复/启动停机）在交互对话里是
	// 噪音——每条消息会搅出好几个多余的提示符重绘。chat 模式只
	// 放行失败类与审批类日志，日常动态由回复与行动步骤自己呈现。
	chatLogger := func(format string, args ...any) {
		msg := fmt.Sprintf(format, args...)
		if strings.Contains(msg, "失败") || strings.Contains(msg, "错误") || strings.Contains(msg, "批准") {
			term.Linef("· %s", msg)
		}
	}

	// 单实例：锁空闲则接管启动心智；已被持有则只接入对话。
	lock, owned, err := mind.TryRunLock(id.Timeline)
	if err != nil {
		return c.fail(err)
	}
	defer lock.Release()

	// dispatchDone 只在接管模式非 nil：调度器退出（含恢复失败等启动
	// 错误）后主循环必须把它摆到屏幕上，不能静默吞掉。
	var dispatchDone chan error
	if owned {
		stack, aerr := c.assembleMindStack(id, mindStackOpts{
			// chat 的底线是"先能对话"：未配置模型时降级 echo。
			clientFactory: func() (*llm.Client, error) { return c.newChatClient(id), nil },
			poll:          200 * time.Millisecond,
			watchdog:      watchdog,
			logger:        chatLogger,
		})
		if aerr != nil {
			term.Finish()
			return c.fail(aerr)
		}
		if err := clearStopFlag(id); err != nil {
			term.Plainf("⚠ 消费残留停机标志失败: %v", err)
		}
		dispatchCtx, cancelDispatch := context.WithCancel(c.ctx)
		dispatchDone = make(chan error, 1)
		// 持锁启动：持久任务恢复只允许在身份运行权下进行。
		go func() { dispatchDone <- stack.dispatcher.RunOwned(dispatchCtx, lock) }()
		// 优雅停机：输入循环退出后先停心跳，再等在途思考收尾——
		// 声明在 defer lock.Release() 之后，LIFO 保证 join 先于
		// 释放运行锁；真释放仍由在途执行退出驱动（Release 只请求）。
		defer func() {
			cancelDispatch()
			if !stack.dispatcher.WaitIdle(10 * time.Second) {
				term.Plainf("⚠ 10 秒内思考未全部收尾，放弃等待直接停机")
			}
		}()
		term.Plainf("心智 %s 已启动（本窗口接管；exit 或 Ctrl+C 退出并停机）", id.Name)
	} else {
		term.Plainf("心智 %s 已在运行，本窗口接入对话（停机请用 mind stop）", id.Name)
	}

	// 实时显示 ada 的动态：回复、行动结论、错误。自己输入的
	// 消息不重复打印。printedMu/lastPrintedReply 供 EOF 等待去重。
	tailCtx, stopTail := context.WithCancel(c.ctx)
	defer stopTail()
	var printedMu sync.Mutex
	lastPrintedReply := ""
	printLine := func(format string, args ...any) { term.Linef(format, args...) }
	go func() {
		cursor := traj.NewCursorAtEnd(id.Timeline.Path)
		tick := time.NewTicker(150 * time.Millisecond)
		defer tick.Stop()
		var lastWarn time.Time
		for {
			select {
			case <-tailCtx.Done():
				return
			case <-tick.C:
				steps, err := cursor.ReadNew()
				if err != nil {
					// 读失败不再静默放弃——那会让对话视图从此
					// 停摆。限频警告并继续重试。
					if time.Since(lastWarn) > 5*time.Second {
						lastWarn = time.Now()
						term.Plainf("⚠ 读取新步骤失败（继续重试）: %v", err)
					}
					continue
				}
				for _, s := range steps {
					from, _ := s.Field("from")
					switch s.Type {
					case traj.TypeMessage:
						if from == "operator" {
							continue // 自己说的话终端里已经有了
						}
						content, _ := s.Field("content")
						// EOF 兜底可能已打印过同一条回复：双向去重。
						printedMu.Lock()
						already := lastPrintedReply == s.StepID
						if !already {
							lastPrintedReply = s.StepID
						}
						printedMu.Unlock()
						if already {
							continue
						}
						// 心智自己的产出 → 切到 working 提示符，
						// 再画：代表"还在持续活动"，你此刻打字会被
						// 下一次心跳打断或回应。
						term.Set(promptWorking)
						printLine("%s", replyLine(id.Name, content))
					case traj.TypeAction:
						if content, ok := s.Field("content"); ok {
							term.Set(promptWorking)
							printLine("  [%s 行动] %s", id.Name, oneLineLocal(content, 200))
						}
					case traj.TypeError:
						if content, ok := s.Field("content"); ok {
							printLine("  [错误] %s", oneLineLocal(content, 200))
						}
					}
				}
			}
		}
	}()

	term.Plainf("输入消息回车发送；/exit 退出。")
	term.Show()

	input := make(chan string)
	go func() {
		sc := bufio.NewScanner(os.Stdin)
		for sc.Scan() {
			input <- sc.Text()
		}
		if err := sc.Err(); err != nil {
			printLine("⚠ 读取输入失败: %v", err)
		}
		close(input)
	}()

	lastSent := ""
	for {
		select {
		case <-c.ctx.Done():
			term.Finish()
			return nil
		case derr := <-dispatchDone:
			// 调度器退出只影响后台心智，不踢掉对话窗口：把原因说
			// 清楚（恢复失败/运行错误绝不能静默消失），用户仍可
			// 正常 /exit。
			dispatchDone = nil
			switch {
			case derr == nil || errors.Is(derr, context.Canceled):
			case errors.Is(derr, mind.ErrStopRequested):
				term.Plainf("· 心智已停机（本窗口仅剩对话；重启: mindloop chat %s）", id.Name)
			default:
				term.Plainf("⚠ 心智调度器已退出（本窗口仅剩对话）: %v", derr)
			}
		case line, ok := <-input:
			if !ok {
				// stdin 结束（管道/文件输入）：最后一条消息如果
				// 刚发出，等它的回复到再走——否则管道方式永远
				// 看不到回答。
				if lastSent != "" {
					if content, stepID, _ := waitReply(id, lastSent, 8*time.Second); content != "" {
						printedMu.Lock()
						already := lastPrintedReply == stepID
						if !already {
							lastPrintedReply = stepID
						}
						printedMu.Unlock()
						if !already {
							printLine("%s", replyLine(id.Name, content))
						}
					}
				}
				term.Finish()
				return nil
			}
			line = strings.TrimSpace(line)
			// 你按了回车：等输入的语义结束——切到 working 状态，
			// 接下来任何异步输出都将以"…"而非"你>"开头。
			term.InputConsumed()
			term.Set(promptWorking)
			if line == "" {
				term.Show()
				continue
			}
			if line == "/exit" || line == "exit" || line == "quit" {
				term.Plainf("再见。")
				return nil
			}
			if err := mind.PostMessage(id.Timeline, "operator", id.Name, "chat", line); err != nil {
				return c.fail(err)
			}
			// PostMessage 只回错误；回执 id 取刚落盘的最后一步，
			// 供 EOF 等待路径去重（读失败降级为不去重，仅少一次兜底）。
			if last, lerr := id.Timeline.LastStep(); lerr == nil {
				lastSent = last.StepID
			}
			// 发送后不画提示符：异步回复经 Linef 自然补回。
			// 兜底：窗口内没有任何动静时说明原因并恢复"工作态"
			// 提示符（判据方向见 armSilenceFallback）。
			armSilenceFallback(term, silenceGrace)
		}
	}
}

// mindLog 是 run 模式共用的心智日志行前缀（无提示符联动）。
func (c *CLI) mindLog(format string, args ...any) {
	fmt.Fprintf(c.stderr, "· "+format+"\n", args...)
}
