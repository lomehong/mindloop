package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// agentGuard：agent 沙箱内的 CLI 写入口门禁（superagent-lessons
// §7.0 路径①③④，2026-11-02 修复；2026-11-04 加祖先链第二信号）。
//
// 双信号判定（任一命中即沙箱内）：
//   - 信号一 MINDLOOP_RUN_ID：runner 启动沙箱时注入（runner.go
//     exeEnv），系统提示明示 agent 它的存在；
//   - 信号二 进程祖先链：沿 PPID 上溯，任何祖先是 mindloop 可执行
//     文件本体（runner 进程）即命中——沙箱 shell 与其子进程天然
//     挂在 runner 之下，这条信号**不能被 unset**。
//
// 已知边界（如实声明）：同时 unset 环境变量并把进程脱离 runner 的
// 子树（setsid/新建会话）可逃逸双信号——门槛从「一行 unset」抬到
// 「刻意脱离进程树」，防常规路径与审计溯源，不防定向对抗。真正
// 不可伪造的凭据（父进程签发调用令牌 + 只读注入）仍是后续工作，
// 见 superagent-lessons §7.0 修复说明。
func agentSandboxed() bool {
	if os.Getenv("MINDLOOP_RUN_ID") != "" {
		return true
	}
	return ancestryHitsMindloop()
}

// procChainNames 由平台文件提供（Windows 快照 / Linux procfs），
// 测试注入假链。
var procChainNames = defaultProcChainNames

// ancestryHitsMindloop 报告祖先链上是否有 mindloop 可执行文件本体。
func ancestryHitsMindloop() bool {
	selfBase := selfExeBase()
	if selfBase == "" {
		return false
	}
	for _, name := range procChainNames() {
		if strings.EqualFold(name, selfBase) {
			return true
		}
	}
	return false
}

// rejectAgent 拒绝 agent 沙箱内的自我配置/自我授权类写操作——
// 这些入口改写的是 agent 自己未来的上下文、配置与授权面，
// 只许操作员修改。
func rejectAgent(what string) error {
	return fmt.Errorf("%s：agent 沙箱内不可用——自我配置面只许操作员修改（superagent-lessons §7.0）", what)
}

// selfExeBase 是平台文件提供的自身可执行文件 basename（小写归一
// 由各平台实现负责；比较用 EqualFold 双保险）。
var selfExeBase = defaultSelfExeBase

func defaultSelfExeBase() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Base(exe)
}
