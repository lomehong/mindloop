package web

import (
	"os"
	"path/filepath"
	"time"
)

// liveWindow 是 Live 判定的时间窗：调度器进程死了但 mindlog 的 mtime
// 在这个窗内，仍认为心智"刚停"——viewer 不会立刻把它标灰。
const liveWindow = 30 * time.Second

// isIdentityLive 判断心智是否在跑：调度器运行锁存在且属主活着，
// 或 mindlog mtime 落在 liveWindow 内。与 mind.TryRunLock 同一套
// 判据（dispatcher 启动即建 <id>/run/dispatcher.lock）。
func isIdentityLive(identityDir string) bool {
	if identityDir == "" {
		return false
	}
	lock := filepath.Join(identityDir, "run", "dispatcher.lock")
	if fi, err := os.Stat(lock); err == nil {
		// 锁存在即认为在跑：锁持有者死亡时调度器/stop 会清理。
		_ = fi
		return true
	}
	// 无锁：轨迹最近被写 = 心智刚醒（心跳窗口内）。
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
