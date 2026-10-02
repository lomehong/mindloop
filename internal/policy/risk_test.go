package policy

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAskAutoApprovesReadOnly：ask 门 + 自动放行档——只读脚本免审批
// 直接通过、不产生待批文件、审计落 auto-readonly；非只读脚本照旧
// 进等待。
func TestAskAutoApprovesReadOnly(t *testing.T) {
	g, dir := testGate(t, Ask)
	g.AutoReadOnly = true

	readOnly := testExec(t, "ls -la\ncat notes.md", "run-1")
	if err := g.Authorize(context.Background(), readOnly); err != nil {
		t.Fatalf("只读脚本应自动放行: %v", err)
	}
	if pending, _ := ListPending(dir); len(pending) != 0 {
		t.Fatalf("自动放行不应产生待批请求: %v", pending)
	}
	audit, err := os.ReadFile(filepath.Join(dir, "audit.jsonl"))
	if err != nil {
		t.Fatalf("审计应落盘: %v", err)
	}
	if !strings.Contains(string(audit), `"decision":"auto-readonly"`) {
		t.Fatalf("审计应记 auto-readonly: %s", audit)
	}

	// 非只读照旧等待审批：Authorize 会阻塞到 TTL，挂后台等它把
	// 待批请求写出来再取消。
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- g.Authorize(ctx, testExec(t, "rm -rf /", "run-2")) }()
	waitForPending(t, dir, 1)
	cancel()
	if err := <-errCh; err == nil {
		t.Fatal("取消的等待不应返回 nil")
	}
}

// TestAutoReadOnlyKillSwitch：开关关闭时只读脚本也照旧审批——豁免
// 是可关闭的便利，不是新的缺省信任。
func TestAutoReadOnlyKillSwitch(t *testing.T) {
	g, dir := testGate(t, Ask)
	g.AutoReadOnly = false

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- g.Authorize(ctx, testExec(t, "ls -la", "run-1")) }()
	waitForPending(t, dir, 1)
	cancel()
	if err := <-errCh; err == nil {
		t.Fatal("取消的等待不应返回 nil")
	}
}

// TestAutoReadOnlyFromEnv：显式 "0" 关闭，未设置与其余值开启。
func TestAutoReadOnlyFromEnv(t *testing.T) {
	t.Setenv("MINDLOOP_EXEC_POLICY_AUTO", "")
	if !AutoReadOnlyFromEnv() {
		t.Fatal("未设置应开启")
	}
	t.Setenv("MINDLOOP_EXEC_POLICY_AUTO", "0")
	if AutoReadOnlyFromEnv() {
		t.Fatal("显式 0 应关闭")
	}
	t.Setenv("MINDLOOP_EXEC_POLICY_AUTO", "1")
	if !AutoReadOnlyFromEnv() {
		t.Fatal("1 应开启")
	}
}
