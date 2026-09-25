package mind

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"mindloop/internal/traj"
)

// ErrStopRequested：收到停机标志（mind stop 或 chat 退出时的优雅
// 停机路径）。
var ErrStopRequested = errors.New("mind: 收到停机请求")

// RunLock 是心智的单实例锁：同一身份同时至多一个调度器在跑。
// chat 命令用它决定"接管启动"还是"接入已有实例"。
type RunLock struct {
	dir     string
	release func() error
}

// TryRunLock 尝试获取心智运行锁（<轨迹目录>/run/dispatcher.lock）。
// owned=false 表示已有活着的调度器在跑（chat 场景 = 接入模式）。
// 属主已死的残留锁会被自动接管。
func TryRunLock(tl *traj.Timeline) (*RunLock, bool, error) {
	dir := filepath.Join(tl.Dir, "run", "dispatcher.lock")
	release, owned, err := traj.TryDirLock(context.Background(), dir)
	if err != nil {
		return nil, false, err
	}
	return &RunLock{dir: dir, release: release}, owned, nil
}

// Release 释放运行锁。
func (l *RunLock) Release() {
	if l != nil && l.release != nil {
		l.release()
	}
}

// RequestStop 向运行中的调度器投递停机标志。调度器在下一次心跳
// （默认 200ms 内）检测到后优雅退出——比杀进程干净：在途的思考
// 有机会收尾。
func RequestStop(tl *traj.Timeline) error { return RequestStopDir(tl.Dir) }

// RequestStopDir 是 RequestStop 的目录形态——web 仪表盘手里只有
// 身份目录时用同一套实现，不自己拼 stop 文件路径。
func RequestStopDir(tlDir string) error {
	dir := controlDir(tlDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	stop := filepath.Join(dir, "stop")
	if err := os.WriteFile(stop, []byte(traj.NowString()), 0o644); err != nil {
		return err
	}
	return nil
}

// ClearStopFlag 清除可能残留的停机标志。必须在 TryRunLock 返回
// owned=true 之后、Dispatcher.Run 之前调用——运行锁的单实例语义
// 保证此刻没有并发的标志消费者。事故场景：mind stop 写下标志时
// 调度器已经死了，标志没有消费者删除，残留到下一次启动会让调度器
// 在首个心跳"启动即优雅退出"，chat 接管模式下更是表象正常、心智
// 已死。web 侧早已自己清理（handleThinkersAll），CLI 侧经此 API
// 统一对齐。
func ClearStopFlag(tl *traj.Timeline) error { return ClearStopFlagDir(tl.Dir) }

// ClearStopFlagDir 是 ClearStopFlag 的目录形态——调用方手里只有
// 身份目录时用同一套实现。文件不存在是正常状态，不算错误。
func ClearStopFlagDir(tlDir string) error {
	if err := os.Remove(filepath.Join(controlDir(tlDir), "stop")); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (d *Dispatcher) stopPath() string {
	return filepath.Join(d.tl.Dir, "run", "stop")
}

// checkStop 检查并消费停机标志。放这里而不是 Run：心跳粒度一致
// （200ms 内响应）。
func (d *Dispatcher) checkStop() bool {
	if _, err := os.Stat(d.stopPath()); err == nil {
		_ = os.Remove(d.stopPath())
		return true
	}
	return false
}
