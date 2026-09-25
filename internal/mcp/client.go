package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
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

// Client 是一台已握手的 MCP 服务器连接（stdio 传输）。方法调用
// 串行化——agent 的工具调用本就是一问一答，不需要并发管道。
type Client struct {
	name    string
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	pending sync.Map // int64 → chan rpcResult
	nextID  int64
	wmu     sync.Mutex

	stderrTail *tailBuffer
	doneCh     chan struct{}
	cancel     context.CancelFunc
	closeOnce  sync.Once
}

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

// Start 启动服务器进程并完成 initialize 握手。
func Start(ctx context.Context, name string, cfg ServerConfig) (*Client, error) {
	switch {
	case cfg.URL != "":
		return nil, fmt.Errorf("mcp: 服务器 %q 配置了 url（HTTP 传输）——当前版本仅支持 stdio（command/args）", name)
	case cfg.Command == "":
		return nil, fmt.Errorf("mcp: 服务器 %q 缺少 command", name)
	}
	cmd := exec.Command(cfg.Command, cfg.Args...)
	cmd.Env = append(os.Environ(), flattenEnv(cfg.Env)...)

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
	c := &Client{
		name:       name,
		cmd:        cmd,
		stdin:      stdin,
		nextID:     0,
		stderrTail: stderr,
		doneCh:     make(chan struct{}),
		cancel:     cancel,
	}
	go func() {
		defer close(c.doneCh)
		<-ctx.Done()
		c.closeOnce.Do(func() {
			stdin.Close()
			_ = cmd.Process.Kill()
		})
	}()
	c.pump(stdout)

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
	if err := c.notify(ctx, "notifications/initialized"); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

// pump 在后台读服务器输出，把响应按 id 分发到等待者，通知丢弃。
func (c *Client) pump(stdout io.ReadCloser) {
	go func() {
		defer func() {
			c.pending.Range(func(_, v any) bool {
				ch := v.(chan rpcResult)
				ch <- rpcResult{Err: fmt.Errorf("服务器进程已退出；stderr: %s", c.stderrTail.String())}
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

// call 发一个请求并等待同 id 的响应。
func (c *Client) call(ctx context.Context, method string, params any, result any) error {
	c.wmu.Lock()
	c.nextID++
	id := c.nextID
	c.wmu.Unlock()

	ch := make(chan rpcResult, 1)
	c.pending.Store(id, ch)
	defer c.pending.Delete(id)

	req := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		req["params"] = params
	}
	line, err := json.Marshal(req)
	if err != nil {
		return err
	}
	c.wmu.Lock()
	if _, werr := fmt.Fprintln(c.stdin, string(line)); werr != nil {
		c.wmu.Unlock()
		return fmt.Errorf("mcp: 写请求失败（服务器可能已退出）: %w；stderr: %s", werr, c.stderrTail.String())
	}
	c.wmu.Unlock()

	select {
	case res := <-ch:
		if res.Err != nil {
			return res.Err
		}
		if result != nil {
			return json.Unmarshal(res.Result, result)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-c.doneCh:
		return fmt.Errorf("mcp: 服务器进程已退出；stderr: %s", c.stderrTail.String())
	}
}

// notify 发一个无需响应的通知。
func (c *Client) notify(ctx context.Context, method string) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	line, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method})
	_, err := fmt.Fprintln(c.stdin, string(line))
	return err
}

// ListTools 返回服务器暴露的工具清单（MCP tools/list）。
func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	var out struct {
		Tools []Tool `json:"tools"`
	}
	if err := c.call(ctx, "tools/list", map[string]any{}, &out); err != nil {
		return nil, fmt.Errorf("mcp: tools/list: %w", err)
	}
	return out.Tools, nil
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

// Close 停止服务器进程（stdin 关闭是 MCP 服务器的标准退出信号，
// kill 兜底；ctx 取消链一并释放）。
func (c *Client) Close() {
	c.closeOnce.Do(func() {
		_ = c.stdin.Close()
		_ = c.cmd.Process.Kill()
		c.cancel()
	})
	_ = c.cmd.Wait()
}

func flattenEnv(env map[string]string) []string {
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}
