// gate.go 定义请求准入的哨兵错误：每日预算与熔断冷却。守卫的
// 实现（obs.Guard）装配到 llm.Client.Gate 上；错误从 llm 包导出，
// 让 runner/mind 等调用方能以 errors.Is 识别并映射任务状态。
package llm

import "errors"

var (
	// ErrDailyBudget：身份当日 token 用量达到配置上限，停止发起
	// 新请求（用量防线，非精确账单硬封顶；UTC 日界重置）。
	ErrDailyBudget = errors.New("llm: 身份每日 token 预算已用完")
	// ErrCircuitOpen：模型调用连续失败进入冷却，冷却期内拒绝新
	// 请求；冷却结束后只放行一次探测，用户可显式恢复。
	ErrCircuitOpen = errors.New("llm: 模型供应商连续失败，熔断冷却中")
)
