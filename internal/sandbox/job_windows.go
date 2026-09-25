//go:build windows

package sandbox

import (
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"unsafe"
)

// Job Object 是 Windows 对 POSIX 进程组的原生替代，而且更强：
// 一次分配、整树管辖（子进程自动入 job，breakaway 被拒绝）、
// 一调用杀全树、句柄关闭即兜底收割。Headlong 在 /proc 里遍历
// 进程树并与 reparent 竞态搏斗的一切，这里都不存在。
//
// 通过 syscall 的 LazyDLL 直接声明：保持全仓库零第三方依赖。
var (
	kernel32                     = syscall.NewLazyDLL("kernel32.dll")
	procCreateJobObjectW         = kernel32.NewProc("CreateJobObjectW")
	procSetInformationJobObject  = kernel32.NewProc("SetInformationJobObject")
	procAssignProcessToJobObject = kernel32.NewProc("AssignProcessToJobObject")
	procTerminateJobObject       = kernel32.NewProc("TerminateJobObject")
)

const (
	jobObjectExtendedLimitInformationClass = 9
	jobObjectLimitKillOnJobClose           = 0x00002000
	jobObjectLimitProcessMemory            = 0x00000100
	processSetQuota                        = 0x0100
	processTerminate                       = 0x0001
)

type ioCounters struct {
	ReadOperationCount  uint64
	WriteOperationCount uint64
	OtherOperationCount uint64
	ReadTransferCount   uint64
	WriteTransferCount  uint64
	OtherTransferCount  uint64
}

type basicLimitInformation struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

type extendedLimitInformation struct {
	BasicLimitInformation basicLimitInformation
	IoInfo                ioCounters
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

// job 是一个 Job Object 句柄的薄包装。
type job struct{ handle syscall.Handle }

// newJob 创建 kill-on-close 的 Job Object；memLimit > 0 时附加
// 单进程内存上限（尽力而为：设置失败不阻止执行）。
func newJob(memLimit uint64) (*job, error) {
	h, _, callErr := procCreateJobObjectW.Call(0, 0)
	if h == 0 {
		return nil, fmt.Errorf("sandbox: CreateJobObjectW: %w", callErr)
	}
	info := extendedLimitInformation{}
	info.BasicLimitInformation.LimitFlags = jobObjectLimitKillOnJobClose
	if memLimit > 0 {
		info.BasicLimitInformation.LimitFlags |= jobObjectLimitProcessMemory
		info.ProcessMemoryLimit = uintptr(memLimit)
	}
	r1, _, callErr := procSetInformationJobObject.Call(
		h,
		jobObjectExtendedLimitInformationClass,
		uintptr(unsafe.Pointer(&info)),
		unsafe.Sizeof(info),
	)
	if r1 == 0 {
		syscall.CloseHandle(syscall.Handle(h))
		return nil, fmt.Errorf("sandbox: SetInformationJobObject: %w", callErr)
	}
	return &job{handle: syscall.Handle(h)}, nil
}

// prepare 在进程启动前挂钩子。Windows 的 Job Object 在启动后
// AssignProcessToJobObject 即可完成管辖，启动前无需准备
// （POSIX 版在此挂 Setpgid）。
func (j *job) prepare(_ *exec.Cmd) {}

// assignPID 把进程纳入 job。不在启动前挂起再分配，存在子进程在
// 分配前逃逸的理论窗口（极小）；杀树时另有兜底。
func (j *job) assignPID(pid int) error {
	if j == nil {
		return errors.New("sandbox: 无效 job")
	}
	ph, err := syscall.OpenProcess(processSetQuota|processTerminate, false, uint32(pid))
	if err != nil {
		return fmt.Errorf("sandbox: OpenProcess(%d): %w", pid, err)
	}
	defer syscall.CloseHandle(ph)
	r1, _, callErr := procAssignProcessToJobObject.Call(uintptr(j.handle), uintptr(ph))
	if r1 == 0 {
		return fmt.Errorf("sandbox: AssignProcessToJobObject: %w", callErr)
	}
	return nil
}

// terminate 杀掉 job 内的全部进程。
func (j *job) terminate() {
	if j == nil {
		return
	}
	procTerminateJobObject.Call(uintptr(j.handle), 1)
}

// close 关闭句柄；KILL_ON_JOB_CLOSE 会兜底收割一切残留。
func (j *job) close() {
	if j == nil {
		return
	}
	syscall.CloseHandle(j.handle)
}
