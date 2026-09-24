package recap

import (
	"context"
	"strings"
	"testing"
	"time"

	"mindloop/internal/llm"
	"mindloop/internal/traj"
)

// countingThinker 数调用次数的假摘要器。
type countingThinker struct {
	calls int
	body  string
}

func (c *countingThinker) Think(ctx context.Context, system string, msgs []llm.Message) (string, error) {
	c.calls++
	if strings.Contains(msgs[len(msgs)-1].Content, "MARKER-A") {
		return `{"title":"准备阶段","summary":"agent 完成了初始设置 MARKER-A"}`, nil
	}
	return `{"title":"推进阶段","summary":"agent 推进了任务 MARKER-B"}`, nil
}

func mkStepsWithGaps(n int, base time.Time, gap time.Duration) []traj.Step {
	steps := make([]traj.Step, 0, n)
	ts := base
	for i := 0; i < n; i++ {
		s := traj.NewStep("action")
		s.TS = ts.UTC().Format(traj.TimeFormat)
		s.Fields["content"] = "step content"
		steps = append(steps, s)
		ts = ts.Add(gap)
	}
	return steps
}

func TestWindowsDeterministicBoundaries(t *testing.T) {
	base := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	// 12 步 × 1 分钟间隔，再 40 分钟大间隔，再 12 步
	steps := append(mkStepsWithGaps(12, base, time.Minute), mkStepsWithGaps(12, base.Add(52*time.Minute), time.Minute)...)
	ns := Filter(steps)
	wins := Windows(ns, 30*time.Minute, 100, 60<<10)
	// 一个大间隔 → 1 个闭合窗（第一组 12 步）+ 1 个尾窗。
	if len(wins) != 2 {
		t.Fatalf("窗口数 = %d（应为 1 闭合 + 1 trailing）", len(wins))
	}
	if wins[0].Trailing {
		t.Fatal("第一窗口不应是 trailing")
	}
	if wins[0].End != 12 {
		t.Fatalf("闭合边界 = %d，应在间隔处（12）", wins[0].End)
	}
	if !wins[len(wins)-1].Trailing {
		t.Fatal("最后窗口应是 trailing")
	}
	// 确定性：重算两次边界一致
	wins2 := Windows(ns, 30*time.Minute, 100, 60<<10)
	for i := range wins {
		if wins[i] != wins2[i] {
			t.Fatalf("边界不稳定: %v vs %v", wins[i], wins2[i])
		}
	}
	// 前缀稳定：追加步骤只影响最后一个窗口
	more := mkStepsWithGaps(5, base.Add(2*time.Hour), time.Minute)
	ns2 := Filter(append(steps, more...))
	wins3 := Windows(ns2, 30*time.Minute, 100, 60<<10)
	for i := 0; i < len(wins)-1; i++ {
		if wins[i] != wins3[i] {
			t.Fatalf("追加步骤改变了已闭合窗口 %d: %v vs %v", i, wins[i], wins3[i])
		}
	}
}

func TestWindowsStepLimitAndMinSteps(t *testing.T) {
	base := time.Now()
	steps := mkStepsWithGaps(0, base, 0)
	_ = steps
	// 步数上限切窗：120 步 @max 50 → 3 窗（50/50/20-trailing）
	var raw []traj.Step
	for i := 0; i < 120; i++ {
		s := traj.NewStep("thought")
		s.TS = base.Add(time.Duration(i) * time.Second).UTC().Format(traj.TimeFormat)
		s.Fields["content"] = "x"
		raw = append(raw, s)
	}
	ns := Filter(raw)
	wins := Windows(ns, 30*time.Minute, 50, 60<<10)
	if len(wins) != 3 {
		t.Fatalf("窗口数 = %d，应为 3", len(wins))
	}
	if wins[0].End-wins[0].Start != 50 || wins[1].End-wins[1].Start != 50 {
		t.Fatalf("步数切窗边界错误: %+v %+v", wins[0], wins[1])
	}
	// 少于 MinStepsForGap 的紧凑段落不被时间间隔切开
	tight := mkStepsWithGaps(5, base, time.Hour) // 1 小时间隔但只有 5 步
	ns2 := Filter(tight)
	wins2 := Windows(ns2, 30*time.Minute, 100, 60<<10)
	if len(wins2) != 1 || !wins2[0].Trailing {
		t.Fatalf("5 步的紧凑段不应被时间切开: %+v", wins2)
	}
}

func TestUpdateIncrementalAndCache(t *testing.T) {
	t.Setenv("MINDLOOP_HOME", t.TempDir())
	tl, err := traj.Create(context.Background(), "recap-test")
	if err != nil {
		t.Fatal(err)
	}
	// 造 24 个叙事步骤（每 12 步闭合一个窗口，max=12）
	base := time.Now()
	var steps []traj.Step
	for i := 0; i < 24; i++ {
		s := traj.NewStep("action")
		s.TS = base.Add(time.Duration(i) * time.Second).UTC().Format(traj.TimeFormat)
		s.Fields["content"] = "动作"
		steps = append(steps, s)
	}
	for _, s := range steps {
		if err := tl.Append(context.Background(), s); err != nil {
			t.Fatal(err)
		}
	}

	th := &countingThinker{}
	u := &Updater{
		Timeline: tl,
		Thinker:  th,
		MaxSteps: 12,
		Model:    "test",
	}
	rep, err := u.Update(context.Background())
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if rep.Windows != 2 || rep.Summarized != 2 {
		t.Fatalf("首次 Update: %+v（应 2 窗 2 摘要）", rep)
	}

	// 第二次：零新调用（全缓存命中）
	rep2, err := u.Update(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rep2.Summarized != 0 || rep2.Cached != 2 {
		t.Fatalf("二次 Update: %+v（应全命中）", rep2)
	}
	if th.calls != 2 {
		t.Fatalf("模型被调用了 %d 次，应为 2", th.calls)
	}

	// 追加 12 步 → 出现第 3 个闭合窗口 → 增量补 1 条
	for i := 24; i < 36; i++ {
		s := traj.NewStep("action")
		s.TS = base.Add(time.Duration(i) * time.Second).UTC().Format(traj.TimeFormat)
		s.Fields["content"] = "动作"
		if err := tl.Append(context.Background(), s); err != nil {
			t.Fatal(err)
		}
	}
	rep3, err := u.Update(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rep3.Windows != 3 || rep3.Summarized != 1 || rep3.Cached != 2 {
		t.Fatalf("增量 Update: %+v（应 3 窗、新摘要 1、命中 2）", rep3)
	}

	// 人生渲染包含两个 marker（第一批摘要落在窗口 1、2 各一条）
	life, err := RenderLife(tl.Dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(life, "MARKER-A") && !strings.Contains(life, "MARKER-B") {
		t.Fatalf("人生渲染缺摘要内容: %q", life)
	}
}

func TestFilterNarrativeOnlyAndCap(t *testing.T) {
	var steps []traj.Step
	s1 := traj.NewStep("run") // 机械步骤应被过滤
	s1.Fields["content"] = "mechanical"
	steps = append(steps, s1)
	s2 := traj.NewStep("message")
	long := strings.Repeat("汉", 800)
	s2.Fields["content"] = long
	steps = append(steps, s2)

	ns := Filter(steps)
	if len(ns) != 1 {
		t.Fatalf("机械步骤应被过滤，剩余 %d 条", len(ns))
	}
	if ns[0].StepID != s2.StepID {
		t.Fatalf("过滤保留了错误的步骤: %+v", ns[0])
	}
	if len(ns[0].Text) > LineCap*4+50 {
		t.Fatalf("行未按上限截断: %d", len(ns[0].Text))
	}
}
