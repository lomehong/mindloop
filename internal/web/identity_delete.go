package web

// 身份删除（DELETE /api/identities/{名}）——工作台「新建身份」的对称面。
// 危险操作，编排顺序固定：
//
//	① 停机：运行中的心智先写停机标志（RequestStopDir，调度器心跳内
//	   优雅退出——不硬杀），轮询等真活进程退场（≤15s）；超时拒删 409。
//	② 清共享侧索引：释放进程内对该身份（含子轨迹）的观察——Windows
//	   的打开句柄会挡住 RemoveAll。
//	③ identity.Remove：整目录（轨迹/记忆/人格/.env 密钥）删除；
//	   心智根（~/.mindloop 含全局 .env）不在范围内（remove 自身有护栏）。
//
// CLI 的裸 remove 保持不变——这里是 web 面的停机编排。

import (
	"errors"
	"net/http"
	"os"
	"time"

	"github.com/lomehong/mindloop/internal/identity"
	"github.com/lomehong/mindloop/internal/mind"
	"github.com/lomehong/mindloop/internal/traj"
)

// deleteStopWait 停机等待上限：调度器心跳默认 200ms，在途思考收尾也远
// 小于此；超时意味着状态异常，如实拒删而不是硬杀/硬删。（测试可改小。）
var deleteStopWait = 15 * time.Second

// deleteBlockingLive 是删除专用的判活：只看运行锁属主进程是否真实存活。
// isIdentityLive 的 mtime 时间窗（30s）适合 UI 标灰，但会挡住「刚停就删」；
// 真正挡 RemoveAll 的只有活进程的文件句柄。
func deleteBlockingLive(tlDir string) bool {
	lock := mind.RunLockDir(tlDir)
	if _, err := os.Stat(lock); err != nil {
		return false
	}
	return traj.LockOwnerAlive(lock)
}

func (s *Server) handleIdentityDelete(w http.ResponseWriter, r *http.Request, id *identity.Identity) {
	stopped := false
	if isIdentityLive(id.Timeline.Dir) {
		if err := mind.RequestStopDir(id.Timeline.Dir); err != nil {
			writeError(w, 500, "写入停机标志失败: "+err.Error())
			return
		}
		stopped = true
		deadline := time.Now().Add(deleteStopWait)
		for time.Now().Before(deadline) && deleteBlockingLive(id.Timeline.Dir) {
			time.Sleep(250 * time.Millisecond)
		}
		if deleteBlockingLive(id.Timeline.Dir) {
			writeError(w, http.StatusConflict, "心智仍在运行（停机未在时限内完成）——请稍后再试")
			return
		}
	}
	s.indexes.forgetUnder(id.Dir)
	if err := identity.Remove(id.Name); err != nil {
		if errors.Is(err, identity.ErrNotFound) {
			writeError(w, 404, "身份不存在")
			return
		}
		writeError(w, 500, "删除身份失败: "+err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"ok": true, "id": id.Name, "name": id.Name, "stopped": stopped,
	})
}
