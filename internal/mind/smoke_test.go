package mind

import (
	"context"
	"strings"
	"testing"
	"time"

	"mindloop/internal/sandbox"
	"mindloop/internal/traj"
)

// requireRealBash 与 sandbox/runner 测试同一能力语义：BashPath 内部
// 做真实执行探测（bash -c true），拿不到可用 bash 就干净跳过。
func requireRealBash(t *testing.T) {
	t.Helper()
	if _, err := sandbox.BashPath(); err != nil {
		t.Skipf("跳过： %v", err)
	}
}

// TestMonolithRealExecutionSmoke：心智侧的真实执行冒烟。包内其他
// 测试虽也经 runner 走真实执行，但断言都绕在分类与回退上；这一条
// 显式断言"脚本输出真的进了轨迹、非 IDLE 结论真的落了 action 步骤"
// ——在"PATH 里的 bash 不能用"的环境下它最先暴露执行链断裂（本机
// WSL 存根事故的回归钉子）。
func TestMonolithRealExecutionSmoke(t *testing.T) {
	requireRealBash(t)
	t.Setenv("MINDLOOP_HOME", t.TempDir())
	tl, err := traj.Create(context.Background(), "monolith-smoke")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	token := "mindloop-monolith-smoke-9417"
	th := NewMonolith(MonolithOptions{
		Timeline: tl,
		Thinker: &scriptThinker{responses: []string{
			fence("echo " + token + "\n" + `FINAL="smoke ok"`),
		}},
		Backoff: &BackoffPolicy{Base: time.Second, Max: time.Minute, ThoughtCap: 30 * time.Second},
	})
	out := th.Wake(context.Background(), Wake{Step: syntheticStep("monolith-wake"), Kind: WakeWatchdog})
	if !strings.Contains(out.Note, "smoke ok") {
		t.Fatalf("唤醒 Note = %q，应含结论", out.Note)
	}
	steps, err := tl.Steps()
	if err != nil {
		t.Fatal(err)
	}
	sawOutput, sawAction := false, false
	for _, s := range steps {
		c, _ := s.Field("content")
		switch s.Type {
		case "shell-output":
			if strings.Contains(c, token) {
				sawOutput = true
			}
		case "action":
			if strings.Contains(c, "smoke ok") {
				sawAction = true
			}
		}
	}
	if !sawOutput {
		t.Fatalf("真实执行的输出未落盘（步骤数 %d）", len(steps))
	}
	if !sawAction {
		t.Fatal("非 IDLE 结论未作为 action 步骤落盘")
	}
}
