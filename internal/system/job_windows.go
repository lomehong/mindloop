//go:build windows

// job_windows.go — Windows 作业对象：全部子进程登记进一个
// KILL_ON_JOB_CLOSE 的 job，宿主退出（含被杀）连带收割孩子——孤儿
// 不会占住运行锁饿死重启后的同名人。
package system

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

type jobObject struct {
	handle windows.Handle
}

// init 建作业对象并设 KILL_ON_JOB_CLOSE（Extended 变体是 Go 生态
// 的标准用法——Basic 结构体直传会报参数错误，冒烟实证）。
func (j *jobObject) init() error {
	h, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if ret, err := windows.SetInformationJobObject(h, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); ret == 0 && err != nil {
		_ = windows.CloseHandle(h)
		return err
	}
	j.handle = h
	return nil
}

// assign 把进程登记进作业（已在其他作业里的进程会失败——嵌套宿主
// 场景，忽略）。
func (j *jobObject) assign(p *os.Process) {
	if j.handle == 0 || p == nil {
		return
	}
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(p.Pid))
	if err != nil {
		return
	}
	defer windows.CloseHandle(h)
	_ = windows.AssignProcessToJobObject(j.handle, h)
}

func (j *jobObject) close() {
	if j.handle != 0 {
		_ = windows.CloseHandle(j.handle) // KILL_ON_JOB_CLOSE 收割全部孩子
		j.handle = 0
	}
}
