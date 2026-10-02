// Package sensor 是感知系统的感官家族与判定层（perception.md §4）。
// 本包保持纯逻辑：不 import traj/mind——感官的轨迹写位由装配层闭包
// 提供（与 schedule.Options.Alert 同构）；判定是纯函数加显式状态，
// 重启语义由装配层回读轨迹事件重建（视图皆派生）。
//
// 五感共用一套神经系统：感官实现 Sensor 接口产出 PEvent，判定层
// 打显著性分级（S0 沉淀 / S1 报告素材 / S2 叫醒 / S3 告警），订阅
// 面（mind 包）只收 S2+。看见 ≠ 打扰：分级是保守缺省，校准靠
// Phase 4 的味觉证据面。
package sensor

import (
	"context"
	"errors"
	"strings"
	"time"
)

// Salience 是感知事件的显著性分级。
type Salience string

const (
	// S0 背景变化：只落轨迹，不叫醒（喂记忆代谢）。
	S0 Salience = "s0"
	// S1 值得知道：落轨迹 + 并入下次报告/巡检的素材投影。
	S1 Salience = "s1"
	// S2 值得现在看：落轨迹 + 进 monolith 订阅面（自发档预算）。
	S2 Salience = "s2"
	// S3 必须现在处理：落轨迹 + alert 步骤（coalesced 背压）。
	S3 Salience = "s3"
)

// 事件种类词表（event 步骤的 kind 字段）。
const (
	KindChanged   = "changed"   // 已观察主体的内容变化
	KindAppeared  = "appeared"  // 新主体出现
	KindRemoved   = "removed"   // 主体消失
	KindSpike     = "spike"     // 速率突增（鼻）
	KindThreshold = "threshold" // 阈值越界（鼻/内感受）
	KindAnomaly   = "anomaly"   // 模式异常（鼻）
	KindAbsence   = "absence"   // 缺席：心跳类感官该来的没来
	KindGateDeny  = "gate-denied" // 预算拒绝的 S2 降级归因（monolith 写）
)

// PEvent 是感官产出的纯数据事件。
type PEvent struct {
	Kind    string // KindChanged 等；空 = KindChanged
	Subject string // 归一化主体：路径/URL/仓库/会话
	Dedup   string // 内容指纹；空 = 用 Subject
	Digest  string // 结构化摘要 ≤200 字：变了什么/在哪/何时。检索指引
	// 不是决策依据——思考者被唤醒后必经回读 subject 才处置。
}

// Sensor 是一个感知通道。契约（perception.md §4.2，装配层保证）：
//  1. Watch 阻塞运行至 ctx 取消；错误返回即本通道本轮观察结束，
//     由装配层决定重启（指数退避）；
//  2. onEvent 由装配层提供，实现不得假设它不阻塞——装配层内部
//     走有界队列，下游写轨迹的 Windows 瞬时争用不拖垮观察循环；
//  3. 装配层对每感官统一 recover + 退避重启——感官的 panic 绝不
//     带走心智进程。
type Sensor interface {
	ID() string
	Watch(ctx context.Context, onEvent func(PEvent)) error
}

// EventRecord 是判定状态重建的输入行：装配层从轨迹 event/alert
// 步骤读出后喂给 State.Rebuild。纯数据，与步骤类型解耦。
type EventRecord struct {
	TS       time.Time
	Source   string
	Kind     string
	Subject  string
	Dedup    string
	Salience string
	Reason   string // 判定理由（rule:<名> 等），重建规则冷却用
}

// ReflexView 是装配层注入的关联快照（"眼睛里有事"的"事"字来源：
// 观察议程对准当前事务）。心跳/配置变更时刷新；判定只读它。
type ReflexView struct {
	// Paths 是与当前事务相关的路径前缀（任务工作目录等）：主体
	// 命中前缀即视为与事务相关，保底升 S1。
	Paths []string
	// Keywords 是事务关键词（记忆中在意的项目名等）：主体或摘要
	// 词面命中即视为相关。
	Keywords []string
}

// ViewFromConfigs 从感官配置聚合 ReflexView 的 MVP 形态：观察目标
// 本身就是议程（用户配了盯什么，什么就是"事"）。记忆派生的关键词
// 升级留给记忆代谢（roadmap §2.4）落地后接入。
func ViewFromConfigs(cfgs []SensorConfig) ReflexView {
	v := ReflexView{}
	for _, c := range cfgs {
		if !c.IsEnabled() {
			continue
		}
		if p := strings.TrimSpace(c.Path); p != "" {
			v.Paths = append(v.Paths, p)
		}
		if u := strings.TrimSpace(c.URL); u != "" {
			v.Paths = append(v.Paths, u)
		}
		v.Keywords = append(v.Keywords, c.Keywords...)
	}
	return v
}

// ErrDisabled 是感官被配置禁用（enabled=false）时 Watch 返回的
// 哨兵——装配层按静默停通道处理，不算故障不计退避。
var ErrDisabled = errors.New("sensor: 已禁用")
