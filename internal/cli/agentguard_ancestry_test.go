package cli

import (
	"os"
	"testing"
)

// withFakeChain 注入假祖先链与自身 exe 名，测完还原。
func withFakeChain(t *testing.T, self string, chain []string) {
	t.Helper()
	oldChain, oldSelf := procChainNames, selfExeBase
	procChainNames = func() []string { return chain }
	selfExeBase = func() string { return self }
	t.Cleanup(func() { procChainNames, selfExeBase = oldChain, oldSelf })
}

func TestAncestryHits(t *testing.T) {
	withFakeChain(t, "mindloop.exe", []string{"bash.exe", "mindloop.exe", "explorer.exe"})
	if !ancestryHitsMindloop() {
		t.Error("链上有 mindloop.exe 应命中")
	}
}

func TestAncestryMiss(t *testing.T) {
	withFakeChain(t, "mindloop.exe", []string{"bash.exe", "windowsterminal.exe", "explorer.exe"})
	if ancestryHitsMindloop() {
		t.Error("操作员链不该命中")
	}
}

func TestAncestryEmptyChainFallsBackToEnv(t *testing.T) {
	// 探测失败（nil 链）→ 第二信号缺席——env 信号独立生效。
	withFakeChain(t, "mindloop.exe", nil)
	t.Setenv("MINDLOOP_RUN_ID", "run-x")
	if !agentSandboxed() {
		t.Error("env 信号独立生效")
	}
	os.Unsetenv("MINDLOOP_RUN_ID")
	if agentSandboxed() {
		t.Error("双缺席时不该判沙箱")
	}
}

func TestAncestryCaseInsensitive(t *testing.T) {
	// Linux comm 是 "mindloop"，Windows 快照小写归一——EqualFold 双保险。
	withFakeChain(t, "mindloop.exe", []string{"bash", "MINDLOOP.EXE"})
	if !ancestryHitsMindloop() {
		t.Error("大小写不敏感应命中")
	}
}
