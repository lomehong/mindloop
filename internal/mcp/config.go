// Package mcp 实现 Model Context Protocol（MCP）的客户端接入：
// 让 mindloop 的 agent 能把任意标准 MCP 服务器（filesystem、github、
// postgres……）当作工具面使用。
//
// 协议：JSON-RPC 2.0 over stdio（newline 分帧）——initialize 握手、
// tools/list、tools/call，MCP 规范的标准子集。配置形态与业界通用
// 的 mcpServers JSON 一致（Claude Desktop / Cursor 等同款）：
//
//	{ "mcpServers": { "github": { "command": "npx", "args": ["-y",
//	  "@modelcontextprotocol/server-github"], "env": { ... } } } }
//
// 协议是标准，实现是自己的：JSON-RPC 手写不过百行，核心库维持
// 零第三方依赖。v1 传输层支持 stdio（生态中最普遍的本地传输）；
// HTTP/SSE 传输在配置里出现时显式报错而非静默忽略。
package mcp

import (
	"encoding/json"
	"fmt"
	"os"
)

// ServerConfig 是一台 MCP 服务器的接入配置。
type ServerConfig struct {
	Command string            `json:"command,omitempty"` // stdio：可执行文件
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"` // 追加到进程环境
	URL     string            `json:"url,omitempty"` // HTTP 传输（v1 未实现，出现即报错）
}

// Config 是 mcp.json 的顶层形态。键名 mcpServers 是生态标准拼写，
// 不改拼——标准配置文件应当原样互通。
type Config struct {
	MCPServers map[string]ServerConfig `json:"mcpServers"`
}

// LoadConfig 依次读入多个 mcp.json（优先级递增：后面的覆盖前面
// 的同名服务器）。文件不存在不是错误——"没配"是合法状态。
func LoadConfig(paths ...string) (Config, error) {
	merged := Config{MCPServers: map[string]ServerConfig{}}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return merged, fmt.Errorf("mcp: 读取 %s: %w", p, err)
		}
		var c Config
		if err := json.Unmarshal(data, &c); err != nil {
			return merged, fmt.Errorf("mcp: 解析 %s: %w", p, err)
		}
		for name, sc := range c.MCPServers {
			merged.MCPServers[name] = sc
		}
	}
	return merged, nil
}

// Names 返回按字母序排列的服务器名。
func (c Config) Names() []string {
	names := make([]string, 0, len(c.MCPServers))
	for n := range c.MCPServers {
		names = append(names, n)
	}
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j] < names[j-1]; j-- {
			names[j], names[j-1] = names[j-1], names[j]
		}
	}
	return names
}
