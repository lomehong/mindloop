package sandbox

import (
	"os/exec"
	"strings"
	"testing"
)

func TestBashPathResolvesInRealWindows(t *testing.T) {
	p, err := BashPath()
	if err != nil {
		t.Skipf("BashPath 在本机找不到 bash: %v", err)
	}
	if !safeBashPath(p) {
		t.Fatalf("BashPath 返回的路径未通过收口校验: %q", p)
	}
	cmd := &exec.Cmd{Path: p, Args: []string{p, "-c", "echo ok"}}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bash 实际可执行失败: %v, %s", err, out)
	}
	if !strings.Contains(string(out), "ok") {
		t.Fatalf("bash 输出异常: %q", out)
	}
}
