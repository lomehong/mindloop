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
	out, err := exec.Command(p, "-c", "echo ok").CombinedOutput()
	if err != nil {
		t.Fatalf("bash 实际可执行失败: %v, %s", err, out)
	}
	if !strings.Contains(string(out), "ok") {
		t.Fatalf("bash 输出异常: %q", out)
	}
}
