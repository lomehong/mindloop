//go:build windows

package traj

import "syscall"

// processAlive 报告给定 pid 的进程是否存在。申请 SYNCHRONIZE 权限
// 后做一次零超时等待：WAIT_TIMEOUT 表示进程仍在运行，句柄被置为
// 已通知状态表示已退出。死 pid 会在 OpenProcess 处失败并按死亡
// 上报；若等待调用本身出错则保守地按存活处理。
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := syscall.OpenProcess(syscall.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(h)
	event, err := syscall.WaitForSingleObject(h, 0)
	if err != nil {
		return true // 无法判断：绝不偷可能活着的锁
	}
	return event == syscall.WAIT_TIMEOUT
}
