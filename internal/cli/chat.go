package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"mindloop/internal/config"
	"mindloop/internal/llm"
	"mindloop/internal/mind"
	"mindloop/internal/obs"
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

// newChatClient 构造模型客户端；未配置 key 时降级为 echo 占位并
// 给出醒目提示——"先能对话，再谈智能"比直接报错更符合预期。
func (c *CLI) newChatClient() *llm.Client {
	client, err := llm.FromEnv()
	if err != nil {
		fmt.Fprintln(c.stderr, "⚠ 未配置模型（编辑 ~\\.mindloop\\.env 填入 MINDLOOP_MODEL 与 MINDLOOP_API_KEY）——ada 处于占位模式，回复为固定内容")
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

// Pending 报告提示符是否还挂着（兜底超时回调用，避免在回复到达
// 后又补画一条假的提示符）。
func (p *promptWriter) Pending() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pending
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
	// 放行失败类日志，日常动态由回复与行动步骤自己呈现。
	chatLogger := func(format string, args ...any) {
		msg := fmt.Sprintf(format, args...)
		if strings.Contains(msg, "失败") || strings.Contains(msg, "错误") {
			term.Linef("· %s", msg)
		}
	}

	// 单实例：锁空闲则接管启动心智；已被持有则只接入对话。
	lock, owned, err := mind.TryRunLock(id.Timeline)
	if err != nil {
		return c.fail(err)
	}
	defer lock.Release()

	if owned {
		client := c.newChatClient()
		client.OnDone = obs.UsageRecorder(id.Dir, client.Model, client.Provider, c.mindLog)
		requestClient := requestTierClient(client, id.Dir, c.mindLog)
		dispatchCtx, cancelDispatch := context.WithCancel(c.ctx)
		dispatcher := mind.NewDispatcher(id.Timeline, 200*time.Millisecond)
		dispatcher.SetLogger(chatLogger)
		persona, _ := id.Persona()
		dispatcher.Register(mind.NewMonolith(mind.MonolithOptions{
			Timeline:       id.Timeline,
			Thinker:        mind.LLMThinker{Client: client},
			RequestThinker: mind.LLMThinker{Client: requestClient},
			Persona:        persona,
			MemDir:         id.Dir + "/memories",
			EnableRecap:    true,
			Watchdog:       watchdog,
			SetLogger:      chatLogger,
		}))
		dispatcher.Register(mind.NewResponder(mind.ResponderOptions{
			Timeline: id.Timeline,
			Thinker:  mind.LLMThinker{Client: client},
			SelfName: id.Name,
			Persona:  persona,
		}))
		go dispatcher.Run(dispatchCtx)
		// 优雅停机：输入循环退出后先停心跳，再等在途思考收尾——
		// 声明在 defer lock.Release() 之后，LIFO 保证 join 先于
		// 释放运行锁，跑一半的 bash 与落盘不被腰斩。
		defer func() {
			cancelDispatch()
			if !dispatcher.WaitIdle(10 * time.Second) {
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
		for {
			select {
			case <-tailCtx.Done():
				return
			case <-tick.C:
				steps, err := cursor.ReadNew()
				if err != nil {
					return
				}
				for _, s := range steps {
					from, _ := s.Field("from")
					switch s.Type {
					case "message":
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
						printLine("%s> %s", id.Name, oneLineLocal(content, 400))
					case "action":
						if content, ok := s.Field("content"); ok {
							term.Set(promptWorking)
							printLine("  [%s 行动] %s", id.Name, oneLineLocal(content, 200))
						}
					case "error":
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
		close(input)
	}()

	lastSent := ""
	for {
		select {
		case <-c.ctx.Done():
			term.Finish()
			return nil
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
							printLine("%s> %s", id.Name, oneLineLocal(content, 400))
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
			s := traj.NewStep("message")
			s.Fields["from"] = "operator"
			s.Fields["to"] = id.Name
			s.Fields["source"] = "chat"
			s.Fields["content"] = line
			if err := id.Timeline.Append(c.ctx, s); err != nil {
				return c.fail(err)
			}
			lastSent = s.StepID
			// 发送后不画提示符：异步回复经 Linef 自然补回。
			// 兜底：30 秒仍无回复时恢复"工作态"提示符（让用户能继续输入）。
			time.AfterFunc(30*time.Second, func() {
				if term.Pending() {
					term.Plainf("（心跳超时，无回复，按 enter 重发 / Ctrl+C 退出）")
					term.Show()
				}
			})
		}
	}
}

// mindLog 是 run 模式共用的心智日志行前缀（无提示符联动）。
func (c *CLI) mindLog(format string, args ...any) {
	fmt.Fprintf(c.stderr, "· "+format+"\n", args...)
}
