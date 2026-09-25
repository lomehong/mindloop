package mind

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// readWorking 读并解析 working 状态文件；不存在返回 nil。
func readWorking(t *testing.T, tlDir string) *workingState {
	t.Helper()
	data, err := os.ReadFile(workingPath(tlDir))
	if err != nil {
		return nil
	}
	var ws workingState
	if err := json.Unmarshal(data, &ws); err != nil {
		t.Fatalf("working 形状不可解析: %v (%s)", err, data)
	}
	return &ws
}

// TestDispatcherWorkingProjectionLifecycle：busy 集合转换驱动状态
// 文件——阻塞式思考者被投递时文件出现且形状正确，释放后文件消失。
func TestDispatcherWorkingProjectionLifecycle(t *testing.T) {
	d, tl := newTestDispatcher(t)
	tk := &recorderThinker{
		name:    "monolith",
		sub:     Subscription{Types: []string{"message"}},
		blocks:  1,
		release: make(chan struct{}),
	}
	d.Register(tk)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	appendStep(t, tl, "message", "")
	waitFor(t, 2*time.Second, func() bool { return tk.wakeCount() >= 1 })

	// 忙碌期间：文件存在 ⇔ 有人在工作，形状与契约逐字一致。
	ws := readWorking(t, tl.Dir)
	if ws == nil {
		t.Fatal("思考者忙碌期间 working 状态文件应存在")
	}
	if !ws.Working || len(ws.Busy) != 1 {
		t.Fatalf("working 形状 = %+v，应 working:true 且 busy 恰 1 条", ws)
	}
	e := ws.Busy[0]
	if e.Thinker != "monolith" {
		t.Fatalf("busy[0].thinker = %q", e.Thinker)
	}
	if e.Wake != "step" {
		t.Fatalf("busy[0].wake = %q，应为 step", e.Wake)
	}
	if _, err := time.Parse(time.RFC3339, e.Since); err != nil {
		t.Fatalf("busy[0].since = %q 不是 RFC3339: %v", e.Since, err)
	}

	// 释放后：忙集变空，文件删除。
	close(tk.release)
	waitFor(t, 2*time.Second, func() bool { return readWorking(t, tl.Dir) == nil })
}

// TestDispatcherWorkingForceReleaseRemovesEntry：期限强制释放是独立
// 的 busy 释放路径——挂死的 Wake 还没返回，其忙集投影也必须摘除。
func TestDispatcherWorkingForceReleaseRemovesEntry(t *testing.T) {
	d, tl := newTestDispatcher(t)
	d.WakeTimeout = 60 * time.Millisecond
	h := &hungThinker{
		name:    "hung",
		sub:     Subscription{Types: []string{"message"}},
		release: make(chan struct{}),
	}
	d.Register(h)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	appendStep(t, tl, "message", "")
	waitFor(t, 2*time.Second, func() bool { return h.wakes.Load() >= 1 })
	waitFor(t, 2*time.Second, func() bool {
		ws := readWorking(t, tl.Dir)
		return ws == nil // 强制释放后（Wake 仍挂着）投影应已摘除
	})
	close(h.release)
}

// TestDispatcherWorkingWriteFailureTolerated：把 working 路径占位成
// 目录（写必败）——投影写失败绝不能打断投递与调度。
func TestDispatcherWorkingWriteFailureTolerated(t *testing.T) {
	d, tl := newTestDispatcher(t)
	if err := os.MkdirAll(filepath.Dir(workingPath(tl.Dir)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(workingPath(tl.Dir), 0o755); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(workingPath(tl.Dir))

	tk := &recorderThinker{name: "w", sub: Subscription{Types: []string{"message"}}}
	d.Register(tk)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	appendStep(t, tl, "message", "")
	waitFor(t, 2*time.Second, func() bool { return tk.wakeCount() >= 1 })
	// 投递照常发生即证明写失败被容忍；WaitIdle 确认释放路径同样不炸
	//（blocks=0 时 Wake 不经过 release channel，无需放行）。
	waitFor(t, 2*time.Second, func() bool { return d.WaitIdle(time.Second) })
}
