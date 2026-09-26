package mind

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mindloop/internal/traj"
)

// newTestTimeline 在临时目录建一条轨迹（运行锁与控制面的测试基底）。
func newTestTimeline(t *testing.T) *traj.Timeline {
	t.Helper()
	root := t.TempDir()
	tl, err := traj.CreateAt(context.Background(), filepath.Join(root, "trajectories"), "mind-test")
	if err != nil {
		t.Fatal(err)
	}
	return tl
}

// TestTryRunLockOwnership：运行锁的单实例语义——首次获取 owned=true，
// 持有期间再次尝试 owned=false（同进程属主活着），释放后可重新获取。
// 这是 web 控制面（"锁存在即在跑"）与 chat 接管模式的同一判据。
func TestTryRunLockOwnership(t *testing.T) {
	tl := newTestTimeline(t)

	first, owned, err := TryRunLock(tl)
	if err != nil {
		t.Fatal(err)
	}
	if !owned {
		t.Fatal("空闲锁首次获取应 owned=true")
	}

	_, owned, err = TryRunLock(tl)
	if err != nil {
		t.Fatal(err)
	}
	if owned {
		t.Fatal("已被活着的属主持有时应 owned=false")
	}

	first.Release()
	second, owned, err := TryRunLock(tl)
	if err != nil {
		t.Fatal(err)
	}
	if !owned {
		t.Fatal("释放后应可重新获取")
	}
	second.Release()
}

// runOwned 通过生产入口启动，缺失入口时直接报告行为尚未实现。
func runOwned(d *Dispatcher, ctx context.Context, lock *RunLock) error {
	owned, ok := any(d).(interface {
		RunOwned(context.Context, *RunLock) error
	})
	if !ok {
		return fmt.Errorf("尚无持锁运行入口")
	}
	return owned.RunOwned(ctx, lock)
}

func TestRunOwnedRetainsLockUntilWorkerExits(t *testing.T) {
	tl := newTestTimeline(t)
	lock, owned, err := TryRunLock(tl)
	if err != nil || !owned {
		t.Fatalf("获取运行锁: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	h := &hungThinker{name: "hung", sub: Subscription{Types: []string{traj.TypeMessage}}, release: make(chan struct{}), contexts: make(chan context.Context, 1)}
	d := NewDispatcher(tl, time.Millisecond)
	d.Register(h)
	done := make(chan error, 1)
	t.Cleanup(func() {
		cancel()
		close(h.release)
		d.WaitIdle(time.Second)
		lock.Release()
	})
	go func() { done <- runOwned(d, ctx, lock) }()
	if err := tl.Append(ctx, traj.NewStep(traj.TypeMessage)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-h.contexts:
	case err := <-done:
		t.Fatalf("执行未启动: %v", err)
	case <-time.After(time.Second):
		t.Fatal("执行未启动")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("调度循环未退出")
	}
	lock.Release()
	if d.WaitIdle(10 * time.Millisecond) {
		t.Fatal("未退出的 worker 被误报为空闲")
	}
	other, acquired, err := TryRunLock(tl)
	if acquired {
		other.Release()
		t.Fatal("旧执行未退出却释放了身份执行权")
	}
	if err != nil {
		t.Fatal(err)
	}
	retryCtx, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer stop()
	if err := runOwned(NewDispatcher(tl, time.Millisecond), retryCtx, lock); err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("同一个锁不应被另一个调度器复用: %v", err)
	}
	h.release <- struct{}{}
	if !d.WaitIdle(time.Second) {
		t.Fatal("旧执行未退出")
	}
	next, acquired, err := TryRunLock(tl)
	if err != nil || !acquired {
		t.Fatalf("真实退出后运行锁未释放: %v", err)
	}
	next.Release()
}

func TestRunOwnedRejectsMissingOrForeignLock(t *testing.T) {
	tl := newTestTimeline(t)
	foreign, owned, err := TryRunLock(newTestTimeline(t))
	if err != nil || !owned {
		t.Fatal(err)
	}
	defer foreign.Release()
	for _, lock := range []*RunLock{nil, foreign} {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		err := runOwned(NewDispatcher(tl, time.Millisecond), ctx, lock)
		cancel()
		if err == nil || errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("无身份运行权仍启动调度: %v", err)
		}
	}
}

// TestRequestStopDir：停机标志写到 run/stop 且幂等可写。
func TestRequestStopDir(t *testing.T) {
	tl := newTestTimeline(t)
	if err := RequestStopDir(tl.Dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(tl.Dir, "run", "stop")); err != nil {
		t.Fatalf("停机标志未写入: %v", err)
	}
	// 调度器判据（checkStop）能读到。
	d := NewDispatcher(tl, 50*time.Millisecond)
	if !d.checkStop() {
		t.Fatal("checkStop 应消费到停机标志")
	}
	if d.checkStop() {
		t.Fatal("停机标志应被消费（读完即删）")
	}
}

// TestClearStopFlag：残留停机标志清理。事故链：mind stop 写下标志
// 时调度器已死 → 标志残留 → 下一次启动在首个心跳"启动即优雅退出"。
// 清理必须幂等，且清理后 checkStop 不再消费到任何东西。
func TestClearStopFlag(t *testing.T) {
	tl := newTestTimeline(t)
	if err := RequestStopDir(tl.Dir); err != nil {
		t.Fatal(err)
	}
	if err := ClearStopFlag(tl); err != nil {
		t.Fatalf("ClearStopFlag: %v", err)
	}
	// 幂等：文件已不存在，再清一次必须仍是 nil。
	if err := ClearStopFlagDir(tl.Dir); err != nil {
		t.Fatalf("ClearStopFlagDir 应幂等: %v", err)
	}
	d := NewDispatcher(tl, 50*time.Millisecond)
	if d.checkStop() {
		t.Fatal("清理后 checkStop 不应再消费到停机标志")
	}
}

// TestControlFaceNameValidation：控制面 thinker 名走白名单——
// wake.<name> 直接拼文件名，穿越段与分隔符必须在入口被拒。
func TestControlFaceNameValidation(t *testing.T) {
	tl := newTestTimeline(t)
	for _, bad := range []string{`..\..\target`, "a/b", "", "x\ny", strings.Repeat("a", 65)} {
		if err := SignalWake(tl.Dir, bad); err == nil {
			t.Errorf("SignalWake(%q) 应拒绝", bad)
		}
		if err := SetThinkerEnabled(tl.Dir, bad, false); err == nil {
			t.Errorf("SetThinkerEnabled(%q) 应拒绝", bad)
		}
	}
	if _, err := os.Stat(filepath.Join(tl.Dir, "run")); !os.IsNotExist(err) {
		t.Fatalf("被拒的名字不应留下任何控制面文件: %v", err)
	}
	if err := SignalWake(tl.Dir, "responder"); err != nil {
		t.Fatalf("合法名应通过: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tl.Dir, "run", "wake.responder")); err != nil {
		t.Fatalf("合法 wake 信号未写入: %v", err)
	}
}
