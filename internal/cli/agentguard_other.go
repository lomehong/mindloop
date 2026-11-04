//go:build !windows

// agentguard 的非 Windows 实现：/proc 祖先链探测（第二信号）。
// stat 的 comm 字段在括号内可含空格——从最后一个 ')' 之后解析 PPid。
// macOS 无 /proc：返回 nil（env 信号仍在，文档已声明平台差）。
package cli

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const maxAncestryWalk = 32

// defaultProcChainNames 返回自身进程的祖先可执行文件名列表（父→
// 远祖）。失败返回 nil——探测失败按「无第二信号」处理，回落 env 判定。
func defaultProcChainNames() []string {
	if _, err := os.Stat("/proc/self"); err != nil {
		return nil // 非 procfs 平台（darwin）
	}
	selfName := selfExeBase()
	if selfName == "" {
		return nil
	}
	var out []string
	pid := os.Getpid()
	for i := 0; i < maxAncestryWalk; i++ {
		data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
		if err != nil {
			break
		}
		s := string(data)
		idx := strings.LastIndex(s, ")")
		if idx < 0 || idx+2 > len(s) {
			break
		}
		fields := strings.Fields(s[idx+2:])
		if len(fields) < 2 {
			break
		}
		ppid, err := strconv.Atoi(fields[1])
		if err != nil || ppid <= 0 {
			break
		}
		comm, err := os.ReadFile("/proc/" + strconv.Itoa(ppid) + "/comm")
		if err != nil {
			break
		}
		name := strings.TrimSpace(string(comm))
		out = append(out, name)
		if name == selfName {
			return out
		}
		pid = ppid
	}
	return out
}

func defaultSelfExeBase() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Base(exe)
}
