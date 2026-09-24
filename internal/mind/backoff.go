package mind

import (
	"time"
)

// BackoffPolicy 是闲置回退的纯函数化——Headlong
// design/monolith_backoff.md 的对应物：
//
//	首档             → Base（第一次闲置立即降到第一档，不空转）
//	level n ≥ 1      → min(Base·2^(n-1), Max)
//	thought-only 唤醒  → 额外封顶在 ThoughtCap（思考不算产出，但也
//	                     不该被罚到和彻底闲置一样深）
//	驻留             → 每级停留 Hold 次空唤醒再加深（dwell）
//
// 纯函数 + 可注入参数，让升级曲线可以被穷举测试。
type BackoffPolicy struct {
	Base        time.Duration // level 1 的延迟
	Max         time.Duration // 封顶（Headlong 默认 5 分钟）
	ThoughtCap  time.Duration // 思考型唤醒的额外封顶（默认 60 秒）
	MinInterval time.Duration // 思考型 delay 的下限（默认 5 秒）
	Hold        int // 每级驻留的空唤醒次数（默认 3）——Headlong 的 dwell
}

func (p BackoffPolicy) normalized() BackoffPolicy {
	if p.Base <= 0 {
		p.Base = 5 * time.Second
	}
	if p.Max <= 0 {
		p.Max = 5 * time.Minute
	}
	if p.ThoughtCap <= 0 {
		p.ThoughtCap = 60 * time.Second
	}
	// 心跳安全网：即使 level=0 且是思考型（0 秒路径），也至少
	// 等这么多毫秒，避免"IDLE→0秒→醒来→IDLE→0秒→醒来"的紧密
	// 循环把心智烧掉。这是从一次真实事故来的：429 恢复后 ada
	// 进入 IDLE 状态，回退每轮都重置 0，立即再醒，间隔跌到 8s→0s，
	// 既浪费 token 也让轨迹日志被空步骤淹没。
	if p.MinInterval < 0 {
		p.MinInterval = 5 * time.Second
	}
	if p.Hold <= 0 {
		p.Hold = 3
	}
	return p
}

// Delay 返回 level（0 起）对应的下次唤醒延迟。
func (p BackoffPolicy) Delay(level int, thoughtOnly bool) time.Duration {
	p = p.normalized()
	raw := delayForLevel(p, level, thoughtOnly)
	// 地板只夹思考型：主动型（人类消息等外部触发）必须保持 0——
	// 人类消息不能等 5 秒才回（TestMinIntervalPreventsIdleZeroLoop）。
	if thoughtOnly && p.MinInterval > 0 && raw < p.MinInterval {
		return p.MinInterval
	}
	return raw
}

// delayForLevel 是去掉 MinInterval 地板的纯指数曲线，留出来给
// 测试直接断言“曲线本身”（而不被默认 5 秒地板掩盖）。
func delayForLevel(p BackoffPolicy, level int, thoughtOnly bool) time.Duration {
	if level <= 0 {
		return 0
	}
	d := p.Base
	for i := 1; i < level; i++ {
		d *= 2
		if d >= p.Max {
			d = p.Max
			break
		}
	}
	if d > p.Max {
		d = p.Max
	}
	// 思考封顶最后套用：先修订完总曲线，思考型唤醒也回落到
	// ThoughtCap——思考不算产出，但也不罚到与彻底闲置一样深。
	if thoughtOnly && d > p.ThoughtCap {
		return p.ThoughtCap
	}
	return d
}

// WakeClass 是一次唤醒产出的工作分类——Headlong 的 work probe：
// 关键区分是"可见的持久产物"与"原地思考"，后者不该重置回退也不该
// 被罚到底。
type WakeClass int

const (
	// ClassWork：产生了可见的持久产物（action/observation/merge 等）。
	ClassWork WakeClass = iota
	// ClassThoughtOnly：思考了、执行了，但没有持久产物。
	ClassThoughtOnly
	// ClassIdle：无事可做（FINAL=IDLE 或运行失败）。
	ClassIdle
)

// Escalate 根据本次分类推进回退状态（层级 + 驻留计数），返回新
// 状态。可见工作或外部触发整体归零；闲置唤醒的节奏（Headlong
// 的 dwell，2026-08-24 修订）：首次闲置立即落到第一档（Base），
// 之后每级驻留 Hold 次空唤醒、驻留期满才加深一级——节奏是
// base×H, base×H, 2·base×H, 4·base×H…直到封顶后永驻。
//
// 与 Headlong 原版的一处有意差异：原版曲线开头是 `0,0,0`（三次
// 零延迟空转），mindloop 的 MinInterval 安全网（"IDLE 紧密循环"
// 事故）禁止零延迟空唤醒，所以首档直接落在 Base。
func (p BackoffPolicy) Advance(level, ticks int, class WakeClass, triggerReactive bool) (int, int) {
	if triggerReactive || class == ClassWork {
		return 0, 0
	}
	if level <= 0 {
		return 1, 1
	}
	ticks++
	if ticks > p.normalized().Hold {
		// 驻留期满：加深一级；已到封顶（再深延迟不变）则原地驻留。
		ticks = 1
		if p.ladder(level+1) > p.ladder(level) {
			level++
		}
	}
	return level, ticks
}

// ladder 是无思考封顶的纯指数曲线（截到 Max）——Advance 的加深
// 判据用它比较"再深一级是否还有意义"。
func (p BackoffPolicy) ladder(level int) time.Duration {
	d := p.normalized().Base
	for i := 1; i < level; i++ {
		d *= 2
		if d >= p.Max {
			return p.Max
		}
	}
	if d > p.Max {
		return p.Max
	}
	return d
}
