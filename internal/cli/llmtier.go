package cli

import (
	"os"
	"strings"

	"mindloop/internal/config"
	"mindloop/internal/llm"
	"mindloop/internal/obs"
	"mindloop/internal/traj"
)

// 本文件是模型档位的解析与装配：think（主档）走 thinkClient，
// request/summary 两档共用 resolveTierClient 的回退纪律。
// providers.json 的解析器（config.ResolveTier）与 web 配置页共用——
// 页面上看到的生效配置就是这里实际使用的配置。

// thinkClient 构造思考档（主档）。MINDLOOP_MODEL 显式设置时直接走
// env 快速路径（旧行为 100%，坏 providers.json 也不拦）；否则看
// providers.json 的 think 绑定，坏 JSON / 坏引用报错交出——配置
// 错误必须被看见，chat 场景由调用方降级 echo，mind run 直接拒启。
// 两者都没有时 FromEnv 给出 ErrNoProvider 的旧文案。
func thinkClient(identityDir string) (*llm.Client, error) {
	if strings.TrimSpace(os.Getenv("MINDLOOP_MODEL")) != "" {
		return llm.FromEnv()
	}
	providers, err := config.LoadProviders(traj.Home(), identityDir)
	if err != nil {
		return nil, err
	}
	choice, err := config.ResolveTier(providers, "think", os.Getenv)
	if err != nil {
		return nil, err
	}
	if choice.Source != "providers.json" {
		return llm.FromEnv()
	}
	return llm.New(llm.Spec{
		Provider: choice.Profile.Provider,
		BaseURL:  choice.Profile.BaseURL,
		APIKey:   choice.APIKey,
		Model:    choice.Binding.Model,
	})
}

// requestTierClient 构造双模型分层的请求档客户端（Headlong
// 2026-09-23 的实测：成熟身份的空闲唤醒占绝大多数，把自发的空
// 唤醒留在便宜的思考档、反应式唤醒升级到请求档，安静日的模型
// 开销降 70-80%）。MINDLOOP_REQUEST_MODEL 未设时看 providers.json
// 的 request 绑定；两者皆无、同名或构造失败都回落思考档——回退
// 纪律见 obs.TierClient。identityDir 同时是 providers.json 的二级
// 位置与用量台账落盘目录；logf 是构建期警告通道（可为 nil）。
func requestTierClient(identityDir string, think *llm.Client, logf func(format string, args ...any)) *llm.Client {
	return resolveTierClient("request", "MINDLOOP_REQUEST_MODEL", "请求档", identityDir, identityDir, think, logf)
}

// summaryTierClient 构造摘要档客户端（MINDLOOP_SUMMARY_MODEL）：
// recap 的摘要调用可独立配置更便宜/更快的模型，与请求档同一套
// 回退纪律。返回的就是 *llm.Client 本体，无包装层。
func summaryTierClient(identityDir string, think *llm.Client, logf func(format string, args ...any)) *llm.Client {
	return resolveTierClient("summary", "MINDLOOP_SUMMARY_MODEL", "摘要档", identityDir, identityDir, think, logf)
}

// summaryTierGlobal 是 CLI 维护命令（recap）的摘要档入口：从任意
// 轨迹工作、没有可靠的身份归属——只读全局 providers.json，台账
// 落 usageDir。
func summaryTierGlobal(usageDir string, think *llm.Client, logf func(format string, args ...any)) *llm.Client {
	return resolveTierClient("summary", "MINDLOOP_SUMMARY_MODEL", "摘要档", "", usageDir, think, logf)
}

// resolveTierClient 是请求/摘要档共用的解析核心：
//   - env 旧键非空 → obs.TierClient 老路径（未设/同名的回落都已
//     在那里实现，行为 100% 不变）；
//   - 否则查 providers.json 绑定 → llm.New 显式构造（密钥来自
//     档案解析），构造成功挂用量观测；
//   - 坏 JSON、坏引用、档案不可用（缺 key 等）→ 警告并回落思考档：
//     配置错误被看见（logf），但旁观档位的坏配置不拦运行。
func resolveTierClient(tier, envKey, label, identityDir, usageDir string, think *llm.Client, logf func(format string, args ...any)) *llm.Client {
	providers, err := config.LoadProviders(traj.Home(), identityDir)
	if err != nil {
		warnf(logf, "%s: providers.json 无法加载（%v）——回落环境变量路径", label, err)
		return obs.TierClient(think, envKey, label, usageDir, logf)
	}
	choice, err := config.ResolveTier(providers, tier, os.Getenv)
	if err != nil {
		warnf(logf, "%s: %v——回落思考档 %q", label, err, think.Model)
		return think
	}
	if choice.Source != "providers.json" {
		return obs.TierClient(think, envKey, label, usageDir, logf)
	}
	c, err := llm.New(llm.Spec{
		Provider: choice.Profile.Provider,
		BaseURL:  choice.Profile.BaseURL,
		APIKey:   choice.APIKey,
		Model:    choice.Binding.Model,
	})
	if err != nil {
		warnf(logf, "%s模型 %q 不可用（%v）——回落思考档 %q", label, choice.Binding.Model, err, think.Model)
		return think
	}
	// JSON 绑定是显式的：即使模型与思考档同名也照常构造（可能指向
	// 不同档案/端点），不做 env 路径的"同名回落"。
	c.OnDone = obs.UsageRecorder(usageDir, c.Model, c.Provider, logf)
	return c
}

// warnf 经构建期日志通道喊一条警告（logf 可为 nil）。
func warnf(logf func(format string, args ...any), format string, args ...any) {
	if logf != nil {
		logf(format, args...)
	}
}
