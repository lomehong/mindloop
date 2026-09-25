package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

// ServerTool 是本服务器暴露的一个工具：标准 inputSchema + 处理器。
// 处理器返回 text 内容；返回错误时以 isError 内容块回给客户端
//（错误文本原样保留——调用方仍能展示诊断）。
type ServerTool struct {
	Name        string
	Description string
	InputSchema json.RawMessage
	Handler     func(ctx context.Context, args json.RawMessage) (string, error)
}

// ServeStdio 在 stdin/stdout 上运行 MCP 服务器（stdio 传输）——
// Claude Desktop / Cursor 等标准客户端经 mcpServers 配置即可接入。
func ServeStdio(ctx context.Context, serverName, version string, tools []ServerTool) error {
	return serve(ctx, os.Stdin, os.Stdout, serverName, version, tools)
}

// serve 是与进程无关的服务器循环（测试注入管道用）。
func serve(ctx context.Context, r io.Reader, w io.Writer, serverName, version string, tools []ServerTool) error {
	byName := map[string]ServerTool{}
	for _, t := range tools {
		byName[t.Name] = t
	}
	var wmu sync.Mutex
	write := func(msg map[string]any) error {
		line, err := json.Marshal(msg)
		if err != nil {
			return err
		}
		wmu.Lock()
		defer wmu.Unlock()
		_, werr := fmt.Fprintln(w, string(line))
		return werr
	}
	respond := func(id *int64, result any) {
		if id == nil {
			return
		}
		_ = write(map[string]any{"jsonrpc": "2.0", "id": *id, "result": result})
	}
	rpcErr := func(id *int64, code int, message string) {
		if id == nil {
			return
		}
		_ = write(map[string]any{"jsonrpc": "2.0", "id": *id, "error": map[string]any{"code": code, "message": message}})
	}

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var msg rpcMessage
		if json.Unmarshal([]byte(line), &msg) != nil {
			continue // 坏行跳过
		}
		switch msg.Method {
		case "initialize":
			respond(msg.ID, map[string]any{
				"protocolVersion": protocolVersion,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": serverName, "version": version},
			})
		case "notifications/initialized":
			// 通知无响应。
		case "ping":
			respond(msg.ID, map[string]any{})
		case "tools/list":
			list := make([]map[string]any, 0, len(tools))
			for _, t := range tools {
				entry := map[string]any{"name": t.Name, "description": t.Description}
				if len(t.InputSchema) > 0 {
					entry["inputSchema"] = t.InputSchema
				}
				list = append(list, entry)
			}
			respond(msg.ID, map[string]any{"tools": list})
		case "tools/call":
			var raw struct {
				Params struct {
					Name      string          `json:"name"`
					Arguments json.RawMessage `json:"arguments"`
				} `json:"params"`
			}
			if err := json.Unmarshal([]byte(line), &raw); err != nil {
				continue
			}
			tool, ok := byName[raw.Params.Name]
			if !ok {
				rpcErr(msg.ID, -32602, "未知工具: "+raw.Params.Name)
				continue
			}
			text, err := tool.Handler(ctx, raw.Params.Arguments)
			isErr := false
			if err != nil {
				isErr = true
				if text == "" {
					text = err.Error()
				} else {
					text = text + "\n" + err.Error()
				}
			}
			respond(msg.ID, map[string]any{"content": []map[string]any{
				{"type": "text", "text": text},
			}, "isError": isErr})
		case "":
			// 无 method 且带 id：对本请求的响应，服务器不消费。
		default:
			rpcErr(msg.ID, -32601, "方法不存在: "+msg.Method)
		}
	}
	return sc.Err()
}
