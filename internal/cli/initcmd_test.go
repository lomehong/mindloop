package cli

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mindloop/internal/identity"
)

// readHomeEnv 用 os.Root 把读取限制在状态根内——测试断言 .env 内容
// 的合规形态（也确实更安全：路径无法越出根）。
func readHomeEnv(t *testing.T, home string) string {
	t.Helper()
	root, err := os.OpenRoot(home)
	if err != nil {
		t.Fatalf("打开状态根: %v", err)
	}
	defer root.Close()
	f, err := root.Open(".env")
	if err != nil {
		t.Fatalf("读 .env: %v", err)
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("读 .env 内容: %v", err)
	}
	return string(data)
}

// homeHasEnv 报告状态根下是否有 .env。
func homeHasEnv(t *testing.T, home string) bool {
	t.Helper()
	root, err := os.OpenRoot(home)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	_, err = root.Stat(".env")
	return err == nil
}

// TestInitDemoEndToEnd：--demo --yes 一条龙——.env 写入 echo、身份
// ada 落地、echo 探针离线即通、下一步指引出现。
func TestInitDemoEndToEnd(t *testing.T) {
	home := newTestHome(t)
	code, out, errOut := runCLI(t, "init", "--demo", "--yes")
	if code != 0 {
		t.Fatalf("exit = %d\nout=%s\nerr=%s", code, out, errOut)
	}
	envData := readHomeEnv(t, home)
	if !strings.Contains(envData, "MINDLOOP_MODEL=echo") {
		t.Fatalf(".env 应含 echo 模型: %q", envData)
	}
	if _, err := identity.Load("ada"); err != nil {
		t.Fatalf("身份 ada 应已创建: %v", err)
	}
	if !strings.Contains(out, "chat ada") {
		t.Fatalf("输出应含下一步指引: %s", out)
	}
}

// TestInitFlagsWithProbe：显式 --model/--api-key/--base-url 时探测
// 打向本地 OpenAI-compatible 假端点——全链路（配置→探测→汇报延迟）
// 不出网。
func TestInitFlagsWithProbe(t *testing.T) {
	home := newTestHome(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"pong"}}]}`))
	}))
	defer srv.Close()

	code, out, errOut := runCLI(t, "init", "--model", "gpt-4o-mini",
		"--api-key", "test-value-fixture-key", "--base-url", srv.URL,
		"--identity", "bob", "--yes")
	if code != 0 {
		t.Fatalf("exit = %d\nout=%s\nerr=%s", code, out, errOut)
	}
	envData := readHomeEnv(t, home)
	for _, want := range []string{
		"MINDLOOP_MODEL=gpt-4o-mini",
		"MINDLOOP_API_KEY=test-value-fixture-key",
		"MINDLOOP_BASE_URL=" + srv.URL,
	} {
		if !strings.Contains(envData, want) {
			t.Fatalf(".env 缺 %s: %q", want, envData)
		}
	}
	if !strings.Contains(out, "通（") {
		t.Fatalf("探测应报告连通: %s", out)
	}
	if _, err := identity.Load("bob"); err != nil {
		t.Fatalf("身份 bob 应已创建: %v", err)
	}
}

// TestInitEnvMergePreservesComments：原位合并——既有注释与无关键
// 原样保留，目标键原位更新。
func TestInitEnvMergePreservesComments(t *testing.T) {
	home := newTestHome(t)
	envPath := filepath.Join(home, ".env")
	old := "# 我的手写注释\nMINDLOOP_MODEL=old-model\nOTHER_KEY=keep-me\n"
	if err := os.WriteFile(envPath, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}

	code, _, errOut := runCLI(t, "init", "--demo", "--yes")
	if code != 0 {
		t.Fatalf("exit = %d err=%s", code, errOut)
	}
	got := readHomeEnv(t, home)
	for _, want := range []string{
		"# 我的手写注释",
		"OTHER_KEY=keep-me",
		"MINDLOOP_MODEL=echo",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("合并丢失 %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "old-model") {
		t.Fatalf("旧模型值应被替换:\n%s", got)
	}
}

// TestInitExistingIdentityReused：身份已存在时复用而非重建——
// 重复 init 不该擦掉 ada 的既有轨迹与记忆。
func TestInitExistingIdentityReused(t *testing.T) {
	newTestHome(t)
	if _, err := identity.Create(context.Background(), "ada"); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runCLI(t, "init", "--demo", "--yes")
	if code != 0 {
		t.Fatalf("exit = %d err=%s", code, errOut)
	}
	if !strings.Contains(out, "已存在，直接复用") {
		t.Fatalf("应提示复用身份: %s", out)
	}
}

// TestInitConfirmDecline：交互确认答 n 时不写任何文件。
func TestInitConfirmDecline(t *testing.T) {
	home := newTestHome(t)
	old := initStdin
	initStdin = bufio.NewReader(strings.NewReader("1\nn\n"))
	t.Cleanup(func() { initStdin = old })

	code, out, _ := runCLI(t, "init")
	if code != 0 {
		t.Fatalf("exit = %d out=%s", code, out)
	}
	if homeHasEnv(t, home) {
		t.Fatal("拒绝后不应有 .env")
	}
	if !strings.Contains(out, "已取消") {
		t.Fatalf("应提示已取消: %s", out)
	}
}

// TestMaskKey：key 展示只留首尾。
func TestMaskKey(t *testing.T) {
	if got := maskKey("sk-abcdef1234567890xyz"); strings.Contains(got, "abcdef123456") {
		t.Fatalf("key 中段不应出现在展示里: %q", got)
	}
	if got := maskKey(""); got != "(未设置)" {
		t.Fatalf("空 key 展示: %q", got)
	}
}
