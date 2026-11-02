package cli

import (
	"fmt"
	"os"
)

// agentGuard：agent 沙箱内的 CLI 写入口门禁（superagent-lessons
// §7.0 路径①③④，2026-11-02 修复）。
//
// 判定信号：MINDLOOP_RUN_ID——runner 启动沙箱时注入（runner.go
// exeEnv），系统提示明示 agent 它的存在。它在 agent 环境恒存在、在
// 操作员环境恒缺席，是「谁在调用」的尽责检测。
//
// 已知边界（如实声明）：agent 脚本可以 unset 该变量后调用以伪装成
// 操作员。env 门禁防的是常规路径滥用与审计溯源，不防主动伪装；
// 不可伪造凭据（父进程签发的调用令牌 + 只读注入）是后续工作，
// 见 superagent-lessons §7.0 修复说明。
func agentSandboxed() bool { return os.Getenv("MINDLOOP_RUN_ID") != "" }

// rejectAgent 拒绝 agent 沙箱内的自我配置/自我授权类写操作——
// 这些入口改写的是 agent 自己未来的上下文、配置与授权面，
// 只许操作员修改。
func rejectAgent(what string) error {
	return fmt.Errorf("%s：agent 沙箱内不可用——自我配置面只许操作员修改（superagent-lessons §7.0）", what)
}
