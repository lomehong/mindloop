//go:build windows

package web

import (
	"errors"
	"syscall"
)

// createBreakawayFromJob 是 CREATE_BREAKAWAY_FROM_JOB 创建标志：
// 子进程尝试脱离所属 Job Object。web 在桌面壳的 Job 里（KillOnJobClose
// + BREAKAWAY_OK，desktop/src-tauri/src/job.rs），而 `mind run` 有意
// 活过 web 的生命周期（README：mind/connector 不归壳管）——不脱离的
// 话壳退出会连坐在途心智（2026-10-14 审查 #1，语义炸弹）。
const createBreakawayFromJob = 0x01000000

// mindRunSysProcAttr 返回 `mind run` 派生的进程属性：脱离所属 Job。
func mindRunSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: createBreakawayFromJob}
}

// mindRunFallbackWithoutAttr 报告 err 是否为「Job 不允许 breakaway」
// 的启动失败（ERROR_ACCESS_DENIED）。此时降级为不脱离重试——旧语义
//（壳退出连坐）回归，但启动可用；配合壳侧放行 BREAKAWAY_OK 后正常
// 路径永远走不到这里。
func mindRunFallbackWithoutAttr(err error) bool {
	return errors.Is(err, syscall.ERROR_ACCESS_DENIED)
}
