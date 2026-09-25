package mind

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// DispatchEvent 是调度事件流一行的类型化形态——kind ∈
// step | dispatch | other。字段与 dispatcher 的写入面一一对应；
// 读取时坏行跳过、未知字段忽略，schema 漂移在编译期暴露而不是
// 在仪表盘上静默断裂。
type DispatchEvent struct {
	Kind      string `json:"kind"`
	Type      string `json:"type,omitempty"`
	Thinker   string `json:"thinker,omitempty"`
	Source    string `json:"source,omitempty"`
	StepID    string `json:"step_id,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Synthetic bool   `json:"synthetic,omitempty"`
	TS        string `json:"ts"`
}

// dispatchLogEvents 是调度器的事件落盘器：把每次投递与合成唤醒
// 写成 NDJSON 到 <轨迹目录>/run/dispatcher.log：单写者（dispatcher
// 的心跳 goroutine）+ O_APPEND 单行写，Windows/POSIX 都原子。
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
func (d *dispatchLogEvents) Append(ev DispatchEvent) {
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
func ReadEvents(path string) ([]DispatchEvent, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []DispatchEvent
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev DispatchEvent
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue // 坏行跳过
		}
		out = append(out, ev)
	}
	return out, nil
}

// DispatchLogPath 返回某轨迹的事件文件路径（web 层读取用）。
func DispatchLogPath(tlDir string) string {
	return filepath.Join(tlDir, "run", "dispatcher.log")
}

// NewDispatchLogForTest 暴露给测试构造事件写入器（生产路径由
// NewDispatcher 内部创建）。
func NewDispatchLogForTest(tlDir string) *dispatchLogEvents {
	return newDispatchLog(tlDir)
}
