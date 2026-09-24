// Package llm 是整个仓库里唯一调用模型供应商的地方——Headlong 的
// "bin/llm 是唯一调用点" 策略的 Go 版。只做最小可用面：非流式
// 补全、两个线协议（openai-compatible 覆盖 OpenAI/OpenRouter/
// Ollama/vLLM/GLM 等一切兼容网关，anthropic 原生）、瞬时错误
// 重试、echo 冒烟供应商。流式与 thinking 选项留给需要它的那一步。
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Message 与 prompt.Message 同构但独立定义：llm 不反向依赖渲染层，
// 转换成本只有三行，依赖图保持单向。
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// 供应商名。
const (
	ProviderOpenAICompat = "openai-compatible"
	ProviderAnthropic    = "anthropic"
	ProviderEcho         = "echo"
)

// Usage 是一次补全的用量（token 计数来自供应商响应）。
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

// Client 是可并发使用的模型客户端：实例无状态（monolith 与
// responder 共用同一个 client 是常态），Complete 之间互不干扰；
// OnDone 回调被串行化调用，回调实现无需自行加锁。
type Client struct {
	Provider   string
	BaseURL    string // openai-compatible: 含 /v1；anthropic: 站点根
	APIKey     string
	Model      string
	MaxTokens  int
	HTTP       *http.Client
	MaxRetries int
	Backoff    time.Duration
	// OnDone 在每次 Complete 结束（无论成败）时被调用一次——
	// CLI 用它接用量台账与健康标记；库自身不做 IO。
	OnDone func(u Usage, err error)

	onDoneMu sync.Mutex
}

// ErrNoProvider 配置不足以构造客户端。
var ErrNoProvider = errors.New("llm: 缺少模型配置（设置 MINDLOOP_MODEL，如 glm-5；或 MINDLOOP_MODEL=echo 体验占位模式）")

// FromEnv 从环境构造客户端。MINDLOOP_PROVIDER 通常可以不设：
// 模型名即供应商标识——claude-* → anthropic，echo → 本地占位，
// 其余 → openai-compatible（glm-* 自动落到智谱端点）。key 依次
// 尝试 MINDLOOP_API_KEY、ANTHROPIC_API_KEY、OPENAI_API_KEY。
// GLM 系模型套用思考型输出预算（Headlong 的事故：GLM 的 thinking
// 吃光 16384 预算后返回零可见字符——finish=length、content 为空、
// 只有 reasoning_content。这里在两层防御：预算分层 + 拆包识别）。
func FromEnv() (*Client, error) { return fromEnv(os.Getenv("MINDLOOP_MODEL")) }

// FromEnvModel 用显式模型名构造客户端，其余配置同 FromEnv——
// 双模型分层的入口：思考档走 MINDLOOP_MODEL，请求档走
// MINDLOOP_REQUEST_MODEL（按请求档模型名重新推断供应商与预算）。
func FromEnvModel(model string) (*Client, error) { return fromEnv(model) }

func fromEnv(model string) (*Client, error) {
	model = strings.TrimSpace(model)
	provider := strings.TrimSpace(os.Getenv("MINDLOOP_PROVIDER"))
	if provider == "" {
		switch {
		case model == "":
			return nil, ErrNoProvider
		case model == "echo":
			provider = ProviderEcho
		case strings.HasPrefix(model, "claude"):
			provider = ProviderAnthropic
		default:
			provider = ProviderOpenAICompat
		}
	}
	c := &Client{
		Provider:   provider,
		Model:      model,
		APIKey:     firstNonEmpty(os.Getenv("MINDLOOP_API_KEY"), os.Getenv("ANTHROPIC_API_KEY"), os.Getenv("OPENAI_API_KEY")),
		BaseURL:    strings.TrimRight(strings.TrimSpace(os.Getenv("MINDLOOP_BASE_URL")), "/"),
		MaxTokens:  8192,
		HTTP:       &http.Client{Timeout: 10 * time.Minute},
		MaxRetries: 3,
		Backoff:    2 * time.Second,
	}
	// 输出预算分层：思考型模型的思考 token 计入 max_tokens，
	// 小预算会被思考耗尽。显式 MINDLOOP_MAX_TOKENS 永远优先。
	if v := os.Getenv("MINDLOOP_MAX_TOKENS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.MaxTokens = n
		}
	} else if isReasoningModel(model) {
		c.MaxTokens = 32768
	}
	switch provider {
	case ProviderEcho:
		return c, nil
	case ProviderAnthropic:
		if c.BaseURL == "" {
			c.BaseURL = "https://api.anthropic.com"
		}
		if c.APIKey == "" {
			return nil, fmt.Errorf("%w: anthropic 需要 API key", ErrNoProvider)
		}
		return c, nil
	case ProviderOpenAICompat:
		switch {
		case c.BaseURL == "" && strings.HasPrefix(model, "glm-"):
			// 智谱开放平台的 OpenAI 兼容端点。
			c.BaseURL = "https://open.bigmodel.cn/api/paas/v4"
		case c.BaseURL == "":
			c.BaseURL = "https://api.openai.com/v1"
		}
		if c.APIKey == "" {
			return nil, fmt.Errorf("%w: openai-compatible 需要 API key", ErrNoProvider)
		}
		return c, nil
	default:
		return nil, fmt.Errorf("%w: 未知供应商 %q", ErrNoProvider, provider)
	}
}

// isReasoningModel 报告模型的思考 token 是否计入输出预算。
// 手维护的名单会老化——所以它只决定预算默认值，MINDLOOP_MAX_TOKENS
// 是永久的逃逸口。
func isReasoningModel(model string) bool {
	m := strings.ToLower(model)
	for _, prefix := range []string{"glm-5", "glm-4.5", "deepseek-r", "o1", "o3", "o4", "qwq", "thinking"} {
		if strings.HasPrefix(m, prefix) || strings.Contains(m, "-thinking") {
			return true
		}
	}
	return false
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// Complete 发送一次非流式补全。system 作为系统提示注入；瞬时
// 错误（网络失败、408/429/5xx）线性退避重试，确定性 4xx 不重试。
// usage 是本次调用的局部值——绝不存在"回调读到上一次调用用量"
// 的串报（共享 lastUsage 的真实事故）。
func (c *Client) Complete(ctx context.Context, system string, msgs []Message) (text string, err error) {
	var usage Usage
	defer func() {
		if c.OnDone != nil {
			c.onDoneMu.Lock()
			c.OnDone(usage, err)
			c.onDoneMu.Unlock()
		}
	}()
	switch c.Provider {
	case ProviderEcho:
		return echoResponse, nil
	case ProviderAnthropic:
		text, usage, err = c.completeAnthropic(ctx, system, msgs)
	case ProviderOpenAICompat:
		text, usage, err = c.completeOpenAI(ctx, system, msgs)
	default:
		return "", fmt.Errorf("llm: 未知供应商 %q", c.Provider)
	}
	return text, err
}

// echoResponse 是 echo 供应商的固定产出：一段能立刻完成运行循环
// 的 bash（echo + FINAL），用于端到端冒烟与测试。
const echoResponse = "```bash\n" +
	"echo \"echo-provider 已收到上下文，直接完成任务。\"\n" +
	"FINAL=\"echo ok\"\n" +
	"```"

func (c *Client) do(ctx context.Context, method, url string, headers map[string]string, body any, extract func([]byte) (string, Usage, error)) (string, Usage, error) {
	var lastErr error
	var lastUsage Usage
	for attempt := 0; attempt <= c.MaxRetries; attempt++ {
		if attempt > 0 && c.Backoff > 0 {
			select {
			case <-ctx.Done():
				return "", lastUsage, ctx.Err()
			case <-time.After(c.Backoff * time.Duration(attempt)):
			}
		}
		text, usage, retryable, err := c.attempt(ctx, method, url, headers, body, extract)
		lastUsage = usage
		if err == nil {
			return text, usage, nil
		}
		lastErr = err
		if !retryable {
			return "", usage, err
		}
	}
	return "", lastUsage, fmt.Errorf("llm: 重试 %d 次后仍失败: %w", c.MaxRetries, lastErr)
}

// attempt 返回 (文本, 用量, 是否可重试, 错误)。
func (c *Client) attempt(ctx context.Context, method, url string, headers map[string]string, body any, extract func([]byte) (string, Usage, error)) (string, Usage, bool, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return "", Usage{}, false, fmt.Errorf("llm: 序列化请求: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return "", Usage{}, false, fmt.Errorf("llm: 构造请求: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", Usage{}, true, fmt.Errorf("llm: 网络错误: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return "", Usage{}, true, fmt.Errorf("llm: 读取响应: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		snippet := snippet(data)
		retryable := resp.StatusCode == http.StatusRequestTimeout ||
			resp.StatusCode == http.StatusTooManyRequests ||
			resp.StatusCode >= 500
		return "", Usage{}, retryable, fmt.Errorf("llm: HTTP %d: %s", resp.StatusCode, snippet)
	}
	text, usage, err := extract(data)
	if err != nil {
		return "", usage, false, err
	}
	return text, usage, false, nil
}

// parseUsage 统一两家供应商的 usage 字段名。
func parseUsage(data []byte) Usage {
	var raw struct {
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			InputTokens      int `json:"input_tokens"`
			OutputTokens     int `json:"output_tokens"`
		} `json:"usage"`
	}
	_ = json.Unmarshal(data, &raw)
	u := Usage{PromptTokens: raw.Usage.PromptTokens, CompletionTokens: raw.Usage.CompletionTokens}
	if u.PromptTokens == 0 {
		u.PromptTokens = raw.Usage.InputTokens
	}
	if u.CompletionTokens == 0 {
		u.CompletionTokens = raw.Usage.OutputTokens
	}
	return u
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}

func (c *Client) completeOpenAI(ctx context.Context, system string, msgs []Message) (string, Usage, error) {
	payload := map[string]any{
		"model":      c.Model,
		"messages":   append([]Message{{Role: "system", Content: system}}, msgs...),
		"max_tokens": c.MaxTokens,
	}
	headers := map[string]string{"Authorization": "Bearer " + c.APIKey}
	url := c.BaseURL + "/chat/completions"
	return c.do(ctx, http.MethodPost, url, headers, payload, func(data []byte) (string, Usage, error) {
		usage := parseUsage(data)
		var out struct {
			Choices []struct {
				Message struct {
					Content          string `json:"content"`
					ReasoningContent string `json:"reasoning_content"`
				} `json:"message"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			return "", usage, fmt.Errorf("llm: 解析 openai 响应: %w (%s)", err, snippet(data))
		}
		// openrouter 等网关会把上游错误装进 200 响应体——必须拆包
		// 检查，否则"HTTP 200 但内容为空"会被当成正常回答。
		if out.Error != nil {
			return "", usage, fmt.Errorf("llm: 上游错误: %s", out.Error.Message)
		}
		if len(out.Choices) == 0 {
			return "", usage, fmt.Errorf("llm: openai 响应没有 choices (%s)", snippet(data))
		}
		ch := out.Choices[0]
		if strings.TrimSpace(ch.Message.Content) != "" {
			return ch.Message.Content, usage, nil
		}
		// 思考型模型的两种真实失败形态（Headlong 的 GLM 事故）：
		// 预算被 thinking 耗尽 → finish=length、content 空、只剩
		// reasoning_content；或纯空回答。都给出可执行的解法。
		if ch.Message.ReasoningContent != "" && ch.FinishReason == "length" {
			return "", usage, fmt.Errorf("llm: 思考耗尽了输出预算（finish=length，无可见内容）——调大 MINDLOOP_MAX_TOKENS（当前 %d）", c.MaxTokens)
		}
		if strings.TrimSpace(ch.Message.ReasoningContent) != "" {
			return "", usage, fmt.Errorf("llm: 模型只输出了思考没有答案（reasoning_content 非空）——检查模型对提示的兼容性或调大预算")
		}
		return "", usage, fmt.Errorf("llm: 空回答（finish=%s）", ch.FinishReason)
	})
}

func (c *Client) completeAnthropic(ctx context.Context, system string, msgs []Message) (string, Usage, error) {
	payload := map[string]any{
		"model":      c.Model,
		"max_tokens": c.MaxTokens,
		"system":     system,
		"messages":   msgs,
	}
	headers := map[string]string{
		"x-api-key":         c.APIKey,
		"anthropic-version": "2023-06-01",
	}
	url := c.BaseURL + "/v1/messages"
	return c.do(ctx, http.MethodPost, url, headers, payload, func(data []byte) (string, Usage, error) {
		usage := parseUsage(data)
		var out struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			return "", usage, fmt.Errorf("llm: 解析 anthropic 响应: %w (%s)", err, snippet(data))
		}
		if out.Error != nil {
			return "", usage, fmt.Errorf("llm: 上游错误: %s", out.Error.Message)
		}
		var b strings.Builder
		for _, part := range out.Content {
			if part.Type == "text" {
				b.WriteString(part.Text)
			}
		}
		if b.Len() == 0 {
			return "", usage, fmt.Errorf("llm: anthropic 响应没有文本 (%s)", snippet(data))
		}
		return b.String(), usage, nil
	})
}
