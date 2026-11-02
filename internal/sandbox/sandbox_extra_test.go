package sandbox

import (
	"testing"
)

// TestBashPathPassesSafeBashPath：BashPath 的产出必须通过收口校验
// （安全属性钉子）。真实可执行性由沙箱主路径的测试覆盖（如
// schedule 的 exec 放行用例真跑 bash）——本用例不 spawn 进程：
// Mimosa 引擎对「函数返回值进 Path 位」过度归因（台账 B4），台账
// 预先批准的整改方向即改为纯校验验证（2026-11-02 执行）。
func TestBashPathPassesSafeBashPath(t *testing.T) {
	p, err := BashPath()
	if err != nil {
		t.Skipf("BashPath 在本机找不到 bash: %v", err)
	}
	if !safeBashPath(p) {
		t.Fatalf("BashPath 返回的路径未通过收口校验: %q", p)
	}
}
