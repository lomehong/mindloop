package mind

import (
	"testing"
	"time"
)

func TestBackoffEscalationAndReset(t *testing.T) {
	p := BackoffPolicy{Base: 5 * time.Second, Max: 5 * time.Minute, ThoughtCap: 60 * time.Second}
	if d := p.Delay(0, false); d != 0 {
		t.Fatalf("level 0 应为 0，得到 %v", d)
	}
	if d := p.Delay(1, false); d != 5*time.Second {
		t.Fatalf("level 1 应为 base，得到 %v", d)
	}
	if d := p.Delay(3, false); d != 20*time.Second {
		t.Fatalf("level 3 应为 20s，得到 %v", d)
	}
	if d := p.Delay(10, false); d != 5*time.Minute {
		t.Fatalf("封顶应为 max，得到 %v", d)
	}
	if d := p.Delay(10, true); d != 60*time.Second {
		t.Fatalf("thought-only 应封顶在 60s，得到 %v", d)
	}

	// Advance 的驻留节奏（Hold=2）：1,1 → 2,2 → 4,4——每级停留
	// 2 次空唤醒才加深（Headlong dwell 的 mindloop 适配版，无
	// 开头的零延迟段，见 Advance 注释）。
	p2 := BackoffPolicy{Base: time.Second, Max: time.Minute, ThoughtCap: 30 * time.Second, Hold: 2}
	level, ticks := 0, 0
	type state struct {
		level, ticks int
		delay        time.Duration
	}
	var got []state
	for i := 0; i < 5; i++ {
		level, ticks = p2.Advance(level, ticks, ClassIdle, false)
		got = append(got, state{level, ticks, p2.Delay(level, false)})
	}
	want := []state{
		{1, 1, time.Second},
		{1, 2, time.Second},
		{2, 1, 2 * time.Second},
		{2, 2, 2 * time.Second},
		{3, 1, 4 * time.Second},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("驻留序列第 %d 步 = %v，应为 %v；全序列 %v", i+1, got[i], want[i], got)
		}
	}

	// 可见工作整体归零；外部触发同样归零（即使产出是 IDLE）。
	level, ticks = p2.Advance(level, ticks, ClassWork, false)
	if level != 0 || ticks != 0 {
		t.Fatalf("可见工作应整体归零，得到 (%d,%d)", level, ticks)
	}
	level, ticks = p2.Advance(level, ticks, ClassIdle, true)
	if level != 0 || ticks != 0 {
		t.Fatalf("外部触发应整体归零，得到 (%d,%d)", level, ticks)
	}

	// 封顶后原地永驻：再深一级延迟不再变化就不再加深。
	p3 := BackoffPolicy{Base: 5 * time.Second, Max: 10 * time.Second, ThoughtCap: 60 * time.Second, Hold: 1}
	level, ticks = 0, 0
	for i := 0; i < 6; i++ {
		level, ticks = p3.Advance(level, ticks, ClassIdle, false)
	}
	if level != 2 || ticks != 1 {
		t.Fatalf("封顶后应永驻 (2,1)，得到 (%d,%d)", level, ticks)
	}
}

// TestMinIntervalPreventsIdleZeroLoop：真实事故回归——IDLE 唤醒
// 退出后若 level 重置为 0、且是思考型，回退公式返回 0 秒，造成
// "IDLE→0秒→醒来→IDLE"的紧密循环。修复：MinInterval 给任意
// delay 设下限（默认 5 秒）。主动型（triggerReactive=true）
// 仍然保持 0——人类消息不能等 5 秒才回。
func TestMinIntervalPreventsIdleZeroLoop(t *testing.T) {
	p := BackoffPolicy{Base: 5 * time.Second, Max: 5 * time.Minute, ThoughtCap: 60 * time.Second, MinInterval: 5 * time.Second}
	if got := p.Delay(0, true); got != 5*time.Second {
		t.Fatalf("Level=0 思考型应被 MinInterval 地板夹住，得到 %v", got)
	}
	if got := p.Delay(0, false); got != 0 {
		t.Fatalf("Level=0 主动型（观察到操作员消息）应保持 0 秒，得到 %v", got)
	}
	// 已经高于下限，不应被改写。
	if got := p.Delay(2, true); got != 10*time.Second {
		t.Fatalf("Level=2 思考型应保持 10s（高于 MinInterval），得到 %v", got)
	}
}
