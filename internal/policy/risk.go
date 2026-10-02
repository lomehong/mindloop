package policy

import (
	"regexp"
	"strings"
)

// 只读白名单判定——ask 策略的自动放行档。
//
// 设计取向与 riskPatterns 相反：那里是"命中危险词就提示"的黑名单，
// 这里是"逐命令全部可证明只读才放行"的白名单。黑名单漏一条就是
// 事故；白名单漏一条只是回到人工审批——默认失败方向是安全的
// （fail-closed）。判定保守到近乎苛刻：
//
//   - 逐行扫描，整行注释与空行跳过；引号内的分隔符不做特殊解析，
//     误切分只会导致"不认识 → 审批"，不会放行；
//   - 命令替换（$( )、反引号）、进程替换（<( )、变量赋值前缀
//     （PATH=/tmp/evil ls）、输出重定向（仅豁免 /dev/null 与流
//     互换）、heredoc 正文——一律不算只读；
//   - 命令必须逐个出现在只读白名单；git 与 $MINDLOOP_EXE 只放行
//     固定的只读子命令集；find 追加禁用 -exec/-delete 族；
//   - 循环（for/while）与未知命令一律回到人工审批。
//
// 漏报（把只读判成需审批）是体验问题；误放（把会写的东西判成
// 只读）是安全问题——本文件的一切含糊处都朝前者倒。

// ReadOnly 报告脚本是否逐命令都落在只读白名单内。它只回答
// "能不能免审批自动放行"，不回答"脚本是什么"——ask 策略下白名单
// 之外的一切照旧等人批准。
func ReadOnly(script string) bool {
	for _, rawLine := range strings.Split(script, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// 行级硬拒绝：任何形式的动态执行都超出静态可证范围。
		if strings.ContainsAny(line, "`") ||
			strings.Contains(line, "$(") ||
			strings.Contains(line, "<(") {
			return false
		}
		for _, seg := range splitSegments(line) {
			if seg == "" {
				continue
			}
			if !readOnlySegment(seg) {
				return false
			}
		}
	}
	return true
}

// splitSegments 按命令分隔符（; | &）粗切一行，带引号感知：引号内
// 的分隔符是普通字符。& 只在紧邻 > 时视为重定向语法（2>&1、&>file），
// 其余一律是分隔符。未加引号的分隔符永远切开——echo a;rm -rf x 这类
// 拼接无法蒙混。
func splitSegments(line string) []string {
	var segs []string
	var cur strings.Builder
	var quote byte
	split := func() {
		segs = append(segs, cur.String())
		cur.Reset()
	}
	for i := 0; i < len(line); i++ {
		c := line[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			cur.WriteByte(c)
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
			cur.WriteByte(c)
		case ';', '|':
			split()
		case '&':
			prev := byte(0)
			if cur.Len() > 0 {
				s := cur.String()
				prev = s[len(s)-1]
			}
			next := byte(0)
			if i+1 < len(line) {
				next = line[i+1]
			}
			if prev == '>' || next == '>' {
				cur.WriteByte(c)
			} else {
				split()
			}
		default:
			cur.WriteByte(c)
		}
	}
	split()
	return segs
}

// transparentTokens 是流程关键字与背景等待：剥掉之后段落的第一个
// 词才可能是命令。
var transparentTokens = map[string]bool{
	"if": true, "then": true, "else": true, "elif": true, "fi": true,
	"!": true, "time": true, "do": true, "done": true,
}

// assignRe 是变量赋值前缀（FOO=bar cmd 形态）。赋值可以改写 PATH
// 等查找环境——一律不算只读。
var assignRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// readOnlyCommands 是"程序本身无副作用"的命令：输出只进
// stdout/stderr，不落盘、不联网、不改自身之外的任何状态。刻意
// 不含 sed/awk（-i 与重定向、system() 使其可写）与一切解释器。
var readOnlyCommands = map[string]bool{
	"ls": true, "pwd": true, "echo": true, "printf": true,
	"cat": true, "head": true, "tail": true, "nl": true, "tac": true,
	"wc": true, "grep": true, "egrep": true, "fgrep": true,
	"sort": true, "uniq": true, "cut": true, "tr": true,
	"diff": true, "cmp": true, "comm": true, "paste": true, "join": true,
	"od": true, "hexdump": true, "strings": true, "rev": true,
	"fold": true, "fmt": true, "expand": true, "unexpand": true,
	"column": true, "jq": true, "base64": true,
	"md5sum": true, "sha1sum": true, "sha256sum": true, "cksum": true,
	"file": true, "stat": true, "du": true, "df": true,
	"cd": true, "true": true, "false": true, ":": true,
	"date": true, "uname": true, "id": true, "whoami": true,
	"hostname": true, "sleep": true, "ps": true,
	"which": true, "basename": true, "dirname": true,
	"realpath": true, "readlink": true,
	"test": true, "[": true, "[[": true,
	"find": true, "tree": true,
}

// gitReadOnlySubcommands 是 git 的只读子命令。git 整体不在白名单：
// tag/branch/config 的无旗标形式就是写操作（git tag v1 建标签）。
var gitReadOnlySubcommands = map[string]bool{
	"status": true, "log": true, "diff": true, "show": true,
	"rev-parse": true, "describe": true, "ls-files": true,
	"ls-remote": true, "ls-tree": true, "shortlog": true,
	"reflog": true, "blame": true, "cat-file": true,
	"grep": true, "help": true, "version": true,
}

// exeReadOnlySubcommands 是 $MINDLOOP_EXE 的只读子命令面：二级 →
// 允许的三级动词。不在表内的组合（mem add、mcp call、traj append…）
// 都有副作用，回到人工审批。
var exeReadOnlySubcommands = map[string]map[string]bool{
	"mem":    {"search": true, "list": true},
	"traj":   {"show": true, "tail": true, "check": true},
	"skills": {"list": true},
	"mcp":    {"tools": true},
	"mind":   {"status": true},
}

// findForbidden 是让 find 获得执行/批量删除能力的旗标——find 本身
// 只读，但这些旗标把它的输出变成了任意命令的入口。
var findForbidden = []string{
	"-exec", "-execdir", "-ok", "-okdir",
	"-delete", "-fprint", "-fprintf", "-fls",
}

// redirBare 是允许的"独立重定向 token"：下一个 token 必须是
// /dev/null。
var redirBare = map[string]bool{
	">": true, ">>": true, "&>": true, "2>": true, "1>": true,
}

// redirGlued 是允许的"粘连重定向 token"——除此之外任何含 > 的
// token（>out.txt、&>file、2>err.log…）都算落盘。
var redirGlued = map[string]bool{
	">/dev/null": true, ">>/dev/null": true, "&>/dev/null": true,
	"2>/dev/null": true, "1>/dev/null": true,
	"2>&1": true, "1>&2": true, ">&1": true, ">&2": true,
}

// exeNameRe 归一 $MINDLOOP_EXE 的书写形态：带引号、${} 花括号都算
// 同一个词。
var exeNameRe = regexp.MustCompile(`^\$"?(\{)?MINDLOOP_EXE(\})?"?$`)

// readOnlySegment 判定单个命令段。可见性：本函数返回 false 的路径
// 远多于 true——每个分支都值得一条测试。
func readOnlySegment(seg string) bool {
	tokens := strings.Fields(seg)
	if len(tokens) == 0 {
		return true
	}
	// 重定向检查先行：token 流里任何不允许的 > 形态都直接否决，
	// 命令白名单再干净也救不回一个落盘。
	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		if redirBare[tok] {
			if i+1 >= len(tokens) || tokens[i+1] != "/dev/null" {
				return false
			}
			i++
			continue
		}
		if strings.Contains(tok, ">") && !redirGlued[tok] {
			return false
		}
	}
	// 剥掉流程关键字，找真正的命令词。
	cmd := ""
	idx := 0
	for ; idx < len(tokens); idx++ {
		if !transparentTokens[tokens[idx]] {
			cmd = tokens[idx]
			break
		}
	}
	if cmd == "" {
		return true
	}
	// 变量赋值前缀（FOO=bar cmd）不改写成白名单调用。
	if assignRe.MatchString(cmd) {
		return false
	}
	cmd = strings.Trim(cmd, `"'`)
	// $MINDLOOP_EXE：固定二级→三级白名单。
	if exeNameRe.MatchString(cmd) {
		if len(tokens) < idx+3 {
			return false
		}
		subs, ok := exeReadOnlySubcommands[tokens[idx+1]]
		if !ok || !subs[tokens[idx+2]] {
			return false
		}
		return true
	}
	// git：只读子命令集。
	if cmd == "git" {
		return len(tokens) > idx+1 && gitReadOnlySubcommands[tokens[idx+1]]
	}
	// find：命令本身只读，但执行/删除族旗标把它变成任意命令入口。
	if cmd == "find" {
		for _, bad := range findForbidden {
			if strings.Contains(seg, bad) {
				return false
			}
		}
		return true
	}
	return readOnlyCommands[cmd]
}
