package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"mindloop/internal/identity"
	"mindloop/internal/traj"
)

// syncBuf 是并发安全的输出缓冲：命令在 goroutine 里跑、测试在主
// goroutine 读——裸 bytes.Buffer 在 -race 下会报数据竞争。
type syncBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// waitForCLI 轮询条件；命令提前退出视为失败并带出诊断。
func waitForCLI(t *testing.T, what string, done <-chan int, errBuf *syncBuf, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		select {
		case code := <-done:
			t.Fatalf("%s 前命令已退出（exit %d）: %s", what, code, errBuf.String())
		default:
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("等待超时: %s（诊断: %s）", what, errBuf.String())
}

func newCLIIdentity(t *testing.T, name string) *identity.Identity {
	t.Helper()
	newTestHome(t)
	t.Setenv("MINDLOOP_MODEL", "echo")
	t.Setenv("MINDLOOP_STREAM", "0")
	if code, _, errOut := runCLI(t, "identity", "create", name); code != 0 {
		t.Fatalf("identity create: %d %s", code, errOut)
	}
	id, err := identity.Load(name)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// TestMindRunStartsWithOwnedLockAndStopsCleanly 钉死 CLI 的持锁启动
// 接线：monolith 是持久思考者，调度器只接受 RunOwned；mind run 必须
// 真实进入调度循环（启动日志可证），取消后以 exit 0 干净退出。
func TestMindRunStartsWithOwnedLockAndStopsCleanly(t *testing.T) {
	newCLIIdentity(t, "cli-mind")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var errBuf syncBuf
	done := make(chan int, 1)
	go func() { done <- Execute(ctx, []string{"mind", "run", "cli-mind"}, io.Discard, &errBuf) }()
	waitForCLI(t, "调度器启动", done, &errBuf, func() bool {
		return strings.Contains(errBuf.String(), "调度器启动")
	})
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("取消后退出码 = %d: %s", code, errBuf.String())
		}
	case <-time.After(20 * time.Second):
		t.Fatalf("取消后未退出: %s", errBuf.String())
	}
}

// TestChatTakeoverRunsDispatcherAndReplies 钉死 chat 接管模式同样走
// 持锁启动：echo 假模型下投递一条消息必须得到回复（证明调度器真的
// 在跑），/exit 干净退出（exit 0）。
func TestChatTakeoverRunsDispatcherAndReplies(t *testing.T) {
	id := newCLIIdentity(t, "cli-chat")
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldStdin := os.Stdin
	os.Stdin = r
	// 恢复必须发生在命令完全退出之后（bufio.Scanner 持有 os.Stdin）。
	defer func() { os.Stdin = oldStdin; w.Close(); r.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var errBuf syncBuf
	done := make(chan int, 1)
	go func() { done <- Execute(ctx, []string{"chat", "cli-chat"}, io.Discard, &errBuf) }()
	waitForCLI(t, "chat 启动横幅", done, &errBuf, func() bool {
		return strings.Contains(errBuf.String(), "已启动")
	})
	if _, err := io.WriteString(w, "你好\n"); err != nil {
		t.Fatal(err)
	}
	waitForCLI(t, "回复落盘", done, &errBuf, func() bool {
		steps, err := id.Timeline.Steps()
		if err != nil {
			return false
		}
		replied := map[string]bool{}
		var lastOperator string
		for _, s := range steps {
			if s.Type != traj.TypeMessage {
				continue
			}
			if rt, ok := s.Field("reply_to"); ok {
				replied[rt] = true
			}
			if from, _ := s.Field("from"); from == "operator" {
				lastOperator = s.StepID
			}
		}
		return lastOperator != "" && replied[lastOperator]
	})
	if _, err := io.WriteString(w, "/exit\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("chat 退出码 = %d: %s", code, errBuf.String())
		}
	case <-time.After(20 * time.Second):
		t.Fatalf("chat 未退出: %s", errBuf.String())
	}
}

// TestChatSurfacesDispatcherStartupFailure 钉死失败可见：恢复失败时
// 调度器不会启动，但 chat 必须把退出原因摆到屏幕上（不能静默吞掉），
// 并继续允许 /exit 干净退出。
func TestChatSurfacesDispatcherStartupFailure(t *testing.T) {
	id := newCLIIdentity(t, "cli-broken")
	// 未知协议版本的任务事实 → 严格投影失败关闭 → 恢复必败。
	corrupt := `{"type":"message","step_id":"11111111-1111-4111-8111-111111111111","ts":"2026-09-25T00:00:00.000Z","protocol_version":999,"message_kind":"task","task_id":"11111111-1111-4111-8111-111111111111","from":"operator","to":"cli-broken","content":"未知协议"}` + "\n"
	f, err := os.OpenFile(id.Timeline.Path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(corrupt); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = oldStdin; w.Close(); r.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var errBuf syncBuf
	done := make(chan int, 1)
	go func() { done <- Execute(ctx, []string{"chat", "cli-broken"}, io.Discard, &errBuf) }()
	waitForCLI(t, "恢复失败警告", done, &errBuf, func() bool {
		return strings.Contains(errBuf.String(), "心智调度器已退出")
	})
	if _, err := io.WriteString(w, "/exit\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("chat 退出码 = %d: %s", code, errBuf.String())
		}
	case <-time.After(20 * time.Second):
		t.Fatalf("chat 未退出: %s", errBuf.String())
	}
}
