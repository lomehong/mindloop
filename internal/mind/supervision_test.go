package mind

import (
	"strings"
	"testing"
	"time"

	"github.com/lomehong/mindloop/internal/traj"
)

func supStep(ts string, content string, fields map[string]any) traj.Step {
	s := traj.NewStep(traj.TypeError)
	s.TS = ts
	s.Fields["content"] = content
	for k, v := range fields {
		s.Fields[k] = v
	}
	return s
}

func TestSupervisionSnapshotHealthy(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.Local)
	// 零错误与单次瞬态错误都不物化——段的存在本身就是信号。
	if got := supervisionSnapshot(nil, now); got != "" {
		t.Errorf("空轨迹不应物化: %q", got)
	}
	one := []traj.Step{supStep(traj.NowString(), "transient flake", nil)}
	if got := supervisionSnapshot(one, now.Add(time.Minute)); got != "" {
		t.Errorf("单次错误是噪音，不应物化: %q", got)
	}
}

func TestSupervisionSnapshotErrorsAndRepeat(t *testing.T) {
	now := time.Now()
	fresh := func(offset time.Duration) string {
		return now.Add(offset).UTC().Format(traj.TimeFormat)
	}
	steps := []traj.Step{
		supStep(fresh(-23*time.Hour), "很早的一次", nil),
		supStep(fresh(-3*time.Hour), "端口被占用", map[string]any{"task_id": "task-abc", "attempt": "1"}),
		supStep(fresh(-1*time.Hour), "端口仍被占用", map[string]any{"task_id": "task-abc", "attempt": "2"}),
		supStep(fresh(-5*time.Minute), "无关的第三条错误", nil),
		// 窗口外的陈年错误不计入。
		supStep(fresh(-72*time.Hour), "上古错误", nil),
	}
	got := supervisionSnapshot(steps, now)
	if got == "" {
		t.Fatal("≥2 次窗口内错误应物化")
	}
	if !strings.Contains(got, "错误共 4 次") {
		t.Errorf("窗口内计数不符（应为 4：23h/3h/1h/5min 四条都在 24h 内）: %s", got)
	}
	if !strings.Contains(got, "task-abc 窗口内失败 2 次") {
		t.Errorf("重复失败未标注: %s", got)
	}
	if !strings.Contains(got, "打转") {
		t.Errorf("重复失败缺研判提示: %s", got)
	}
	if !strings.Contains(got, "研判纪律") {
		t.Errorf("缺研判纪律尾行: %s", got)
	}
	if strings.Contains(got, "上古错误") {
		t.Errorf("窗口外错误泄入: %s", got)
	}
}

func TestSupervisionSnapshotLineCap(t *testing.T) {
	now := time.Now()
	var steps []traj.Step
	for i := 0; i < 8; i++ {
		steps = append(steps, supStep(now.Add(-time.Duration(i+1)*time.Minute).UTC().Format(traj.TimeFormat), "错误", nil))
	}
	got := supervisionSnapshot(steps, now)
	if !strings.Contains(got, "另有 4 次") {
		t.Errorf("超 4 行应折叠计数: %s", got)
	}
	// 折叠后不应逐条列出全部 8 次。
	if got == strings.Replace(got, "错误\n", "", -1) && strings.Count(got, "]-") > 6 {
		t.Errorf("行数未受上限约束: %s", got)
	}
}
