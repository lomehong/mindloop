package mind

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// 控制面文件原语——web 仪表盘与 CLI 通过文件向运行中的调度器传
// 指令（同一套"日志即总线"的哲学：控制也是文件）。调度器每个
// 心跳消费一次；文件极小，直接读写。

// controlDir 返回 <轨迹目录>/run（控制文件都放这里）。
func controlDir(tlDir string) string { return filepath.Join(tlDir, "run") }

// RunLockDir 返回心智运行锁目录——web 监控面与 mind 共用同一
// 判据，不再各自硬编码路径。
func RunLockDir(tlDir string) string {
	return filepath.Join(controlDir(tlDir), "dispatcher.lock")
}

// validThinkerName 是 thinker 名白名单：wake.<name> 直接拼控制面
// 文件名，而名字来自 URL/web 控制面——必须拒绝穿越段与分隔符
// （Windows 下 \ 也是路径分隔符，HTTP mux 的 cleanPath 不消化它）。
func validThinkerName(name string) bool {
	if name == "" || len(name) > 64 || strings.Contains(name, "..") {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') &&
			!(r >= '0' && r <= '9') && r != '-' && r != '_' && r != '.' {
			return false
		}
	}
	return true
}

// disabledPath 是 thinker 禁用名单：每行一个 thinker 名。
func disabledPath(tlDir string) string {
	return filepath.Join(controlDir(tlDir), "thinkers.disabled")
}

// wakePath 是手动唤醒信号：文件存在即要求调度器唤醒该 thinker。
func wakePath(tlDir, thinker string) string {
	return filepath.Join(controlDir(tlDir), "wake."+thinker)
}

// SetThinkerEnabled 写/删禁用名单条目（web/CLI 控制面调用）。
func SetThinkerEnabled(tlDir, thinker string, enabled bool) error {
	if !validThinkerName(thinker) {
		return fmt.Errorf("mind: 非法 thinker 名 %q", thinker)
	}
	if err := os.MkdirAll(controlDir(tlDir), 0o755); err != nil {
		return err
	}
	cur := readDisabled(tlDir)
	next := make([]string, 0, len(cur)+1)
	for _, n := range cur {
		if n != thinker {
			next = append(next, n)
		}
	}
	if !enabled {
		next = append(next, thinker)
	}
	if len(next) == 0 {
		_ = os.Remove(disabledPath(tlDir))
		return nil
	}
	// tmp + 原子改名：就地截断重写会让并发读者读到半行名单（调度器
	// 每个心跳都读），两个并发写请求的中间态也会互相可见——曾经
	// 因此出现"禁用名单短暂不完整"的投影抖动。pid 后缀让多个
	// web/CLI 进程互不踩临时文件。
	tmp := fmt.Sprintf("%s.%d.tmp", disabledPath(tlDir), os.Getpid())
	if err := os.WriteFile(tmp, []byte(strings.Join(next, "\n")+"\n"), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, disabledPath(tlDir))
}

// SignalWake 写入手动唤醒信号文件（调度器下个心跳消费并删除）。
func SignalWake(tlDir, thinker string) error {
	if !validThinkerName(thinker) {
		return fmt.Errorf("mind: 非法 thinker 名 %q", thinker)
	}
	if err := os.MkdirAll(controlDir(tlDir), 0o755); err != nil {
		return err
	}
	return os.WriteFile(wakePath(tlDir, thinker), []byte("manual"), 0o644)
}

// IsThinkerDisabled 读禁用名单。web 与 dispatcher 共用同一实现。
func IsThinkerDisabled(tlDir, thinker string) bool {
	for _, n := range readDisabled(tlDir) {
		if n == thinker {
			return true
		}
	}
	return false
}

// readDisabled 读禁用名单（不存在返回空）。
func readDisabled(tlDir string) []string {
	data, err := os.ReadFile(disabledPath(tlDir))
	if err != nil {
		return nil
	}
	var out []string
	for _, ln := range strings.Split(string(data), "\n") {
		if t := strings.TrimSpace(ln); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// collectWakeSignals 读出全部待消费的唤醒信号并删除文件（原子消费：
// 调度器单读者，读完即删避免重复唤醒）。
func collectWakeSignals(tlDir string) []string {
	dir := controlDir(tlDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "wake.") {
			thinker := strings.TrimPrefix(name, "wake.")
			if thinker != "" {
				out = append(out, thinker)
			}
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
	return out
}
