package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"mindloop/internal/traj"
)

// exactArgs 带中文用法提示的位置参数校验（cobra 内置校验的文案是
// 英文，退出码也统一为 2）。
func exactArgs(n int, usage string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) != n {
			return usageErr(usage)
		}
		return nil
	}
}

func (c *CLI) newTrajCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "traj",
		Short: "追加式 JSONL 轨迹日志：创建、追加、查询、校验",
	}
	cmd.AddCommand(
		c.newTrajNewCmd(),
		c.newTrajAppendCmd(),
		c.newTrajShowCmd(),
		c.newTrajTailCmd(),
		c.newTrajCatCmd(),
		c.newTrajListCmd(),
		c.newTrajCheckCmd(),
		c.newTrajMergeCmd(),
		c.newTrajRootCmd(),
	)
	return cmd
}

func (c *CLI) newTrajNewCmd() *cobra.Command {
	var slug, parent string
	cmd := &cobra.Command{
		Use:   "new",
		Short: "新建一条轨迹（打印轨迹 id）",
		Example: `  mindloop traj new --slug demo
  mindloop traj new --slug subtask --parent <父轨迹id>`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if slug == "" {
				slug = nowStamp()
			}
			var (
				t   *traj.Timeline
				err error
			)
			if parent != "" {
				var p *traj.Timeline
				if p, err = traj.Load(parent); err != nil {
					return c.fail(err)
				}
				t, err = traj.CreateFork(c.ctx, slug, p)
			} else {
				t, err = traj.Create(c.ctx, slug)
			}
			if err != nil {
				return c.fail(err)
			}
			fmt.Fprintln(c.stdout, t.ID)
			return nil
		},
	}
	cmd.Flags().StringVar(&slug, "slug", "", "短名（默认：UTC 时间戳）")
	cmd.Flags().StringVar(&parent, "parent", "", "从该轨迹 id 或前缀分叉")
	return cmd
}

func (c *CLI) newTrajAppendCmd() *cobra.Command {
	var content, contentFile string
	var fields []string

	cmd := &cobra.Command{
		Use:   "append <traj> <type> [flags]",
		Short: "向轨迹追加一个步骤（打印步骤 id）",
		Long: `向轨迹追加一个步骤。

载荷字段两种写法等价:
  --field from=operator
  --from operator          （未注册的 --键 值 自动收编为载荷字段）
值以横线开头时必须用等号形态: --to=-ada。JSON 对象/数组会被解析。`,
		Example: `  mindloop traj append <id> message --from operator --content "hello"
  mindloop traj append <id> facts --field keys=[a,b,c] --content-file data.txt`,
		// 自由字段旗标（--from operator）需要在我们这边收编，
		// 所以关闭 cobra 的旗标解析、手工解析已知旗标。
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			for _, a := range args {
				if a == "-h" || a == "--help" {
					return cmd.Help()
				}
			}
			restArgs, collected := collectFieldFlags(args, map[string]bool{
				"content": true, "content-file": true, "field": true, "h": true, "help": true,
			})
			fields = append(fields, collected...)
			if err := cmd.Flags().Parse(restArgs); err != nil {
				return usageErr(err.Error())
			}
			rest := cmd.Flags().Args()
			if len(rest) != 2 {
				return usageErr(`用法: mindloop traj append <traj> <type> [--content C] [--field K=V] [--任意键 值]`)
			}
			t, err := traj.Load(rest[0])
			if err != nil {
				return c.fail(err)
			}
			typ := rest[1]

			if contentFile != "" {
				data, err := readInput(contentFile)
				if err != nil {
					return c.fail(err)
				}
				content = string(data)
			}
			s := traj.NewStep(typ)
			if content != "" {
				s.Fields["content"] = content
			}
			for _, kv := range fields {
				k, v, ok := strings.Cut(kv, "=")
				if !ok {
					return c.fail(fmt.Errorf("--field %q 不是 K=V 形式", kv))
				}
				s.Fields[k] = parseValue(v)
			}
			// 运行溯源：在运行内部，步骤继承运行 id，除非类型是
			// 结构性的——Headlong 的 traj append 规则，保证嵌套运行
			// 永远不会被打上启动者的簿记标记。
			if runID := os.Getenv("MINDLOOP_RUN_ID"); runID != "" && !traj.IsReservedType(typ) {
				if _, exists := s.Fields["run_id"]; !exists {
					s.Fields["run_id"] = runID
				}
			}
			if err := t.Append(c.ctx, s); err != nil {
				return c.fail(err)
			}
			fmt.Fprintln(c.stdout, s.StepID)
			return nil
		},
	}
	fs := cmd.Flags()
	fs.StringVar(&content, "content", "", "content 载荷")
	fs.StringVar(&contentFile, "content-file", "", "从文件读取 content（'-' = stdin）")
	fs.StringArrayVar(&fields, "field", nil, "载荷字段 K=V（可重复；JSON 对象/数组会被解析）")
	return cmd
}

func (c *CLI) newTrajShowCmd() *cobra.Command {
	var full bool
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "显示一个轨迹或步骤（id 或前缀）",
		Args:  exactArgs(1, "用法: mindloop traj show <id>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if t, err := traj.Load(args[0]); err == nil {
				h, err := t.Header()
				if err != nil {
					return c.fail(err)
				}
				printPretty(c.stdout, h, full)
				return nil
			}
			s, _, err := traj.FindStepAnywhere(args[0])
			if err != nil {
				return c.fail(err)
			}
			printPretty(c.stdout, s, full)
			return nil
		},
	}
	cmd.Flags().BoolVar(&full, "full", false, "完整输出字段值")
	return cmd
}

func (c *CLI) newTrajTailCmd() *cobra.Command {
	var n int
	var pretty bool
	var types []string
	cmd := &cobra.Command{
		Use:   "tail <traj>",
		Short: "查看轨迹的最后 N 个步骤",
		Example: `  mindloop traj tail <id> -n 5 --pretty
  mindloop traj tail -n 3 <id> --type message`,
		Args: exactArgs(1, "用法: mindloop traj tail <traj> [flags]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			t, err := traj.Load(args[0])
			if err != nil {
				return c.fail(err)
			}
			steps, err := t.Tail(n, types)
			if err != nil {
				return c.fail(err)
			}
			for _, s := range steps {
				if pretty {
					printPretty(c.stdout, s, false)
				} else {
					printJSONL(c.stdout, s)
				}
			}
			return nil
		},
	}
	cmd.Flags().IntVarP(&n, "num", "n", 20, "最后 N 个步骤（0 = 全部）")
	cmd.Flags().BoolVar(&pretty, "pretty", false, "人类可读输出")
	cmd.Flags().StringArrayVar(&types, "type", nil, "仅这些步骤类型（可重复）")
	return cmd
}

func (c *CLI) newTrajCatCmd() *cobra.Command {
	var types, filters []string
	cmd := &cobra.Command{
		Use:   "cat <traj>",
		Short: "按类型与 K=V 过滤输出步骤",
		Args:  exactArgs(1, "用法: mindloop traj cat <traj> [flags]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			t, err := traj.Load(args[0])
			if err != nil {
				return c.fail(err)
			}
			fm := make(map[string]string, len(filters))
			for _, kv := range filters {
				k, v, ok := strings.Cut(kv, "=")
				if !ok {
					return c.fail(fmt.Errorf("--filter %q 不是 K=V 形式", kv))
				}
				fm[k] = v
			}
			steps, err := t.Cat(fm, types)
			if err != nil {
				return c.fail(err)
			}
			for _, s := range steps {
				printJSONL(c.stdout, s)
			}
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&types, "type", nil, "仅这些步骤类型（可重复）")
	cmd.Flags().StringArrayVar(&filters, "filter", nil, "仅匹配 K=V 的步骤（可重复）")
	return cmd
}

func (c *CLI) newTrajListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "列出全部轨迹（最新在前）",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			infos, err := traj.List()
			if err != nil {
				return c.fail(err)
			}
			for _, in := range infos {
				fmt.Fprintf(c.stdout, "%s  %-24s %6d 步骤  %s\n", shortID(in.ID), in.Slug, in.Steps, in.Path)
			}
			return nil
		},
	}
}

func (c *CLI) newTrajCheckCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "check <traj>",
		Short: "行级校验轨迹文件",
		Args:  exactArgs(1, "用法: mindloop traj check <traj>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			t, err := traj.Load(args[0])
			if err != nil {
				return c.fail(err)
			}
			bad, err := t.Check()
			if err != nil {
				return c.fail(err)
			}
			if len(bad) == 0 {
				fmt.Fprintln(c.stdout, "ok")
				return nil
			}
			for _, ln := range bad {
				fmt.Fprintf(c.stdout, "坏行 %d\n", ln)
			}
			return exitError{code: 1}
		},
	}
}

func (c *CLI) newTrajMergeCmd() *cobra.Command {
	var to, content string
	cmd := &cobra.Command{
		Use:     "merge <child> --to <parent>",
		Short:   "把子轨迹的结果合并回父轨迹",
		Example: `  mindloop traj merge <子轨迹id> --to <父轨迹id> --content "done"`,
		Args:    exactArgs(1, "用法: mindloop traj merge <child> --to <parent> [--content C]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if to == "" {
				return usageErr("用法: mindloop traj merge <child> --to <parent> [--content C]（--to 必填）")
			}
			child, err := traj.Load(args[0])
			if err != nil {
				return c.fail(err)
			}
			parent, err := traj.Load(to)
			if err != nil {
				return c.fail(err)
			}
			if content == "" {
				if last, err := child.LastStep(); err == nil {
					content, _ = last.Field("content")
				}
			}
			m, err := parent.Merge(c.ctx, child, content)
			if err != nil {
				return c.fail(err)
			}
			fmt.Fprintln(c.stdout, m.StepID)
			return nil
		},
	}
	cmd.Flags().StringVar(&to, "to", "", "合并进哪个父轨迹（必填）")
	cmd.Flags().StringVar(&content, "content", "", "结果载荷（默认取子轨迹最后一个 content 字段）")
	return cmd
}

func (c *CLI) newTrajRootCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "root <id>",
		Short: "沿 parent 链找到最顶层的思维日志",
		Args:  exactArgs(1, "用法: mindloop traj root <id>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			t, err := traj.Load(args[0])
			if err != nil {
				return c.fail(err)
			}
			r, err := t.Root()
			if err != nil {
				return c.fail(err)
			}
			fmt.Fprintf(c.stdout, "%s  %s\n", r.ID, r.Path)
			return nil
		},
	}
}
