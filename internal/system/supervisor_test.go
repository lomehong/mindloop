package system

// supervisor_test.go — 监督行为的回归测试（退出检测与停后重启）。
// 曾被冒烟漏掉的两条路径在此钉死：
//   1. 子进程退出必须被检出并按退避重启（ProcessState 缺 Wait 不落
//      定的教训——没有 Wait goroutine 时 reap 恒视作运行中）；
//   2. 停→启循环在同一宿主生命周期内必须能重新拉起（旧实现中
//      children 条目永不摘除，Diff 恒认为"已在运行"）。
//
// helper 子进程 = 测试二进制自身重执行（-test.run 定向到
// TestHelperChildProcess），行为由环境变量 MINDLOOP_SYS_TEST_CHILD
// 控制——t.Setenv 设的值随进程环境被子进程继承。

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

// TestHelperChildProcess 是监督测试的被控子进程；模式环境变量缺席
// 时为空操作（正常测试运行不受影响）。
func TestHelperChildProcess(t *testing.T) {
	switch os.Getenv("MINDLOOP_SYS_TEST_CHILD") {
	case "die":
		os.Exit(3)
	case "alive":
		time.Sleep(30 * time.Second)
		os.Exit(0)
	}
}

func helperSpawn(_ ChildName) (string, []string) {
	return os.Args[0], []string{"-test.run=TestHelperChildProcess"}
}

// waitStatus 轮询状态投影直至条件满足（15s 上限，失败时打印全量）。
func waitStatus(t *testing.T, sup *Supervisor, what string, cond func([]ChildStatus) bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		st := sup.Status()
		if cond(st) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("等待 %s 超时: %+v", what, st)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func statusOf(st []ChildStatus, name ChildName) (ChildStatus, bool) {
	for _, c := range st {
		if c.Name == string(name) {
			return c, true
		}
	}
	return ChildStatus{}, false
}

// TestBackoffDelayCurve：退避曲线纯函数——1s 起指数翻倍、60s 封顶；
// 非法输入（0/负）按首次处理。集成测试只断言"重启 ≥2 次"，翻倍节奏
// 与封顶值由此钉死（2026-10-04 测试 #6：全部退化 1s 的回归曾发生）。
func TestBackoffDelayCurve(t *testing.T) {
	cases := []struct {
		fails int
		want  time.Duration
	}{
		{1, time.Second}, {2, 2 * time.Second}, {3, 4 * time.Second},
		{4, 8 * time.Second}, {5, 16 * time.Second}, {6, 32 * time.Second},
		{7, backoffCap}, {8, backoffCap}, {100, backoffCap},
		{0, time.Second}, {-5, time.Second},
	}
	for _, c := range cases {
		if got := backoffDelay(c.fails); got != c.want {
			t.Fatalf("backoffDelay(%d) = %v，应为 %v", c.fails, got, c.want)
		}
	}
}

// TestSupervisorStableRunClearsFails：稳定运行 ≥ stableAfter 清零连败
// ——退避从 1s 重新起步（否则早期偶发连败让后续每次重试都等到封顶）。
// 直接驱动 reap 的稳态分支，不真起进程。
func TestSupervisorStableRunClearsFails(t *testing.T) {
	home := t.TempDir()
	sup := NewSupervisor(home, os.Args[0], nil, nil)
	name := MindChild("t")
	sup.fails[name] = 5
	sup.children[name] = &childProc{name: name, lastOK: time.Now().Add(-stableAfter - time.Minute)}
	sup.reap()
	if got := sup.fails[name]; got != 0 {
		t.Fatalf("稳定运行后连败应清零，得 %d", got)
	}
	// 未达稳定期：连败保持（不清零是常态，清零是奖赏）。
	sup.fails[name] = 3
	sup.children[name].lastOK = time.Now()
	sup.reap()
	if got := sup.fails[name]; got != 3 {
		t.Fatalf("未达稳定期的连败不该被清零，得 %d", got)
	}
}

func TestSupervisorRestartsExitedChild(t *testing.T) {
	t.Setenv("MINDLOOP_SYS_TEST_CHILD", "die")
	home := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable: %v", err)
	}
	cfg := &Config{Version: 1, Identities: map[string]Ident{"t": {Enabled: true, Mind: true}}}
	sup := NewSupervisor(home, exe, cfg, nil)
	sup.spawn = helperSpawn

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- sup.Run(ctx) }()

	// 子进程每次启动即死：退避重启必须把 Restarts 推过 ≥2。
	waitStatus(t, sup, "崩溃重启 ≥2 次", func(st []ChildStatus) bool {
		c, ok := statusOf(st, MindChild("t"))
		return ok && c.Restarts >= 2
	})

	cancel()
	if err := <-runErr; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Run: %v", err)
	}
}

func TestSupervisorRestartAfterDisableEnable(t *testing.T) {
	t.Setenv("MINDLOOP_SYS_TEST_CHILD", "alive")
	home := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable: %v", err)
	}
	on := &Config{Version: 1, Identities: map[string]Ident{"t": {Enabled: true, Mind: true}}}
	sup := NewSupervisor(home, exe, on, nil)
	sup.spawn = helperSpawn

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- sup.Run(ctx) }()

	waitStatus(t, sup, "心智初次拉起", func(st []ChildStatus) bool {
		c, ok := statusOf(st, MindChild("t"))
		return ok && c.Running
	})
	first, _ := statusOf(sup.Status(), MindChild("t"))

	// 停：热加载关掉身份能力 → 孩子被计划内停止（没有重启）。
	off := &Config{Version: 1, Identities: map[string]Ident{"t": {Enabled: false}}}
	if err := Save(home, off); err != nil {
		t.Fatalf("Save(off): %v", err)
	}
	sup.reload()
	waitStatus(t, sup, "心智按计划停止", func(st []ChildStatus) bool {
		c, ok := statusOf(st, MindChild("t"))
		return !ok || !c.Running
	})

	// 启：同一宿主生命周期内必须重新拉起（修复前此步卡死：退出未
	// 检出 → children 条目残留 → Diff 恒认为"已在运行"）。
	if err := Save(home, on); err != nil {
		t.Fatalf("Save(on): %v", err)
	}
	sup.reload()
	waitStatus(t, sup, "心智重新拉起（新 pid）", func(st []ChildStatus) bool {
		c, ok := statusOf(st, MindChild("t"))
		return ok && c.Running && c.PID != first.PID
	})

	cancel()
	if err := <-runErr; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Run: %v", err)
	}
}
