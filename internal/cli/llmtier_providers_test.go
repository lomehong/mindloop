package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mindloop/internal/llm"
)

// writeProvidersDoc 在 dir 下落一份 providers.json。
func writeProvidersDoc(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "providers.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// echoThink 构造回落断言用的"思考档"占位客户端。
func echoThink() *llm.Client { return &llm.Client{Provider: llm.ProviderEcho, Model: "echo"} }

// clearLLMEnv 清掉可能污染用例的 LLM 环境变量。
func clearLLMEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"MINDLOOP_MODEL", "MINDLOOP_PROVIDER", "MINDLOOP_BASE_URL", "MINDLOOP_API_KEY",
		"ANTHROPIC_API_KEY", "OPENAI_API_KEY",
		"MINDLOOP_REQUEST_MODEL", "MINDLOOP_SUMMARY_MODEL",
	} {
		t.Setenv(k, "")
	}
}

// TestThinkClientFromProvidersJSON：MINDLOOP_MODEL 未设时 think 绑定
// 从全局 providers.json 解析——连接来自档案、供应商按模型名推断、
// 密钥经约定键 MINDLOOP_PROFILE_<ID>_API_KEY 取。
func TestThinkClientFromProvidersJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MINDLOOP_HOME", home)
	clearLLMEnv(t)
	t.Setenv("MINDLOOP_PROFILE_ZHIPU_API_KEY", "sk-json")
	writeProvidersDoc(t, home, `{
      "version": 1,
      "profiles": [{"id": "zhipu", "label": "智谱", "base_url": "https://open.bigmodel.cn/api/paas/v4", "models": ["glm-5"]}],
      "tiers": {"think": {"profile": "zhipu", "model": "glm-5"}}
    }`)
	c, err := thinkClient("")
	if err != nil {
		t.Fatalf("thinkClient: %v", err)
	}
	if c.Model != "glm-5" || c.Provider != llm.ProviderOpenAICompat {
		t.Fatalf("model/provider = %q/%q", c.Model, c.Provider)
	}
	if c.BaseURL != "https://open.bigmodel.cn/api/paas/v4" {
		t.Fatalf("BaseURL = %q", c.BaseURL)
	}
	if c.APIKey != "sk-json" {
		t.Fatalf("APIKey = %q（应来自约定键）", c.APIKey)
	}
}

// TestThinkClientEnvWinsOverJSON：MINDLOOP_MODEL 显式设置时 env 赢——
// 旧配置永不因 providers.json 存在而改变行为。
func TestThinkClientEnvWinsOverJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MINDLOOP_HOME", home)
	clearLLMEnv(t)
	t.Setenv("MINDLOOP_MODEL", "glm-4-flash")
	t.Setenv("MINDLOOP_API_KEY", "sk-env")
	writeProvidersDoc(t, home, `{
      "profiles": [{"id": "zhipu", "base_url": "https://x", "models": ["glm-5"]}],
      "tiers": {"think": {"profile": "zhipu", "model": "glm-5"}}
    }`)
	c, err := thinkClient("")
	if err != nil {
		t.Fatalf("thinkClient: %v", err)
	}
	if c.Model != "glm-4-flash" || c.APIKey != "sk-env" {
		t.Fatalf("env 应赢: model=%q key=%q", c.Model, c.APIKey)
	}
}

// TestThinkClientBrokenJSONErrors：想用 JSON 配置（MINDLOOP_MODEL 未设）
// 而 providers.json 损坏 → 报错拒启，错误带文件路径。
func TestThinkClientBrokenJSONErrors(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MINDLOOP_HOME", home)
	clearLLMEnv(t)
	writeProvidersDoc(t, home, "{ not json")
	_, err := thinkClient("")
	if err == nil || !strings.Contains(err.Error(), "providers.json") {
		t.Fatalf("坏 JSON 应报错且带路径: %v", err)
	}
}

// TestThinkClientEnvFastPathSkipsBrokenJSON：MINDLOOP_MODEL 显式设置
// 时直接走 env 快速路径——坏 providers.json 不拦旧配置的运行。
func TestThinkClientEnvFastPathSkipsBrokenJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MINDLOOP_HOME", home)
	clearLLMEnv(t)
	t.Setenv("MINDLOOP_MODEL", "echo")
	writeProvidersDoc(t, home, "{ not json")
	c, err := thinkClient("")
	if err != nil {
		t.Fatalf("env 快速路径不应受坏 JSON 影响: %v", err)
	}
	if c.Provider != llm.ProviderEcho {
		t.Fatalf("provider = %q", c.Provider)
	}
}

// TestThinkClientNoConfigKeepsOldError：无 env 无 JSON → ErrNoProvider
// 旧文案（chat 的降级提示依赖它）。
func TestThinkClientNoConfigKeepsOldError(t *testing.T) {
	t.Setenv("MINDLOOP_HOME", t.TempDir())
	clearLLMEnv(t)
	_, err := thinkClient("")
	if !errors.Is(err, llm.ErrNoProvider) {
		t.Fatalf("应报 ErrNoProvider: %v", err)
	}
}

// TestRequestTierFromProvidersJSON：MINDLOOP_REQUEST_MODEL 未设时
// request 绑定从 providers.json 解析，构造独立客户端并挂用量观测。
func TestRequestTierFromProvidersJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MINDLOOP_HOME", home)
	clearLLMEnv(t)
	t.Setenv("MINDLOOP_PROFILE_OR_API_KEY", "sk-or")
	writeProvidersDoc(t, home, `{
      "profiles": [{"id": "or", "base_url": "https://openrouter.ai/api/v1", "models": ["openai/gpt-oss-120b"]}],
      "tiers": {"request": {"profile": "or", "model": "openai/gpt-oss-120b"}}
    }`)
	think := echoThink()
	got := requestTierClient(t.TempDir(), think, nil)
	if got == think {
		t.Fatal("JSON 绑定应构造独立请求档")
	}
	if got.Model != "openai/gpt-oss-120b" || got.BaseURL != "https://openrouter.ai/api/v1" || got.APIKey != "sk-or" {
		t.Fatalf("got model=%q base=%q key=%q", got.Model, got.BaseURL, got.APIKey)
	}
	if got.OnDone == nil {
		t.Fatal("请求档应挂用量观测")
	}
}

// TestTierIdentityOverridesGlobal：身份级 providers.json 覆盖全局——
// 同名档案整体替换、tiers 逐档覆盖，请求档用身份级的绑定。
func TestTierIdentityOverridesGlobal(t *testing.T) {
	home, idDir := t.TempDir(), t.TempDir()
	t.Setenv("MINDLOOP_HOME", home)
	clearLLMEnv(t)
	t.Setenv("MINDLOOP_PROFILE_LOCAL_API_KEY", "sk-local")
	writeProvidersDoc(t, home, `{
      "profiles": [{"id": "or", "base_url": "https://openrouter.ai/api/v1", "models": ["openai/gpt-oss-120b"]}],
      "tiers": {"request": {"profile": "or", "model": "openai/gpt-oss-120b"}}
    }`)
	writeProvidersDoc(t, idDir, `{
      "profiles": [{"id": "local", "base_url": "http://127.0.0.1:11434/v1", "models": ["llama3"]}],
      "tiers": {"request": {"profile": "local", "model": "llama3"}}
    }`)
	think := echoThink()
	got := requestTierClient(idDir, think, nil)
	if got == think || got.Model != "llama3" || got.BaseURL != "http://127.0.0.1:11434/v1" || got.APIKey != "sk-local" {
		t.Fatalf("身份级绑定应赢: %+v", got)
	}
}

// TestTierFallbackDiscipline：JSON 路径的回落纪律——档案缺 key
// （llm.New 失败）警告并回落思考档；坏 JSON 警告并回落 env 路径；
// env 路径完整时照常生效。
func TestTierFallbackDiscipline(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MINDLOOP_HOME", home)
	clearLLMEnv(t)
	writeProvidersDoc(t, home, `{
      "profiles": [{"id": "or", "base_url": "https://openrouter.ai/api/v1"}],
      "tiers": {"request": {"profile": "or", "model": "openai/gpt-oss-120b"}}
    }`)
	think := echoThink()
	var warned []string
	logf := func(format string, args ...any) { warned = append(warned, fmt.Sprintf(format, args...)) }
	// 档案缺 key → 警告 + 回落。
	if got := requestTierClient("", think, logf); got != think {
		t.Fatal("档案缺 key 应回落思考档")
	}
	if len(warned) == 0 || !strings.Contains(warned[0], "回落") {
		t.Fatalf("应给出回落警告: %v", warned)
	}
	// 坏 JSON + env 未设 → 警告 + 回落。
	writeProvidersDoc(t, home, "{ not json")
	warned = nil
	if got := requestTierClient("", think, logf); got != think {
		t.Fatal("坏 JSON 且 env 未设应回落思考档")
	}
	if len(warned) == 0 {
		t.Fatal("坏 JSON 应被警告")
	}
	// 坏 JSON + env 完整 → env 路径照常生效（老行为不被坏新面拦）。
	t.Setenv("MINDLOOP_REQUEST_MODEL", "fast-model")
	t.Setenv("MINDLOOP_BASE_URL", "http://127.0.0.1:9/v1")
	t.Setenv("MINDLOOP_API_KEY", "k")
	got := requestTierClient("", think, nil)
	if got == think || got.Model != "fast-model" {
		t.Fatalf("坏 JSON 时 env 路径应生效: %+v", got)
	}
}

// TestSummaryTierGlobal：CLI recap 的摘要档——从任意轨迹工作、没有
// 身份归属，只读全局 providers.json。
func TestSummaryTierGlobal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MINDLOOP_HOME", home)
	clearLLMEnv(t)
	t.Setenv("MINDLOOP_PROFILE_OR_API_KEY", "sk-or")
	writeProvidersDoc(t, home, `{
      "profiles": [{"id": "or", "base_url": "https://openrouter.ai/api/v1"}],
      "tiers": {"summary": {"profile": "or", "model": "openai/gpt-oss-20b"}}
    }`)
	think := echoThink()
	got := summaryTierGlobal(t.TempDir(), think, nil)
	if got == think || got.Model != "openai/gpt-oss-20b" {
		t.Fatalf("全局 summary 绑定应生效: %+v", got)
	}
	if got.OnDone == nil {
		t.Fatal("摘要档应挂用量观测")
	}
}
