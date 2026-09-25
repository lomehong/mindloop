//go:build !windows

package traj

import (
	"os"
	"path/filepath"
	"testing"
)

// holdLockDirBroken 把锁目录的父目录置为只读，使 rename 报 EACCES
// （POSIX rename 的权限判据在父目录的写位），制造 rename CAS 的
// 负向场景。POSIX 上文件内的打开句柄不阻止 rename，所以用权限位。
func holdLockDirBroken(t *testing.T, lockDir string) func() {
	t.Helper()
	parent := filepath.Dir(lockDir)
	fi, err := os.Stat(parent)
	if err != nil {
		t.Fatalf("Stat 父目录: %v", err)
	}
	if err := os.Chmod(parent, 0o555); err != nil {
		t.Skipf("无法置只读（可能是 root）: %v", err)
	}
	return func() {
		if err := os.Chmod(parent, fi.Mode().Perm()); err != nil {
			t.Errorf("恢复父目录权限: %v", err)
		}
	}
}
