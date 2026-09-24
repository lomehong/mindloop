//go:build !windows

package traj

import (
	"fmt"
	"os"
	"syscall"
)

// fileIdentity 在 POSIX 系统上返回 "<dev>-<ino>"。
func fileIdentity(f *os.File) string {
	fi, err := f.Stat()
	if err != nil {
		return ""
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	return fmt.Sprintf("%d-%d", st.Dev, st.Ino)
}
