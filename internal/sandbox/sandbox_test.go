package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// requireBash 在没有 bash 的环境跳过（CI 与开发机都有 Git Bash）。
func requireBash(t *testing.T) {
	t.Helper()
	if _, err := BashPath(); err != nil {
		t.Skipf("跳过： %v", err)
	}
}

func newWorkDir(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

func TestRunCapturesOutputAndExitCode(t *testing.T) {
	requireBash(t)
	dir := newWorkDir(t)
	res, err := Run(context.Background(), Request{
		Dir:    dir,
		Script: "echo hello-mindloop\necho oops >&2\nexit 3",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(res.Stdout, "hello-mindloop") {
		t.Fatalf("stdout = %q", res.Stdout)
	}
	if !strings.Contains(res.Stderr, "oops") {
		t.Fatalf("stderr = %q", res.Stderr)
	}
	if res.ExitCode != 3 {
		t.Fatalf("exit = %d，应为 3", res.ExitCode)
	}
	if res.KillReason != "" {
		t.Fatalf("KillReason = %q，应为空", res.KillReason)
	}
}

func TestRunFinalProtocol(t *testing.T) {
	requireBash(t)
	dir := newWorkDir(t)
	finalPath := filepath.Join(dir, ".mindloop_final")
	res, err := Run(context.Background(), Request{
		Dir:       dir,
		Script:    "echo working\nFINAL=\"任务完成\"",
		FinalPath: finalPath,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.FinalSet {
		t.Fatal("FINAL 已设置但哨兵文件未被识别")
	}
	if b, err := os.ReadFile(finalPath); err != nil || string(b) != "任务完成" {
		t.Fatalf("哨兵内容 = %q, %v", b, err)
	}

	// 失败的脚本不产生 FINAL（set -e 下协议尾不执行）。
	if err := os.Remove(finalPath); err != nil {
		t.Fatal(err)
	}
	res, err = Run(context.Background(), Request{
		Dir:       dir,
		Script:    "false\nFINAL=\"不该出现\"",
		FinalPath: finalPath,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.FinalSet {
		t.Fatal("失败脚本的 FINAL 不应生效")
	}
}

func TestRunIdleKill(t *testing.T) {
	requireBash(t)
	dir := newWorkDir(t)
	start := time.Now()
	res, err := Run(context.Background(), Request{
		Dir:         dir,
		Script:      "echo start\nsleep 30",
		IdleTimeout: 400 * time.Millisecond,
		Timeout:     20 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.KillReason != KillIdle {
		t.Fatalf("KillReason = %q，应为 idle", res.KillReason)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("空闲杀树耗时 %v；看门狗失效", elapsed)
	}
}

func TestRunOutputCapKill(t *testing.T) {
	requireBash(t)
	dir := newWorkDir(t)
	res, err := Run(context.Background(), Request{
		Dir:            dir,
		Script:         "i=0\nwhile [ $i -lt 200000 ]; do echo aaaaaaaaaaaaaaaaaaaa; i=$((i+1)); done",
		MaxOutputBytes: 20000,
		Timeout:        30 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.KillReason != KillOutput {
		t.Fatalf("KillReason = %q，应为 output", res.KillReason)
	}
	if int64(len(res.Stdout)) > 40000 {
		t.Fatalf("保留的 stdout %d 字节，应被截在限额附近", len(res.Stdout))
	}
}

func TestRunTotalTimeoutKill(t *testing.T) {
	requireBash(t)
	dir := newWorkDir(t)
	start := time.Now()
	res, err := Run(context.Background(), Request{
		Dir:         dir,
		Script:      "sleep 30",
		IdleTimeout: 25 * time.Second, // 高于 Timeout：让总时长先到
		Timeout:     500 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.KillReason != KillTimeout {
		t.Fatalf("KillReason = %q，应为 timeout", res.KillReason)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("超时杀树耗时 %v", elapsed)
	}
}

func TestRunContextCancel(t *testing.T) {
	requireBash(t)
	dir := newWorkDir(t)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(300 * time.Millisecond)
		cancel()
	}()
	res, err := Run(ctx, Request{
		Dir:         dir,
		Script:      "sleep 30",
		IdleTimeout: 20 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.KillReason != KillCanceled {
		t.Fatalf("KillReason = %q，应为 canceled", res.KillReason)
	}
}

// TestRunKillsProcessTree 是 Job Object 的关键断言：脚本后台拉起
// 的子进程（甚至脱离脚本的孤儿）必须随脚本一起死。用产物文件
// 证明：子进程每秒续写一个文件，杀树后文件必须停止增长。
func TestRunKillsProcessTree(t *testing.T) {
	requireBash(t)
	dir := newWorkDir(t)
	marker := filepath.Join(dir, "heartbeat")
	res, err := Run(context.Background(), Request{
		Dir: dir,
		Script: "echo boot\n" +
			"(while true; do echo tick >> \"$0_heartbeat_placeholder\" 2>/dev/null; echo tick >> '" + strings.ReplaceAll(marker, "\\", "/") + "'; sleep 1; done) &\n" +
			"sleep 30",
		IdleTimeout: 600 * time.Millisecond,
		Timeout:     20 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.KillReason != KillIdle {
		t.Fatalf("KillReason = %q，应为 idle", res.KillReason)
	}
	size1 := heartbeatSize(marker)
	time.Sleep(2500 * time.Millisecond)
	size2 := heartbeatSize(marker)
	if size2 != size1 {
		t.Fatalf("杀树后孤儿进程仍在写心跳（%d → %d 字节）；Job Object 未管辖子进程", size1, size2)
	}
}

func heartbeatSize(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		return -1
	}
	return fi.Size()
}
