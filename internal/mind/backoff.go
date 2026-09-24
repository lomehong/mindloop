package mind

import (
	"time"
)

// BackoffPolicy 是闲置回退的纯函数化——Headlong
// design/monolith_backoff.md 的对应物：
//
//	level 0            → 0（刚做完可见工作或被外部事件触发：立即继续）
//	level n ≥ 1        → min(Base·2^(n-1), Max)
//	thought-only 唤醒  → 额外封顶在 ThoughtCap（思考不算产出，但也
//	                     不该被罚到和彻底闲置一样深）
//
// 纯函数 + 可注入参数，让升级曲线可以被穷举测试。
type BackoffPolicy struct {
	Base        time.Duration // level 1 的延迟
	Max         time.Duration // 封顶（Headlong 默认 5 分钟）
	ThoughtCap  time.Duration // 思考型唤醒的额外封顶（默认 60 秒）
	MinInterval time.Duration // 任意 delay 的下限——IDLE 也得等这点（默认 5 秒）
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
	return p
}

// Delay 返回 level（0 起）对应的下次唤醒延迟。
func (p BackoffPolicy) Delay(level int, thoughtOnly bool) time.Duration {
	p = p.normalized()
	raw := delayForLevel(p, level, thoughtOnly)
	if p.MinInterval > 0 && raw < p.MinInterval {
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

// Escalate 根据本次分类推进回退层级，返回新层级。
// 可见工作归零；其余加深一层。triggerReactive 表示本次唤醒由外部
// 事件触发（如人类消息）——无论产出如何都归零，外部交互优先。
func (p BackoffPolicy) Escalate(level int, class WakeClass, triggerReactive bool) int {
	if triggerReactive || class == ClassWork {
		return 0
	}
	return level + 1
}
