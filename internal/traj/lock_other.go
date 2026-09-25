//go:build !windows

package traj

import (
	"errors"
	"syscall"
)

// processAlive 是 Windows 版检查在 POSIX 上的对应实现：信号 0 只
// 探测存在性，不惊扰进程。EPERM（进程存在但无权向它发信号——典型
// 是另一用户的进程）必须按存活处理；把它当成死亡会偷走活进程的
// 锁目录，让两个"单实例"调度器并存。原则：无法确认死亡，绝不偷。
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
