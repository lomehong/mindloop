//go:build !windows

// job_other.go — 非 Windows 无作业对象：宿主停机走 stopAll 的
// Process.Kill，被强杀时的孤儿由运行锁与 stop 命令兜底（设计 §6）。
package system

import "os"

type jobObject struct{}

func (j *jobObject) init() error        { return nil }
func (j *jobObject) assign(*os.Process) {}
func (j *jobObject) close()             {}
