package cli

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"mindloop/internal/prompt"
	"mindloop/internal/traj"
)

func (c *CLI) newPromptCmd() *cobra.Command {
	var headP, tailP, blockP, limitP, blockLimitP, maxBytesP int
	var formatP, assistantP, excludeP string
	var pins []string
	cmd := &cobra.Command{
		Use:   "prompt <traj>",
		Short: "把轨迹渲染为 LLM 消息序列（缓存网格 + 字节预算）",
		Long: `运行循环每次调用模型前的最后一站。

截断档位是行号的纯函数，日志在块内增长时不重写任何既存行——
为 provider 的 prompt cache 保持前缀稳定。截断处会留下
"mindloop traj show <id> --full" 的取回命令：分级是索引而不是证词。`,
		Example: `  mindloop prompt <id> --format messages --max-bytes 60000
  mindloop prompt <id> --tail 30 --block 10 --format text`,
		Args: exactArgs(1, "用法: mindloop prompt <traj> [flags]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			t, err := traj.Load(args[0])
			if err != nil {
				return c.fail(err)
			}
			steps, err := t.Steps()
			if err != nil {
				return c.fail(err)
			}
			opts := prompt.Options{
				Head:            headP,
				Tail:            tailP,
				Block:           blockP,
				Pin:             pins,
				FieldLimit:      limitP,
				BlockFieldLimit: blockLimitP,
				MaxBytes:        maxBytesP,
				AssistantTypes:  splitCSV(assistantP),
				ExcludeFields:   splitCSV(excludeP),
			}
			msgs := prompt.Render(steps, opts)
			switch formatP {
			case "text":
				for _, m := range msgs {
					fmt.Fprintf(c.stdout, "## %s\n\n%s\n\n", m.Role, m.Content)
				}
			case "messages":
				var buf bytes.Buffer
				enc := json.NewEncoder(&buf)
				enc.SetEscapeHTML(false)
				if err := enc.Encode(msgs); err != nil {
					return c.fail(err)
				}
				c.stdout.Write(buf.Bytes())
			default:
				return usageErr(fmt.Sprintf("未知格式 %q（可选 messages|text）", formatP))
			}
			return nil
		},
	}
	fs := cmd.Flags()
	fs.IntVar(&headP, "head", prompt.DefaultHead, "开头原样保留的步骤数")
	fs.IntVar(&tailP, "tail", prompt.DefaultTail, "结尾尾窗的步骤数")
	fs.IntVar(&blockP, "block", prompt.DefaultBlock, "新近整块大小（0 = 不分块）")
	fs.IntVar(&limitP, "limit", prompt.DefaultFieldLimit, "常规区域单字段字节上限")
	fs.IntVar(&blockLimitP, "block-limit", prompt.DefaultBlockFieldLimit, "新近整块区域单字段字节上限")
	fs.IntVar(&maxBytesP, "max-bytes", 0, "内容总字节预算（0 = 不限）")
	fs.StringVar(&formatP, "format", "messages", "输出格式：messages（JSON 数组）或 text")
	fs.StringVar(&assistantP, "assistant-types", "final,reasoning", "渲染为 assistant 角色的类型（逗号分隔）")
	fs.StringVar(&excludeP, "exclude-fields", "", "额外剔除的字段（逗号分隔）")
	fs.StringArrayVar(&pins, "pin", nil, "额外保留的 step id 前缀（可重复）")
	return cmd
}
