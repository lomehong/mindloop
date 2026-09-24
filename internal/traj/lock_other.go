//go:build !windows

package traj

import "syscall"

// processAlive 是 Windows 版检查在 POSIX 上的对应实现：信号 0 只
// 探测存在性，不惊扰进程。
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}
