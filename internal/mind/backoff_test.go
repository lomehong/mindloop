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

	level := 0
	level = p.Escalate(level, ClassIdle, false)
	level = p.Escalate(level, ClassIdle, false)
	if level != 2 {
		t.Fatalf("闲置应加深层级，得到 %d", level)
	}
	level = p.Escalate(level, ClassWork, false)
	if level != 0 {
		t.Fatalf("可见工作应归零，得到 %d", level)
	}
	level = p.Escalate(level, ClassIdle, true)
	if level != 0 {
		t.Fatalf("外部触发应归零，得到 %d", level)
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
