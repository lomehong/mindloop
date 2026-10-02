package policy

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadOnlyAllowlist：只读白名单判定的全量用例。true 侧是允许
// 自动放行的形态；false 侧每条都是一个具体的绕过尝试——白名单外
// 一律回到人工审批（fail-closed）。
func TestReadOnlyAllowlist(t *testing.T) {
	cases := []struct {
		name   string
		script string
		want   bool
	}{
		// —— 放行侧：纯只读形态 ——
		{"空脚本", "", true},
		{"单命令", "ls -la", true},
		{"多行", "ls -la\ncat foo.txt\npwd", true},
		{"注释与空行", "# rm -rf /\n\nls", true},
		{"管道", "grep -rn TODO . | wc -l", true},
		{"链式与 cd", "cd src && head -5 a.txt; pwd", true},
		{"git 只读子命令", "git status\ngit log --oneline -5\ngit diff HEAD~1 --stat", true},
		{"exe 引号形态", `"$MINDLOOP_EXE" mem search 偏好`, true},
		{"exe 花括号形态", "${MINDLOOP_EXE} traj show abc", true},
		{"重定向到空设备", "ls > /dev/null\nls 2>/dev/null\ncat f 2>&1\nls >/dev/null 2>&1", true},
		{"条件与取反", "if grep -q x f; then echo yes; fi\n! grep -q secret f", true},
		{"引号内分隔符是普通字符", `echo "a;b" | wc -l`, true},
		{"未加引号分隔符永远切开", "echo hi;rm -rf x", false},
		{"test 族", "test -f x && cat x\n[ -f a ]", true},
		{"干净的 find", "find . -name '*.txt' -maxdepth 2", true},

		// —— 审批侧：每一个都是具体绕过尝试 ——
		{"未知命令", "rm -rf /", false},
		{"覆盖写", "echo hi > out.txt", false},
		{"追加写", "echo hi >> out.txt", false},
		{"重定向到真实文件", "ls > results.txt 2>&1", false},
		{"命令替换", "cat $(pwd)/f", false},
		{"反引号", "ls `pwd`", false},
		{"进程替换", "cat <(rm -rf x)", false},
		{"赋值前缀劫持 PATH", "PATH=/tmp/evil ls", false},
		{"find 批量删除", "find . -name x -delete", false},
		{"find 执行", "find . -exec rm {} ;", false},
		{"git 写子命令", "git tag v1", false},
		{"git remote 写", "git remote add o https://example.com/x.git", false},
		{"git config 写", "git config user.name x", false},
		{"exe 写记忆", `"$MINDLOOP_EXE" mem add --type fact 内容`, false},
		{"exe 调用 MCP", `"$MINDLOOP_EXE" mcp call github get_issue`, false},
		{"联网", "curl https://example.com", false},
		{"sed 原地改", "sed -i s/a/b/ f", false},
		{"awk 可写可执行", "awk '{print $1}' f", false},
		{"循环", "for f in *.txt; do cat $f; done", false},
		{"解释器", "python -c 'import os'", false},
		{"tee 落盘", "echo x | tee out.txt", false},
		{"改权限", "chmod 777 f", false},
	}
	for _, tc := range cases {
		if got := ReadOnly(tc.script); got != tc.want {
			t.Errorf("%s: ReadOnly = %v，应为 %v\n脚本: %q", tc.name, got, tc.want, tc.script)
		}
	}
}

// TestAskAutoApprovesReadOnly：ask 门 + 自动放行档——只读脚本免审批
// 直接通过、不产生待批文件、审计落 auto-readonly；非只读脚本照旧
// 进等待。
func TestAskAutoApprovesReadOnly(t *testing.T) {
	g, dir := testGate(t, Ask)
	g.AutoReadOnly = true

	readOnly := testExec(t, "ls -la\ncat notes.md", "run-1")
	if err := g.Authorize(context.Background(), readOnly); err != nil {
		t.Fatalf("只读脚本应自动放行: %v", err)
	}
	if pending, _ := ListPending(dir); len(pending) != 0 {
		t.Fatalf("自动放行不应产生待批请求: %v", pending)
	}
	audit, err := os.ReadFile(filepath.Join(dir, "audit.jsonl"))
	if err != nil {
		t.Fatalf("审计应落盘: %v", err)
	}
	if !strings.Contains(string(audit), `"decision":"auto-readonly"`) {
		t.Fatalf("审计应记 auto-readonly: %s", audit)
	}

	// 非只读照旧等待审批：Authorize 会阻塞到 TTL，挂后台等它把
	// 待批请求写出来再取消。
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- g.Authorize(ctx, testExec(t, "rm -rf /", "run-2")) }()
	waitForPending(t, dir, 1)
	cancel()
	if err := <-errCh; err == nil {
		t.Fatal("取消的等待不应返回 nil")
	}
}

// TestAutoReadOnlyKillSwitch：开关关闭时只读脚本也照旧审批——豁免
// 是可关闭的便利，不是新的缺省信任。
func TestAutoReadOnlyKillSwitch(t *testing.T) {
	g, dir := testGate(t, Ask)
	g.AutoReadOnly = false

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- g.Authorize(ctx, testExec(t, "ls -la", "run-1")) }()
	waitForPending(t, dir, 1)
	cancel()
	if err := <-errCh; err == nil {
		t.Fatal("取消的等待不应返回 nil")
	}
}

// TestAutoReadOnlyFromEnv：显式 "0" 关闭，未设置与其余值开启。
func TestAutoReadOnlyFromEnv(t *testing.T) {
	t.Setenv("MINDLOOP_EXEC_POLICY_AUTO", "")
	if !AutoReadOnlyFromEnv() {
		t.Fatal("未设置应开启")
	}
	t.Setenv("MINDLOOP_EXEC_POLICY_AUTO", "0")
	if AutoReadOnlyFromEnv() {
		t.Fatal("显式 0 应关闭")
	}
	t.Setenv("MINDLOOP_EXEC_POLICY_AUTO", "1")
	if !AutoReadOnlyFromEnv() {
		t.Fatal("1 应开启")
	}
}
