package obs

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mindloop/internal/llm"
	"mindloop/internal/traj"
)

// writeHealth 直接写健康标记，模拟 UsageRecorder 的产物形态。
func writeHealth(t *testing.T, dir string, h Health) {
	t.Helper()
	data, err := json.Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "llm-health.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// appendLedger 追加一行用量台账。
func appendLedger(t *testing.T, dir, ts string, prompt, completion int, failed bool) {
	t.Helper()
	usageDir := filepath.Join(dir, "usage")
	if err := os.MkdirAll(usageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := map[string]any{"ts": ts, "prompt_tokens": prompt, "completion_tokens": completion}
	if failed {
		rec["error"] = "boom"
	}
	line, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(usageDir, "llm-usage.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		t.Fatal(err)
	}
}

func iso(t time.Time) string { return t.UTC().Format(traj.TimeFormat) }

// TestGuardTripsAfterThreeFailures：连续 3 次失败后进入冷却，冷却期内
// 拒绝新请求（ErrCircuitOpen）。
func TestGuardTripsAfterThreeFailures(t *testing.T) {
	dir := t.TempDir()
	g := newGuard(dir, 0, nil)
	writeHealth(t, dir, Health{ConsecutiveErrors: 3, LastErrorAt: iso(time.Now())})
	if err := g.Allow(context.Background()); !errors.Is(err, llm.ErrCircuitOpen) {
		t.Fatalf("err = %v，应为 ErrCircuitOpen", err)
	}
}

// TestGuardSingleProbeAfterCooldown：冷却结束后只允许一次探测；探测
// 失败（结果落盘）重新冷却，冷却过后再次放行探测。
func TestGuardSingleProbeAfterCooldown(t *testing.T) {
	dir := t.TempDir()
	g := newGuard(dir, 0, nil)
	past := iso(time.Now().Add(-6 * time.Minute))
	future := iso(time.Now().Add(time.Second))
	writeHealth(t, dir, Health{ConsecutiveErrors: 3, LastErrorAt: past})
	if err := g.Allow(context.Background()); err != nil {
		t.Fatalf("冷却已过应放行一次探测: %v", err)
	}
	if err := g.Allow(context.Background()); !errors.Is(err, llm.ErrCircuitOpen) {
		t.Fatalf("探测进行中不应再放行: %v", err)
	}
	// 探测失败：结果落盘（CE=4、LastErrorAt=now、LastCheck 晚于放行）
	// → 重新冷却。
	writeHealth(t, dir, Health{ConsecutiveErrors: 4, LastErrorAt: iso(time.Now()), LastCheck: future})
	if err := g.Allow(context.Background()); !errors.Is(err, llm.ErrCircuitOpen) {
		t.Fatalf("探测失败应重新冷却: %v", err)
	}
	// 探测失败的新冷却过后 → 再次放行探测。
	writeHealth(t, dir, Health{ConsecutiveErrors: 4, LastErrorAt: past, LastCheck: future})
	if err := g.Allow(context.Background()); err != nil {
		t.Fatalf("新冷却过后应再次探测: %v", err)
	}
}

// TestGuardRecoversAfterSuccessfulProbe：探测成功（健康标记清零）后
// 恢复正常放行。
func TestGuardRecoversAfterSuccessfulProbe(t *testing.T) {
	dir := t.TempDir()
	g := newGuard(dir, 0, nil)
	future := iso(time.Now().Add(time.Second))
	writeHealth(t, dir, Health{ConsecutiveErrors: 3, LastErrorAt: iso(time.Now().Add(-6 * time.Minute))})
	if err := g.Allow(context.Background()); err != nil {
		t.Fatalf("冷却已过应放行探测: %v", err)
	}
	writeHealth(t, dir, Health{ConsecutiveErrors: 0, LastOK: iso(time.Now()), LastCheck: future})
	if err := g.Allow(context.Background()); err != nil {
		t.Fatalf("探测成功后应恢复: %v", err)
	}
}

// TestGuardDailyBudget：当日 token 合计达到阈值后拒绝新请求；失败
// 行（error 非空）与旧日行不计入。
func TestGuardDailyBudget(t *testing.T) {
	dir := t.TempDir()
	g := newGuard(dir, 100, nil)
	appendLedger(t, dir, traj.NowString(), 60, 30, false)  // 90
	appendLedger(t, dir, traj.NowString(), 999, 999, true) // 失败行不计
	if err := g.Allow(context.Background()); err != nil {
		t.Fatalf("90 < 100 应放行: %v", err)
	}
	appendLedger(t, dir, traj.NowString(), 20, 0, false) // 110
	if err := g.Allow(context.Background()); !errors.Is(err, llm.ErrDailyBudget) {
		t.Fatalf("err = %v，应为 ErrDailyBudget", err)
	}
}

// TestGuardDailyBudgetIgnoresOldDays：旧日用量重置为新一天的零起点。
func TestGuardDailyBudgetIgnoresOldDays(t *testing.T) {
	dir := t.TempDir()
	g := newGuard(dir, 100, nil)
	appendLedger(t, dir, iso(time.Now().Add(-24*time.Hour)), 5000, 5000, false)
	if err := g.Allow(context.Background()); err != nil {
		t.Fatalf("旧日用量不应计入今天: %v", err)
	}
}

// TestGuardDailyDisabled：预算未设置（<=0）即不启用。
func TestGuardDailyDisabled(t *testing.T) {
	dir := t.TempDir()
	g := newGuard(dir, 0, nil)
	appendLedger(t, dir, traj.NowString(), 10_000_000, 10_000_000, false)
	if err := g.Allow(context.Background()); err != nil {
		t.Fatalf("未设置预算应放行: %v", err)
	}
}

// TestClearHealth：显式恢复返回清除前状态并清零连续错误计数。
func TestClearHealth(t *testing.T) {
	dir := t.TempDir()
	writeHealth(t, dir, Health{ConsecutiveErrors: 3, LastErrorAt: traj.NowString(), LastError: "boom"})
	old, err := ClearHealth(dir)
	if err != nil {
		t.Fatal(err)
	}
	if old.ConsecutiveErrors != 3 {
		t.Fatalf("应返回清除前状态: %+v", old)
	}
	h := LoadHealth(filepath.Join(dir, "llm-health.json"))
	if h.ConsecutiveErrors != 0 || h.LastError != "" || h.LastErrorAt != "" {
		t.Fatalf("恢复后应清零: %+v", h)
	}
}

// TestNewGuardReadsDailyEnv：NewGuard 从 MINDLOOP_DAILY_TOKENS 读
// 每日预算；非法值按未设置处理。
func TestNewGuardReadsDailyEnv(t *testing.T) {
	t.Setenv("MINDLOOP_DAILY_TOKENS", "4321")
	if g := NewGuard(t.TempDir(), nil); g.daily != 4321 {
		t.Fatalf("daily = %d，应为 4321", g.daily)
	}
	t.Setenv("MINDLOOP_DAILY_TOKENS", "not-a-number")
	if g := NewGuard(t.TempDir(), nil); g.daily != 0 {
		t.Fatalf("非法值应为 0（未启用），得到 %d", g.daily)
	}
}

// TestLoadAdmissionSnapshot：只读快照报告预算上限与当日已用；失败行
// 与旧日行不计入，无健康标记时熔断为正常态。
func TestLoadAdmissionSnapshot(t *testing.T) {
	t.Setenv("MINDLOOP_DAILY_TOKENS", "1000")
	dir := t.TempDir()
	appendLedger(t, dir, traj.NowString(), 200, 100, false)
	appendLedger(t, dir, iso(time.Now().Add(-24*time.Hour)), 5000, 5000, false)
	appendLedger(t, dir, traj.NowString(), 999, 999, true)
	st := LoadAdmission(dir)
	if st.DailyLimit != 1000 || st.UsedToday != 300 {
		t.Fatalf("admission = %+v（应 limit=1000 used=300）", st)
	}
	if st.CircuitThreshold != CircuitThreshold || st.ConsecutiveErrors != 0 || st.CoolingUntil != "" {
		t.Fatalf("无健康标记时应为正常态: %+v", st)
	}
}

// TestLoadAdmissionUnsetBudget：预算未设置报告 0（界面据此显示
// "未设置"），当日消耗仍如实统计。
func TestLoadAdmissionUnsetBudget(t *testing.T) {
	dir := t.TempDir()
	appendLedger(t, dir, traj.NowString(), 10, 10, false)
	st := LoadAdmission(dir)
	if st.DailyLimit != 0 || st.UsedToday != 20 {
		t.Fatalf("admission = %+v（应 limit=0 used=20）", st)
	}
}

// TestLoadAdmissionCooling：熔断冷却中报告冷却截止时刻；冷却过后
// 不再报告冷却（下一次调用会被放行为探测）。
func TestLoadAdmissionCooling(t *testing.T) {
	dir := t.TempDir()
	writeHealth(t, dir, Health{ConsecutiveErrors: 3, LastErrorAt: iso(time.Now())})
	st := LoadAdmission(dir)
	if st.ConsecutiveErrors != 3 || st.CoolingUntil == "" {
		t.Fatalf("冷却中应报告截止时刻: %+v", st)
	}
	if _, err := time.Parse(traj.TimeFormat, st.CoolingUntil); err != nil {
		t.Fatalf("CoolingUntil 应为 TimeFormat: %q（%v）", st.CoolingUntil, err)
	}
	writeHealth(t, dir, Health{ConsecutiveErrors: 3, LastErrorAt: iso(time.Now().Add(-6 * time.Minute))})
	if st = LoadAdmission(dir); st.CoolingUntil != "" {
		t.Fatalf("冷却已过不应报告截止时刻: %+v", st)
	}
}

// TestLoadAdmissionNoSideEffects：只读快照不写任何文件、不占用探测
// 名额——连续两次读取同一健康标记结果一致。
func TestLoadAdmissionNoSideEffects(t *testing.T) {
	dir := t.TempDir()
	writeHealth(t, dir, Health{ConsecutiveErrors: 3, LastErrorAt: iso(time.Now().Add(-6 * time.Minute))})
	first := LoadAdmission(dir)
	second := LoadAdmission(dir)
	if first != second {
		t.Fatalf("两次读取应一致: %+v vs %+v", first, second)
	}
	if _, err := os.Stat(filepath.Join(dir, "llm-health.json.lock")); !os.IsNotExist(err) {
		t.Fatalf("只读快照不应创建锁文件: %v", err)
	}
}
