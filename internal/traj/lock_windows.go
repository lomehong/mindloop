//go:build windows

package traj

import (
	"errors"
	"syscall"
)

// processAlive 报告给定 pid 的进程是否存在。申请 SYNCHRONIZE 权限
// 后做一次零超时等待：WAIT_TIMEOUT 表示进程仍在运行，句柄被置为
// 已通知状态表示已退出。
//
// 判死方向必须保守：OpenProcess 失败有两种截然
// 不同的含义——pid 不存在（ERROR_INVALID_PARAMETER 等）⇒ 确实死
// 了；权限不足（ERROR_ACCESS_DENIED：属主以更高完整性级别或另一
// 用户运行）⇒ 进程明明活着。后者绝不能报死，否则偷锁方会删掉活
// 调度器的锁目录。原则：无法确认死亡，绝不偷。
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := syscall.OpenProcess(syscall.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return errors.Is(err, syscall.ERROR_ACCESS_DENIED) // 权限不足 ⇒ 进程存在
	}
	defer syscall.CloseHandle(h)
	event, err := syscall.WaitForSingleObject(h, 0)
	if err != nil {
		return true // 无法判断：绝不偷可能活着的锁
	}
	return event == syscall.WAIT_TIMEOUT
}
