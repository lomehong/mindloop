package cli

import (
	"os"
	"strings"
	"testing"
)

// writeHomeFile 用 os.Root 把测试写入限制在状态根内（路径无法越出
// 根——穿越规则的合规形态，也更安全）。
func writeHomeFile(t *testing.T, home, name, content string) {
	t.Helper()
	root, err := os.OpenRoot(home)
	if err != nil {
		t.Fatalf("打开状态根: %v", err)
	}
	defer root.Close()
	f, err := root.Create(name)
	if err != nil {
		t.Fatalf("写 %s: %v", name, err)
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("写 %s 内容: %v", name, err)
	}
}

// TestDoctorGreenAfterInit：init --demo 之后体检应全绿——demo 模型
// 探针离线即通，viewer 命中任意一级来源都算可用（本机/CI 环境差异
// 不影响结论）。
func TestDoctorGreenAfterInit(t *testing.T) {
	newTestHome(t)
	t.Setenv("MINDLOOP_MODEL", "echo")
	if code, out, errOut := runCLI(t, "init", "--demo", "--yes"); code != 0 {
		t.Fatalf("init exit = %d out=%s err=%s", code, out, errOut)
	}
	code, out, _ := runCLI(t, "doctor")
	if code != 0 {
		t.Fatalf("doctor exit = %d（应全绿）:\n%s", code, out)
	}
	for _, want := range []string{"✓  状态根", "✓  模型", "✓  身份", "全部可用"} {
		if !strings.Contains(out, want) {
			t.Fatalf("体检缺 %q:\n%s", want, out)
		}
	}
}

// TestDoctorFailsWithoutConfig：未配置模型时模型项失败、退出码 1
// ——doctor 的失败必须可被脚本捕获。
func TestDoctorFailsWithoutConfig(t *testing.T) {
	newTestHome(t)
	t.Setenv("MINDLOOP_MODEL", "")
	code, out, _ := runCLI(t, "doctor", "--no-probe")
	if code != 1 {
		t.Fatalf("doctor exit = %d（应为 1）:\n%s", code, out)
	}
	if !strings.Contains(out, "✗  模型") || !strings.Contains(out, "MINDLOOP_MODEL 未配置") {
		t.Fatalf("失败项信息不全:\n%s", out)
	}
	if strings.Contains(out, "全部可用") {
		t.Fatalf("有失败项不应报全部可用:\n%s", out)
	}
}

// TestDoctorProbeSkip：--no-probe 时只报配置形态，不实测。
func TestDoctorProbeSkip(t *testing.T) {
	newTestHome(t)
	t.Setenv("MINDLOOP_MODEL", "echo")
	code, out, _ := runCLI(t, "doctor", "--no-probe")
	if code != 0 {
		t.Fatalf("exit = %d:\n%s", code, out)
	}
	if !strings.Contains(out, "未实测") {
		t.Fatalf("--no-probe 应注明未实测:\n%s", out)
	}
}

// TestDoctorMCPBrokenCommand：mcp.json 里的 stdio 服务器命令不可达
// 应点名并判失败。
func TestDoctorMCPBrokenCommand(t *testing.T) {
	home := newTestHome(t)
	t.Setenv("MINDLOOP_MODEL", "echo")
	writeHomeFile(t, home, "mcp.json",
		`{"mcpServers":{"ghost":{"command":"definitely-not-a-real-binary-xyz","args":["-p"]}}}`)
	code, out, _ := runCLI(t, "doctor", "--no-probe")
	if code != 1 {
		t.Fatalf("exit = %d（应为 1）:\n%s", code, out)
	}
	if !strings.Contains(out, "ghost") || !strings.Contains(out, "不可达") {
		t.Fatalf("应点名不可达的 MCP 服务器:\n%s", out)
	}
}

// TestDoctorTierLabel：viewer 来源分级进入体检输出（本机命中哪级
// 都行，但必须带可读的分级说明）。
func TestDoctorTierLabel(t *testing.T) {
	newTestHome(t)
	t.Setenv("MINDLOOP_MODEL", "echo")
	code, out, _ := runCLI(t, "doctor", "--no-probe")
	if code != 0 {
		t.Fatalf("exit = %d:\n%s", code, out)
	}
	found := strings.Contains(out, "磁盘自动探测") ||
		strings.Contains(out, "内嵌（release 构建）") ||
		strings.Contains(out, "旗标指定") ||
		strings.Contains(out, "环境变量指定")
	if !found {
		t.Fatalf("viewer 行应带来源分级:\n%s", out)
	}
}
