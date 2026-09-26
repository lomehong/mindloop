package cli

import (
	"testing"

	"mindloop/internal/llm"
)

// TestTierClientFallbackAndOverride：双档分层的回退纪律——未设、
// 与思考档同名、或构造失败（缺 key）都回落思考档；配了独立模型
// 则返回挂好用量观测的新客户端。摘要档与请求档同一套纪律。
func TestTierClientFallbackAndOverride(t *testing.T) {
	for _, k := range []string{"MINDLOOP_PROVIDER", "MINDLOOP_BASE_URL", "MINDLOOP_API_KEY", "ANTHROPIC_API_KEY", "OPENAI_API_KEY"} {
		t.Setenv(k, "")
	}
	// 隔离状态根：装配路径会读 providers.json，测试不能被真实用户
	// 配置污染。
	t.Setenv("MINDLOOP_HOME", t.TempDir())
	t.Setenv("MINDLOOP_MODEL", "echo")
	t.Setenv("MINDLOOP_SUMMARY_MODEL", "")
	t.Setenv("MINDLOOP_REQUEST_MODEL", "")
	think, err := llm.FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	dir := t.TempDir()

	// 未设 → 原样返回思考档。
	if got := summaryTierClient(dir, think, nil); got != think {
		t.Fatal("未设摘要档时应回落思考档")
	}
	if got := requestTierClient(dir, think, nil); got != think {
		t.Fatal("未设请求档时应回落思考档")
	}
	// 同名 → 同样回落。
	t.Setenv("MINDLOOP_SUMMARY_MODEL", "echo")
	if got := summaryTierClient(dir, think, nil); got != think {
		t.Fatal("与思考档同名时应回落")
	}
	// 构造失败（openai-compatible 缺 key）→ 回落。
	t.Setenv("MINDLOOP_SUMMARY_MODEL", "echo-mini")
	if got := summaryTierClient(dir, think, nil); got != think {
		t.Fatal("构造失败时应回落思考档")
	}
	// 可构造的独立模型 → 新客户端 + 用量观测。
	t.Setenv("MINDLOOP_API_KEY", "test-key")
	got := summaryTierClient(dir, think, nil)
	if got == think {
		t.Fatal("配了独立摘要模型应返回新客户端")
	}
	if got.Model != "echo-mini" {
		t.Fatalf("Model = %q", got.Model)
	}
	if got.OnDone == nil {
		t.Fatal("摘要档客户端应挂用量观测")
	}
}
