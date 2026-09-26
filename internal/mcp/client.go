package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"mindloop/internal/childenv"
)

// protocolVersion 是本客户端支持的 MCP 协议版本。握手时发送给
// 服务器；服务器按规范回它自己支持的版本，我们一律接受（版本
// 协商的分歧留给后续方法调用时自然暴露）。
const protocolVersion = "2025-06-18"

// Tool 是 tools/list 返回的一个工具。
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

// CallResult 是 tools/call 的结果文本——content 数组里的 text 片段
// 按序拼接（MCP 内容块标准形态；非文本块以占位符标注）。
type CallResult struct {
	Text string
}

// rpcMessage 是 JSON-RPC 2.0 消息的解码形态（请求/响应/通知通吃）。
type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id"`
	Method  string          `json:"method"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("mcp: rpc %d: %s", e.Code, e.Message) }

// conn 是传输层接口：stdio 与 streamable HTTP 各一个实现。方法
// 语义即 JSON-RPC 语义——call 发请求并等同 id 响应，notify 发通知。
type conn interface {
	call(ctx context.Context, id int64, method string, params any) (json.RawMessage, error)
	notify(ctx context.Context, method string) error
	close() error
}

// Client 是一台已握手的 MCP 服务器连接。方法调用串行化——agent
// 的工具调用本就是一问一答，不需要并发管道。
type Client struct {
	name   string
	conn   conn
	nextID int64
	mu     sync.Mutex
}

// Start 启动连接并完成 initialize 握手。传输按配置自动选择：
// url 非空 → streamable HTTP；否则 command → stdio。
func Start(ctx context.Context, name string, cfg ServerConfig) (*Client, error) {
	c := &Client{name: name}
	switch {
	case cfg.URL != "":
		c.conn = newHTTPConn(cfg)
	case cfg.Command != "":
		var err error
		if c.conn, err = newStdioConn(ctx, name, cfg); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("mcp: 服务器 %q 缺少 command 或 url", name)
	}

	// initialize 握手 + initialized 通知（MCP 规范的标准序列）。
	var info struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := c.call(ctx, "initialize", map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "mindloop", "version": "0.6.0"},
	}, &info); err != nil {
		c.Close()
		return nil, fmt.Errorf("mcp: 服务器 %q initialize 失败: %w", name, err)
	}
	if err := c.conn.notify(ctx, "notifications/initialized"); err != nil {
		c.Close()
		return nil, fmt.Errorf("mcp: 服务器 %q initialized 通知失败: %w", name, err)
	}
	return c, nil
}

// call 发一个请求并等待同 id 的响应（id 由 Client 统一分配）。
func (c *Client) call(ctx context.Context, method string, params any, result any) error {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	c.mu.Unlock()

	raw, err := c.conn.call(ctx, id, method, params)
	if err != nil {
		return err
	}
	if result != nil {
		return json.Unmarshal(raw, result)
	}
	return nil
}

// notify 发一个无需响应的通知。
func (c *Client) notify(ctx context.Context, method string) error {
	return c.conn.notify(ctx, method)
}

// ListTools 返回服务器暴露的工具清单（MCP tools/list）。
func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	tools := make([]Tool, 0)
	params := map[string]any{}
	seen := map[string]bool{}
	for {
		var out struct {
			Tools      []Tool `json:"tools"`
			NextCursor string `json:"nextCursor"`
		}
		if err := c.call(ctx, "tools/list", params, &out); err != nil {
			return nil, fmt.Errorf("mcp: tools/list: %w", err)
		}
		tools = append(tools, out.Tools...)
		if out.NextCursor == "" {
			return tools, nil
		}
		if seen[out.NextCursor] {
			return nil, fmt.Errorf("mcp: tools/list 返回重复 cursor %q", out.NextCursor)
		}
		seen[out.NextCursor] = true
		params["cursor"] = out.NextCursor
	}
}

// CallTool 调用一个工具（MCP tools/call），拼接 text 内容块为结果。
// 服务器标注 isError 时返回错误（错误文本一并带回，调用方仍能展示）。
func (c *Client) CallTool(ctx context.Context, tool string, args json.RawMessage) (CallResult, error) {
	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	params := map[string]any{"name": tool}
	if len(args) > 0 {
		params["arguments"] = args
	} else {
		params["arguments"] = map[string]any{}
	}
	if err := c.call(ctx, "tools/call", params, &out); err != nil {
		return CallResult{}, fmt.Errorf("mcp: tools/call %s: %w", tool, err)
	}
	var b strings.Builder
	for _, part := range out.Content {
		if part.Type == "text" {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(part.Text)
		} else {
			fmt.Fprintf(&b, "\n[%s 内容块]", part.Type)
		}
	}
	res := CallResult{Text: b.String()}
	if out.IsError {
		return res, fmt.Errorf("mcp: 工具 %s 报错: %s", tool, res.Text)
	}
	return res, nil
}

// Close 停止连接（stdio 关 stdin + kill 兜底；HTTP 无常驻资源）。
func (c *Client) Close() {
	if c.conn != nil {
		_ = c.conn.close()
	}
}

// ---------------------------------------------------------------- stdio

// rpcResult 是响应分发通道里的载荷。
type rpcResult struct {
	Result json.RawMessage
	Err    error
}

// tailBuffer 收集服务器 stderr 的最后若干字节——进程崩溃时给
// 出能定位的诊断而不是一句 "exit status 1"。
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > 4096 {
		t.buf = t.buf[len(t.buf)-4096:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(string(t.buf))
}

// stdioConn 是 newline 分帧的 JSON-RPC over stdio——本地 MCP 服务
// 器最普遍的传输形态。
type stdioConn struct {
	name     string
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	pending  sync.Map // int64 → chan rpcResult
	stderr   *tailBuffer
	doneCh   chan struct{}
	cancel   context.CancelFunc
	closed   sync.Once
	closeErr error
}

func newStdioConn(ctx context.Context, name string, cfg ServerConfig) (*stdioConn, error) {
	cmd := exec.Command(cfg.Command, cfg.Args...)
	// MCP 子进程环境是白名单：服务器进程往往是第三方包（npx 拉来
	// 的），只获得运行所需的基础变量与自己的显式配置凭据；父环境
	// 里的模型网关与仪表盘凭据不下传。
	cmd.Env = append(childenv.Inherit(os.Environ(), nil), flattenEnv(cfg.Env)...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr := &tailBuffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mcp: 启动服务器 %q (%s): %w", name, cfg.Command, err)
	}
	ctx, cancel := context.WithCancel(ctx)
	c := &stdioConn{
		name:   name,
		cmd:    cmd,
		stdin:  stdin,
		stderr: stderr,
		doneCh: make(chan struct{}),
		cancel: cancel,
	}
	go func() {
		defer close(c.doneCh)
		<-ctx.Done()
		_ = stdin.Close()
		_ = cmd.Process.Kill()
	}()
	c.pump(stdout)
	return c, nil
}

// pump 在后台读服务器输出，把响应按 id 分发到等待者，通知丢弃。
func (c *stdioConn) pump(stdout io.ReadCloser) {
	go func() {
		defer func() {
			c.pending.Range(func(_, v any) bool {
				v.(chan rpcResult) <- rpcResult{Err: fmt.Errorf("服务器进程已退出；stderr: %s", c.stderr.String())}
				return true
			})
		}()
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				continue
			}
			var msg rpcMessage
			if json.Unmarshal([]byte(line), &msg) != nil {
				continue // 坏行跳过（服务器的非协议输出混入时不应崩）
			}
			if msg.ID == nil {
				continue // 通知：v1 不处理服务器主动推送
			}
			if v, ok := c.pending.Load(*msg.ID); ok {
				c.pending.Delete(*msg.ID)
				res := rpcResult{Result: msg.Result}
				if msg.Error != nil {
					res.Err = msg.Error
				}
				v.(chan rpcResult) <- res
			}
		}
	}()
}

func (c *stdioConn) call(ctx context.Context, id int64, method string, params any) (json.RawMessage, error) {
	ch := make(chan rpcResult, 1)
	c.pending.Store(id, ch)
	defer c.pending.Delete(id)

	if err := c.writeMessage(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		return nil, err
	}
	select {
	case res := <-ch:
		return res.Result, res.Err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.doneCh:
		return nil, fmt.Errorf("mcp: 服务器进程已退出；stderr: %s", c.stderr.String())
	}
}

func (c *stdioConn) notify(ctx context.Context, method string) error {
	return c.writeMessage(map[string]any{"jsonrpc": "2.0", "method": method})
}

func (c *stdioConn) writeMessage(msg map[string]any) error {
	line, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(c.stdin, string(line))
	if err != nil {
		return fmt.Errorf("mcp: 写请求失败（服务器可能已退出）: %w；stderr: %s", err, c.stderr.String())
	}
	return nil
}

func (c *stdioConn) close() error {
	c.closed.Do(func() {
		c.closeErr = func() error {
			_ = c.stdin.Close()
			_ = c.cmd.Process.Kill()
			c.cancel()
			return c.cmd.Wait()
		}()
	})
	return c.closeErr
}

// ------------------------------------------------- streamable HTTP

// httpConn 是 Streamable HTTP 传输（MCP 2025-03-26+ 规范）：
// JSON-RPC 经 POST 上行，服务器以 application/json（单响应）或
// text/event-stream（SSE 分帧）回送；initialize 返回的
// Mcp-Session-Id 在后续请求回传。
type httpConn struct {
	url       string
	headers   map[string]string
	client    *http.Client
	sessionMu sync.Mutex
	sessionID string
}

// httpTimeout 是单次 HTTP 上行的兜底超时：调用方的 ctx 之外再保
// 一层——服务器挂起时调用不至于无限等（与 llm.Client 的默认同款）。
const httpTimeout = 10 * time.Minute

func newHTTPConn(cfg ServerConfig) *httpConn {
	return &httpConn{
		url:     cfg.URL,
		headers: cfg.Headers,
		client:  &http.Client{Timeout: httpTimeout},
	}
}

// post 上行一条消息，返回响应。expectBody=false 时（通知）接受
// 202 空响应；通知路径从不向调用方交付 body——一律就地关闭，
// 服务器不守规范回 200 时也不泄漏连接。
func (h *httpConn) post(ctx context.Context, line []byte, expectBody bool) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.url, strings.NewReader(string(line)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	// 规范要求同时接受两种响应形态。
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", protocolVersion)
	h.sessionMu.Lock()
	if h.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", h.sessionID)
	}
	h.sessionMu.Unlock()
	for k, v := range h.headers {
		req.Header.Set(k, v)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mcp: http 上行失败: %w", err)
	}
	if !expectBody {
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			snippet := readSnippet(resp.Body)
			return nil, fmt.Errorf("mcp: http %d: %s", resp.StatusCode, snippet)
		}
		if resp.StatusCode != http.StatusAccepted {
			_ = readSnippet(resp.Body) // 排干再关，连接可回池
		}
		return resp, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		snippet := readSnippet(resp.Body)
		return nil, fmt.Errorf("mcp: http %d: %s", resp.StatusCode, snippet)
	}
	return resp, nil
}

// readSnippet 读响应体的前若干字节用于错误诊断。
func readSnippet(r io.Reader) string {
	sn, _ := io.ReadAll(io.LimitReader(r, 300))
	return strings.TrimSpace(string(sn))
}

func (h *httpConn) call(ctx context.Context, id int64, method string, params any) (json.RawMessage, error) {
	line, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		return nil, err
	}
	resp, err := h.post(ctx, line, true)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// 会话建立：initialize 的响应头带 Mcp-Session-Id，后续回传。
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		h.sessionMu.Lock()
		h.sessionID = sid
		h.sessionMu.Unlock()
	}

	ct := resp.Header.Get("Content-Type")
	switch {
	case strings.HasPrefix(ct, "text/event-stream"):
		return parseSSEResponse(resp.Body, id)
	default:
		var msg rpcMessage
		// 响应体限幅：与 stdio 帧上限一致，防失控服务器撑爆内存。
		if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&msg); err != nil {
			return nil, fmt.Errorf("mcp: 解析 http 响应: %w", err)
		}
		if msg.Error != nil {
			return nil, msg.Error
		}
		return msg.Result, nil
	}
}

// parseSSEResponse 从 SSE 分帧里取出与本请求 id 匹配的那条响应
// （data: 行承载 JSON-RPC 消息，event: message）。
func parseSSEResponse(body io.Reader, id int64) (json.RawMessage, error) {
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		var msg rpcMessage
		if json.Unmarshal([]byte(data), &msg) != nil {
			continue
		}
		if msg.ID == nil || *msg.ID != id {
			continue
		}
		if msg.Error != nil {
			return nil, msg.Error
		}
		return msg.Result, nil
	}
	return nil, fmt.Errorf("mcp: SSE 流结束仍未收到 id=%d 的响应", id)
}

func (h *httpConn) notify(ctx context.Context, method string) error {
	line, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method})
	if err != nil {
		return err
	}
	_, err = h.post(ctx, line, false)
	return err
}

func (h *httpConn) close() error { return nil }

func flattenEnv(env map[string]string) []string {
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}
