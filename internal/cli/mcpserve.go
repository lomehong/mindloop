package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"mindloop/internal/identity"
	"mindloop/internal/mcp"
	"mindloop/internal/mem"
	"mindloop/internal/mind"
	"mindloop/internal/skills"
	"mindloop/internal/traj"
)

// mcpServeTools 把 mindloop 的能力暴露为标准 MCP 工具——Claude
// Desktop / Cursor 等客户端经 mcpServers 配置本命令即可驱动身份的
// 对话、记忆与技能。所有工具的 identity 参数可省略（回落 --identity
// 指定的默认身份）。
func mcpServeTools(defaultIdentity string) []mcp.ServerTool {
	load := func(name string) (*identity.Identity, error) {
		if name == "" {
			name = defaultIdentity
		}
		if name == "" {
			return nil, fmt.Errorf("未指定身份（传 identity 参数，或用 --identity 启动 serve）")
		}
		return identity.Load(name)
	}
	return []mcp.ServerTool{
		{
			Name:        "mind_status",
			Description: "查看身份的心智状态：是否在运行、步骤数、最近活动时间",
			InputSchema: json.RawMessage(`{
  "type": "object",
  "properties": { "identity": { "type": "string", "description": "身份名（默认取 serve 的 --identity）" } }
}`),
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				var a struct {
					Identity string `json:"identity"`
				}
				if err := json.Unmarshal(args, &a); err != nil {
					return "", mcpInvalidArgs(err)
				}
				id, err := load(a.Identity)
				if err != nil {
					return "", err
				}
				// 只读探测：看一眼状态不该创建/偷取运行锁。
				live := mindRunning(id.Timeline.Dir)
				steps, err := id.Timeline.Steps()
				if err != nil {
					return "", err
				}
				out, _ := json.Marshal(map[string]any{
					"name": id.Name, "live": live, "step_count": len(steps),
					"last_step_ts": lastStepTS(steps),
				})
				return string(out), nil
			},
		},
		{
			Name:        "chat_send",
			Description: "给身份发一条人类消息——运行中的心智会拾取并回复（异步）",
			InputSchema: json.RawMessage(`{
  "type": "object",
  "required": ["content"],
  "properties": {
    "content": { "type": "string", "description": "消息内容" },
    "from_name": { "type": "string", "description": "发送者名字（默认 mcp）" },
    "identity": { "type": "string" }
  }
}`),
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				var a struct {
					Content  string `json:"content"`
					FromName string `json:"from_name"`
					Identity string `json:"identity"`
				}
				if err := json.Unmarshal(args, &a); err != nil {
					return "", mcpInvalidArgs(err)
				}
				if strings.TrimSpace(a.Content) == "" {
					return "", mcpInvalidArgs(fmt.Errorf("content 不能为空"))
				}
				id, err := load(a.Identity)
				if err != nil {
					return "", err
				}
				from := a.FromName
				if from == "" {
					from = "mcp"
				}
				if err := mind.PostMessage(id.Timeline, from, id.Name, "mcp", a.Content); err != nil {
					return "", err
				}
				last, err := id.Timeline.LastStep()
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("已投递（step %s）。运行中的心智下个心跳拾取；未运行时消息等待其醒来。", last.StepID), nil
			},
		},
		{
			Name:        "chat_history",
			Description: "查看身份的对话历史（message 步骤）",
			InputSchema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "n": { "type": "integer", "description": "最近 N 条（默认 20）" },
    "identity": { "type": "string" }
  }
}`),
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				var a struct {
					N        int    `json:"n"`
					Identity string `json:"identity"`
				}
				if err := json.Unmarshal(args, &a); err != nil {
					return "", mcpInvalidArgs(err)
				}
				if a.N <= 0 {
					a.N = 20
				}
				id, err := load(a.Identity)
				if err != nil {
					return "", err
				}
				steps, err := id.Timeline.Steps()
				if err != nil {
					return "", err
				}
				out := []map[string]string{}
				for _, s := range steps {
					if s.Type != traj.TypeMessage {
						continue
					}
					from, _ := s.Field("from")
					to, _ := s.Field("to")
					content, _ := s.Field("content")
					out = append(out, map[string]string{"ts": s.TS, "from": from, "to": to, "content": content})
				}
				if len(out) > a.N {
					out = out[len(out)-a.N:]
				}
				data, _ := json.Marshal(out)
				return string(data), nil
			},
		},
		{
			Name:        "mindlog_tail",
			Description: "查看身份思维流的最近步骤（全部类型，含思考/行动）",
			InputSchema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "n": { "type": "integer", "description": "最近 N 步（默认 20）" },
    "identity": { "type": "string" }
  }
}`),
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				var a struct {
					N        int    `json:"n"`
					Identity string `json:"identity"`
				}
				if err := json.Unmarshal(args, &a); err != nil {
					return "", mcpInvalidArgs(err)
				}
				if a.N <= 0 {
					a.N = 20
				}
				id, err := load(a.Identity)
				if err != nil {
					return "", err
				}
				steps, err := id.Timeline.Steps()
				if err != nil {
					return "", err
				}
				if len(steps) > a.N {
					steps = steps[len(steps)-a.N:]
				}
				out := []map[string]string{}
				for _, s := range steps {
					content, _ := s.Field("content")
					out = append(out, map[string]string{"ts": s.TS, "type": s.Type, "content": traj.OneLine(content, 200)})
				}
				data, _ := json.Marshal(out)
				return string(data), nil
			},
		},
		{
			Name:        "mem_add",
			Description: "为身份添加一条记忆（markdown + frontmatter 存储）",
			InputSchema: json.RawMessage(`{
  "type": "object",
  "required": ["content"],
  "properties": {
    "content": { "type": "string" },
    "type": { "type": "string", "description": "fact/preference/todo 等（默认 fact）" },
    "identity": { "type": "string" }
  }
}`),
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				var a struct {
					Content  string `json:"content"`
					Type     string `json:"type"`
					Identity string `json:"identity"`
				}
				if err := json.Unmarshal(args, &a); err != nil {
					return "", mcpInvalidArgs(err)
				}
				if strings.TrimSpace(a.Content) == "" {
					return "", mcpInvalidArgs(fmt.Errorf("content 不能为空"))
				}
				id, err := load(a.Identity)
				if err != nil {
					return "", err
				}
				if a.Type == "" {
					a.Type = "fact"
				}
				store := mem.Store{Dir: filepath.Join(id.Dir, "memories")}
				m, err := store.Add(ctx, a.Type, a.Content)
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("已写入记忆 %s", m.ID), nil
			},
		},
		{
			Name:        "mem_search",
			Description: "检索身份的记忆（BM25：ASCII 词元 + CJK 二元组）",
			InputSchema: json.RawMessage(`{
  "type": "object",
  "required": ["query"],
  "properties": {
    "query": { "type": "string" },
    "k": { "type": "integer", "description": "返回条数（默认 5）" },
    "identity": { "type": "string" }
  }
}`),
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				var a struct {
					Query    string `json:"query"`
					K        int    `json:"k"`
					Identity string `json:"identity"`
				}
				if err := json.Unmarshal(args, &a); err != nil {
					return "", mcpInvalidArgs(err)
				}
				if a.K <= 0 {
					a.K = 5
				}
				id, err := load(a.Identity)
				if err != nil {
					return "", err
				}
				hits, err := (mem.Store{Dir: filepath.Join(id.Dir, "memories")}).Search(a.Query, a.K)
				if err != nil {
					return "", err
				}
				out := []map[string]any{}
				for _, h := range hits {
					out = append(out, map[string]any{"id": h.ID, "type": h.Type, "summary": h.Summary})
				}
				data, _ := json.Marshal(out)
				return string(data), nil
			},
		},
		{
			Name:        "skills_list",
			Description: "列出可用技能（Agent Skills 标准，身份级遮蔽全局）",
			InputSchema: json.RawMessage(`{
  "type": "object",
  "properties": { "identity": { "type": "string" } }
}`),
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				var a struct {
					Identity string `json:"identity"`
				}
				if err := json.Unmarshal(args, &a); err != nil {
					return "", mcpInvalidArgs(err)
				}
				id, err := load(a.Identity)
				if err != nil {
					return "", err
				}
				// 全局层 = MINDLOOP_HOME/skills，与 cli.skillsStore 同一
				// 布局；不借道 identity.Home() 拼 ".."。
				store := skills.Store{Dirs: []string{filepath.Join(id.Dir, "skills"), filepath.Join(traj.Home(), "skills")}}
				items, _ := store.List()
				out := []map[string]string{}
				for _, it := range items {
					out = append(out, map[string]string{"name": it.Name, "description": it.Description, "dir": it.Dir})
				}
				data, _ := json.Marshal(out)
				return string(data), nil
			},
		},
	}
}

// lastStepTS 取最后一步的 ts（空轨迹返回 nil 的 JSON 处理交给调用方）。
func lastStepTS(steps []traj.Step) string {
	if len(steps) == 0 {
		return ""
	}
	return steps[len(steps)-1].TS
}

// newMCPServeCmd：mindloop mcp serve——把上述工具经 stdio 暴露为
// 标准 MCP 服务器。
func (c *CLI) newMCPServeCmd() *cobra.Command {
	var identityP string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "把 mindloop 暴露为 MCP 服务器（stdio）——Claude Desktop / Cursor 可直接接入",
		Long: `在 stdin/stdout 上运行标准 MCP 服务器，暴露身份的状态、对话、
记忆与技能工具。客户端配置示例（Claude Desktop 的 claude_desktop_
config.json）：

  { "mcpServers": { "mindloop": {
      "command": "mindloop", "args": ["mcp", "serve", "--identity", "ada"] } } }

工具的 identity 参数可省略（回落 --identity）。`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if identityP != "" {
				if _, err := identity.Load(identityP); err != nil {
					return c.fail(err)
				}
			}
			ctx, cancel := context.WithCancel(c.ctx)
			defer cancel()
			return mcp.ServeStdio(ctx, "mindloop", Version, mcpServeTools(identityP))
		},
	}
	cmd.Flags().StringVar(&identityP, "identity", "", "默认身份（工具未传 identity 参数时使用）")
	return cmd
}
