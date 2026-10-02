//go:build !windows

package traj

import (
	"fmt"
	"os"
	"syscall"
)

// dirIdentity 返回目录的文件身份（"dev:ino"）——POSIX 上 rename(2)
// 可以原子替换一个空目录：路径相同，身份可能易主。锁声明的正确性
// 依赖"我 mkdir 出来的目录还是我的"，身份守卫在写属主前后各验一次。
func dirIdentity(dir string) (string, bool) {
	fi, err := os.Stat(dir)
	if err != nil {
		return "", false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return "", false
	}
	return fmt.Sprintf("%d:%d", st.Dev, st.Ino), true
}
