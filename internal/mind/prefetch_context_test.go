package mind

import (
	"context"
	"strings"
	"testing"

	"github.com/lomehong/mindloop/internal/traj"
)

// TestWakeTaskIncludesPrefetchSegment：预热缓存段（ACI）出现在唤醒
// 提示里——日程 exec 条目的周期性产物由系统提前备好，模型直接引用，
// 不必重查；未启用（nil 闭包）时不得出现该段。
func TestWakeTaskIncludesPrefetchSegment(t *testing.T) {
	t.Setenv("MINDLOOP_HOME", t.TempDir())
	ctx := context.Background()
	tl, err := traj.Create(ctx, "prefetch-ctx")
	if err != nil {
		t.Fatal(err)
	}

	seg := "预取缓存（日程预热产物，系统已备好——需要时直接引用，不必重查；过期后由日程自动刷新）：\n- [weather] 刚刚\n  北京 晴 18°C"
	m := NewMonolith(MonolithOptions{
		Timeline: tl,
		Thinker:  &scriptThinker{responses: []string{fence(`FINAL="IDLE"`)}},
		PrefetchSeg: func() string { return seg },
	}).(*monolith)

	prompt := m.wakeTask(ctx, "test-wake", Wake{})
	for _, want := range []string{"预取缓存", "[weather]", "北京 晴 18°C"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("唤醒提示缺预热段内容 %q:\n%.400s", want, prompt)
		}
	}

	// 未启用：无段、无定位语。
	mOff := NewMonolith(MonolithOptions{
		Timeline: tl,
		Thinker:  &scriptThinker{responses: []string{fence(`FINAL="IDLE"`)}},
	}).(*monolith)
	if off := mOff.wakeTask(ctx, "test-wake", Wake{}); strings.Contains(off, "预取缓存") {
		t.Fatalf("未启用预热时不应出现该段:\n%.400s", off)
	}
}

// TestWakeTaskPrefetchBudgetCapped：超长预热段按预算截断——分段预算
// 账本对 prefetch 段同样生效，超限内容不进入唤醒提示。
func TestWakeTaskPrefetchBudgetCapped(t *testing.T) {
	t.Setenv("MINDLOOP_HOME", t.TempDir())
	ctx := context.Background()
	tl, err := traj.Create(ctx, "prefetch-cap")
	if err != nil {
		t.Fatal(err)
	}
	// 20MB 输入，预算 2KB：段必须被裁剪到不撑爆唤醒提示。
	huge := strings.Repeat("长", 20*1024*1024)
	m := NewMonolith(MonolithOptions{
		Timeline:      tl,
		Thinker:       &scriptThinker{responses: []string{fence(`FINAL="IDLE"`)}},
		PrefetchSeg:   func() string { return huge },
		PrefetchBytes: 2 * 1024,
	}).(*monolith)
	prompt := m.wakeTask(ctx, "test-wake", Wake{})
	if strings.Contains(prompt, huge[:4096]) {
		t.Fatal("超预算的预热段未被裁剪")
	}
	if len(prompt) > 32*1024 {
		t.Fatalf("唤醒提示异常膨胀: %d bytes", len(prompt))
	}
}
