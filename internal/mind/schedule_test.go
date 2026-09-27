package mind

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"mindloop/internal/schedule"
)

// scheduleExecProbe 记录 exec 条目的执行次数。
type scheduleExecProbe struct {
	mu    sync.Mutex
	calls int
}

func (p *scheduleExecProbe) run(_ context.Context, _ schedule.Parsed) schedule.ExecOutcome {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	return schedule.ExecOutcome{}
}

func (p *scheduleExecProbe) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// scheduleSubmitProbe 记录 at 条目的提交幂等键。
type scheduleSubmitProbe struct {
	mu   sync.Mutex
	keys []string
}

func (p *scheduleSubmitProbe) submit(_ context.Context, _ string, key string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.keys = append(p.keys, key)
	return "task:probe", nil
}

func (p *scheduleSubmitProbe) snapshot() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.keys...)
}

// TestDispatcherDrivesScheduleRuntime 验证调度器心跳驱动日程原语：
// Run 启动时 Recover 补提交当天已过点的 at 条目；exec 条目由心跳
// 触发；同一日期键不得重复提交。
func TestDispatcherDrivesScheduleRuntime(t *testing.T) {
	d, _ := newTestDispatcher(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "schedule.json")
	body := `{"items":[` +
		`{"id":"sample","every":"1s","exec":"echo 采样"},` +
		`{"id":"daily","at":"00:00","task":"晚报 {{date}}"}]}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ex, sub := &scheduleExecProbe{}, &scheduleSubmitProbe{}
	d.SetSchedule(schedule.New(schedule.Options{
		Path:      path,
		Dir:       dir,
		LogPath:   filepath.Join(dir, "run", "schedule.log"),
		StatePath: filepath.Join(dir, "run", "schedule-state.json"),
		Submit:    sub.submit,
		Execute:   ex.run,
	}))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	// at "00:00" 启动：48h 回看窗口内（昨天+今天）的两个错过各自
	// 补提交一次——键由计划时刻导出，互不相同。
	waitFor(t, 2*time.Second, func() bool { return len(sub.snapshot()) == 2 })
	// exec 条目在首个心跳即开跑。
	waitFor(t, 2*time.Second, func() bool { return ex.count() >= 1 })

	// 观察窗：已补提交的键不得重复（跨午夜时新计划键是正确行为）。
	time.Sleep(300 * time.Millisecond)
	keys := sub.snapshot()
	if len(keys) != 2 || !strings.HasPrefix(keys[0], "sched-daily-") {
		t.Fatalf("at 提交不符: %v", keys)
	}
	seen := map[string]bool{}
	for _, k := range keys {
		if seen[k] {
			t.Fatalf("同日重复提交: %v", keys)
		}
		seen[k] = true
	}
}
