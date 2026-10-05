package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lomehong/mindloop/internal/identity"
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

// TestDoctorTierLabel：viewer 来源分级进入体检输出。来源显式钉在
// "环境变量指定"档（伪造一份已构建产物——index.html 即视为就绪），
// 测试不依赖机器上是否真的构建过前端（CI 的 go job 不构建）。
func TestDoctorTierLabel(t *testing.T) {
	home := newTestHome(t)
	t.Setenv("MINDLOOP_MODEL", "echo")
	distDir := filepath.Join(home, "viewer-dist")
	if err := os.MkdirAll(distDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeHomeFile(t, home, filepath.Join("viewer-dist", "index.html"), "<html>x</html>")
	t.Setenv("MINDLOOP_VIEWER_DIR", distDir)

	code, out, _ := runCLI(t, "doctor", "--no-probe")
	if code != 0 {
		t.Fatalf("exit = %d:\n%s", code, out)
	}
	if !strings.Contains(out, "viewer-dist") || !strings.Contains(out, "环境变量指定") {
		t.Fatalf("viewer 行应报告环境变量指定的来源分级:\n%s", out)
	}
}

// TestDoctorSensors：感官体检（第八项）——无 --identity 给提示；
// 指定身份时列感官清单，不可达观察目标点名警告（配置错误绝不静默：
// "感知没醒"要能诊断）而不是判失败。
func TestDoctorSensors(t *testing.T) {
	home := newTestHome(t)
	t.Setenv("MINDLOOP_MODEL", "echo")
	t.Setenv("MINDLOOP_SPONTANEOUS_TOKENS", "1000")

	code, out, _ := runCLI(t, "doctor", "--no-probe")
	if code != 0 {
		t.Fatalf("exit = %d:\n%s", code, out)
	}
	if !strings.Contains(out, "感知") || !strings.Contains(out, "按身份配置——加 --identity") {
		t.Fatalf("未指定身份应给提示:\n%s", out)
	}

	if _, err := identity.Create(context.Background(), "ada"); err != nil {
		t.Fatal(err)
	}
	writeHomeFile(t, home, filepath.Join("identities", "ada", "sensors.json"), `{"version":1,"sensors":[
		{"id":"web1","type":"web","url":"https://x.example","learning_days":-1},
		{"id":"missing","type":"file","path":"D:/definitely-not-there-xyz","learning_days":-1}
	]}`)

	code, out, _ = runCLI(t, "doctor", "--no-probe", "--identity", "ada")
	if code != 0 {
		t.Fatalf("exit = %d（警告不该判失败）:\n%s", code, out)
	}
	for _, want := range []string{"感知", "web1(web)", "missing（目标不可达"} {
		if !strings.Contains(out, want) {
			t.Fatalf("感官体检缺 %q:\n%s", want, out)
		}
	}

	// 全部禁用：如实报"全部禁用"，不列空清单。
	writeHomeFile(t, home, filepath.Join("identities", "ada", "sensors.json"), `{"version":1,"sensors":[
		{"id":"web1","type":"web","url":"https://x.example","enabled":false}
	]}`)
	code, out, _ = runCLI(t, "doctor", "--no-probe", "--identity", "ada")
	if code != 0 {
		t.Fatalf("exit = %d:\n%s", code, out)
	}
	if !strings.Contains(out, "全部禁用") {
		t.Fatalf("全禁用应如实报出:\n%s", out)
	}
}
