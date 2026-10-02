package risk

import (
	"testing"
)

// TestReadOnlyAllowlist：只读白名单判定的全量用例。true 侧是允许
// 判真的形态；false 侧每条都是一个具体的绕过尝试——白名单外一律
// 判非只读（fail-closed）。
func TestReadOnlyAllowlist(t *testing.T) {
	cases := []struct {
		name   string
		script string
		want   bool
	}{
		// —— 判真侧：纯只读形态 ——
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
		{"test 族", "test -f x && cat x\n[ -f a ]", true},
		{"干净的 find", "find . -name '*.txt' -maxdepth 2", true},
		{"引号内分隔符是普通字符", `echo "a;b" | wc -l`, true},
		{"FINAL 协议赋值（补全轮）", `FINAL="看完了"`, true},
		{"FINAL 赋值后跟只读命令", `FINAL="x" && ls`, true},

		// —— 判假侧：每一个都是具体绕过尝试 ——
		{"未知命令", "rm -rf /", false},
		{"覆盖写", "echo hi > out.txt", false},
		{"追加写", "echo hi >> out.txt", false},
		{"重定向到真实文件", "ls > results.txt 2>&1", false},
		{"命令替换", "cat $(pwd)/f", false},
		{"反引号", "ls `pwd`", false},
		{"进程替换", "cat <(rm -rf x)", false},
		{"赋值前缀劫持 PATH", "PATH=/tmp/evil ls", false},
		{"其他变量赋值不算只读", "FOO=bar ls", false},
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
		{"未加引号分隔符永远切开", "echo hi;rm -rf x", false},
	}
	for _, tc := range cases {
		if got := ReadOnly(tc.script); got != tc.want {
			t.Errorf("%s: ReadOnly = %v，应为 %v\n脚本: %q", tc.name, got, tc.want, tc.script)
		}
	}
}
