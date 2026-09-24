package web

import (
	"os"
	"path/filepath"
	"time"

	"mindloop/internal/mind"
	"mindloop/internal/traj"
)

// liveWindow 是 Live 判定的时间窗：调度器进程死了但 mindlog 的 mtime
// 在这个窗内，仍认为心智"刚停"——viewer 不会立刻把它标灰。
const liveWindow = 30 * time.Second

// isIdentityLive 判断心智是否在跑：运行锁属主进程活着（mind.RunLockDir
// 同一路径，owner.json 的 pid 经 OpenProcess 探活），或 mindlog mtime
// 落在 liveWindow 内。残留锁（属主已死）不算 live——那是崩溃现场，
// 不是运行中。
func isIdentityLive(identityDir string) bool {
	if identityDir == "" {
		return false
	}
	lock := mind.RunLockDir(identityDir)
	if _, err := os.Stat(lock); err == nil {
		if traj.LockOwnerAlive(lock) {
			return true
		}
		// 锁在但属主探活失败：可能是刚建锁还没写属主文件的启动
		// 窗口（lockGrace 内），也可能是残留锁——落到 mtime 判据，
		// 与无锁路径同一套语义。
	}
	// 无锁或残留锁：轨迹最近被写 = 心智刚醒（心跳窗口内）。
	md := filepath.Join(identityDir, "trajectories")
	entries, err := os.ReadDir(md)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		if time.Since(fi.ModTime()) < liveWindow {
			return true
		}
	}
	return false
}
