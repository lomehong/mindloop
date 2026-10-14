//go:build !windows

package web

import "syscall"

// 非 Windows：无 Job Object 语义，mind run 本就不随 web 退出。
func mindRunSysProcAttr() *syscall.SysProcAttr { return nil }

func mindRunFallbackWithoutAttr(error) bool { return false }
