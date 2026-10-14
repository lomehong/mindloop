//go:build windows

package web

import (
	"os/exec"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// 本文件用真实 Job Object 实锤 mind run 派生所依赖的 breakaway 语义
//（2026-10-14 审查 #1 要求「先写最小验证程序」）：壳的 Job 是
// KillOnJobClose + BREAKAWAY_OK，web 派生 `mind run` 时带
// CREATE_BREAKAWAY_FROM_JOB——子进程必须**不在** Job 里；不带则
// **必须**在（连坐）。与 sandbox/job_windows.go 同款零依赖声明。
//
// 安全注意：测试进程会 AssignCurrentProcess 进 Job。KillOnJobClose
// 在**最后一个句柄关闭**时触发——测试故意泄漏一个句柄（不关闭），
// Job 永不触发收割，测试进程与子进程都安全；泄漏的句柄随测试进程
// 退出由系统回收（此时 Job 内已无存活进程，无收割目标）。

const (
	jobLimitBreakaway      uint32 = 0x00000800
	jobLimitKillOnJobClose uint32 = 0x00002000
	processQueryLimited    uint32 = 0x00001000
)

var procIsProcessInJob = syscall.NewLazyDLL("kernel32.dll").NewProc("IsProcessInJob")

// 与 JOBOBJECT_EXTENDED_LIMIT_INFORMATION 的 x64 布局逐字段对齐
// （总长 144 字节，长度不对 SetInformationJobObject 报参数错误）。
type basicLimitT struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	_                       uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	_                       uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

type extendedLimitT struct {
	Basic                 basicLimitT
	IoInfo                [48]byte
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

// createShellLikeJob 建一个与桌面壳同款旗标的 Job：KillOnClose +
// BreakawayOk，返回句柄（测试**故意不关闭**，见文件头安全注意）。
func createShellLikeJob(t *testing.T) syscall.Handle {
	t.Helper()
	k32 := syscall.NewLazyDLL("kernel32.dll")
	create := k32.NewProc("CreateJobObjectW")
	set := k32.NewProc("SetInformationJobObject")
	h, _, err := create.Call(0, 0)
	if h == 0 {
		t.Fatalf("CreateJobObjectW: %v", err)
	}
	info := extendedLimitT{}
	info.Basic.LimitFlags = jobLimitKillOnJobClose | jobLimitBreakaway
	if r, _, err := set.Call(h, 9, uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info)); r == 0 {
		t.Fatalf("SetInformationJobObject: %v", err)
	}
	return syscall.Handle(h)
}

func assignCurrentProcess(t *testing.T, job syscall.Handle) {
	t.Helper()
	assign := syscall.NewLazyDLL("kernel32.dll").NewProc("AssignProcessToJobObject")
	cur, _ := syscall.GetCurrentProcess()
	if r, _, err := assign.Call(uintptr(job), uintptr(cur)); r == 0 {
		t.Fatalf("AssignProcessToJobObject: %v", err)
	}
}

func inJob(t *testing.T, pid int, job syscall.Handle) bool {
	t.Helper()
	open := syscall.NewLazyDLL("kernel32.dll").NewProc("OpenProcess")
	h, _, err := open.Call(uintptr(processQueryLimited), 0, uintptr(pid))
	if h == 0 {
		t.Fatalf("OpenProcess(%d): %v", pid, err)
	}
	defer syscall.CloseHandle(syscall.Handle(h))
	var out int32
	r2, _, err2 := procIsProcessInJob.Call(h, uintptr(job), uintptr(unsafe.Pointer(&out)))
	if r2 == 0 {
		t.Fatalf("IsProcessInJob: %v", err2)
	}
	return out != 0
}

func spawnSleep(t *testing.T, attr *syscall.SysProcAttr) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("cmd", "/c", "ping", "-n", "20", "127.0.0.1", ">nul")
	cmd.SysProcAttr = attr
	if err := cmd.Start(); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	return cmd
}

// TestMindRunBreakawaySemantics：带 CREATE_BREAKAWAY_FROM_JOB 的子
// 进程逃出 Job（壳退出不连坐心智的核心前提）；不带则在 Job 里
// （旧连坐语义，也是测试自身的对照面）。这就是 mindrun_windows.go
// 两个返回值所依赖的全部 Windows 语义。
func TestMindRunBreakawaySemantics(t *testing.T) {
	if testing.Short() {
		t.Skip("短测跳过真实进程派生")
	}
	job := createShellLikeJob(t)
	// 泄漏一个副本句柄：本测试进程在 Job 里，若句柄全部关闭会触发
	// KillOnJobClose 把自己杀掉——故意不关（文件头安全注意）。
	dup := syscall.Handle(0)
	dupProc := syscall.NewLazyDLL("kernel32.dll").NewProc("DuplicateHandle")
	cur, _ := syscall.GetCurrentProcess()
	dupProc.Call(uintptr(cur), uintptr(job), uintptr(cur), uintptr(unsafe.Pointer(&dup)), 0, 1, 2 /*DUPLICATE_SAME_ACCESS*/)
	assignCurrentProcess(t, job)
	time.Sleep(50 * time.Millisecond) // Job 成员关系生效的宽限

	// 不带标志：入 Job（连坐面）。
	child := spawnSleep(t, nil)
	if !inJob(t, child.Process.Pid, job) {
		t.Fatalf("无标志子进程应在 Job 里")
	}

	// 带 CREATE_BREAKAWAY_FROM_JOB：逃出 Job（mind run 路径）。
	free := spawnSleep(t, mindRunSysProcAttr())
	if inJob(t, free.Process.Pid, job) {
		t.Fatalf("breakaway 子进程不应在 Job 里")
	}
}
