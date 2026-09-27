package web

// /api/identities/{name}/connections——"外部连接"面板的读取面：
// MCP 服务器清单（分来源标注）+ 配置文件状态 + 未来渠道桥接的
// 预留槽位。只读：配置编辑仍走 mcp.json 与 CLI（mcp add/remove），
// web 不提供第二套写路径。
//
// 安全：env/headers 的值可能含凭据，绝不回显——只给键名（测试
// 钉死这一条）。

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"mindloop/internal/identity"
	"mindloop/internal/mcp"
	"mindloop/internal/traj"
)

// mcpServerView 是一台 MCP 服务器的展示形态（键值分离：
// env/headers 只给键名，值不回显）。
type mcpServerView struct {
	Name       string   `json:"name"`
	Transport  string   `json:"transport"` // stdio | http（按 url 有无选择）
	Command    string   `json:"command,omitempty"`
	Args       []string `json:"args,omitempty"`
	URL        string   `json:"url,omitempty"`
	EnvKeys    []string `json:"env_keys"`    // 只给键名——值可能含凭据
	HeaderKeys []string `json:"header_keys"` // 同上
	Source     string   `json:"source"`      // identity | global
}

// mcpFileView 描述一层的配置文件状态。
type mcpFileView struct {
	Label  string `json:"label"`
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
}

// connectionsView 是 connections 页响应。
type connectionsView struct {
	Identity       map[string]string `json:"identity"`
	MCPServers     []mcpServerView   `json:"mcp_servers"`
	MCPConfigError string            `json:"mcp_config_error,omitempty"`
	MCPFiles       []mcpFileView     `json:"mcp_files"`
	Channels       []channelView     `json:"channels"` // 外部渠道桥（配置读写走 /channels）
}

// handleConnections GET /api/identities/{id}/connections。
func (s *Server) handleConnections(w http.ResponseWriter, _ *http.Request, id *identity.Identity) {
	globalPath := filepath.Join(traj.Home(), "mcp.json")
	identityPath := filepath.Join(id.Dir, "mcp.json")

	// 合并语义与装配层一致（身份覆盖全局）。分文件读取以标注
	// 来源；某层损坏时另一层仍可见——错误字段明说当前不生效
	// （装配层对坏配置是 fail-closed：丢弃全部 MCP 面）。
	globalCfg, globalErr := mcp.LoadConfig(globalPath)
	identityCfg, identityErr := mcp.LoadConfig(identityPath)

	out := connectionsView{
		Identity:   map[string]string{"id": id.Name, "name": id.Name},
		MCPServers: []mcpServerView{},
		MCPFiles: []mcpFileView{
			{Label: "全局", Path: globalPath, Exists: fileExists(globalPath)},
			{Label: "身份", Path: identityPath, Exists: fileExists(identityPath)},
		},
		Channels: []channelView{s.wecomView(id)},
	}
	if globalErr != nil || identityErr != nil {
		var parts []string
		if globalErr != nil {
			parts = append(parts, globalErr.Error())
		}
		if identityErr != nil {
			parts = append(parts, identityErr.Error())
		}
		out.MCPConfigError = strings.Join(parts, "；") +
			"（配置解析失败时 MCP 全部不生效，修复后重启心智加载）"
	}

	merged := map[string]mcp.ServerConfig{}
	source := map[string]string{}
	for name, sc := range globalCfg.MCPServers {
		merged[name] = sc
		source[name] = "global"
	}
	for name, sc := range identityCfg.MCPServers {
		merged[name] = sc
		source[name] = "identity"
	}
	names := make([]string, 0, len(merged))
	for name := range merged {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		sc := merged[name]
		v := mcpServerView{
			Name:       name,
			Transport:  "stdio",
			Command:    sc.Command,
			Args:       sc.Args,
			URL:        sc.URL,
			EnvKeys:    sortedKeys(sc.Env),
			HeaderKeys: sortedKeys(sc.Headers),
			Source:     source[name],
		}
		if sc.URL != "" {
			v.Transport = "http"
		}
		out.MCPServers = append(out.MCPServers, v)
	}
	writeJSON(w, 200, out)
}

// sortedKeys 返回 map 的键名清单（字母序）——值不出现在任何响应里。
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// fileExists 报告文件是否存在（mcp_files 的展示用）。
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
