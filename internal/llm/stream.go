// 流式补全：CompleteStream 与三 provider 的 SSE 解析。与 Complete
// 共享 Client 配置（MaxTokens 分层、退避参数、OnDone 观测），差异
// 只在传输与解析——增量经 onDelta 逐段交给调用方，完整结果在返回
// 值里一次给全。
package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Request 是一次补全请求（Complete 的 (system, msgs) 二参形态的
// 结构化版本——流式签名里要给回调留位置，参数收敛成结构体）。
type Request struct {
	System   string
	Messages []Message
}

// Result 是一次补全的完整结果：Text 恒等于全部增量拼接（流式与
// 非流式同一语义），Usage/Model/FinishReason 来自供应商响应。
type Result struct {
	Text         string
	Usage        Usage
	Model        string
	FinishReason string
}

// streamConsumer 消费一段 SSE 响应体：把可见增量经 onDelta 逐段
// 交出，流正常结束后返回完整 Result。
type streamConsumer func(r io.Reader, onDelta func(string)) (Result, error)

// errStreamInterrupted 标记传输层的流中断（连接断开、[DONE] 缺失）
// ——与"供应商明确报错"区分：前者在首个增量之前可重试，后者不可。
var errStreamInterrupted = errors.New("llm: 流被中断")

// echoChunkRunes 是 echo 供应器的合成分段粒度（rune 数）：足够小
// 能暴露调用方的增量拼接 bug，又不至于把回调打成碎片洪水。
const echoChunkRunes = 24

// CompleteStream 发起一次流式补全：模型每产出一小段可见文本就调
// 用一次 onDelta（UTF-8 安全，绝不切在多字节字符中间），结束后
// 返回完整 Result。onDelta 为 nil 时退化为非流式消费。
//
// 重试语义：仅当**首个增量尚未发出**时，连接错误与瞬时 HTTP 错误
// （408/429/5xx）按现行线性退避 + Retry-After 重试；首个增量一旦
// 回调出去，任何后续错误都直接返回——已经交给调用方拼进界面的
// 文本无法撤回，重试只会造成重复。错误信息会注明中断点在首增量
// 之后，调用方可据此决定已渲染内容的处置。
func (c *Client) CompleteStream(ctx context.Context, req Request, onDelta func(string)) (Result, error) {
	if onDelta == nil {
		onDelta = func(string) {}
	}
	var res Result
	var err error
	defer func() {
		if c.OnDone != nil {
			c.onDoneMu.Lock()
			c.OnDone(res.Usage, err)
			c.onDoneMu.Unlock()
		}
	}()
	switch c.Provider {
	case ProviderEcho:
		res = streamEcho(onDelta)
	case ProviderAnthropic:
		res, err = c.doStream(ctx, c.streamHeaders("anthropic"),
			c.anthropicStreamBody(req), c.parseAnthropicStream, onDelta)
	case ProviderOpenAICompat:
		res, err = c.doStream(ctx, c.streamHeaders("openai"),
			c.openaiStreamBody(req), c.parseOpenAIStream, onDelta)
	default:
		return Result{}, fmt.Errorf("llm: 未知供应商 %q", c.Provider)
	}
	return res, err
}

// streamURL 与请求体构造和非流式路径逐字段对齐（system 注入、
// max_tokens 思考型预算分层），只多 "stream": true。
func (c *Client) streamURL() string {
	if c.Provider == ProviderAnthropic {
		return c.BaseURL + "/v1/messages"
	}
	return c.BaseURL + "/chat/completions"
}

func (c *Client) streamHeaders(kind string) map[string]string {
	if kind == "anthropic" {
		return map[string]string{
			"x-api-key":         c.APIKey,
			"anthropic-version": "2023-06-01",
			"accept":            "text/event-stream",
		}
	}
	return map[string]string{
		"Authorization": "Bearer " + c.APIKey,
		"accept":        "text/event-stream",
	}
}

func (c *Client) openaiStreamBody(req Request) map[string]any {
	msgs := make([]Message, 0, len(req.Messages)+1)
	if req.System != "" {
		msgs = append(msgs, Message{Role: "system", Content: req.System})
	}
	msgs = append(msgs, req.Messages...)
	return map[string]any{
		"model":      c.Model,
		"messages":   msgs,
		"max_tokens": c.MaxTokens,
		"stream":     true,
	}
}

func (c *Client) anthropicStreamBody(req Request) map[string]any {
	return map[string]any{
		"model":      c.Model,
		"max_tokens": c.MaxTokens,
		"system":     req.System,
		"messages":   req.Messages,
		"stream":     true,
	}
}

// doStream 是流式路径的重试循环，与非流式的 do 同一套退避纪律
// （线性 + 抖动 + Retry-After，封顶 maxBackoff），多一条铁律：任何
// 一次尝试已经发出过增量，就绝不再重试。
func (c *Client) doStream(ctx context.Context, headers map[string]string, body any, consume streamConsumer, onDelta func(string)) (Result, error) {
	var lastErr error
	var retryAfter time.Duration
	for attempt := 0; attempt <= c.MaxRetries; attempt++ {
		if attempt > 0 && c.Backoff > 0 {
			select {
			case <-ctx.Done():
				return Result{}, ctx.Err()
			case <-time.After(backoffDelay(c.Backoff, attempt, retryAfter)):
			}
		}
		res, emitted, retryable, suggested, err := c.streamAttempt(ctx, headers, body, consume, onDelta)
		retryAfter = suggested
		if err == nil {
			return res, nil
		}
		lastErr = err
		if emitted {
			// 首增量之后：已发出的文本收不回来，重试必然重复拼接
			// ——立即失败并把中断语义交给调用方。
			return Result{}, fmt.Errorf("llm: 流式输出中断于首增量之后（已发出部分有效，本次调用失败）: %w", err)
		}
		if !retryable {
			return Result{}, err
		}
	}
	return Result{}, fmt.Errorf("llm: 重试 %d 次后仍失败: %w", c.MaxRetries, lastErr)
}

// streamAttempt 发起一次流式请求并消费到流结束。emitted 报告本次
// 尝试是否已向 onDelta 发出过增量。
func (c *Client) streamAttempt(ctx context.Context, headers map[string]string, body any, consume streamConsumer, onDelta func(string)) (res Result, emitted bool, retryable bool, retryAfter time.Duration, err error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return Result{}, false, false, 0, fmt.Errorf("llm: 序列化请求: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.streamURL(), bytes.NewReader(payload))
	if err != nil {
		return Result{}, false, false, 0, fmt.Errorf("llm: 构造请求: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	// 流式不复用 c.HTTP 的整体超时：http.Client.Timeout 把响应体
	// 读取也封在 10 分钟里，长回答会在半途被拦腰斩断且按语义不可
	// 重试。流式活性由 ctx 与上层的空闲看护负责。
	hc := *c.HTTP
	hc.Timeout = 0
	resp, err := hc.Do(req)
	if err != nil {
		return Result{}, false, true, 0, fmt.Errorf("llm: 网络错误: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, rerr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		if rerr != nil {
			return Result{}, false, true, 0, fmt.Errorf("llm: 读取错误响应: %w", rerr)
		}
		retryable := resp.StatusCode == http.StatusRequestTimeout ||
			resp.StatusCode == http.StatusTooManyRequests ||
			resp.StatusCode >= 500
		return Result{}, false, retryable, parseRetryAfter(resp.Header.Get("Retry-After")),
			fmt.Errorf("llm: HTTP %d: %s", resp.StatusCode, snippet(data))
	}
	gotDelta := false
	wrapped := func(delta string) {
		gotDelta = true
		onDelta(delta)
	}
	res, err = consume(resp.Body, wrapped)
	// 流中断（连接断开、哨兵缺失）若发生在首增量之前，属于可重试
	// 的连接类失败；供应商明确报错（上游错误对象）不重试。首增量
	// 之后的可否重试由 doStream 的 emitted 铁律裁决。
	retryable = err != nil && !gotDelta && errors.Is(err, errStreamInterrupted)
	return res, gotDelta, retryable, 0, err
}

// sseScanner 把响应体切成 SSE data 行：跳过注释行（": keep-alive"）
// 与空行，去掉 "data:" 前缀与两侧空白。行重组由 Scanner 完成——
// 供应商把一条 JSON 分多次 TCP 写、多字节字符跨 chunk 切割都在
// 这里被无损拼回。
func sseScanner(r io.Reader) *bufio.Scanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	return sc
}

func nextSSEData(sc *bufio.Scanner) (string, bool) {
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		return strings.TrimSpace(strings.TrimPrefix(line, "data:")), true
	}
	return "", false
}

// ---------------------------------------------------------------- openai

// openaiStreamChunk 是 openai-compatible 流式响应的一个 data 载荷。
// ReasoningContent：GLM 等思考型模型的思考增量——不进正文，但
// "只有思考没有答案"的失败形态要在流式路径给出同样的诊断。
type openaiStreamChunk struct {
	Model   string `json:"model"`
	Choices []struct {
		Delta struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *Usage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// parseOpenAIStream 消费 openai-compatible SSE：choices[0].delta.content
// 逐段回调；[DONE] 哨兵结束；末 chunk 的 usage 若有则采集；网关把
// 错误装进 HTTP 200 的行为同样拆包识别。流结束时没有 [DONE] 视为
// 中断（内容可能被截断，不能当完整结果交付）。
func (c *Client) parseOpenAIStream(r io.Reader, onDelta func(string)) (Result, error) {
	var res Result
	var sawReasoning bool
	done := false
	sc := sseScanner(r)
	for {
		data, ok := nextSSEData(sc)
		if !ok {
			break
		}
		if data == "[DONE]" {
			done = true
			break
		}
		var chunk openaiStreamChunk
		if json.Unmarshal([]byte(data), &chunk) != nil {
			continue // 坏行跳过，与非流式的容错纪律一致
		}
		if chunk.Error != nil {
			return Result{}, fmt.Errorf("llm: 上游错误: %s", chunk.Error.Message)
		}
		if chunk.Model != "" {
			res.Model = chunk.Model
		}
		if chunk.Usage != nil && (chunk.Usage.PromptTokens > 0 || chunk.Usage.CompletionTokens > 0) {
			res.Usage = *chunk.Usage
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		ch := chunk.Choices[0]
		if ch.FinishReason != "" {
			res.FinishReason = ch.FinishReason
		}
		if strings.TrimSpace(ch.Delta.ReasoningContent) != "" {
			sawReasoning = true
		}
		if ch.Delta.Content != "" {
			res.Text += ch.Delta.Content
			onDelta(ch.Delta.Content)
		}
	}
	if err := sc.Err(); err != nil {
		return Result{}, fmt.Errorf("%w: 读取响应: %v", errStreamInterrupted, err)
	}
	if !done {
		return Result{}, fmt.Errorf("%w: 流结束但没有收到 [DONE] 哨兵", errStreamInterrupted)
	}
	if res.Model == "" {
		res.Model = c.Model
	}
	// 与 Complete 相同的两种思考型失败形态诊断（GLM 事故）。
	if strings.TrimSpace(res.Text) == "" {
		if sawReasoning && res.FinishReason == "length" {
			return Result{}, fmt.Errorf("llm: 思考耗尽了输出预算（finish=length，无可见内容）——调大 MINDLOOP_MAX_TOKENS（当前 %d）", c.MaxTokens)
		}
		if sawReasoning {
			return Result{}, errors.New("llm: 模型只输出了思考没有答案（reasoning_content 非空）——检查模型对提示的兼容性或调大预算")
		}
		return Result{}, fmt.Errorf("llm: 空回答（finish=%s）", res.FinishReason)
	}
	return res, nil
}

// ---------------------------------------------------------------- anthropic

// anthropicStreamEvent 是 anthropic SSE 的一个 data 载荷。anthropic
// 没有 [DONE] 哨兵——message_stop 事件即流结束；event: 行不参与
// 分发（每个 data 载荷自带 type 字段）。
type anthropicStreamEvent struct {
	Type  string `json:"type"`
	Delta struct {
		Type       string `json:"type"`
		Text       string `json:"text"`
		StopReason string `json:"stop_reason"`
	} `json:"delta"`
	Message *struct {
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	} `json:"message"`
	Usage *struct {
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// parseAnthropicStream 消费 anthropic SSE：content_block_delta 的
// text_delta 逐段回调；message_start 带输入 token，message_delta 带
// stop_reason 与输出 token。
func (c *Client) parseAnthropicStream(r io.Reader, onDelta func(string)) (Result, error) {
	var res Result
	sc := sseScanner(r)
	stopped := false
	for {
		data, ok := nextSSEData(sc)
		if !ok {
			break
		}
		var ev anthropicStreamEvent
		if json.Unmarshal([]byte(data), &ev) != nil {
			continue
		}
		switch ev.Type {
		case "error":
			return Result{}, fmt.Errorf("llm: 上游错误: %s", ev.Error.Message)
		case "content_block_delta":
			if ev.Delta.Type == "text_delta" && ev.Delta.Text != "" {
				res.Text += ev.Delta.Text
				onDelta(ev.Delta.Text)
			}
		case "message_start":
			if ev.Message != nil && ev.Message.Usage.InputTokens > 0 {
				res.Usage.PromptTokens = ev.Message.Usage.InputTokens
			}
		case "message_delta":
			if ev.Delta.StopReason != "" {
				res.FinishReason = ev.Delta.StopReason
			}
			if ev.Usage != nil && ev.Usage.OutputTokens > 0 {
				res.Usage.CompletionTokens = ev.Usage.OutputTokens
			}
		case "message_stop":
			stopped = true
		}
	}
	if err := sc.Err(); err != nil {
		return Result{}, fmt.Errorf("%w: 读取响应: %v", errStreamInterrupted, err)
	}
	if !stopped {
		return Result{}, fmt.Errorf("%w: 流结束但没有收到 message_stop 事件", errStreamInterrupted)
	}
	if strings.TrimSpace(res.Text) == "" {
		return Result{}, fmt.Errorf("llm: anthropic 流式响应没有文本（stop_reason=%s）", res.FinishReason)
	}
	return res, nil
}

// ---------------------------------------------------------------- echo

// streamEcho 合成分段流：把固定产出按 rune 切片逐段回调——无网络
// 可测，还能暴露调用方"忘记拼接增量"之类的 bug。UTF-8 安全由
// rune 切片天然保证。
func streamEcho(onDelta func(string)) Result {
	runes := []rune(echoResponse)
	for i := 0; i < len(runes); i += echoChunkRunes {
		end := i + echoChunkRunes
		if end > len(runes) {
			end = len(runes)
		}
		onDelta(string(runes[i:end]))
	}
	return Result{Text: echoResponse, Model: "echo", FinishReason: "stop"}
}
