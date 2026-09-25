package cli

import (
	"os"
	"strings"

	"mindloop/internal/llm"
	"mindloop/internal/obs"
)

// requestTierClient 构造双模型分层的请求档客户端（Headlong
// 2026-09-23 的实测：成熟身份的空闲唤醒占绝大多数，把自发的空
// 唤醒留在便宜的思考档、反应式唤醒升级到请求档，安静日的模型
// 开销降 70-80%）。
//
// MINDLOOP_REQUEST_MODEL 未设、与思考档同名、或构造失败（如缺
// key）时原样返回思考档——分层永远只是优化，不能变成新的故障面。
// dir 是用量台账的落盘目录；logf 是构建期警告通道（可为 nil）。
//
// 返回的就是 *llm.Client 本体，无包装层：Complete/CompleteStream
// 与 OnDone 观测（CompleteStream 结束时同样回调，用量台账对两档、
// 两种调用形态口径一致）对思考档/请求档天然同权透传。
func requestTierClient(think *llm.Client, dir string, logf func(format string, args ...any)) *llm.Client {
	name := strings.TrimSpace(os.Getenv("MINDLOOP_REQUEST_MODEL"))
	if name == "" || name == think.Model {
		return think
	}
	rc, err := llm.FromEnvModel(name)
	if err != nil {
		if logf != nil {
			logf("请求档模型 %q 不可用（%v）——全部唤醒走思考档 %q", name, err, think.Model)
		}
		return think
	}
	rc.OnDone = obs.UsageRecorder(dir, rc.Model, rc.Provider, logf)
	return rc
}
