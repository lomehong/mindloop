package mind

import (
	"context"
	"strings"
	"testing"
	"time"

	"mindloop/internal/llm"
	"mindloop/internal/traj"
)

// scriptThinker 是 runner.Thinker 的脚本化假实现：每轮返回预设
// 文本（直接产出 FINAL，不经沙箱即可驱动 monolith 的分类路径）。
type scriptThinker struct {
	responses []string
	calls     int
}

func (s *scriptThinker) Think(ctx context.Context, system string, msgs []llm.Message) (string, error) {
	r := s.responses[min(s.calls, len(s.responses)-1)]
	s.calls++
	return r, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func fence(code string) string { return "```bash\n" + code + "\n```" }

func newMonolith(t *testing.T) (Thinker, *traj.Timeline) {
	t.Helper()
	t.Setenv("MINDLOOP_HOME", t.TempDir())
	tl, err := traj.Create(context.Background(), "monolith-test")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	th := NewMonolith(MonolithOptions{
		Timeline: tl,
		Thinker: &scriptThinker{responses: []string{
			fence(`FINAL="IDLE"`),
			fence(`FINAL="完成了第一件事"`),
			fence(`FINAL="IDLE"`),
			fence(`FINAL="IDLE"`),
		}},
		Backoff: &BackoffPolicy{Base: 2 * time.Second, Max: time.Minute, ThoughtCap: 30 * time.Second},
	})
	return th, tl
}

func TestMonolithIdleEscalatesWorkResets(t *testing.T) {
	th, tl := newMonolith(t)

	// 1. 闲置 → level 1，延迟 = base
	out := th.Wake(context.Background(), Wake{Step: syntheticStep("monolith-wake"), Kind: WakeWatchdog})
	if got := out.NextWakeIn; got != 2*time.Second {
		t.Fatalf("第一次闲置后延迟 = %v，应为 base 2s", got)
	}

	// 2. 可见工作 → level 归零，延迟归零
	out = th.Wake(context.Background(), Wake{Step: syntheticStep("monolith-wake"), Kind: WakeScheduled})
	if got := out.NextWakeIn; got != 0 {
		t.Fatalf("工作后延迟 = %v，应为 0", got)
	}

	// 3. 外部消息触发 → reactive 归零（即使产出是 IDLE）
	out = th.Wake(context.Background(), Wake{Step: traj.NewStep("message"), Kind: WakeStep})
	if out.NextWakeIn != 0 {
		t.Fatalf("reactive 唤醒延迟 = %v，应为 0", out.NextWakeIn)
	}

	// 4. 再闲置 → 从 level 0 加深到 1
	out = th.Wake(context.Background(), Wake{Step: syntheticStep("monolith-wake"), Kind: WakeWatchdog})
	if out.NextWakeIn != 2*time.Second {
		t.Fatalf("重新闲置后延迟 = %v，应为 2s", out.NextWakeIn)
	}

	// 工作分类把结论作为 action 步骤落盘——日志里可审计。
	steps, _ := tl.Steps()
	var actions []string
	for _, s := range steps {
		if s.Type == "action" {
			if c, _ := s.Field("content"); strings.Contains(c, "第一件事") {
				actions = append(actions, c)
			}
		}
	}
	if len(actions) != 1 {
		t.Fatalf("action 结论应落盘一次，实际 %d 次", len(actions))
	}
}

func TestMonolithLaunchedByStamp(t *testing.T) {
	th, tl := newMonolith(t)
	th.Wake(context.Background(), Wake{Step: syntheticStep("monolith-wake"), Kind: WakeScheduled})
	steps, _ := tl.Steps()
	for _, s := range steps {
		if s.Type == "reasoning" || s.Type == "shell-output" || s.Type == "run" {
			if by, _ := s.Field("launched_by"); by != "monolith" {
				t.Fatalf("步骤 %s 的 launched_by = %q", s.Type, by)
			}
		}
	}
}

// TestMonolithSubscriptionsNeverSelfTrigger 钉死订阅面：monolith
// 不订阅自己会产出的任何类型。
func TestMonolithSubscriptionsNeverSelfTrigger(t *testing.T) {
	th, _ := newMonolith(t)
	own := map[string]bool{"run": true, "prompt": true, "reasoning": true, "shell-output": true, "final": true, "error": true, "action": true}
	for _, typ := range th.Subscriptions().Types {
		if own[typ] {
			t.Fatalf("monolith 订阅了自己的产物类型 %q——这会构成自触发回路", typ)
		}
	}
}
