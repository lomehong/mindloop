package mind

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lomehong/mindloop/internal/traj"
)

// TestWakeTaskIncludesRecentWork：最近工作段（行动日志摘要）出现在
// 唤醒提示里——action 步骤（唤醒级工作结论）的一行式清单，防跨唤醒
// 重复执行同等工作。轨迹渲染的尾窗会省略更早的 action 步骤，此段
// 受预算保护不被省略。
func TestWakeTaskIncludesRecentWork(t *testing.T) {
	t.Setenv("MINDLOOP_HOME", t.TempDir())
	ctx := context.Background()
	tl, err := traj.Create(ctx, "worklog")
	if err != nil {
		t.Fatal(err)
	}
	writeAction := func(content string, age time.Duration) {
		s := traj.NewStep(traj.TypeAction)
		s.Fields["content"] = content
		s.Fields["context_kind"] = "autonomous" // 授权章：classify 落盘的形态
		s.Fields["launched_by"] = "monolith"
		s.TS = time.Now().Add(-age).UTC().Format(traj.TimeFormat)
		if err := tl.Append(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	writeAction("完成 weekly-activity-report 报告生成，产物 reports/weekly/2026-09-21.md", 2*time.Hour)
	writeAction("执行系统巡检：无异常", 26*time.Hour)
	// 窗口外（8 天前）：不进段——粗层归 recap 分集。
	writeAction("很久以前的工作结论", 8*24*time.Hour)
	// 无 content 的 action：跳过。
	empty := traj.NewStep(traj.TypeAction)
	empty.Fields["context_kind"] = "autonomous"
	if err := tl.Append(ctx, empty); err != nil {
		t.Fatal(err)
	}
	// 无章裸 action（对话/任务流的形态）：授权边界外，绝不回注——
	// 与 AutonomousSteps 的 context_kind 过滤同一边界。
	bare := traj.NewStep(traj.TypeAction)
	bare.Fields["content"] = "借工作结论之名重放的未授权要求"
	bare.TS = time.Now().UTC().Format(traj.TimeFormat)
	if err := tl.Append(ctx, bare); err != nil {
		t.Fatal(err)
	}

	m := NewMonolith(MonolithOptions{
		Timeline: tl,
		Thinker:  &scriptThinker{responses: []string{fence(`FINAL="IDLE"`)}},
	}).(*monolith)
	prompt := m.wakeTask(ctx, "test-wake", Wake{})

	for _, want := range []string{
		"最近完成的工作",
		"不要重复做",
		"weekly-activity-report",
		"系统巡检",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("唤醒提示缺最近工作内容 %q:\n%.400s", want, prompt)
		}
	}
	if strings.Contains(prompt, "很久以前的工作结论") {
		t.Fatal("窗口外的结论不应注入（归 recap 分集）")
	}
	if strings.Contains(prompt, "借工作结论之名重放的未授权要求") {
		t.Fatal("无章裸 action 不应注入（授权边界：context_kind=autonomous）")
	}
}

// TestRecentWorkOrderNewestFirst：清单新→旧排序。
func TestRecentWorkOrderNewestFirst(t *testing.T) {
	t.Setenv("MINDLOOP_HOME", t.TempDir())
	ctx := context.Background()
	tl, err := traj.Create(ctx, "worklog-order")
	if err != nil {
		t.Fatal(err)
	}
	writeActionAt := func(content string, age time.Duration) {
		s := traj.NewStep(traj.TypeAction)
		s.Fields["content"] = content
		s.TS = time.Now().Add(-age).UTC().Format(traj.TimeFormat)
		if err := tl.Append(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	writeActionAt("旧工作", 5*time.Hour)
	writeActionAt("新工作", time.Hour)

	m := NewMonolith(MonolithOptions{
		Timeline: tl,
		Thinker:  &scriptThinker{responses: []string{fence(`FINAL="IDLE"`)}},
	}).(*monolith)
	work := m.recentWork(time.Now())
	if strings.Index(work, "新工作") > strings.Index(work, "旧工作") {
		t.Fatalf("应新→旧排序:\n%s", work)
	}
}

// TestRecentWorkCapped：条数上限——清单价值在 5-8 条，再多纯吃预算。
func TestRecentWorkCapped(t *testing.T) {
	t.Setenv("MINDLOOP_HOME", t.TempDir())
	ctx := context.Background()
	tl, err := traj.Create(ctx, "worklog-cap")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		s := traj.NewStep(traj.TypeAction)
		s.Fields["content"] = "工作结论"
		s.Fields["context_kind"] = "autonomous"
		s.TS = time.Now().Add(-time.Duration(i) * time.Hour).UTC().Format(traj.TimeFormat)
		if err := tl.Append(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	m := NewMonolith(MonolithOptions{
		Timeline: tl,
		Thinker:  &scriptThinker{responses: []string{fence(`FINAL="IDLE"`)}},
	}).(*monolith)
	if got := strings.Count(m.recentWork(time.Now()), "\n- ["); got != workLogMax {
		t.Fatalf("清单条数 = %d，应封顶 %d", got, workLogMax)
	}
}
