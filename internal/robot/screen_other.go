//go:build !windows

// 非 Windows 降级：全部返回 ErrUnsupported——跨平台编译不断，
// 运行到非 Windows 上如实报错（绝不伪装成功）。
package robot

import "time"

func ScreenshotPNG() ([]byte, ScreenInfo, error) { return nil, ScreenInfo{}, ErrUnsupported }

func ForegroundTitle() (string, bool) { return "", false }

func WindowTitles() []string { return nil }

func FocusWindow(string) (string, error) { return "", ErrUnsupported }

func CursorPos() (int, int, error) { return 0, 0, ErrUnsupported }

func Click(int, int) error { return ErrUnsupported }

func RightClick(int, int) error { return ErrUnsupported }

func TypeText(string) error { return ErrUnsupported }

// lastInputAge 非 Windows 降级：robot.go 的 OperatorIdle 包装转调这里，
// 与 screen_windows.go 保持同一符号契约（包装器无平台分身）。
func lastInputAge() (time.Duration, error) { return 0, ErrUnsupported }
