// 用量页的准入与未知用量展示测试：台账里 usage_known=false 的成功
// 调用必须被显式标出（不能伪装成零 token 的已知调用）；admission
// 块报告每日预算与熔断状态（未设置时报 daily_limit=0，界面据此
// 显示"未设置"）。
package web

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mindloop/internal/identity"
	"mindloop/internal/traj"
)

// writeUsageLedger 追加台账行（自动补换行）。
func writeUsageLedger(t *testing.T, dir string, lines ...string) {
	t.Helper()
	usageDir := filepath.Join(dir, "usage")
	if err := os.MkdirAll(usageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(usageDir, "llm-usage.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(strings.Join(lines, "\n") + "\n"); err != nil {
		t.Fatal(err)
	}
}

// writeHealthMark 写健康标记（UsageRecorder 的产物形态）。
func writeHealthMark(t *testing.T, dir string, consecutiveErrors int, lastErrorAt string) {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"consecutive_errors": consecutiveErrors,
		"last_error_at":      lastErrorAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "llm-health.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestUsageUnknownCalls：成功但供应商未返回用量的调用计入
// unknown_calls（旧行缺 usage_known 字段按已知处理，失败行由 error
// 单独归类，都不算未知）。
func TestUsageUnknownCalls(t *testing.T) {
	dir, _ := newIdentityHome(t)
	writeUsageLedger(t, dir,
		`{"ts":"2026-09-23T10:00:00.000Z","prompt_tokens":100,"completion_tokens":50,"model":"glm-5"}`,
		`{"ts":"2026-09-23T11:00:00.000Z","usage_known":false,"model":"glm-5"}`,
		`{"ts":"2026-09-23T12:00:00.000Z","usage_known":false,"error":"429","model":"glm-5"}`,
	)
	ts, _ := newTestServer(t, identity.Home(), "")
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/api/identities/ada/usage")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Totals struct {
			Calls        int `json:"calls"`
			UnknownCalls int `json:"unknown_calls"`
		} `json:"totals"`
		Daily [][2]any `json:"daily"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Totals.Calls != 3 || out.Totals.UnknownCalls != 1 {
		t.Fatalf("totals = %+v（应 calls=3 unknown_calls=1）", out.Totals)
	}
	if len(out.Daily) != 1 {
		t.Fatalf("daily 应有 1 天，得到 %d", len(out.Daily))
	}
	day, ok := out.Daily[0][1].(map[string]any)
	if !ok {
		t.Fatalf("daily 元素结构异常: %T", out.Daily[0][1])
	}
	if got := day["unknown"]; got != float64(1) {
		t.Fatalf("daily unknown = %v，应为 1", got)
	}
}

// TestUsageAdmissionStatus：admission 块报告预算上限、当日已用与
// 熔断冷却状态（冷却截止为 TimeFormat 时刻）。
func TestUsageAdmissionStatus(t *testing.T) {
	dir, _ := newIdentityHome(t)
	t.Setenv("MINDLOOP_DAILY_TOKENS", "5000")
	now := traj.NowString()
	writeUsageLedger(t, dir, `{"ts":"`+now+`","prompt_tokens":120,"completion_tokens":80,"model":"glm-5"}`)
	writeHealthMark(t, dir, 3, now)
	ts, _ := newTestServer(t, identity.Home(), "")
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/api/identities/ada/usage")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Admission struct {
			DailyLimit        int    `json:"daily_limit"`
			UsedToday         int    `json:"used_today"`
			ConsecutiveErrors int    `json:"consecutive_errors"`
			CircuitThreshold  int    `json:"circuit_threshold"`
			CoolingUntil      string `json:"cooling_until"`
		} `json:"admission"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Admission.DailyLimit != 5000 || out.Admission.UsedToday != 200 {
		t.Fatalf("admission 预算 = %+v（应 limit=5000 used=200）", out.Admission)
	}
	if out.Admission.ConsecutiveErrors != 3 || out.Admission.CircuitThreshold != 3 || out.Admission.CoolingUntil == "" {
		t.Fatalf("admission 熔断 = %+v（应 cooling 中）", out.Admission)
	}
	if _, err := time.Parse(traj.TimeFormat, out.Admission.CoolingUntil); err != nil {
		t.Fatalf("cooling_until 应为 TimeFormat: %q（%v）", out.Admission.CoolingUntil, err)
	}
}

// TestUsageAdmissionUnset：预算未设置时报 daily_limit=0，当日消耗
// 仍如实统计（界面显示"未设置"而不是这个零）。
func TestUsageAdmissionUnset(t *testing.T) {
	dir, _ := newIdentityHome(t)
	t.Setenv("MINDLOOP_DAILY_TOKENS", "")
	now := traj.NowString()
	writeUsageLedger(t, dir, `{"ts":"`+now+`","prompt_tokens":7,"completion_tokens":3,"model":"glm-5"}`)
	ts, _ := newTestServer(t, identity.Home(), "")
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/api/identities/ada/usage")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Admission struct {
			DailyLimit int `json:"daily_limit"`
			UsedToday  int `json:"used_today"`
		} `json:"admission"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Admission.DailyLimit != 0 || out.Admission.UsedToday != 10 {
		t.Fatalf("admission = %+v（应 limit=0 used=10）", out.Admission)
	}
}
