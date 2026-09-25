//go:build !windows

package sandbox

import (
	"os/exec"
	"syscall"
)

// 非 Windows 平台没有 Job Object：降级为"独立进程组"管辖——启动
// 前把 bash 放进自己的进程组（Setpgid），超时/取消时对整组发
// SIGKILL。这仍弱于 Job Object（对 exec/setpgid 主动挣脱进程组的
// 逃逸不设防，见 README 的平台对照表），但脚本里 `sleep 3600 &`
// 这类后台子进程不再把每一轮拖到总超时：它们随整组一起死，持有
// 的输出管道随之关闭，cmd.Wait 不再被吊住（曾有的缺陷：后台
// 子进程握住管道，让每一轮都等到总超时才收场）。
type job struct{ cmd *exec.Cmd }

// newJob 在 POSIX 上总是成功：进程组管辖不涉及资源分配。
// memLimit 没有对应实现，忽略。
func newJob(uint64) (*job, error) { return &job{}, nil }

// prepare 必须发生在 Start 之前：Setpgid 让 bash 成为新进程组的
// 组长，组 id 即 bash 的 pid。
func (j *job) prepare(cmd *exec.Cmd) {
	if j == nil || cmd == nil {
		return
	}
	j.cmd = cmd
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// assignPID 无事可做：进程组归属在 Start 时已由 Setpgid 确定。
func (j *job) assignPID(int) error { return nil }

// terminate 对整个进程组发 SIGKILL——后台孙进程一并收割。组信号
// 失败（组已消失等）时退回单进程 Kill 兜底。
func (j *job) terminate() {
	if j == nil || j.cmd == nil || j.cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-j.cmd.Process.Pid, syscall.SIGKILL)
	_ = j.cmd.Process.Kill()
}

// close 无资源需要释放。
func (j *job) close() {}
