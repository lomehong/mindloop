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
	"math/rand"
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
	// Known 报告供应商响应是否真的带了 usage 数据：缺失时 token
	// 计数保持零值但 Known=false——未知就是未知，不伪装成零成本。
	Known bool `json:"usage_known"`
	// LatencyMS 是本次调用（含重试与退避）的墙钟耗时。
	LatencyMS int64 `json:"latency_ms,omitempty"`
	// Retries 是本次调用实际重试的次数（首次即成功为 0）。
	Retries int `json:"retries,omitempty"`
	// 归因字段：这次调用属于哪个任务/运行/尝试/思考者/阶段，经
	// ctx（WithAttrib）传播，由 Complete/CompleteStream 收尾合并。
	Task    string `json:"task,omitempty"`
	Run     string `json:"run,omitempty"`
	Attempt int    `json:"attempt,omitempty"`
	Thinker string `json:"thinker,omitempty"`
	Wake    string `json:"wake,omitempty"`
	Phase   string `json:"phase,omitempty"`
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
	// Gate 是请求前的准入守卫（熔断/每日预算），nil = 不检查。
	// 装配层（obs.Guard）挂载；每次尝试（含重试）前调用，拒绝时
	// 请求不发出且错误原样返回。
	Gate func(ctx context.Context) error

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

// FromEnvLookup 是 FromEnv 的可注入查找版本：web 层把"进程环境 +
// 身份 .env"合成一个 lookup 后调用——配置页探测与 CLI 实际使用走
// 同一条构造路径，键链（MINDLOOP_* > ANTHROPIC/OPENAI）不会漂移。
func FromEnvLookup(getenv func(string) string) (*Client, error) {
	return fromEnvWith(getenv, getenv("MINDLOOP_MODEL"))
}

// FromEnvModel 用显式模型名构造客户端，其余配置同 FromEnv——
// 双模型分层的入口：思考档走 MINDLOOP_MODEL，请求档走
// MINDLOOP_REQUEST_MODEL（按请求档模型名重新推断供应商与预算）。
func FromEnvModel(model string) (*Client, error) { return fromEnv(model) }

// fromEnv 把环境组装成 Spec 交给 New——本包唯一的"环境读取面"，
// providers.json 档案路径不经过它。
func fromEnv(model string) (*Client, error) { return fromEnvWith(os.Getenv, model) }

// fromEnvWith 是 fromEnv 的可注入版本（查找函数由调用方给）。
func fromEnvWith(getenv func(string) string, model string) (*Client, error) {
	return New(Spec{
		Provider: getenv("MINDLOOP_PROVIDER"),
		BaseURL:  getenv("MINDLOOP_BASE_URL"),
		APIKey:   firstNonEmpty(getenv("MINDLOOP_API_KEY"), getenv("ANTHROPIC_API_KEY"), getenv("OPENAI_API_KEY")),
		Model:    model,
	})
}

// reasoningModelPrefixes 是内置思考型模型前缀名单。它是包级变量
// 而非常量：MINDLOOP_REASONING_MODELS 追加的条目在运行期并入，
// 新模型上市不必等发版。
var reasoningModelPrefixes = []string{
	"glm-5", "glm-4.5", "deepseek-r", "o1", "o3", "o4", "qwq", "thinking",
}

// ReasoningModelEnvVar 追加思考型模型前缀（逗号分隔，大小写不敏感）。
// 只追加不替换内置名单；空段与空白值忽略。运行期每次调用读取——
// 改环境变量不需要重启长驻进程的心智也能生效。
const ReasoningModelEnvVar = "MINDLOOP_REASONING_MODELS"

// extraReasoningPrefixes 读环境变量追加的思考型前缀。
func extraReasoningPrefixes() []string {
	raw := os.Getenv(ReasoningModelEnvVar)
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if p := strings.ToLower(strings.TrimSpace(part)); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// isReasoningModel 报告模型的思考 token 是否计入输出预算。
// 手维护的名单会老化——所以它只决定预算默认值，MINDLOOP_MAX_TOKENS
// 是永久的逃逸口；内置名单之外的新模型可用 MINDLOOP_REASONING_MODELS
// 追加前缀（见 ReasoningModelEnvVar）。
func isReasoningModel(model string) bool {
	m := strings.ToLower(model)
	for _, prefix := range append(extraReasoningPrefixes(), reasoningModelPrefixes...) {
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
	start := time.Now()
	defer func() {
		usage.LatencyMS = time.Since(start).Milliseconds()
		usage.applyAttrib(AttribFrom(ctx))
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

// maxBackoff 封顶单次重试等待：供应商给出的 Retry-After 再大也不
// 等超过它（调用方还有 ctx 可随时取消）。
const maxBackoff = 60 * time.Second

func (c *Client) do(ctx context.Context, method, url string, headers map[string]string, body any, extract func([]byte) (string, Usage, error)) (string, Usage, error) {
	var lastErr error
	var lastUsage Usage
	var retryAfter time.Duration
	for attempt := 0; attempt <= c.MaxRetries; attempt++ {
		// 准入守卫（熔断/每日预算）：拒绝的请求不算一次调用尝试，
		// 不消耗配额。
		if c.Gate != nil {
			if err := c.Gate(ctx); err != nil {
				return "", lastUsage, err
			}
		}
		// 每次真实尝试（含重试）过账调用配额；耗尽即停，不退避不重试。
		if err := TakeCall(ctx); err != nil {
			return "", lastUsage, err
		}
		if attempt > 0 && c.Backoff > 0 {
			select {
			case <-ctx.Done():
				return "", lastUsage, ctx.Err()
			case <-time.After(backoffDelay(c.Backoff, attempt, retryAfter)):
			}
		}
		text, usage, retryable, suggested, err := c.attempt(ctx, method, url, headers, body, extract)
		usage.Retries = attempt
		lastUsage = usage
		retryAfter = suggested
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

// backoffDelay 计算第 attempt 次重试前的等待：线性退避叠加 ±20%
// 抖动（让并发的多个思考者不同拍重试），与服务器经 Retry-After
// 给出的建议取较大者，再封顶 maxBackoff。
func backoffDelay(base time.Duration, attempt int, retryAfter time.Duration) time.Duration {
	delay := base * time.Duration(attempt)
	if retryAfter > delay {
		delay = retryAfter
	}
	if delay > maxBackoff {
		delay = maxBackoff
	}
	jittered := time.Duration(float64(delay) * (0.8 + 0.4*rand.Float64()))
	if jittered < 0 {
		jittered = delay
	}
	return jittered
}

// attempt 返回 (文本, 用量, 是否可重试, 建议等待, 错误)。
func (c *Client) attempt(ctx context.Context, method, url string, headers map[string]string, body any, extract func([]byte) (string, Usage, error)) (string, Usage, bool, time.Duration, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return "", Usage{}, false, 0, fmt.Errorf("llm: 序列化请求: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return "", Usage{}, false, 0, fmt.Errorf("llm: 构造请求: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", Usage{}, true, 0, fmt.Errorf("llm: 网络错误: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return "", Usage{}, true, 0, fmt.Errorf("llm: 读取响应: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		snippet := snippet(data)
		retryable := resp.StatusCode == http.StatusRequestTimeout ||
			resp.StatusCode == http.StatusTooManyRequests ||
			resp.StatusCode >= 500
		return "", Usage{}, retryable, parseRetryAfter(resp.Header.Get("Retry-After")),
			fmt.Errorf("llm: HTTP %d: %s", resp.StatusCode, snippet)
	}
	text, usage, err := extract(data)
	if err != nil {
		return "", usage, false, 0, err
	}
	return text, usage, false, 0, nil
}

// parseRetryAfter 解析 Retry-After 头（秒数形态；HTTP 日期形态与
// 非法值一律忽略——它只是退避建议，不是协议义务）。
func parseRetryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		d := time.Duration(n) * time.Second
		if d > maxBackoff {
			return maxBackoff
		}
		return d
	}
	return 0
}

// parseUsage 统一两家供应商的 usage 字段名。用指针检测"usage 对象
// 是否存在"：缺失与"已知的零"是两回事，Known 如实转达。
func parseUsage(data []byte) Usage {
	var raw struct {
		Usage *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			InputTokens      int `json:"input_tokens"`
			OutputTokens     int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(data, &raw); err != nil || raw.Usage == nil {
		return Usage{}
	}
	u := Usage{PromptTokens: raw.Usage.PromptTokens, CompletionTokens: raw.Usage.CompletionTokens, Known: true}
	if u.PromptTokens == 0 {
		u.PromptTokens = raw.Usage.InputTokens
	}
	if u.CompletionTokens == 0 {
		u.CompletionTokens = raw.Usage.OutputTokens
	}
	return u
}

// snippet 截断响应体前若干字符用于错误诊断。按 rune 截断——诊断
// 信息里出现半个多字节字符比省略号更糟。
func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	r := []rune(s)
	if len(r) > 300 {
		return string(r[:300]) + "…"
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
