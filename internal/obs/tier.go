// tier.go 是模型分层的共享构造面：请求档（MINDLOOP_REQUEST_MODEL）
// 与摘要档（MINDLOOP_SUMMARY_MODEL）走同一条纪律——未设、与思考档
// 同名或构造失败都原样回落思考档，分层永远只是优化，不能变成新的
// 故障面。cli 与 web 两个进程共用它，避免两处各写一份回退逻辑。
package obs

import (
	"os"
	"strings"

	"mindloop/internal/llm"
)

// TierClient 读 envName 指定的模型名构造独立档位客户端；成功时
// 挂上用量观测（dir 是台账目录，与思考档口径一致：两档在
// llm-usage.jsonl 里按模型名天然可区分）。回落条件：未设、与思考
// 档同名、或构造失败（缺 key 等）。label 用于构建期警告文案。
func TierClient(think *llm.Client, envName, label, dir string, logf func(format string, args ...any)) *llm.Client {
	name := strings.TrimSpace(os.Getenv(envName))
	if name == "" || name == think.Model {
		return think
	}
	rc, err := llm.FromEnvModel(name)
	if err != nil {
		if logf != nil {
			logf("%s模型 %q 不可用（%v）——回落思考档 %q", label, name, err, think.Model)
		}
		return think
	}
	rc.OnDone = UsageRecorder(dir, rc.Model, rc.Provider, logf)
	return rc
}
