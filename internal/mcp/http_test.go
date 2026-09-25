package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// newStreamableServer 起一台遵循 Streamable HTTP 传输的 mock MCP
// 服务器：initialize 颁发会话头、后续请求必须回传；sseMode=true 时
// 响应改用 text/event-stream 分帧（同一规范允许的两种形态）。
func newStreamableServer(t *testing.T, sseMode bool) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	session := ""
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		var msg rpcMessage
		if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		respond := func(result any) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": msg.ID, "result": result,
			})
		}
		respondSSE := func(result any) {
			w.Header().Set("Content-Type", "text/event-stream")
			body, _ := json.Marshal(map[string]any{
				"jsonrpc": "2.0", "id": msg.ID, "result": result,
			})
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", body)
		}
		switch msg.Method {
		case "initialize":
			mu.Lock()
			session = "sess-" + fmt.Sprint(time.Now().UnixNano())
			sid := session
			mu.Unlock()
			w.Header().Set("Mcp-Session-Id", sid)
			result := map[string]any{
				"protocolVersion": protocolVersion,
				"serverInfo":      map[string]any{"name": "mock-http", "version": "0.1"},
			}
			if sseMode {
				respondSSE(result)
			} else {
				respond(result)
			}
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			// 会话头必须回传——Streamable HTTP 的会话语义。
			mu.Lock()
			sent := session
			mu.Unlock()
			if got := r.Header.Get("Mcp-Session-Id"); got != sent {
				http.Error(w, "session id missing", http.StatusBadRequest)
				return
			}
			result := map[string]any{"tools": []map[string]any{
				{"name": "ping", "description": "返回 pong", "inputSchema": map[string]any{"type": "object"}},
			}}
			if sseMode {
				respondSSE(result)
			} else {
				respond(result)
			}
		case "tools/call":
			result := map[string]any{"content": []map[string]any{
				{"type": "text", "text": "pong"},
			}, "isError": false}
			if sseMode {
				respondSSE(result)
			} else {
				respond(result)
			}
		default:
			if msg.ID != nil {
				respond(map[string]any{})
			}
		}
	}))
}

// TestStreamableHTTPJSON：application/json 形态的全链路——握手
// （会话头下发并回传）、tools/list、tools/call。
func TestStreamableHTTPJSON(t *testing.T) {
	srv := newStreamableServer(t, false)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	c, err := Start(ctx, "remote", ServerConfig{URL: srv.URL + "/mcp"})
	if err != nil {
		t.Fatalf("握手失败: %v", err)
	}
	defer c.Close()

	tools, err := c.ListTools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "ping" {
		t.Fatalf("tools/list 错位: %+v", tools)
	}
	res, err := c.CallTool(ctx, "ping", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "pong" {
		t.Fatalf("结果错位: %q", res.Text)
	}
}

// TestStreamableHTTPSSE：同协议允许的 SSE 分帧形态。
func TestStreamableHTTPSSE(t *testing.T) {
	srv := newStreamableServer(t, true)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	c, err := Start(ctx, "remote-sse", ServerConfig{URL: srv.URL + "/mcp"})
	if err != nil {
		t.Fatalf("握手失败: %v", err)
	}
	defer c.Close()
	if _, err := c.ListTools(ctx); err != nil {
		t.Fatal(err)
	}
	res, err := c.CallTool(ctx, "ping", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "pong" {
		t.Fatalf("结果错位: %q", res.Text)
	}
}

// TestHTTPHeadersPassthrough：配置的 headers 原样到达服务器
// （鉴权头是远程 MCP 服务器的普遍前置条件）。
func TestHTTPHeadersPassthrough(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		var msg rpcMessage
		json.NewDecoder(r.Body).Decode(&msg)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": msg.ID,
			"result": map[string]any{"protocolVersion": protocolVersion},
		})
	}))
	defer srv.Close()
	ctx := context.Background()
	c, err := Start(ctx, "authed", ServerConfig{
		URL:     srv.URL,
		Headers: map[string]string{"Authorization": "Bearer tok"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if !strings.Contains(gotAuth, "Bearer tok") {
		t.Fatalf("Authorization 头未透传: %q", gotAuth)
	}
}
