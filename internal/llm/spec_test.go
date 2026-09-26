package llm

import (
	"errors"
	"testing"
)

// TestNewExplicitProfile：显式 Spec（providers.json 档案路径）——
// 字段原样映射（尾斜杠归一），不经环境推断。
func TestNewExplicitProfile(t *testing.T) {
	t.Setenv("MINDLOOP_MAX_TOKENS", "")
	t.Setenv("MINDLOOP_REASONING_MODELS", "")
	c, err := New(Spec{
		Provider: ProviderOpenAICompat,
		BaseURL:  "https://openrouter.ai/api/v1/",
		APIKey:   "sk-profile",
		Model:    "openai/gpt-oss-120b",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.Provider != ProviderOpenAICompat || c.Model != "openai/gpt-oss-120b" {
		t.Fatalf("provider/model = %q/%q", c.Provider, c.Model)
	}
	if c.BaseURL != "https://openrouter.ai/api/v1" {
		t.Fatalf("BaseURL 尾斜杠应归一: %q", c.BaseURL)
	}
	if c.APIKey != "sk-profile" {
		t.Fatalf("APIKey = %q", c.APIKey)
	}
	if c.MaxTokens != 8192 || c.HTTP == nil || c.MaxRetries != 3 {
		t.Fatalf("默认值缺失: %+v", c)
	}
}

// TestNewInfersProvider：provider 缺省时按模型名推断，与 FromEnv
// 同一规则。
func TestNewInfersProvider(t *testing.T) {
	cases := []struct {
		model string
		want  string
	}{
		{"claude-sonnet-4-5", ProviderAnthropic},
		{"glm-5", ProviderOpenAICompat},
		{"gpt-4o", ProviderOpenAICompat},
	}
	for _, tc := range cases {
		c, err := New(Spec{APIKey: "k", Model: tc.model})
		if err != nil {
			t.Fatalf("New(%q): %v", tc.model, err)
		}
		if c.Provider != tc.want {
			t.Fatalf("New(%q).Provider = %q，应为 %q", tc.model, c.Provider, tc.want)
		}
	}
}

// TestNewDefaultBaseURLs：缺省端点——claude-* → anthropic 站点根、
// glm-* → 智谱兼容端点、其余 → OpenAI。
func TestNewDefaultBaseURLs(t *testing.T) {
	cases := []struct {
		model string
		want  string
	}{
		{"claude-sonnet-4-5", "https://api.anthropic.com"},
		{"glm-5", "https://open.bigmodel.cn/api/paas/v4"},
		{"gpt-4o", "https://api.openai.com/v1"},
	}
	for _, tc := range cases {
		c, err := New(Spec{APIKey: "k", Model: tc.model})
		if err != nil {
			t.Fatalf("New(%q): %v", tc.model, err)
		}
		if c.BaseURL != tc.want {
			t.Fatalf("New(%q).BaseURL = %q，应为 %q", tc.model, c.BaseURL, tc.want)
		}
	}
}

// TestNewErrors：空模型/缺 key/未知供应商 → ErrNoProvider；echo
// 免配置。
func TestNewErrors(t *testing.T) {
	if _, err := New(Spec{}); !errors.Is(err, ErrNoProvider) {
		t.Fatalf("空模型应报 ErrNoProvider: %v", err)
	}
	if _, err := New(Spec{Model: "claude-x"}); !errors.Is(err, ErrNoProvider) {
		t.Fatalf("anthropic 缺 key: %v", err)
	}
	if _, err := New(Spec{Model: "gpt-4o"}); !errors.Is(err, ErrNoProvider) {
		t.Fatalf("openai-compatible 缺 key: %v", err)
	}
	if _, err := New(Spec{Provider: "weird", Model: "m", APIKey: "k"}); err == nil {
		t.Fatal("未知供应商应报错")
	}
	if _, err := New(Spec{Model: "echo"}); err != nil {
		t.Fatalf("echo 免配置: %v", err)
	}
}
