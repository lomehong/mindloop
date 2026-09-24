package mind

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// 控制面文件原语——web 仪表盘与 CLI 通过文件向运行中的调度器传
// 指令（同一套"日志即总线"的哲学：控制也是文件）。调度器每个
// 心跳消费一次；文件极小，直接读写。

// controlDir 返回 <轨迹目录>/run（控制文件都放这里）。
func controlDir(tlDir string) string { return filepath.Join(tlDir, "run") }

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
	return os.WriteFile(disabledPath(tlDir), []byte(strings.Join(next, "\n")+"\n"), 0o644)
}

// SignalWake 写入手动唤醒信号文件（调度器下个心跳消费并删除）。
func SignalWake(tlDir, thinker string) error {
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

var _ = bufio.NewReader // 留作后续流式消费
