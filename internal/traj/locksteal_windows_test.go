//go:build windows

package traj

import (
	"path/filepath"
	"syscall"
	"testing"
)

// holdLockDirBroken 在 owner.json 上开一个不带 FILE_SHARE_DELETE 的
// 句柄：Windows 的目录 rename 要求目录树内没有此类句柄，于是
// stealLock 的 rename CAS 报 sharing violation——正是"外部读者/AV
// 让 rename 被拒绝"的负向场景（保守放弃分支）。
func holdLockDirBroken(t *testing.T, lockDir string) func() {
	t.Helper()
	path, err := syscall.UTF16PtrFromString(filepath.Join(lockDir, ownerFile))
	if err != nil {
		t.Fatalf("UTF16PtrFromString: %v", err)
	}
	h, err := syscall.CreateFile(
		path,
		syscall.GENERIC_READ,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE, // 刻意不含 FILE_SHARE_DELETE
		nil,
		syscall.OPEN_EXISTING,
		syscall.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		t.Fatalf("按无 DELETE 共享打开 owner.json: %v", err)
	}
	return func() { syscall.CloseHandle(h) }
}
