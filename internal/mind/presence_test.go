package mind

import (
	"strings"
	"testing"
	"time"

	"github.com/lomehong/mindloop/internal/traj"
)

// presenceStep 构造一条 presence 步骤（与 web presence 端点的写入
// 形态一致：state=present|away + since RFC3339）。
func presenceStep(state string, since time.Time) traj.Step {
	s := traj.NewStep(traj.TypePresence)
	s.Fields["state"] = state
	s.Fields["since"] = since.UTC().Format(time.RFC3339)
	return s
}

// TestFoldPresenceLatestWins：折叠取最新一条声明；从未声明 = 零值
// （视为在座——没有证据不假装知道操作员不在）；坏状态跳过不反推。
func TestFoldPresenceLatestWins(t *testing.T) {
	t0 := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	t1 := t0.Add(2 * time.Hour)
	steps := []traj.Step{
		presenceStep("away", t0),
		presenceStep("present", t1),
		presenceStep("garbage", t1.Add(time.Minute)), // 坏状态：不翻转
	}
	// 垃圾行在最前不遮蔽后面的合法行
	snap := FoldPresence(append([]traj.Step{presenceStep("garbage", t0)}, steps...))
	if snap.Away {
		t.Fatalf("最新合法声明是 present，Away = true")
	}
	if !snap.Since.Equal(t1) {
		t.Fatalf("Since = %v, want %v", snap.Since, t1)
	}
	// 空轨迹 = 未声明
	empty := FoldPresence(nil)
	if empty.Away || !empty.Since.IsZero() {
		t.Fatalf("空轨迹应为零值，got %+v", empty)
	}
}

// TestPresenceManifest：镜像段的门控矩阵——在座不注入；离开但目标
// 空（默认关）不注入；离开且开启才注入；陈旧离开（>24h）静默。
func TestPresenceManifest(t *testing.T) {
	now := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	targets := []string{"wecom:hongyan"}

	// 在座：不注入
	if got := presenceManifest(PresenceSnapshot{Away: false, Since: now.Add(-time.Minute)}, targets, now); got != "" {
		t.Fatalf("在座不应注入，got %q", got)
	}
	// 离开但镜像关（默认）：不注入
	if got := presenceManifest(PresenceSnapshot{Away: true, Since: now.Add(-time.Minute)}, nil, now); got != "" {
		t.Fatalf("镜像关不应注入，got %q", got)
	}
	// 离开且开启：注入且含地址与 ambient 抑制纪律
	snap := PresenceSnapshot{Away: true, Since: now.Add(-47 * time.Minute)}
	got := presenceManifest(snap, targets, now)
	if got == "" {
		t.Fatal("离开+开启应注入")
	}
	for _, want := range []string{"wecom:hongyan", "离开", "ambient", "inform"} {
		if !strings.Contains(got, want) {
			t.Fatalf("注入段缺 %q：%q", want, got)
		}
	}
	if !strings.Contains(got, "47m") {
		t.Fatalf("注入段应含离开时长读数：%q", got)
	}
	// 陈旧离开（>24h）：静默——隔天陈旧状态不驱动投递
	stale := PresenceSnapshot{Away: true, Since: now.Add(-25 * time.Hour)}
	if got := presenceManifest(stale, targets, now); got != "" {
		t.Fatalf("超龄离开应静默，got %q", got)
	}
}
