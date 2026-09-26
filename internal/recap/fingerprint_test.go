package recap

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mindloop/internal/llm"
	"mindloop/internal/traj"
)

// numberedThinker 每次调用返回递增编号的摘要——用于区分"渲染出的
// 摘要属于哪一代"。
type numberedThinker struct{ calls int }

func (n *numberedThinker) Think(ctx context.Context, system string, msgs []llm.Message) (string, error) {
	n.calls++
	return fmt.Sprintf(`{"title":"S%d","summary":"summary_s%d"}`, n.calls, n.calls), nil
}

// appendActionSteps 追加 n 个内容可辨识的动作步骤。
func appendActionSteps(t *testing.T, tl *traj.Timeline, n int, base time.Time, tag string) {
	t.Helper()
	for i := 0; i < n; i++ {
		s := traj.NewStep("action")
		s.TS = base.Add(time.Duration(i) * time.Second).UTC().Format(traj.TimeFormat)
		s.Fields["content"] = fmt.Sprintf("%s-%02d", tag, i)
		if err := tl.Append(context.Background(), s); err != nil {
			t.Fatal(err)
		}
	}
}

// rewriteStepLine 外部改写轨迹文件：把含 old 的一行替换为 new——
// 模拟手工编辑源日志。要求恰好命中一行。
func rewriteStepLine(t *testing.T, path, old, new string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(data), "\n")
	hits := 0
	for i, ln := range lines {
		if strings.Contains(ln, old) {
			lines[i] = strings.Replace(ln, old, new, 1)
			hits++
		}
	}
	if hits != 1 {
		t.Fatalf("期望恰好替换一行，实际 %d（old=%q）", hits, old)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestUpdateRegeneratesOnSourceChange：源窗口内容变化（外部编辑
// 轨迹）后指纹失配——该窗缓存不复用、重新生成；渲染只见新代。
func TestUpdateRegeneratesOnSourceChange(t *testing.T) {
	t.Setenv("MINDLOOP_HOME", t.TempDir())
	tl, err := traj.Create(context.Background(), "recap-fp")
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	appendActionSteps(t, tl, 24, base, "动作")
	th := &numberedThinker{}
	u := &Updater{Timeline: tl, Thinker: th, MaxSteps: 12, Model: "test"}
	rep, err := u.Update(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Summarized != 2 {
		t.Fatalf("首次应摘 2 窗: %+v", rep)
	}

	// 外部修改源日志：第二窗（步骤 12 起）的内容变化。
	rewriteStepLine(t, tl.Path, `"content":"动作-12"`, `"content":"动作-12-已修订"`)
	rep2, err := u.Update(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rep2.Summarized != 1 || rep2.Cached != 1 {
		t.Fatalf("源变化后应只重摘受影响窗: %+v", rep2)
	}
	if th.calls != 3 {
		t.Fatalf("模型调用 %d 次，应为 3（首次 2 + 重修 1）", th.calls)
	}
	life, err := RenderLife(tl.Dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	// 窗口 1 保留 S1；窗口 2 渲染重修后的 S3，旧代 S2 被去重。
	if !strings.Contains(life, "summary_s1") || !strings.Contains(life, "summary_s3") {
		t.Fatalf("渲染缺摘要: %q", life)
	}
	if strings.Contains(life, "summary_s2") {
		t.Fatalf("过期摘要不应渲染: %q", life)
	}
}

// TestUpdateRegeneratesOnModelChange：摘要章的模型变化使全部窗口
// 失配——逐步重生成（每次节流 1 条），旧代不渲染。
func TestUpdateRegeneratesOnModelChange(t *testing.T) {
	t.Setenv("MINDLOOP_HOME", t.TempDir())
	tl, err := traj.Create(context.Background(), "recap-model")
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	appendActionSteps(t, tl, 24, base, "步")
	th := &numberedThinker{}
	u := &Updater{Timeline: tl, Thinker: th, MaxSteps: 12, Model: "model-a"}
	if rep, err := u.Update(context.Background()); err != nil || rep.Summarized != 2 {
		t.Fatalf("首次: %+v, %v", rep, err)
	}

	u2 := &Updater{Timeline: tl, Thinker: th, MaxSteps: 12, Model: "model-b", MaxSummaries: 1}
	rep2, err := u2.Update(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rep2.Summarized != 1 || rep2.SkippedCost != 1 {
		t.Fatalf("换模型应全窗失效且受节流: %+v", rep2)
	}
	rep3, err := u2.Update(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rep3.Summarized != 1 || rep3.Cached != 1 {
		t.Fatalf("第二次应补上剩余窗并命中已重算窗: %+v", rep3)
	}
	life, err := RenderLife(tl.Dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(life, "summary_s3") || !strings.Contains(life, "summary_s4") {
		t.Fatalf("渲染应为重算后的两代: %q", life)
	}
	if strings.Contains(life, "summary_s1") || strings.Contains(life, "summary_s2") {
		t.Fatalf("旧模型摘要不应渲染: %q", life)
	}
}

// TestUpdateRegeneratesLegacyCache：摘要器升级前的遗留缓存（旧提示
// 版本、无指纹）不被 covered 接受，一次性重建。
func TestUpdateRegeneratesLegacyCache(t *testing.T) {
	t.Setenv("MINDLOOP_HOME", t.TempDir())
	tl, err := traj.Create(context.Background(), "recap-legacy")
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	appendActionSteps(t, tl, 24, base, "旧")
	cache := Cache{Path: filepath.Join(tl.Dir, "recap", "episodes.jsonl")}
	for _, w := range [][2]int{{0, 12}, {12, 24}} {
		if err := cache.Append(context.Background(), Episode{
			Start: w[0], End: w[1], Title: "旧代", Summary: "旧代摘要",
			PromptVersion: PromptVersion - 1,
		}); err != nil {
			t.Fatal(err)
		}
	}
	th := &numberedThinker{}
	u := &Updater{Timeline: tl, Thinker: th, MaxSteps: 12, Model: "test"}
	rep, err := u.Update(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Summarized != 2 || rep.Cached != 0 {
		t.Fatalf("旧版本缓存应全部重算: %+v", rep)
	}
	life, err := RenderLife(tl.Dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(life, "旧代摘要") || !strings.Contains(life, "summary_s1") {
		t.Fatalf("渲染应只见新代: %q", life)
	}
}

// TestRenderLifeFiltersDedupsAndOrders：渲染过滤旧提示版本、同窗
// 保留最后写入的一代、按窗口起点排序。
func TestRenderLifeFiltersDedupsAndOrders(t *testing.T) {
	dir := t.TempDir()
	cache := Cache{Path: filepath.Join(dir, "recap", "episodes.jsonl")}
	ctx := context.Background()
	appendEp := func(e Episode) {
		t.Helper()
		if err := cache.Append(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	appendEp(Episode{Start: 0, End: 5, Title: "旧版", Summary: "旧提示版本摘要", PromptVersion: PromptVersion - 1})
	// 乱序写入 + 同窗两代：最后写入的应是渲染结果。
	appendEp(Episode{Start: 20, End: 30, Title: "尾段", Summary: "尾段摘要", PromptVersion: PromptVersion})
	appendEp(Episode{Start: 10, End: 20, Title: "中段一代", Summary: "中段第一代", PromptVersion: PromptVersion})
	appendEp(Episode{Start: 10, End: 20, Title: "中段二代", Summary: "中段第二代", PromptVersion: PromptVersion})

	life, err := RenderLife(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(life, "旧提示版本摘要") {
		t.Fatalf("旧提示版本摘要不应渲染: %q", life)
	}
	if strings.Contains(life, "中段第一代") {
		t.Fatalf("同窗旧代应被去重: %q", life)
	}
	iMid := strings.Index(life, "中段第二代")
	iTail := strings.Index(life, "尾段摘要")
	if iMid < 0 || iTail < 0 || iMid > iTail {
		t.Fatalf("渲染应按窗口起点排序: %q", life)
	}
}
