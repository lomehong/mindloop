package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMain 在 MINDLOOP_MOCK_MCP=1 时把测试进程变成一台遵循 MCP
// 标准子集的假服务器——客户端测试通过自重执行（os.Executable）
// 与真进程做完整协议往返，而不是 mock 接口。
func TestMain(m *testing.M) {
	if os.Getenv("MINDLOOP_MOCK_MCP") == "1" {
		runMockServer()
		return
	}
	os.Exit(m.Run())
}

func runMockServer() {
	r := bufio.NewReader(os.Stdin)
	w := bufio.NewWriter(os.Stdout)
	defer w.Flush()
	respond := func(msg map[string]any, result any) {
		out, _ := json.Marshal(map[string]any{
			"jsonrpc": "2.0", "id": msg["id"], "result": result,
		})
		fmt.Fprintln(w, string(out))
		w.Flush()
	}
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var msg map[string]any
		if json.Unmarshal([]byte(line), &msg) != nil {
			continue
		}
		switch msg["method"] {
		case "initialize":
			respond(msg, map[string]any{
				"protocolVersion": protocolVersion,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "mock", "version": "0.0.1"},
			})
		case "notifications/initialized":
			// 通知无响应。
		case "tools/list":
			respond(msg, map[string]any{"tools": []map[string]any{
				{
					"name":        "echo",
					"description": "回显输入文本",
					"inputSchema": map[string]any{"type": "object"},
				},
				{
					"name":        "fail",
					"description": "总是报错的工具",
					"inputSchema": map[string]any{"type": "object"},
				},
			}})
		case "tools/call":
			params, _ := msg["params"].(map[string]any)
			name, _ := params["name"].(string)
			switch name {
			case "echo":
				args, _ := params["arguments"].(map[string]any)
				text, _ := args["text"].(string)
				respond(msg, map[string]any{"content": []map[string]any{
					{"type": "text", "text": "echo: " + text},
				}, "isError": false})
			default:
				respond(msg, map[string]any{"content": []map[string]any{
					{"type": "text", "text": "故意失败: " + name},
				}, "isError": true})
			}
		default:
			if _, hasID := msg["id"]; hasID {
				respond(msg, map[string]any{"_unknown_method": msg["method"]})
			}
		}
	}
}

// TestConfigStandardShape：业界通用的 mcpServers JSON 形态必须原样
// 可读——这是与 Claude Desktop / Cursor 等配置互通的契约。
func TestConfigStandardShape(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "mcp.json")
	content := `{
  "mcpServers": {
    "github": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-github"],
      "env": { "GITHUB_TOKEN": "x" }
    },
    "remote": { "url": "https://example.com/mcp" }
  }
}`
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.MCPServers) != 2 {
		t.Fatalf("应 2 台服务器，得到 %d", len(cfg.MCPServers))
	}
	gh := cfg.MCPServers["github"]
	if gh.Command != "npx" || len(gh.Args) != 2 || gh.Env["GITHUB_TOKEN"] != "x" {
		t.Fatalf("github 配置错位: %+v", gh)
	}
	if cfg.Names()[0] != "github" {
		t.Fatalf("Names 应有序: %v", cfg.Names())
	}
}

// TestLoadConfigMergeAndMissing：多路径优先级递增合并；缺失文件
// 是合法状态；坏 JSON 显式报错。
func TestLoadConfigMergeAndMissing(t *testing.T) {
	dir := t.TempDir()
	global := filepath.Join(dir, "global.json")
	ident := filepath.Join(dir, "identity.json")
	os.WriteFile(global, []byte(`{"mcpServers":{"a":{"command":"a1"},"b":{"command":"b1"}}}`), 0o644)
	os.WriteFile(ident, []byte(`{"mcpServers":{"a":{"command":"a2","args":["-x"]}}}`), 0o644)

	cfg, err := LoadConfig(global, ident, filepath.Join(dir, "nope.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.MCPServers) != 2 {
		t.Fatalf("应 2 台（a 覆盖、b 保留），得到 %d", len(cfg.MCPServers))
	}
	if cfg.MCPServers["a"].Command != "a2" || cfg.MCPServers["b"].Command != "b1" {
		t.Fatalf("合并语义错位: %+v", cfg.MCPServers)
	}

	bad := filepath.Join(dir, "bad.json")
	os.WriteFile(bad, []byte(`{ "mcpServers": `), 0o644)
	if _, err := LoadConfig(bad); err == nil {
		t.Fatal("坏 JSON 应报错")
	}
}

// TestClientFullRoundTrip：真子进程 + 真协议——握手、tools/list、
// tools/call 全链路。
func TestClientFullRoundTrip(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	c, err := Start(ctx, "mock", ServerConfig{
		Command: exe,
		Env:     map[string]string{"MINDLOOP_MOCK_MCP": "1"},
	})
	if err != nil {
		t.Fatalf("握手失败: %v", err)
	}
	defer c.Close()

	tools, err := c.ListTools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 2 || tools[0].Name != "echo" || tools[0].Description == "" {
		t.Fatalf("tools/list 错位: %+v", tools)
	}

	res, err := c.CallTool(ctx, "echo", json.RawMessage(`{"text":"你好"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "echo: 你好" {
		t.Fatalf("tools/call 结果错位: %q", res.Text)
	}

	// 服务器标注 isError → 调用返回错误且携带文本。
	if _, err := c.CallTool(ctx, "fail", nil); err == nil || !strings.Contains(err.Error(), "故意失败") {
		t.Fatalf("isError 应转为错误并带文本: %v", err)
	}
}

// TestStartRejects：HTTP 传输与缺 command 显式报错而非静默。
func TestStartRejects(t *testing.T) {
	ctx := context.Background()
	if _, err := Start(ctx, "remote", ServerConfig{URL: "https://example.com/mcp"}); err == nil || !strings.Contains(err.Error(), "HTTP") {
		t.Fatalf("url 配置应报 HTTP 传输不支持: %v", err)
	}
	if _, err := Start(ctx, "empty", ServerConfig{}); err == nil {
		t.Fatal("缺 command 应报错")
	}
	if _, err := Start(ctx, "gone", ServerConfig{Command: "definitely-not-a-real-binary-xyz"}); err == nil {
		t.Fatal("不存在的可执行文件应报错")
	}
}

// TestCallAfterServerExit：服务器进程死掉后的调用要带回 stderr 诊断。
func TestCallAfterServerExit(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	c, err := Start(ctx, "mock", ServerConfig{
		Command: exe,
		Env:     map[string]string{"MINDLOOP_MOCK_MCP": "1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	if _, err := c.ListTools(ctx); err == nil {
		t.Fatal("进程退出后的调用应报错")
	}
}
