package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// requireBash 在没有【可用】bash 的环境干净跳过（CI 与开发机都有
// Git Bash）。BashPath 内部做真实执行探测（bash -c true），所以这
// 里天然是能力语义：PATH 里的 WSL 存根不会被当成可用 bash——存根
// 会 LookPath 命中却执行不了任何脚本。
func requireBash(t *testing.T) {
	t.Helper()
	if _, err := BashPath(); err != nil {
		t.Skipf("跳过： %v", err)
	}
}

// TestBashPathReturnsWorkingBash 钉死 BashPath 的能力语义：返回的
// 路径必须真的能执行脚本，且绝不是 system32 的 WSL 存根。修复前本
// 仓库开发机上它返回的是 WSL 存根——PATH 命中即短路，fallback 永远
// 轮不到，整条执行链静默失效（每次唤醒双倍 LLM 调用）。
func TestBashPathReturnsWorkingBash(t *testing.T) {
	p, err := BashPath()
	if err != nil {
		t.Skipf("跳过： %v", err)
	}
	if isWSLStub(p) {
		t.Fatalf("BashPath 返回了 system32 的 WSL 存根: %s", p)
	}
	if !bashWorks(p) {
		t.Fatalf("BashPath 返回的 bash 探测失败: %s", p)
	}
}

// TestRunScrubsSensitiveEnv：沙箱子进程环境不得携带模型 key 与 web
// token——脚本一行 env 就能把密钥倒带出机器（密钥暴露缺陷：模型
// 生成的脚本原本可见全部进程环境）；扩展通道是显式键名
// （MINDLOOP_SANDBOX_ENV）与显式值（req.Env）。
func TestRunScrubsSensitiveEnv(t *testing.T) {
	requireBash(t)
	dir := newWorkDir(t)
	t.Setenv("MINDLOOP_API_KEY", "sk-mindloop-secret")
	t.Setenv("ANTHROPIC_API_KEY", "sk-anthropic-secret")
	t.Setenv("ACME_API_KEY", "sk-acme-secret")
	t.Setenv("MINDLOOP_WEB_TOKEN", "web-token-secret")
	t.Setenv("MINDLOOP_KEEPME", "keepme-ok")
	t.Setenv("MINDLOOP_SANDBOX_ENV", "mindloop_keepme") // 大小写变体也要点名得上
	res, err := Run(context.Background(), Request{
		Dir:    dir,
		Script: `echo "ml=[$MINDLOOP_API_KEY] an=[$ANTHROPIC_API_KEY] ac=[$ACME_API_KEY] web=[$MINDLOOP_WEB_TOKEN] keep=[$MINDLOOP_KEEPME] extra=[$MINDLOOP_REQEXTRA]"`,
		Env:    []string{"MINDLOOP_REQEXTRA=via-req"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, banned := range []string{"sk-mindloop-secret", "sk-anthropic-secret", "sk-acme-secret", "web-token-secret"} {
		if strings.Contains(res.Stdout, banned) {
			t.Fatalf("敏感值 %q 泄漏进沙箱环境: %q", banned, res.Stdout)
		}
	}
	if !strings.Contains(res.Stdout, "keep=[keepme-ok]") {
		t.Fatalf("显式允许的扩展键应继承: %q", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "extra=[via-req]") {
		t.Fatalf("显式值应下传: %q", res.Stdout)
	}
}

// TestRunEnvIsWhitelist：父环境默认是白名单——未被
// MINDLOOP_SANDBOX_ENV 点名的变量不下传（脚本看不到无关进程环境）。
func TestRunEnvIsWhitelist(t *testing.T) {
	requireBash(t)
	dir := newWorkDir(t)
	t.Setenv("MINDLOOP_KEEPME", "keepme-ok")
	t.Setenv("MINDLOOP_SANDBOX_ENV", "")
	res, err := Run(context.Background(), Request{
		Dir:    dir,
		Script: `echo "keep=[$MINDLOOP_KEEPME]"`,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(res.Stdout, "keepme-ok") {
		t.Fatalf("白名单外的变量不应下传: %q", res.Stdout)
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
// 仅 Windows：杀树由 Job Object 提供（README 的平台对照表）——
// Linux/macOS 的沙箱降级为进程级终止，孤儿语义不做保证。
func TestRunKillsProcessTree(t *testing.T) {
	requireBash(t)
	if runtime.GOOS != "windows" {
		t.Skip("杀树语义依赖 Job Object，仅 Windows 保证（非 Windows 降级为进程级终止）")
	}
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
