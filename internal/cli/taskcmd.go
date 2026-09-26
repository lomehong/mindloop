package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"mindloop/internal/identity"
	"mindloop/internal/ids"
	"mindloop/internal/task"
)

// task 命令组是显式委托的用户入口：任务事实全部来自根轨迹投影，
// CLI 只负责读写事实与呈现——状态推断权在 task 包的状态机，UI 无权
// 根据日志任意一行推断成功。
func (c *CLI) newTaskCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "task",
		Short: "显式委托：提交、查看、等待、取消、重试",
		Long: `任务提交复用根轨迹：一条盖章的入站消息落盘即 queued 事实，
后续状态由任务事件投影，不修改旧日志行。

心智在运行时自动领取排队任务；停机提交也会在下次启动取得运行锁
后自动恢复。普通聊天不是执行授权——需要执行请显式提交任务。`,
	}
	cmd.AddCommand(
		c.newTaskSubmitCmd(),
		c.newTaskListCmd(),
		c.newTaskShowCmd(),
		c.newTaskWaitCmd(),
		c.newTaskCmdCommand("cancel"),
		c.newTaskCmdCommand("retry"),
	)
	return cmd
}

// openTaskStore 解析身份并打开任务投影入口。
func (c *CLI) openTaskStore(name string) (*identity.Identity, *task.Store, error) {
	id, err := c.loadIdentity(name)
	if err != nil {
		return nil, nil, err
	}
	return id, task.New(id.Timeline, id.Name), nil
}

// resolveTaskPrefix 用前缀定位任务：短 id 可复制，完整 id 精确匹配。
// 前缀歧义要求更长的输入——猜测是数据错误，不是便利。
func resolveTaskPrefix(ctx context.Context, store *task.Store, prefix string) (task.Task, error) {
	if strings.TrimSpace(prefix) == "" {
		return task.Task{}, task.ErrNotFound
	}
	all, err := store.List(ctx)
	if err != nil {
		return task.Task{}, err
	}
	var matches []task.Task
	for _, item := range all {
		if item.ID == prefix || strings.HasPrefix(item.ID, prefix) {
			matches = append(matches, item)
		}
	}
	switch len(matches) {
	case 0:
		return task.Task{}, task.ErrNotFound
	case 1:
		return matches[0], nil
	default:
		return task.Task{}, fmt.Errorf("前缀 %q 匹配 %d 个任务，请用更长的前缀", prefix, len(matches))
	}
}

// taskTerminal 报告任务是否已处于终态（等待不再有意义）。
func taskTerminal(state task.State) bool {
	switch state {
	case task.Succeeded, task.Failed, task.Canceled, task.Interrupted, task.BudgetExceeded:
		return true
	}
	return false
}

// taskErr 把任务域错误翻译为可执行的下一步提示。
func taskErr(err error) error {
	switch {
	case errors.Is(err, task.ErrNotFound):
		return exitError{code: 1, err: errors.New("任务不存在，用 mindloop task list 查看")}
	case errors.Is(err, task.ErrConflict):
		return exitError{code: 1, err: errors.New("幂等键冲突：同一键已用于不同请求，换一个 --client-message-id/--request-id")}
	case errors.Is(err, task.ErrTransition):
		return exitError{code: 1, err: errors.New("任务当前状态不允许此操作，用 mindloop task show 查看最新状态")}
	case errors.Is(err, task.ErrInvalid):
		return exitError{code: 1, err: errors.New("任务请求字段无效")}
	case errors.Is(err, task.ErrCorrupt):
		return exitError{code: 1, err: errors.New("任务轨迹事实损坏，拒绝推断或追加状态")}
	case errors.Is(err, task.ErrBusy):
		return exitError{code: 1, err: errors.New("已有执行中的任务占用执行槽")}
	default:
		return exitError{code: 1, err: err}
	}
}

// printTaskJSON 输出机器可读任务 JSON（缩进、不转义 HTML）。
func (c *CLI) printTaskJSON(v any) error {
	enc := json.NewEncoder(c.stdout)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// printTaskSummary 输出人类可读任务摘要；完整 id 首行给出以便复制。
func (c *CLI) printTaskSummary(item task.Task) {
	fmt.Fprintf(c.stdout, "任务 %s\n", item.ID)
	fmt.Fprintf(c.stdout, "  状态: %s（attempt %d）\n", item.State, item.Attempt)
	fmt.Fprintf(c.stdout, "  发起: %s（client_message_id: %s）\n", item.From, item.ClientMessageID)
	fmt.Fprintf(c.stdout, "  提交: %s  更新: %s\n", shortTS(item.CreatedAt), shortTS(item.UpdatedAt))
	fmt.Fprintf(c.stdout, "  内容: %s\n", oneLineLocal(item.Content, 200))
	if item.SourceStepID != "" {
		fmt.Fprintf(c.stdout, "  来源消息: %s\n", item.SourceStepID)
	}
	if item.RunID != "" {
		fmt.Fprintf(c.stdout, "  运行: %s\n", item.RunID)
	}
	if item.Result != "" {
		fmt.Fprintf(c.stdout, "  结果: %s\n", oneLineLocal(item.Result, 500))
	}
	if item.ResultKind != "" {
		fmt.Fprintf(c.stdout, "  结果来源: %s\n", item.ResultKind)
	}
	if item.Reason != "" {
		fmt.Fprintf(c.stdout, "  原因: %s\n", oneLineLocal(item.Reason, 500))
	}
	if len(item.EvidenceStepIDs) > 0 {
		fmt.Fprintf(c.stdout, "  证据: %s\n", strings.Join(item.EvidenceStepIDs, ", "))
	}
}

// shortTS 把 RFC3339 时间戳压到分钟精度（终端列表用）。
func shortTS(ts string) string {
	if len(ts) >= 16 {
		return ts[:10] + " " + ts[11:16]
	}
	return ts
}

// warnOffline 在任务已落盘但心智未运行时给出提示：不失败——
// 停机提交是合法场景（重启后自动恢复）。
func (c *CLI) warnOffline(id *identity.Identity, what string) {
	if !mindRunning(id.Timeline.Dir) {
		fmt.Fprintf(c.stderr, "⚠ 心智没有在运行——%s，启动后会自动执行（mindloop chat %s）\n", what, id.Name)
	}
}

func (c *CLI) newTaskSubmitCmd() *cobra.Command {
	var clientMessageID, from, sourceStep string
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   `submit <身份名> [内容]`,
		Short: "提交一个显式任务（落盘即事实，返回任务 JSON）",
		Long: `任务提交是一次持久化事实写入：消息落盘即任务存在，不依赖随后
启动的执行。同 --client-message-id 同载荷重复提交返回原任务（幂等，
即“落盘后响应丢失”的安全重发）；同键不同载荷是冲突。

--source-step 把既有消息转为任务（“一键转为任务”）：内容从源消息
派生并保留来源关联；同时给出位置参数内容时以参数为准。`,
		Example: `  mindloop task submit ada "把报告写到 D:\out\report.md"
  mindloop task submit ada "跑一遍测试" --client-message-id ci-1234 --json
  mindloop task submit ada --source-step 3f2a1b7c`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, store, err := c.openTaskStore(args[0])
			if err != nil {
				return c.fail(err)
			}
			var content string
			if len(args) > 1 {
				content = args[1]
			}
			var sourceStepID string
			if sourceStep != "" {
				s, ok, err := id.Timeline.FindStep(sourceStep)
				if err != nil {
					return c.fail(err)
				}
				if !ok {
					return c.fail(fmt.Errorf("没有步骤匹配前缀 %q（也可以直接给出任务内容）", sourceStep))
				}
				sourceStepID = s.StepID
				if strings.TrimSpace(content) == "" {
					content, _ = s.Field("content")
					if strings.TrimSpace(content) == "" {
						return usageErr("用法: mindloop task submit <身份名> <内容>（源步骤没有可用的 content 字段）")
					}
				}
			}
			if strings.TrimSpace(content) == "" {
				return usageErr("用法: mindloop task submit <身份名> <内容> [--source-step S] [--client-message-id ID] [--json]")
			}
			cid := clientMessageID
			if cid == "" {
				cid = ids.NewUUID()
			}
			item, err := store.Submit(c.ctx, task.Submission{From: from, ClientMessageID: cid, Content: content, SourceStepID: sourceStepID})
			if err != nil {
				return taskErr(err)
			}
			if jsonOut {
				if err := c.printTaskJSON(item); err != nil {
					return c.fail(err)
				}
			} else {
				c.printTaskSummary(item)
			}
			c.warnOffline(id, "任务已排队")
			return nil
		},
	}
	fs := cmd.Flags()
	fs.StringVar(&clientMessageID, "client-message-id", "", "幂等键（默认随机生成；同键同载荷重发返回原任务）")
	fs.StringVar(&from, "from", "operator", "发起人")
	fs.StringVar(&sourceStep, "source-step", "", "把既有消息（步骤前缀）转为任务并保留来源关联")
	fs.BoolVar(&jsonOut, "json", false, "输出任务 JSON（脚本/前端消费）")
	return cmd
}

func (c *CLI) newTaskListCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "list <身份名>",
		Short: "列出全部任务（日志顺序，新的在最后）",
		Args:  exactArgs(1, "用法: mindloop task list <身份名> [--json]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, store, err := c.openTaskStore(args[0])
			if err != nil {
				return c.fail(err)
			}
			items, err := store.List(c.ctx)
			if err != nil {
				return taskErr(err)
			}
			if jsonOut {
				return c.printTaskJSON(items)
			}
			if len(items) == 0 {
				fmt.Fprintln(c.stdout, "（没有任务。用 mindloop task submit 提交显式委托）")
				return nil
			}
			for _, item := range items {
				fmt.Fprintf(c.stdout, "%s  %-17s  a%d  %s  %s\n",
					shortID(item.ID), item.State, item.Attempt, shortTS(item.UpdatedAt), oneLineLocal(item.Content, 60))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "输出任务数组 JSON")
	return cmd
}

func (c *CLI) newTaskShowCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "show <身份名> <任务前缀>",
		Short: "查看单个任务的完整状态（支持短 id 前缀）",
		Args:  exactArgs(2, "用法: mindloop task show <身份名> <任务前缀> [--json]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, store, err := c.openTaskStore(args[0])
			if err != nil {
				return c.fail(err)
			}
			item, err := resolveTaskPrefix(c.ctx, store, args[1])
			if err != nil {
				return taskErr(err)
			}
			if jsonOut {
				return c.printTaskJSON(item)
			}
			c.printTaskSummary(item)
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "输出完整任务 JSON（含事件历史）")
	return cmd
}

func (c *CLI) newTaskWaitCmd() *cobra.Command {
	var timeout time.Duration
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "wait <身份名> <任务前缀>",
		Short: "等待任务进入终态（succeeded/failed/canceled/interrupted/budget_exceeded）",
		Long: `等待只观察事实，不触发执行——没在跑的心智不会因为 wait 而启动。
超时时输出当前状态快照并以退出码 1 结束：任务可能仍在别处推进，
超时不是“任务失败”。`,
		Example: `  mindloop task wait ada 3f2a1b7c
  mindloop task wait ada 3f2a1b7c --timeout 30m --json`,
		Args: exactArgs(2, "用法: mindloop task wait <身份名> <任务前缀> [--timeout 10m] [--json]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, store, err := c.openTaskStore(args[0])
			if err != nil {
				return c.fail(err)
			}
			item, err := resolveTaskPrefix(c.ctx, store, args[1])
			if err != nil {
				return taskErr(err)
			}
			var deadline time.Time
			if timeout > 0 {
				deadline = time.Now().Add(timeout)
			}
			warned := false
			for {
				cur, err := store.Get(c.ctx, item.ID)
				if err != nil {
					return taskErr(err)
				}
				if taskTerminal(cur.State) {
					if jsonOut {
						return c.printTaskJSON(cur)
					}
					c.printTaskSummary(cur)
					return nil
				}
				if !warned && cur.State == task.Queued {
					warned = true
					c.warnOffline(id, "任务已排队但不会被领取")
				}
				if !deadline.IsZero() && !time.Now().Before(deadline) {
					if jsonOut {
						if err := c.printTaskJSON(cur); err != nil {
							return c.fail(err)
						}
					} else {
						c.printTaskSummary(cur)
					}
					return exitError{code: 1, err: fmt.Errorf("等待超时（%v）；任务仍在 %s，可稍后 wait 或 show", timeout, cur.State)}
				}
				select {
				case <-c.ctx.Done():
					return c.ctx.Err()
				case <-time.After(300 * time.Millisecond):
				}
			}
		},
	}
	cmd.Flags().DurationVar(&timeout, "timeout", 10*time.Minute, "等待上限（0 = 不限）")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "输出终态任务 JSON")
	return cmd
}

// newTaskCmdCommand 构造 cancel/retry：两者都是幂等控制命令——
// --request-id 重复时返回原收据；基准 attempt 固定“看到的状态”，
// 锁内校验不过（状态已变）则明确拒绝而不是误作用于新代次。
func (c *CLI) newTaskCmdCommand(op string) *cobra.Command {
	var attempt int
	var requestID string
	var jsonOut bool
	use, short, usage := "cancel <身份名> <任务前缀>", "请求取消任务（执行未退出时状态为 canceling）",
		"用法: mindloop task cancel <身份名> <任务前缀> [--attempt N] [--request-id R] [--json]"
	if op == "retry" {
		use, short, usage = "retry <身份名> <任务前缀>", "重试终态任务（创建新 attempt，保留旧运行与错误记录）",
			"用法: mindloop task retry <身份名> <任务前缀> [--attempt N] [--request-id R] [--json]"
	}
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  exactArgs(2, usage),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, store, err := c.openTaskStore(args[0])
			if err != nil {
				return c.fail(err)
			}
			item, err := resolveTaskPrefix(c.ctx, store, args[1])
			if err != nil {
				return taskErr(err)
			}
			base := item.Attempt
			if attempt > 0 {
				base = attempt
			}
			rid := requestID
			if rid == "" {
				rid = ids.NewUUID()
			}
			var out task.Task
			if op == "cancel" {
				out, err = store.Cancel(c.ctx, item.ID, base, "operator", rid)
			} else {
				out, err = store.Retry(c.ctx, item.ID, base, "operator", rid)
			}
			if err != nil {
				return taskErr(err)
			}
			if jsonOut {
				return c.printTaskJSON(out)
			}
			c.printTaskSummary(out)
			if op == "cancel" && out.State == task.Canceling {
				fmt.Fprintln(c.stderr, "执行尚未停止：最终以 canceled 或 interrupted 落盘为准（mindloop task wait 可等待）")
			}
			if op == "retry" {
				c.warnOffline(id, "任务已重新排队")
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&attempt, "attempt", 0, "基准 attempt（默认当前 attempt；防止看到的状态已过期）")
	cmd.Flags().StringVar(&requestID, "request-id", "", "幂等键（默认随机生成；重发同键返回原收据）")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "输出任务 JSON")
	return cmd
}
