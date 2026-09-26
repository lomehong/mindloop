package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mindloop/internal/identity"
	"mindloop/internal/llm"
	"mindloop/internal/obs"
	"mindloop/internal/traj"
)

// writeHealthFile 在身份目录写下健康标记——熔断现场由测试直接构造，
// 与 UsageRecorder 的落盘口径一致（llm-health.json）。
func writeHealthFile(t *testing.T, dir string, h obs.Health) {
	t.Helper()
	data, err := json.Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "llm-health.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestLlmResumeClearsCircuit：llm resume 清除连续失败计数，并如实
// 报告清除前的熔断现场。
func TestLlmResumeClearsCircuit(t *testing.T) {
	id := taskTestIdentity(t)
	writeHealthFile(t, id.Dir, obs.Health{
		ConsecutiveErrors: obs.CircuitThreshold,
		LastError:         "provider 500",
		LastErrorAt:       traj.NowString(),
		LastCheck:         traj.NowString(),
	})

	code, out, errOut := runCLI(t, "llm", "resume", "ada")
	if code != 0 {
		t.Fatalf("llm resume 失败: code=%d stderr=%s", code, errOut)
	}
	if !strings.Contains(out, "连续失败 3 次") {
		t.Fatalf("输出应报告清除前的连续失败次数: %q", out)
	}
	h := obs.LoadHealth(filepath.Join(id.Dir, "llm-health.json"))
	if h.ConsecutiveErrors != 0 || h.LastError != "" || h.LastErrorAt != "" {
		t.Fatalf("恢复后熔断现场应被清除: %+v", h)
	}
}

// TestLlmResumeWithoutCircuit：无熔断记录时如实报告"无需恢复"，
// 不伪造一次清除。
func TestLlmResumeWithoutCircuit(t *testing.T) {
	id := taskTestIdentity(t)
	code, out, errOut := runCLI(t, "llm", "resume", "ada")
	if code != 0 {
		t.Fatalf("llm resume 失败: code=%d stderr=%s", code, errOut)
	}
	if !strings.Contains(out, "无需恢复") {
		t.Fatalf("无熔断时输出应说明无需恢复: %q", out)
	}
	h := obs.LoadHealth(filepath.Join(id.Dir, "llm-health.json"))
	if h.ConsecutiveErrors != 0 {
		t.Fatalf("无需恢复也不应产生失败计数: %+v", h)
	}
}

// noopLogger 供装配测试静默日志。
func noopLogger(string, ...any) {}

// newGuardTestStack 以不可达 BaseURL 装配最小心智栈：客户端绝不
// 发出真实请求，只用于验证准入守卫在装配层被挂上。
func newGuardTestStack(t *testing.T, id *identity.Identity) *mindStack {
	t.Helper()
	c := &CLI{ctx: context.Background(), stdout: io.Discard, stderr: io.Discard}
	stack, err := c.assembleMindStack(id, mindStackOpts{
		clientFactory: func() (*llm.Client, error) {
			return &llm.Client{
				Provider:  llm.ProviderOpenAICompat,
				BaseURL:   "http://127.0.0.1:9",
				APIKey:    "k",
				Model:     "test-model",
				MaxTokens: 16,
				HTTP:      &http.Client{Timeout: time.Second},
			}, nil
		},
		poll:   200 * time.Millisecond,
		logger: noopLogger,
	})
	if err != nil {
		t.Fatalf("装配心智栈失败: %v", err)
	}
	return stack
}

// TestAssembleMindStackGuardsCircuit：装配后思考档客户端带准入
// 守卫——熔断冷却中的请求在客户端层被拒绝，不发往供应商。
func TestAssembleMindStackGuardsCircuit(t *testing.T) {
	id := taskTestIdentity(t)
	writeHealthFile(t, id.Dir, obs.Health{
		ConsecutiveErrors: obs.CircuitThreshold,
		LastError:         "boom",
		LastErrorAt:       traj.NowString(),
		LastCheck:         traj.NowString(),
	})
	stack := newGuardTestStack(t, id)

	_, err := stack.client.Complete(context.Background(), "", []llm.Message{{Role: "user", Content: "hi"}})
	if !errors.Is(err, llm.ErrCircuitOpen) {
		t.Fatalf("熔断冷却中请求应被拒绝: err=%v", err)
	}
}

// TestAssembleMindStackGuardsDailyBudget：MINDLOOP_DAILY_TOKENS 配置
// 后，当日用量越过上限的请求在客户端层被拒绝。
func TestAssembleMindStackGuardsDailyBudget(t *testing.T) {
	id := taskTestIdentity(t)
	t.Setenv("MINDLOOP_DAILY_TOKENS", "1")
	ledger := filepath.Join(id.Dir, "usage", "llm-usage.jsonl")
	if err := os.MkdirAll(filepath.Dir(ledger), 0o755); err != nil {
		t.Fatal(err)
	}
	line := fmt.Sprintf(`{"ts":%q,"model":"test-model","prompt_tokens":2,"completion_tokens":0}`+"\n", traj.NowString())
	if err := os.WriteFile(ledger, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	stack := newGuardTestStack(t, id)

	_, err := stack.client.Complete(context.Background(), "", []llm.Message{{Role: "user", Content: "hi"}})
	if !errors.Is(err, llm.ErrDailyBudget) {
		t.Fatalf("每日预算耗尽后请求应被拒绝: err=%v", err)
	}
}

// TestAssembleMindStackGuardsTierClients：分层配置了独立模型时，
// 请求档/摘要档客户端同样挂上准入守卫——分层不能成为预算与熔断
// 的旁路（对话回复走请求档、摘要走摘要档）。
func TestAssembleMindStackGuardsTierClients(t *testing.T) {
	id := taskTestIdentity(t)
	t.Setenv("MINDLOOP_PROVIDER", "")
	t.Setenv("MINDLOOP_BASE_URL", "http://127.0.0.1:9/v1")
	t.Setenv("MINDLOOP_API_KEY", "test-key")
	t.Setenv("MINDLOOP_REQUEST_MODEL", "fast-model")
	t.Setenv("MINDLOOP_SUMMARY_MODEL", "mini-model")
	writeHealthFile(t, id.Dir, obs.Health{
		ConsecutiveErrors: obs.CircuitThreshold,
		LastError:         "boom",
		LastErrorAt:       traj.NowString(),
		LastCheck:         traj.NowString(),
	})
	stack := newGuardTestStack(t, id)
	if stack.request == stack.client || stack.summary == stack.client {
		t.Fatalf("分层应产出独立客户端: request=%p summary=%p think=%p",
			stack.request, stack.summary, stack.client)
	}
	for name, c := range map[string]*llm.Client{"请求档": stack.request, "摘要档": stack.summary} {
		_, err := c.Complete(context.Background(), "", []llm.Message{{Role: "user", Content: "hi"}})
		if !errors.Is(err, llm.ErrCircuitOpen) {
			t.Fatalf("%s熔断冷却中请求应被拒绝: err=%v", name, err)
		}
	}
}
