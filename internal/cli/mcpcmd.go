package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"mindloop/internal/mcp"
	"mindloop/internal/traj"
)

// mcpConfigPaths 返回 mcp.json 的读取顺序：全局在前，身份级在后
// （优先级递增合并，业界 mcpServers 形态）。
func (c *CLI) mcpConfigPaths(identityP string) ([]string, string, error) {
	global := filepath.Join(traj.Home(), "mcp.json")
	paths := []string{global}
	identityFile := ""
	if identityP != "" {
		id, err := c.loadIdentity(identityP)
		if err != nil {
			return nil, "", err
		}
		identityFile = filepath.Join(id.Dir, "mcp.json")
		paths = append(paths, identityFile)
	}
	return paths, identityFile, nil
}

// newMCPCmd 是 MCP（Model Context Protocol）的客户端接入入口：
// agent 在沙箱里经 $MINDLOOP_EXE mcp ... 探索与调用标准 MCP 服务器
// 的工具；人用同一套命令验证连通性。
func (c *CLI) newMCPCmd() *cobra.Command {
	var identityP string
	var timeoutP time.Duration
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "MCP 服务器接入（Model Context Protocol 标准客户端）",
		Long: `配置文件 mcp.json 用业界通用的 mcpServers 形态（与 Claude
Desktop / Cursor 互通）：{"mcpServers": {"名字": {"command": "...",
"args": [...], "env": {...}}}}。全局 ~/.mindloop/mcp.json 与身份级
<身份>/mcp.json 递增合并。

v1 传输层为 stdio（本地服务器最普遍形态）；url（HTTP）配置显式报错。`,
	}
	cmd.PersistentFlags().StringVar(&identityP, "identity", "", "身份名（其 mcp.json 参与合并）")
	cmd.PersistentFlags().DurationVar(&timeoutP, "timeout", 60*time.Second, "单次调用的超时")

	cmd.AddCommand(
		c.newMCPServeCmd(),
		func() *cobra.Command {
			return &cobra.Command{
				Use:   "list",
				Short: "列出配置的 MCP 服务器",
				RunE: func(cmd *cobra.Command, args []string) error {
					paths, _, err := c.mcpConfigPaths(identityP)
					if err != nil {
						return c.fail(err)
					}
					cfg, err := mcp.LoadConfig(paths...)
					if err != nil {
						return c.fail(err)
					}
					if len(cfg.MCPServers) == 0 {
						fmt.Fprintln(c.stdout, "（没有配置——编辑 mcp.json，形态见 mcp --help）")
						return nil
					}
					for _, name := range cfg.Names() {
						sc := cfg.MCPServers[name]
						desc := sc.Command
						if sc.URL != "" {
							desc = sc.URL + "（HTTP，v1 未支持）"
						}
						fmt.Fprintf(c.stdout, "%-16s %s\n", name, desc)
					}
					return nil
				},
			}
		}(),
		func() *cobra.Command {
			return &cobra.Command{
				Use:   "tools <服务器名>",
				Short: "连接服务器并列出工具（tools/list）",
				Args:  exactArgs(1, "用法: mindloop mcp tools <服务器名> [--identity X]"),
				RunE: func(cmd *cobra.Command, args []string) error {
					paths, _, err := c.mcpConfigPaths(identityP)
					if err != nil {
						return c.fail(err)
					}
					cfg, err := mcp.LoadConfig(paths...)
					if err != nil {
						return c.fail(err)
					}
					sc, ok := cfg.MCPServers[args[0]]
					if !ok {
						return c.fail(fmt.Errorf("mcp: 服务器 %q 未配置（mcp list 查看）", args[0]))
					}
					ctx, cancel := context.WithTimeout(c.ctx, timeoutP)
					defer cancel()
					client, err := mcp.Start(ctx, args[0], sc)
					if err != nil {
						return c.fail(err)
					}
					defer client.Close()
					tools, err := client.ListTools(ctx)
					if err != nil {
						return c.fail(err)
					}
					if len(tools) == 0 {
						fmt.Fprintln(c.stdout, "（该服务器没有暴露工具）")
						return nil
					}
					for _, t := range tools {
						fmt.Fprintf(c.stdout, "%s\n  %s\n", t.Name, traj.OneLine(t.Description, 120))
					}
					return nil
				},
			}
		}(),
		func() *cobra.Command {
			var argsP string
			c := &cobra.Command{
				Use:   `call <服务器名> <工具名> ['{"参数": "值"}']`,
				Short: "调用工具（tools/call），JSON 参数省略时为 {}",
				Args:  cobra.RangeArgs(2, 3),
				RunE: func(cmd *cobra.Command, args []string) error {
					if len(args) == 3 {
						argsP = args[2]
					}
					paths, _, err := c.mcpConfigPaths(identityP)
					if err != nil {
						return c.fail(err)
					}
					cfg, err := mcp.LoadConfig(paths...)
					if err != nil {
						return c.fail(err)
					}
					sc, ok := cfg.MCPServers[args[0]]
					if !ok {
						return c.fail(fmt.Errorf("mcp: 服务器 %q 未配置（mcp list 查看）", args[0]))
					}
					var raw json.RawMessage
					if argsP != "" {
						raw = json.RawMessage(argsP)
					}
					ctx, cancel := context.WithTimeout(c.ctx, timeoutP)
					defer cancel()
					client, err := mcp.Start(ctx, args[0], sc)
					if err != nil {
						return c.fail(err)
					}
					defer client.Close()
					res, err := client.CallTool(ctx, args[1], raw)
					if err != nil {
						return c.fail(err)
					}
					fmt.Fprintln(c.stdout, res.Text)
					return nil
				},
			}
			return c
		}(),
		func() *cobra.Command {
			return &cobra.Command{
				Use:   `add <名字> -- <command> [args...]`,
				Short: "添加服务器到 mcp.json（--identity 写身份级，否则全局）",
				Args: func(cmd *cobra.Command, args []string) error {
					dash := cmd.ArgsLenAtDash()
					if dash < 1 || dash >= len(args) {
						return fmt.Errorf("用法: mindloop mcp add <名字> -- <command> [args...]")
					}
					return nil
				},
				RunE: func(cmd *cobra.Command, args []string) error {
					dash := cmd.ArgsLenAtDash()
					_, writeFile, err := c.mcpConfigPaths(identityP)
					if err != nil {
						return c.fail(err)
					}
					cfg := mcp.Config{MCPServers: map[string]mcp.ServerConfig{}}
					if data, rerr := os.ReadFile(writeFile); rerr == nil {
						if jerr := json.Unmarshal(data, &cfg); jerr != nil {
							return c.fail(fmt.Errorf("mcp: 解析 %s: %w", writeFile, jerr))
						}
					}
					if cfg.MCPServers == nil {
						cfg.MCPServers = map[string]mcp.ServerConfig{}
					}
					cfg.MCPServers[args[0]] = mcp.ServerConfig{Command: args[dash], Args: args[dash+1:]}
					data, _ := json.MarshalIndent(cfg, "", "  ")
					if err := os.MkdirAll(filepath.Dir(writeFile), 0o755); err != nil {
						return c.fail(err)
					}
					if err := os.WriteFile(writeFile, append(data, '\n'), 0o644); err != nil {
						return c.fail(err)
					}
					fmt.Fprintf(c.stdout, "已写入 %s\n", writeFile)
					return nil
				},
			}
		}(),
		func() *cobra.Command {
			return &cobra.Command{
				Use:   "remove <名字>",
				Short: "从 mcp.json 移除服务器（身份级优先）",
				Args:  exactArgs(1, "用法: mindloop mcp remove <名字> [--identity X]"),
				RunE: func(cmd *cobra.Command, args []string) error {
					paths, writeFile, err := c.mcpConfigPaths(identityP)
					if err != nil {
						return c.fail(err)
					}
					cfg, err := mcp.LoadConfig(paths...)
					if err != nil {
						return c.fail(err)
					}
					if _, ok := cfg.MCPServers[args[0]]; !ok {
						return c.fail(fmt.Errorf("mcp: 服务器 %q 未配置", args[0]))
					}
					delete(cfg.MCPServers, args[0])
					if writeFile == "" {
						writeFile = paths[0]
					}
					data, _ := json.MarshalIndent(cfg, "", "  ")
					if err := os.WriteFile(writeFile, append(data, '\n'), 0o644); err != nil {
						return c.fail(err)
					}
					fmt.Fprintf(c.stdout, "已从 %s 移除 %s\n", writeFile, args[0])
					return nil
				},
			}
		}(),
	)
	return cmd
}
