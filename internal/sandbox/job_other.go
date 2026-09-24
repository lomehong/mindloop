//go:build !windows

package sandbox

import "errors"

// 非 Windows 平台没有 Job Object：newJob 报错，Run 降级为无 job
// 运行（仍可用，只是失去整树管辖）。本项目以 Windows 为一等公民，
// 此文件仅为可移植编译而存在。
type job struct{}

func newJob(uint64) (*job, error) {
	return nil, errors.New("sandbox: Job Object 仅在 Windows 可用")
}

func (j *job) assignPID(int) error {
	return errors.New("sandbox: Job Object 仅在 Windows 可用")
}

func (j *job) terminate() {}

func (j *job) close() {}
