//go:build windows

package traj

import (
	"fmt"
	"os"
	"syscall"
)

// fileIdentity 返回稳定的（卷序列号, 文件索引）组合——Windows 上
// POSIX inode 的对应物——让 cursor 能区分"原位重写的文件"和"只是
// 变大的文件"。Headlong 的派生视图同样以 inode+size 作为 cursor
// 的键，理由相同：文件被重写必须触发重建，绝不能静默错位。
func fileIdentity(f *os.File) string {
	h := syscall.Handle(f.Fd())
	var info syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(h, &info); err != nil {
		return ""
	}
	return fmt.Sprintf("%08x-%08x%08x", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow)
}
