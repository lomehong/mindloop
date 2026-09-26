package mind

import (
	"context"
	"sync"
	"testing"
	"time"

	"mindloop/internal/llm"
	"mindloop/internal/traj"
)

// taggingThinker 包装脚本化实现并记录每次 Think 用的档位——
// 用于断言双模型分层的路由决策。
type taggingThinker struct {
	tag   string
	inner *scriptThinker

	mu    sync.Mutex
	calls []string
}

func (t *taggingThinker) Think(ctx context.Context, system string, msgs []llm.Message) (string, error) {
	t.mu.Lock()
	t.calls = append(t.calls, t.tag)
	t.mu.Unlock()
	return t.inner.Think(ctx, system, msgs)
}

func (t *taggingThinker) called() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.calls...)
}

// TestMonolithTwoTierModelSelection：双模型分层（Headlong
// 2026-09-23）——反应式唤醒（外部步骤触发：观察/合并，通常是
// responder 报告人类来话）走请求档；自发的预约/watchdog 空唤醒
// 走思考档。未配请求档时两档必须同一（Thinker 兜底）。
func TestMonolithTwoTierModelSelection(t *testing.T) {
	t.Setenv("MINDLOOP_HOME", t.TempDir())
	tl, err := traj.Create(context.Background(), "monolith-tier-test")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	respond := fence(`FINAL="IDLE"`)
	think := &taggingThinker{tag: "think", inner: &scriptThinker{responses: []string{respond, respond, respond, respond, respond}}}
	request := &taggingThinker{tag: "request", inner: &scriptThinker{responses: []string{respond, respond, respond, respond, respond}}}
	th := NewMonolith(MonolithOptions{
		Timeline:       tl,
		Thinker:        think,
		RequestThinker: request,
		Backoff:        &BackoffPolicy{Base: time.Second, Max: time.Minute, ThoughtCap: 30 * time.Second},
	})

	ctx := context.Background()
	th.Wake(ctx, Wake{Step: syntheticStep("monolith-wake"), Kind: WakeScheduled}) // 自发 → 思考档
	th.Wake(ctx, Wake{Step: syntheticStep("monolith-wake"), Kind: WakeWatchdog})  // watchdog → 思考档
	th.Wake(ctx, Wake{Step: traj.NewStep("observation"), Kind: WakeStep})         // 外部产物 → 请求档

	if got := think.called(); len(got) != 2 {
		t.Fatalf("自发唤醒应全走思考档（2 次），实际 %v", got)
	}
	if got := request.called(); len(got) != 1 || got[0] != "request" {
		t.Fatalf("外部步骤唤醒应走请求档（1 次 request），实际 %v", got)
	}
}

// TestMonolithSingleTierFallback：未配 RequestThinker 时全部唤醒
// 走 Thinker——分层是可选优化，缺席时不得出现 nil 解引用或漏调用。
func TestMonolithSingleTierFallback(t *testing.T) {
	t.Setenv("MINDLOOP_HOME", t.TempDir())
	tl, err := traj.Create(context.Background(), "monolith-single-tier")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	respond := fence(`FINAL="IDLE"`)
	think := &taggingThinker{tag: "think", inner: &scriptThinker{responses: []string{respond, respond}}}
	th := NewMonolith(MonolithOptions{
		Timeline: tl,
		Thinker:  think,
	})

	ctx := context.Background()
	th.Wake(ctx, Wake{Step: syntheticStep("monolith-wake"), Kind: WakeScheduled})
	th.Wake(ctx, Wake{Step: traj.NewStep("observation"), Kind: WakeStep})

	if got := think.called(); len(got) != 2 {
		t.Fatalf("单档时全部唤醒应走 Thinker（2 次），实际 %v", got)
	}
}

// TestMonolithRecapUsesSummaryTier：摘要档独立配置时，唤醒前的
// recap 补全走摘要档，主循环仍走思考档（两张档互不串线）。
func TestMonolithRecapUsesSummaryTier(t *testing.T) {
	tl := newTestTimeline(t)
	// 造 101 步观察（间隔 1 秒）：recap 的 100 步窗口闭合 1 个。
	base := time.Now()
	for i := 0; i < 101; i++ {
		s := traj.NewStep(traj.TypeObservation)
		s.TS = base.Add(time.Duration(i) * time.Second).UTC().Format(traj.TimeFormat)
		s.Fields["content"] = "观察"
		if err := tl.Append(context.Background(), s); err != nil {
			t.Fatal(err)
		}
	}
	respond := fence(`FINAL="IDLE"`)
	think := &taggingThinker{tag: "think", inner: &scriptThinker{responses: []string{respond}}}
	summary := &taggingThinker{tag: "summary", inner: &scriptThinker{responses: []string{`{"title":"t","summary":"s"}`}}}
	th := NewMonolith(MonolithOptions{
		Timeline:       tl,
		Thinker:        think,
		SummaryThinker: summary,
		EnableRecap:    true,
		Backoff:        &BackoffPolicy{Base: time.Second, Max: time.Minute, ThoughtCap: 30 * time.Second},
	})
	th.Wake(context.Background(), Wake{Step: syntheticStep("monolith-wake"), Kind: WakeScheduled})

	if got := summary.called(); len(got) == 0 {
		t.Fatalf("recap 摘要应走摘要档，实际 %v（think=%v）", got, think.called())
	}
	if got := think.called(); len(got) == 0 {
		t.Fatal("主循环应走思考档")
	}
}
