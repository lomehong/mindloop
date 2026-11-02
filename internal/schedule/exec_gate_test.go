package schedule

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// exec 审批门三态测试（2026-11-02 §7.0 路径②修复的钉子）：
// nil 门 fail-closed、门拒绝、门放行后到达沙箱层。前两态不碰 bash
// （拒绝发生在 sandbox.Run 之前）；放行态依赖真实 bash，缺失时诚实
// 降级 skip。

// gateParsed 解析一条 exec 条目供 ExecuteNow 使用。
func gateParsed(t *testing.T) Parsed {
	t.Helper()
	parsed, _, err := Parse([]byte(`{"items":[{"id":"g","every":"2m","exec":"echo hi"}]}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(parsed) != 1 {
		t.Fatalf("应解析出 1 条，得 %d", len(parsed))
	}
	return parsed[0]
}

// TestExecuteSandboxNoGateFailClosed：不接审批门（BeforeExecute=nil）
// 且未注入 mock 执行器时，默认沙箱执行器必须拒绝——exec 的执行授权
// 不可静默放行，装配层忘接线是显式失败不是默认放行。
func TestExecuteSandboxNoGateFailClosed(t *testing.T) {
	rt := New(Options{Dir: t.TempDir()})
	out := rt.ExecuteNow(context.Background(), gateParsed(t))
	if !errors.Is(out.Err, ErrNoGate) {
		t.Fatalf("无门应 ErrNoGate fail-closed，得 %v", out.Err)
	}
}

// TestExecuteSandboxGateReject：门返回错误时执行被拦在沙箱之前，
// 门收到的是条目脚本原文（审计链起点）。
func TestExecuteSandboxGateReject(t *testing.T) {
	var gotScript string
	rt := New(Options{
		Dir: t.TempDir(),
		BeforeExecute: func(_ context.Context, script string) error {
			gotScript = script
			return errors.New("需审批：外发类")
		},
	})
	out := rt.ExecuteNow(context.Background(), gateParsed(t))
	if out.Err == nil || !strings.Contains(out.Err.Error(), "审批门拒绝") {
		t.Fatalf("应被门拒绝，得 %v", out.Err)
	}
	if gotScript != "echo hi" {
		t.Fatalf("门应收到脚本原文，得 %q", gotScript)
	}
}

// TestExecuteSandboxGatePassRuns：门放行后命令真实执行（echo hi 经
// bash）。无 bash 环境如实 skip——放行路径的沙箱行为由 sandbox 包
// 自身的测试覆盖。
func TestExecuteSandboxGatePassRuns(t *testing.T) {
	rt := New(Options{
		Dir:           t.TempDir(),
		BeforeExecute: func(context.Context, string) error { return nil },
	})
	out := rt.ExecuteNow(context.Background(), gateParsed(t))
	if out.Err != nil {
		if strings.Contains(out.Err.Error(), "bash") {
			t.Skipf("无 bash 环境: %v", out.Err)
		}
		t.Fatalf("门放行后应执行成功，得 %v（detail: %s）", out.Err, out.Detail)
	}
	if strings.TrimSpace(out.Output) != "hi" {
		t.Fatalf("echo hi 输出应含 hi，得 %q", out.Output)
	}
}
