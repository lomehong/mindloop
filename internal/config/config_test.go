package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := "" +
		"# 注释行\n" +
		"\n" +
		"MINDLOOP_PROVIDER=anthropic\n" +
		"MINDLOOP_API_KEY=\"sk-quoted\"\n" +
		"export MINDLOOP_MODEL=claude-x\n" +
		"MINDLOOP_BASE_URL=https://x.example # 行内注释\n" +
		"BAD LINE WITHOUT EQUALS\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("MINDLOOP_PROVIDER", "explicit-wins")
	os.Unsetenv("MINDLOOP_MODEL")
	os.Unsetenv("MINDLOOP_API_KEY")
	os.Unsetenv("MINDLOOP_BASE_URL")

	if err := LoadEnv(path); err != nil {
		t.Fatalf("LoadEnv: %v", err)
	}
	if got := os.Getenv("MINDLOOP_PROVIDER"); got != "explicit-wins" {
		t.Fatalf("显式环境变量被覆盖: %q", got)
	}
	if got := os.Getenv("MINDLOOP_API_KEY"); got != "sk-quoted" {
		t.Fatalf("API key = %q", got)
	}
	if got := os.Getenv("MINDLOOP_MODEL"); got != "claude-x" {
		t.Fatalf("export 前缀未剥离: %q", got)
	}
	if got := os.Getenv("MINDLOOP_BASE_URL"); got != "https://x.example" {
		t.Fatalf("行内注释未剥离: %q", got)
	}
}

func TestLoadEnvMissingIsFine(t *testing.T) {
	if err := LoadEnv(filepath.Join(t.TempDir(), "absent.env")); err != nil {
		t.Fatalf("缺失文件不应报错: %v", err)
	}
}

func TestLoadEnvHandlesBOMAndCRLF(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	// 记事本/PowerShell 保存的典型形态：BOM + CRLF
	content := "\ufeff" + "MINDLOOP_PROVIDER=anthropic\r\n" + "MINDLOOP_MODEL=claude-x\r\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	os.Unsetenv("MINDLOOP_PROVIDER")
	os.Unsetenv("MINDLOOP_MODEL")
	if err := LoadEnv(path); err != nil {
		t.Fatalf("LoadEnv: %v", err)
	}
	if got := os.Getenv("MINDLOOP_PROVIDER"); got != "anthropic" {
		t.Fatalf("BOM 导致首行键失效: %q", got)
	}
	if got := os.Getenv("MINDLOOP_MODEL"); got != "claude-x" {
		t.Fatalf("CRLF 行解析失败: %q", got)
	}
}
