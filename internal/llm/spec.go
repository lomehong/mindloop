// spec.go 是客户端的显式构造入口：providers.json 档案路径用 Spec
// 直接传入连接信息，不经过环境变量；FromEnv 是它的环境薄壳——
// 两条路径共用同一套默认值、推断与校验，行为不会漂移。
package llm

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// Spec 是显式配置构造客户端的输入（providers.json 档案路径）：
// 连接信息来自 Profile，密钥来自 config.ResolveTier 的解析结果。
type Spec struct {
	Provider string // 空 = 按 Model 名推断
	BaseURL  string // 空 = 供应商默认端点
	APIKey   string
	Model    string
}

// New 用显式 Spec 构造客户端。模型名即供应商标识的推断、默认端点、
// 输出预算分层与 key 校验都发生在这里——FromEnv 只负责从环境组装
// Spec。
func New(spec Spec) (*Client, error) {
	model := strings.TrimSpace(spec.Model)
	provider := strings.TrimSpace(spec.Provider)
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
		APIKey:     spec.APIKey,
		BaseURL:    strings.TrimRight(strings.TrimSpace(spec.BaseURL), "/"),
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
