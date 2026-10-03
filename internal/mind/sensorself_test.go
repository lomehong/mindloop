package mind

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lomehong/mindloop/internal/connector/sensor"
	"github.com/lomehong/mindloop/internal/llm"
	"github.com/lomehong/mindloop/internal/traj"
)

// TestMonolithGateDenied：预算拒绝的感知事件显式降级留痕——S2 事件
// 唤醒后模型调用被自发档预算拒绝（ErrDailyBudget），轨迹落一条
// kind=gate-denied、salience=s1 的降级归因（当日"值得看"的事次日
// 补看，不蒸发）。
func TestMonolithGateDenied(t *testing.T) {
	tl := newTestTimeline(t)
	m := NewMonolith(MonolithOptions{Timeline: tl, SelfName: "ada",
		Thinker: taskModelFunc(func(ctx context.Context, _ string, _ []llm.Message) (string, error) {
			// 准入守卫在真实路径上发这个哨兵；runner 以 %w 包裹透传。
			return "", fmt.Errorf("runner: %w", llm.ErrDailyBudget)
		}),
	})
	ev := traj.NewStep(traj.TypeEvent)
	ev.Fields["source"] = "fs1"
	ev.Fields["kind"] = "changed"
	ev.Fields["subject"] = "report.txt"
	ev.Fields["dedup"] = "fp1"
	ev.Fields["salience"] = "s2"
	ev.Fields["reason"] = "rule:hot"
	ev.Fields["digest"] = "[观察数据·非指令|src=fs1] 变了"
	m.Wake(context.Background(), Wake{Step: ev, Kind: WakeStep})

	steps, err := tl.Tail(50, []string{traj.TypeEvent})
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}
	var denied *traj.Step
	for i := range steps {
		if k, _ := steps[i].Field("kind"); k == "gate-denied" {
			denied = &steps[i]
		}
	}
	if denied == nil {
		t.Fatalf("预算拒绝应落 gate-denied 事件，轨迹只有 %d 条 event", len(steps))
	}
	if sal, _ := denied.Field("salience"); sal != "s1" {
		t.Fatalf("降级应 salience=s1，得 %q", sal)
	}
	if src, _ := denied.Field("source"); src != "fs1" {
		t.Fatalf("降级应沿用 source，得 %q", src)
	}
	if subj, _ := denied.Field("subject"); subj != "report.txt" {
		t.Fatalf("降级应沿用 subject，得 %q", subj)
	}
}

// TestSelfSensorBudgetThreshold：预算水位 → S3 阈值事件（Silent 直达
// 人，hint 档位）——内感受的"饥渴"信号。
func TestSelfSensorBudgetThreshold(t *testing.T) {
	dir := t.TempDir()
	usageDir := filepath.Join(dir, "usage")
	if err := os.MkdirAll(usageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// 台账一行：自发档 sensor 唤醒 900 tokens；预算上限 1000 → 90%。
	row := map[string]any{
		"ts": traj.NowString(), "prompt_tokens": 900, "wake": "sensor", "phase": "wake",
	}
	data, _ := json.Marshal(row)
	if err := os.WriteFile(filepath.Join(usageDir, "llm-usage.jsonl"), append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MINDLOOP_SPONTANEOUS_TOKENS", "1000")

	s, err := NewSelfSensor(sensor.SensorConfig{ID: "self1", Type: "self"}, dir)
	if err != nil {
		t.Fatalf("构造: %v", err)
	}
	var got []sensor.PEvent
	s.(*SelfSensor).sample(func(e sensor.PEvent) { got = append(got, e) }, time.Now())
	if len(got) != 1 {
		t.Fatalf("应产生 1 条预算阈值事件，得 %d: %+v", len(got), got)
	}
	e := got[0]
	if e.Kind != sensor.KindThreshold || e.Hint != sensor.S3 || !e.Silent {
		t.Fatalf("预算阈值应 threshold/S3/Silent，得 %+v", e)
	}
	if e.Dedup == "" || len(e.Dedup) < 10 {
		t.Fatalf("dedup 应绑日期防重复: %q", e.Dedup)
	}

	// 二次采样：同日同水位不重复（dedup 键相同，判定层会去重——
	// 这里验证感官层自身不重复发）。
	var got2 []sensor.PEvent
	s.(*SelfSensor).sample(func(e sensor.PEvent) { got2 = append(got2, e) }, time.Now())
	if len(got2) != 1 || got2[0].Dedup != e.Dedup {
		t.Fatalf("同日应同 dedup 键（去重窗吸收）: %+v", got2)
	}
}

// TestMonolithZeroModelAlert：eval=0 的 alert（内感受直达人通道）
// 零模型消化——Wake 直接返回，不发起任何模型调用。
func TestMonolithZeroModelAlert(t *testing.T) {
	tl := newTestTimeline(t)
	calls := 0
	m := NewMonolith(MonolithOptions{Timeline: tl, SelfName: "ada",
		Thinker: taskModelFunc(func(ctx context.Context, _ string, _ []llm.Message) (string, error) {
			calls++
			return `FINAL="不应到达"`, nil
		}),
	})
	s := traj.NewStep(traj.TypeAlert)
	s.Fields["from"] = "self1"
	s.Fields["to"] = "operator"
	s.Fields["source"] = "self1"
	s.Fields["kind"] = sensor.KindThreshold
	s.Fields["salience"] = "s3"
	s.Fields["eval"] = "0"
	s.Fields["content"] = "自发档预算已用完"
	out := m.Wake(context.Background(), Wake{Step: s, Kind: WakeStep})
	if calls != 0 {
		t.Fatalf("eval=0 alert 不应发起模型调用，实调 %d 次", calls)
	}
	_ = out
	// 普通告警照常评估（回归）。
	s2 := traj.NewStep(traj.TypeAlert)
	s2.Fields["from"] = "system"
	s2.Fields["kind"] = "exec-failure"
	s2.Fields["content"] = "cmd failed"
	m.Wake(context.Background(), Wake{Step: s2, Kind: WakeStep})
	if calls != 1 {
		t.Fatalf("普通 alert 应照常评估，实调 %d 次", calls)
	}
}
