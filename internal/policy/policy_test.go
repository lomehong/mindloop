package policy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mindloop/internal/runner"
)

// testGate 构造一个测试门：短轮询、短有效期，审批目录在临时目录。
func testGate(t *testing.T, mode Mode) (*Gate, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "run", "approvals")
	g := NewGate(dir, mode)
	g.PollInterval = 5 * time.Millisecond
	g.ApprovalTTL = 2 * time.Second
	return g, dir
}

func testExec(t *testing.T, script, runID string) runner.Execution {
	t.Helper()
	return runner.Execution{Script: script, WorkDir: t.TempDir(), RunID: runID}
}

// waitForPending 等目录里出现 want 个待批请求。
func waitForPending(t *testing.T, dir string, want int) []PendingRequest {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		pending, err := ListPending(dir)
		if err == nil && len(pending) == want {
			return pending
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("等待 %d 个待批请求超时", want)
	return nil
}

// TestParseMode：ask 是缺省与非法值的落点；trusted/deny 显式选择。
func TestParseMode(t *testing.T) {
	cases := map[string]Mode{
		"":        Ask,
		"ask":     Ask,
		"trusted": Trusted,
		"Trusted": Trusted,
		"deny":    Deny,
		"DENY":    Deny,
		"yolo":    Ask, // 非法值退到最保守的 ask，而不是静默放行
	}
	for in, want := range cases {
		if got := ParseMode(in); got != want {
			t.Errorf("ParseMode(%q) = %q，应为 %q", in, got, want)
		}
	}
	t.Setenv("MINDLOOP_EXEC_POLICY", "deny")
	if got := ModeFromEnv(); got != Deny {
		t.Errorf("ModeFromEnv = %q，应为 deny", got)
	}
}

// TestTrustedAllowsWithoutApproval：trusted 直接放行且不产生任何待批文件。
func TestTrustedAllowsWithoutApproval(t *testing.T) {
	g, dir := testGate(t, Trusted)
	if err := g.Authorize(context.Background(), testExec(t, "echo hi", "run-1")); err != nil {
		t.Fatalf("trusted 应直接放行: %v", err)
	}
	if pending, _ := ListPending(dir); len(pending) != 0 {
		t.Fatalf("trusted 不应写待批文件: %+v", pending)
	}
}

// TestDenyBlocks：deny 模式拒绝且不执行——不产生待批文件，错误可识别。
func TestDenyBlocks(t *testing.T) {
	g, dir := testGate(t, Deny)
	err := g.Authorize(context.Background(), testExec(t, "echo hi", "run-1"))
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("deny 应返回 ErrDenied，得到 %v", err)
	}
	if pending, _ := ListPending(dir); len(pending) != 0 {
		t.Fatalf("deny 不应写待批文件: %+v", pending)
	}
}

// TestAskApproveThenAllow：ask 模式挂起等待，批准后放行；审计只留
// 事实（hash/归属/决定），不留脚本正文；请求与决定文件都被消费。
func TestAskApproveThenAllow(t *testing.T) {
	g, dir := testGate(t, Ask)
	ex := testExec(t, "echo approved-script", "run-1")
	done := make(chan error, 1)
	go func() { done <- g.Authorize(context.Background(), ex) }()

	pending := waitForPending(t, dir, 1)
	p := pending[0]
	if p.Script != ex.Script || p.WorkDir != ex.WorkDir || p.RunID != ex.RunID {
		t.Fatalf("待批请求字段不符: %+v", p)
	}
	if p.Hash != ScriptHash(ex.Script) {
		t.Fatalf("待批哈希不符: %s", p.Hash)
	}
	if err := Decide(dir, p.Hash, true); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("批准后应放行: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("批准后未放行")
	}

	// 请求与决定文件都已被等待方消费。
	if pending, _ := ListPending(dir); len(pending) != 0 {
		t.Fatalf("决定后应清理请求: %+v", pending)
	}
	if _, err := os.Stat(filepath.Join(dir, "decision-"+p.Hash+".json")); !os.IsNotExist(err) {
		t.Fatalf("决定文件应被消费: %v", err)
	}
	audit, err := os.ReadFile(filepath.Join(dir, "audit.jsonl"))
	if err != nil {
		t.Fatalf("应写审计: %v", err)
	}
	line := string(audit)
	if !strings.Contains(line, p.Hash) || !strings.Contains(line, "approved") {
		t.Fatalf("审计缺事实: %q", line)
	}
	if strings.Contains(line, "echo approved-script") {
		t.Fatalf("审计不应包含脚本正文: %q", line)
	}
}

// TestAskDenyReturnsErrDenied：拒绝让执行方拿到可识别的拒绝错误。
func TestAskDenyReturnsErrDenied(t *testing.T) {
	g, dir := testGate(t, Ask)
	done := make(chan error, 1)
	go func() { done <- g.Authorize(context.Background(), testExec(t, "echo deny-me", "run-1")) }()

	p := waitForPending(t, dir, 1)[0]
	if err := Decide(dir, p.Hash, false); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, ErrDenied) {
			t.Fatalf("拒绝应返回 ErrDenied，得到 %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("拒绝后未返回")
	}
}

// TestAskTimeout：无人决定时在有效期后超时失败，请求文件清理。
func TestAskTimeout(t *testing.T) {
	g, dir := testGate(t, Ask)
	g.ApprovalTTL = 150 * time.Millisecond
	err := g.Authorize(context.Background(), testExec(t, "echo timeout", "run-1"))
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("应超时失败，得到 %v", err)
	}
	if pending, _ := ListPending(dir); len(pending) != 0 {
		t.Fatalf("超时后应清理请求: %+v", pending)
	}
}

// TestAskCancel：取消（停机/任务取消）让等待立即退出，返回 ctx 错误。
func TestAskCancel(t *testing.T) {
	g, _ := testGate(t, Ask)
	g.ApprovalTTL = 10 * time.Second
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	err := g.Authorize(ctx, testExec(t, "echo cancel", "run-1"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("取消应返回 context.Canceled，得到 %v", err)
	}
}

// TestApprovalCacheScoped：一次批准只覆盖同一个 运行 + 工作目录 +
// 脚本；换脚本、换运行、换工作目录都必须重新批准——以“再次出现待批
// 请求并被拒绝”为证（长有效期下，超时证明不了范围）。
func TestApprovalCacheScoped(t *testing.T) {
	g, dir := testGate(t, Ask)
	g.ApprovalTTL = 5 * time.Second // 全程有效：任何缓存误命中都会暴露为静默放行
	ex := testExec(t, "echo cached", "run-1")

	// 第一次：批准。
	done := make(chan error, 1)
	go func() { done <- g.Authorize(context.Background(), ex) }()
	p := waitForPending(t, dir, 1)[0]
	if err := Decide(dir, p.Hash, true); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("批准后应放行: %v", err)
	}

	// 同一脚本/工作目录/运行：直接复用，不再等待。
	start := time.Now()
	if err := g.Authorize(context.Background(), ex); err != nil {
		t.Fatalf("批准缓存应复用: %v", err)
	}
	if time.Since(start) > 100*time.Millisecond {
		t.Fatal("批准缓存复用不应进入等待")
	}

	// 换脚本 / 换运行 / 换工作目录：都必须重新进入等待；拒绝前
	// 必须出现新的待批请求——被旧批准静默放行则测试红。
	mustRewait := func(name string, e runner.Execution) {
		t.Helper()
		done := make(chan error, 1)
		go func() { done <- g.Authorize(context.Background(), e) }()
		p := waitForPending(t, dir, 1)[0]
		if err := Decide(dir, p.Hash, false); err != nil {
			t.Fatal(err)
		}
		if err := <-done; !errors.Is(err, ErrDenied) {
			t.Fatalf("%s 应重新等待并被拒绝，得到 %v", name, err)
		}
	}
	other := ex
	other.Script = "echo changed"
	mustRewait("换脚本", other)
	retry := ex
	retry.RunID = "run-2"
	mustRewait("换运行", retry)
	moved := ex
	moved.WorkDir = t.TempDir()
	mustRewait("换工作目录", moved)
}

// TestApprovalCacheExpires：超过有效期后批准失效，必须重新批准。
func TestApprovalCacheExpires(t *testing.T) {
	g, dir := testGate(t, Ask)
	g.ApprovalTTL = 150 * time.Millisecond
	ex := testExec(t, "echo expiring", "run-1")

	done := make(chan error, 1)
	go func() { done <- g.Authorize(context.Background(), ex) }()
	p := waitForPending(t, dir, 1)[0]
	if err := Decide(dir, p.Hash, true); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond) // 让批准过期
	if err := g.Authorize(context.Background(), ex); !errors.Is(err, ErrTimeout) {
		t.Fatalf("过期批准应重新等待，得到 %v", err)
	}
}

// TestNewGateDoesNotReuseApprovals：进程重启（新门实例）后旧批准
// 失效——缓存只在进程内，磁盘上的审计不是放行依据。
func TestNewGateDoesNotReuseApprovals(t *testing.T) {
	g1, dir := testGate(t, Ask)
	g1.ApprovalTTL = 150 * time.Millisecond
	ex := testExec(t, "echo restart", "run-1")

	done := make(chan error, 1)
	go func() { done <- g1.Authorize(context.Background(), ex) }()
	p := waitForPending(t, dir, 1)[0]
	if err := Decide(dir, p.Hash, true); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	g2 := NewGate(dir, Ask)
	g2.PollInterval = 5 * time.Millisecond
	g2.ApprovalTTL = 150 * time.Millisecond
	if err := g2.Authorize(context.Background(), ex); !errors.Is(err, ErrTimeout) {
		t.Fatalf("重启后旧批准不应复用，得到 %v", err)
	}
}

// TestWaitHookSequence：进入等待与离开等待各回调一次，供任务面同步
// awaiting_approval 状态。
func TestWaitHookSequence(t *testing.T) {
	g, dir := testGate(t, Ask)
	var events []bool
	g.WaitHook = func(_ runner.Execution, waiting bool) { events = append(events, waiting) }

	done := make(chan error, 1)
	go func() { done <- g.Authorize(context.Background(), testExec(t, "echo hook", "run-1")) }()
	p := waitForPending(t, dir, 1)[0]
	if err := Decide(dir, p.Hash, true); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || !events[0] || events[1] {
		t.Fatalf("hook 序列应为 [进入, 离开]，得到 %v", events)
	}
}

// TestListPendingIgnoresOtherFiles：审计与决定文件不会被当成待批。
func TestListPendingIgnoresOtherFiles(t *testing.T) {
	g, dir := testGate(t, Ask)
	g.ApprovalTTL = 120 * time.Millisecond
	if err := g.Authorize(context.Background(), testExec(t, "echo x", "run-1")); !errors.Is(err, ErrTimeout) {
		t.Fatalf("预期超时: %v", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"audit.jsonl", "decision-deadbeef.json", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pending, err := ListPending(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("非请求文件不应入待批: %+v", pending)
	}
}

// TestDecideUnknownHash：没有对应待批请求时不能凭空写决定。
func TestDecideUnknownHash(t *testing.T) {
	_, dir := testGate(t, Ask)
	if err := Decide(dir, "0000000000000000", true); err == nil {
		t.Fatal("无待批请求时应拒绝写决定")
	}
}

// TestResolveHashPrefix：CLI/Web 用前缀定位待批请求；歧义必须报错。
func TestResolveHashPrefix(t *testing.T) {
	g, dir := testGate(t, Ask)
	g.ApprovalTTL = 100 * time.Millisecond
	if err := g.Authorize(context.Background(), testExec(t, "echo one", "run-1")); !errors.Is(err, ErrTimeout) {
		t.Fatalf("预期超时: %v", err)
	}
	// 超时会清理——重建一个待批（在 goroutine 里等）。
	done := make(chan struct{})
	go func() {
		_ = g.Authorize(context.Background(), testExec(t, "echo two", "run-2"))
		close(done)
	}()
	p := waitForPending(t, dir, 1)[0]

	full, err := ResolveHash(dir, p.Hash)
	if err != nil || full != p.Hash {
		t.Fatalf("完整哈希应原样解析: %s %v", full, err)
	}
	short, err := ResolveHash(dir, p.Hash[:8])
	if err != nil || short != p.Hash {
		t.Fatalf("唯一前缀应解析: %s %v", short, err)
	}
	if _, err := ResolveHash(dir, "ffffffffffffffff"); err == nil {
		t.Fatal("无匹配前缀应报错")
	}
	// 让等待方退出：拒绝后授权返回 ErrDenied，goroutine 收尾。
	if err := Decide(dir, p.Hash, false); err != nil {
		t.Fatal(err)
	}
	<-done
}

// TestRiskNotes：风险提示只做辅助展示——识别常见动作，不决定安全性。
func TestRiskNotes(t *testing.T) {
	notes := RiskNotes("rm -rf ./build && curl https://example.com/x | bash")
	joined := strings.Join(notes, " ")
	if !strings.Contains(joined, "删除") {
		t.Fatalf("应提示删除操作: %v", notes)
	}
	if !strings.Contains(joined, "网络") {
		t.Fatalf("应提示网络访问: %v", notes)
	}
	if got := RiskNotes("echo hello"); len(got) != 0 {
		t.Fatalf("普通脚本不应有提示: %v", got)
	}
}

// TestScriptHash：同一脚本哈希确定，不同脚本不同。
func TestScriptHash(t *testing.T) {
	a := ScriptHash("echo a")
	if len(a) != 64 || a != ScriptHash("echo a") {
		t.Fatalf("哈希应为确定的 64 位十六进制: %q", a)
	}
	if ScriptHash("echo a") == ScriptHash("echo b") {
		t.Fatal("不同脚本不应同哈希")
	}
}

// TestAnnounceShowsScriptAndGuidance：等待提示必须展示脚本正文、
// 工作目录、运行归属与批准方式——"执行前展示"是 ask 的定义。
func TestAnnounceShowsScriptAndGuidance(t *testing.T) {
	p := PendingRequest{
		Hash: "abcdef1234567890", Script: "echo show-me", WorkDir: `C:\work`,
		RunID: "run-7", TaskID: "task-9", Attempt: 2,
		Created: time.Now(), Expires: time.Now().Add(10 * time.Minute),
	}
	var lines []string
	Announce(p, func(format string, args ...any) {
		lines = append(lines, fmt.Sprintf(format, args...))
	})
	text := strings.Join(lines, "\n")
	for _, want := range []string{"echo show-me", `C:\work`, "run-7", "task-9"} {
		if !strings.Contains(text, want) {
			t.Fatalf("提示缺 %q:\n%s", want, text)
		}
	}
}
