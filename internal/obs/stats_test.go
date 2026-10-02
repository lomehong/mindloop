package obs

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"mindloop/internal/traj"
)

// ts 是台账行时间戳的测试构造器（台账真形态：traj.TimeFormat）。
func ts(minAgo float64) string {
	return time.Now().Add(-time.Duration(minAgo * float64(time.Minute))).Format(traj.TimeFormat)
}

// TestDeriveBuckets：分账维度——按唤醒/阶段/模型各归各桶，错误行
// 进 Errors 不进 Calls。
func TestDeriveBuckets(t *testing.T) {
	rows := []UsageRow{
		{TS: ts(30), Model: "glm-5", Wake: "watchdog", Phase: "wake", PromptTokens: 100, CompletionTokens: 50},
		{TS: ts(29), Model: "glm-5", Run: "r1", Phase: "task", Task: "t1", PromptTokens: 200, CompletionTokens: 100},
		{TS: ts(28), Model: "glm-4.5-air", Phase: "chat", PromptTokens: 10, CompletionTokens: 5},
		{TS: ts(27), Model: "glm-5", Phase: "chat", Error: "429 too many requests"},
	}
	st := Derive(rows, 90*time.Second)

	if st.Rows != 4 || st.Calls != 3 || st.Errors != 1 {
		t.Fatalf("总数错: rows=%d calls=%d errors=%d", st.Rows, st.Calls, st.Errors)
	}
	if got := st.ByWake["watchdog"].Calls; got != 1 {
		t.Fatalf("watchdog 桶 = %d", got)
	}
	if got := st.ByPhase["chat"].Errors; got != 1 {
		t.Fatalf("chat 桶应含错误行: %+v", st.ByPhase["chat"])
	}
	if got := st.ByModel["glm-5"].Calls; got != 2 {
		t.Fatalf("glm-5 桶 = %d", got)
	}
	if got := st.ByModel["glm-4.5-air"].Calls; got != 1 {
		t.Fatalf("glm-4.5-air 桶 = %d", got)
	}
	if st.PromptTokens != 310 || st.CompletionTokens != 155 {
		t.Fatalf("token 合计错: %+v", st)
	}
}

// TestDeriveIdleRate：空转代理——watchdog 唤醒后窗口内出现新 run
// 章 = 干活；窗口外或从不出现 = 空转；scheduled 唤醒不进分子分母。
func TestDeriveIdleRate(t *testing.T) {
	rows := []UsageRow{
		// 唤醒 A：2 分钟后才有 run（超出 90s 窗口）→ 空转。
		{TS: ts(120), Model: "m", Wake: "watchdog", Phase: "wake"},
		{TS: ts(118), Model: "m", Run: "late-run"},
		// 唤醒 B：30 秒后 run 开动 → 干活。
		{TS: ts(60), Model: "m", Wake: "watchdog", Phase: "wake"},
		{TS: ts(59.5), Model: "m", Run: "busy-run"},
		// 唤醒 C：从没跟随 run → 空转。
		{TS: ts(40), Model: "m", Wake: "watchdog", Phase: "wake"},
		// scheduled 唤醒不进空转分母。
		{TS: ts(30), Model: "m", Wake: "scheduled", Phase: "wake"},
	}
	st := Derive(rows, 90*time.Second)
	if st.WatchdogWakes != 3 {
		t.Fatalf("watchdog 分母 = %d", st.WatchdogWakes)
	}
	if st.IdleNoRun != 2 {
		t.Fatalf("空转分子 = %d", st.IdleNoRun)
	}
	rate, ok := st.IdleRate()
	if !ok || rate < 0.66 || rate > 0.67 {
		t.Fatalf("空转率 = %v ok=%v", rate, ok)
	}
}

// TestDeriveIdleRateNone：没有 watchdog 唤醒时 IdleRate 报告不可用
// ——没醒过和全在干活是两种健康。
func TestDeriveIdleRateNone(t *testing.T) {
	st := Derive([]UsageRow{{TS: ts(5), Model: "m", Phase: "chat"}}, time.Minute)
	if _, ok := st.IdleRate(); ok {
		t.Fatal("无 watchdog 唤醒不应给出空转率")
	}
}

// TestDeriveNoAttribution：无章旧行落到显式命名的桶，不静默消失。
func TestDeriveNoAttribution(t *testing.T) {
	rows := []UsageRow{{TS: ts(5), Model: "m", PromptTokens: 7, CompletionTokens: 3}}
	st := Derive(rows, time.Minute)
	if got := st.ByWake["(无章)"].Calls; got != 1 {
		t.Fatalf("无章行应落 (无章) 桶: %v", st.ByWake)
	}
	if got := st.ByPhase["(无章)"].Calls; got != 1 {
		t.Fatalf("无章行应落 (无章) 桶: %v", st.ByPhase)
	}
}

// TestLoadUsage：缺文件 = 合法的从未运行；坏行跳过；好行照读。
func TestLoadUsage(t *testing.T) {
	rows, err := LoadUsage(filepath.Join(t.TempDir(), "nope", "llm-usage.jsonl"))
	if err != nil || rows != nil {
		t.Fatalf("缺文件应 (nil, nil): %v %v", rows, err)
	}

	dir := t.TempDir()
	content := `{"ts":"2026-10-02T10:00:00.000Z","model":"echo","prompt_tokens":1}
{这不是合法json
{"ts":"2026-10-02T10:01:00.000Z","model":"echo","completion_tokens":2}
`
	// 写入走 os.Root：路径活动限制在临时根之内。
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	f, err := root.Create("llm-usage.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
	f.Close()

	rows, err = LoadUsage(filepath.Join(dir, "llm-usage.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[1].CompletionTokens != 2 {
		t.Fatalf("应读出 2 行好行: %+v", rows)
	}
}
