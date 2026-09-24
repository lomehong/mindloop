package mind

import (
	"context"
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
