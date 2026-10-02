//go:build windows

package traj

// dirIdentity 恒返回固定身份（守卫恒通过）。Windows 的 MoveFileEx
// 不能替换已存在的目录——stealLock 的 rename 回位在落点被占时直接
// 失败，"声明中途目录被整体替换"的结构性窗口在 Windows 上不存在
// （它是 POSIX rename 语义特有的：rename(2) 可替换空目录）。
func dirIdentity(dir string) (string, bool) { return "windows", true }
