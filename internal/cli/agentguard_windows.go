//go:build windows

// agentguard 的 Windows 实现：进程祖先链探测（第二信号）。
// CreateToolhelp32Snapshot 一次快照建 pid→(ppid, exe) 全表，再从
// 自身 pid 沿 PPID 链上溯——任何祖先是 mindloop 可执行文件本体
// 即判定为沙箱内调用（runner 是 mindloop 进程；沙箱 shell 与 CLI
// 是它的子孙）。链长上限防环（快照竞态下 PPID 可能短暂成环）。
package cli

import (
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

const maxAncestryWalk = 32

// defaultProcChainNames 返回自身进程的祖先可执行文件名列表（父→
// 远祖）。失败返回 nil——探测失败按「无第二信号」处理，回落 env 判定。
func defaultProcChainNames() []string {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snap)

	type procInfo struct {
		ppid uint32
		name string
	}
	table := make(map[uint32]procInfo)
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	err = windows.Process32First(snap, &e)
	for err == nil {
		table[e.ProcessID] = procInfo{
			ppid: e.ParentProcessID,
			name: strings.ToLower(strings.TrimRight(windows.UTF16ToString(e.ExeFile[:]), "\x00")),
		}
		err = windows.Process32Next(snap, &e)
	}

	selfName := selfExeBase()
	if selfName == "" {
		return nil
	}
	var out []string
	// 先跳到父进程再开始收集——链上第一项若是自身，self 恒匹配
	// self，人人被判沙箱（测试在发布前逮住的真 bug）。
	pid := windows.GetCurrentProcessId()
	self, ok := table[pid]
	if !ok {
		return nil
	}
	pid = self.ppid
	for i := 0; i < maxAncestryWalk; i++ {
		info, ok := table[pid]
		if !ok {
			break
		}
		out = append(out, info.name)
		if info.name == selfName {
			// 命中即足够——列表截到命中点，调用方比对更快。
			return out
		}
		pid = info.ppid
	}
	return out
}
