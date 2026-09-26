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
func (c *CLI) mcpConfigPaths(cmd *cobra.Command) ([]string, string, error) {
	id, err := c.extensionIdentity(cmd)
	if err != nil {
		return nil, "", err
	}
	global := filepath.Join(traj.Home(), "mcp.json")
	paths := []string{global}
	writeFile := global
	if id != nil {
		writeFile = filepath.Join(id.Dir, "mcp.json")
		paths = append(paths, writeFile)
	}
	return paths, writeFile, nil
}

// newMCPCmd 是 MCP（Model Context Protocol）的客户端接入入口：
// agent 在沙箱里经 $MINDLOOP_EXE mcp ... 探索与调用标准 MCP 服务器
// 的工具；人用同一套命令验证连通性。
func (c *CLI) newMCPCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "MCP 服务器接入（Model Context Protocol 标准客户端）",
		Long: `配置文件 mcp.json 用业界通用的 mcpServers 形态（与 Claude
Desktop / Cursor 互通）：{"mcpServers": {"名字": {"command": "...",
"args": [...], "env": {...}}}}。全局 ~/.mindloop/mcp.json 与身份级
<身份>/mcp.json 递增合并。

传输按配置自动选择：command 走 stdio，url 走 streamable HTTP。`,
	}
	extensionFlags(cmd)
	cmd.PersistentFlags().Duration("timeout", 60*time.Second, "单次调用的超时")

	cmd.AddCommand(
		c.newMCPServeCmd(),
		c.newMCPListCmd(),
		c.newMCPToolsCmd(),
		c.newMCPCallCmd(),
		c.newMCPAddCmd(),
		c.newMCPRemoveCmd(),
	)
	return cmd
}

func (c *CLI) newMCPListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "列出配置的 MCP 服务器",
		RunE: func(cmd *cobra.Command, args []string) error {
			paths, _, err := c.mcpConfigPaths(cmd)
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
					desc = sc.URL + "（HTTP）"
				}
				fmt.Fprintf(c.stdout, "%-16s %s\n", name, desc)
			}
			return nil
		},
	}
}

func (c *CLI) newMCPToolsCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "tools <服务器名>",
		Short: "连接服务器并列出工具（tools/list）",
		Args:  exactArgs(1, "用法: mindloop mcp tools <服务器名> [--identity X]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			paths, _, err := c.mcpConfigPaths(cmd)
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
			timeout, err := cmd.Flags().GetDuration("timeout")
			if err != nil {
				return c.fail(err)
			}
			ctx, cancel := context.WithTimeout(c.ctx, timeout)
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
			if asJSON {
				if err := json.NewEncoder(c.stdout).Encode(tools); err != nil {
					return c.fail(err)
				}
				return nil
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
	cmd.Flags().BoolVar(&asJSON, "json", false, "输出完整工具名称、描述和 inputSchema")
	return cmd
}

func (c *CLI) newMCPCallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   `call <服务器名> <工具名> ['{"参数": "值"}']`,
		Short: "调用工具（tools/call），JSON 参数省略时为 {}",
		Args:  cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			var raw json.RawMessage
			if len(args) == 3 {
				raw = json.RawMessage(args[2])
			}
			paths, _, err := c.mcpConfigPaths(cmd)
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
			timeout, err := cmd.Flags().GetDuration("timeout")
			if err != nil {
				return c.fail(err)
			}
			ctx, cancel := context.WithTimeout(c.ctx, timeout)
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
}

func (c *CLI) newMCPAddCmd() *cobra.Command {
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
			_, writeFile, err := c.mcpConfigPaths(cmd)
			if err != nil {
				return c.fail(err)
			}
			err = mutateMCPConfig(c.ctx, writeFile, func(servers map[string]json.RawMessage) error {
				data, err := json.Marshal(mcp.ServerConfig{Command: args[dash], Args: args[dash+1:]})
				if err != nil {
					return err
				}
				servers[args[0]] = data
				return nil
			})
			if err != nil {
				return c.fail(err)
			}
			fmt.Fprintf(c.stdout, "已写入 %s\n", writeFile)
			return nil
		},
	}
}

func (c *CLI) newMCPRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <名字>",
		Short: "从 mcp.json 移除服务器（身份级优先）",
		Args:  exactArgs(1, "用法: mindloop mcp remove <名字> [--identity X]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, writeFile, err := c.mcpConfigPaths(cmd)
			if err != nil {
				return c.fail(err)
			}
			err = mutateMCPConfig(c.ctx, writeFile, func(servers map[string]json.RawMessage) error {
				if _, ok := servers[args[0]]; !ok {
					return fmt.Errorf("mcp: 服务器 %q 未配置于目标层 %s", args[0], writeFile)
				}
				delete(servers, args[0])
				return nil
			})
			if err != nil {
				return c.fail(err)
			}
			fmt.Fprintf(c.stdout, "已从 %s 移除 %s\n", writeFile, args[0])
			return nil
		},
	}
}

// mutateMCPConfig 锁住目标文件的读改写事务，保留其他配置（包括未知字段）。
// 同目录临时文件落盘后替换，读者只能观察到完整的旧版或新版。
func mutateMCPConfig(ctx context.Context, path string, change func(map[string]json.RawMessage) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	release, err := traj.AcquireDirLock(ctx, path+".lock", 10*time.Second)
	if err != nil {
		return err
	}
	defer release()
	if err := ctx.Err(); err != nil {
		return err
	}
	doc := map[string]json.RawMessage{}
	mode := os.FileMode(0o600)
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("mcp: 读取 %s: %w", path, err)
	}
	if err == nil {
		if err := json.Unmarshal(data, &doc); err != nil {
			return fmt.Errorf("mcp: 解析 %s: %w", path, err)
		}
		if doc == nil {
			return fmt.Errorf("mcp: %s 必须是 JSON 对象", path)
		}
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		mode = info.Mode().Perm()
	}
	servers := map[string]json.RawMessage{}
	if raw, ok := doc["mcpServers"]; ok {
		if err := json.Unmarshal(raw, &servers); err != nil {
			return fmt.Errorf("mcp: 解析 %s 的 mcpServers: %w", path, err)
		}
		if servers == nil {
			return fmt.Errorf("mcp: %s 的 mcpServers 必须是 JSON 对象", path)
		}
	}
	if err := change(servers); err != nil {
		return err
	}
	doc["mcpServers"], err = json.Marshal(servers)
	if err != nil {
		return err
	}
	data, err = json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".mcp-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err := file.Chmod(mode); err != nil {
		return err
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return fmt.Errorf("mcp: 替换 %s: %w", path, err)
	}
	return nil
}
