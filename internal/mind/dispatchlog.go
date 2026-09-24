package mind

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// dispatchLogEvents 是 viewer DispatchEvent 的 Go 形态——kind ∈
// step | dispatch | other。dispatcher 把每次投递与合成唤醒落成
// NDJSON 到 <轨迹目录>/run/dispatcher.log：单写者（dispatcher 的
// 心跳 goroutine）+ O_APPEND 单行写，Windows/POSIX 都原子。
type dispatchLogEvents struct {
	mu   sync.Mutex
	path string
}

func newDispatchLog(tlDir string) *dispatchLogEvents {
	dir := filepath.Join(tlDir, "run")
	_ = os.MkdirAll(dir, 0o755)
	return &dispatchLogEvents{path: filepath.Join(dir, "dispatcher.log")}
}

// Append 写一行事件。失败静默——事件流是可观测性，不该影响调度。
func (d *dispatchLogEvents) Append(ev map[string]any) {
	b, err := json.Marshal(ev)
	if err != nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	f, err := os.OpenFile(d.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	f.Write(append(b, '\n'))
}

// ReadEvents 读全部事件（新→旧排序由前端负责；这里按文件序返回）。
// 文件缺失返回空切片——"还没有事件"是合法状态。
func ReadEvents(path string) ([]map[string]any, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []map[string]any
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var m map[string]any
		if json.Unmarshal([]byte(line), &m) != nil {
			continue // 坏行跳过
		}
		out = append(out, m)
	}
	return out, nil
}

// DispatchLogPath 返回某轨迹的事件文件路径（web 层读取用）。
func DispatchLogPath(tlDir string) string {
	return filepath.Join(tlDir, "run", "dispatcher.log")
}

var _ = context.Background // 保持 context 引用一致性

// NewDispatchLogForTest 暴露给测试构造事件写入器（生产路径由
// NewDispatcher 内部创建）。
func NewDispatchLogForTest(tlDir string) *dispatchLogEvents {
	return newDispatchLog(tlDir)
}
