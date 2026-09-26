package runner

import (
	"strings"
)

// extraction 是从模型回复中提取可执行代码的结果。Notice 是给模型
// 的教学反馈，会在下一轮以用户消息回灌——Headlong 的 self-teaching
// stderr 提示思路：第一次犯错不受罚，但要被教会。Final 非空表示
// 回复里没有任何代码块、但存在裸文本 FINAL= 声明——按完成语义
// 直接采为终局（见 bareFinal），不再进入执行路径。
type extraction struct {
	Code   string
	Notice string
	Final  string
}

// heredoc 前缀匹配：`<<EOF` / `<<-EOF` / `<< 'EOF'` 等。fence 行
// 出现在 heredoc 正文里时不算块边界——这是 Headlong 的
// heredoc-aware awk 提取器的移植。
func extractCode(text string) extraction {
	lines := strings.Split(text, "\n")
	var code []string
	inFence := false
	closed := false
	fenceCount := 0
	heredocWord := ""
	outsideFinal := false

	flush := func() extraction {
		if len(code) == 0 {
			// 无块：先看是不是裸文本 FINAL= 声明。实测事故（轨迹
			// 5518efef）：模型完成任务后把 FINAL="…" 写成裸文本，
			// 旧路径把整段拿去当命令执行 → 必然报错 → 轮次耗尽，
			// 已完成的成果从未回发对话。模型清楚表达了"任务完成
			// +答案"，按完成语义提取，不走教学纠偏。
			if v, ok := bareFinal(text); ok {
				return extraction{Final: v}
			}
			// 无块且无声明：整段当命令执行（Headlong 同款兜底），
			// 但要教会模型正确的格式。
			return extraction{
				Code:   strings.TrimSpace(text),
				Notice: "你的回复里没有 ```bash 代码块，整段文本已被当作命令执行。之后请把要执行的命令放进恰好一个 ```bash 代码块。",
			}
		}
		var notices []string
		if fenceCount > 1 {
			notices = append(notices, "你的回复包含多个代码块，只有第一个被执行。请每轮只输出一个 ```bash 代码块。")
		}
		// 块外 FINAL= 声明不生效（只认块内声明与无块裸声明），但
		// 不能静默：实测事故（mind 轨迹 5518efef）——模型每轮把
		// FINAL= 写在代码块外，系统不认 → 收不了尾 → 轮次耗尽连环循环。
		if outsideFinal {
			notices = append(notices, "你的回复把 FINAL= 写在了代码块外，不会生效。收尾时必须把 FINAL=\"…\" 写进该 bash 代码块内，成为脚本里的一条语句。")
		}
		return extraction{Code: strings.Join(code, "\n"), Notice: strings.Join(notices, "\n")}
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// 第一个块已经闭合：其余内容不进代码，但继续数 fence——
		// 多块提示据此产生（Headlong 的 TRUNCATED 信号语义）。
		if closed {
			if strings.HasPrefix(trimmed, "```") {
				fenceCount++
			} else if strings.HasPrefix(trimmed, "FINAL=") {
				outsideFinal = true
			}
			continue
		}

		// heredoc 正文：直到终止标记出现，一切照搬（含 fence 行）。
		if heredocWord != "" {
			if inFence {
				code = append(code, line)
			}
			if trimmed == heredocWord || strings.TrimRight(trimmed, "\r") == heredocWord {
				heredocWord = ""
			}
			continue
		}

		if !inFence && strings.HasPrefix(trimmed, "```") {
			inFence = true
			fenceCount++
			continue
		}
		if inFence && strings.HasPrefix(trimmed, "```") {
			inFence = false
			closed = true
			continue
		}
		if inFence {
			code = append(code, line)
			if w, ok := heredocOpener(line); ok {
				heredocWord = w
			}
			continue
		}
		// 块外正文行：FINAL= 写在这里不会生效（见 flush 的教学反馈）。
		if strings.HasPrefix(trimmed, "FINAL=") {
			outsideFinal = true
		}
	}
	return flush()
}

// bareFinal 在无代码块的回复里识别裸文本 FINAL= 声明，返回其值。
// 值允许双/单引号包裹（与 shell 赋值同形）；空值不算声明。只认
// 单行——多行值回退教学纠偏路径，模型下一轮自会改用正规块声明。
// 有代码块时本函数根本不会被调用：正规块内声明路径永远优先。
func bareFinal(text string) (string, bool) {
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if !strings.HasPrefix(line, "FINAL=") {
			continue
		}
		v := strings.TrimSpace(strings.TrimPrefix(line, "FINAL="))
		if len(v) >= 2 {
			if q := v[0]; q == '"' || q == '\'' {
				if v[len(v)-1] == q {
					v = strings.TrimSpace(v[1 : len(v)-1])
				}
			}
		}
		if v == "" {
			continue
		}
		return v, true
	}
	return "", false
}

// heredocOpener 检测一行里的 heredoc 开始，返回终止标记。
func heredocOpener(line string) (string, bool) {
	idx := strings.Index(line, "<<")
	if idx < 0 {
		return "", false
	}
	rest := line[idx+2:]
	rest = strings.TrimLeft(rest, "-")
	rest = strings.TrimLeft(rest, " \t")
	// 带引号的形式：<< 'EOF' 或 << "EOF"
	if len(rest) >= 3 && (rest[0] == '\'' || rest[0] == '"') {
		q := rest[0]
		end := strings.IndexByte(rest[1:], q)
		if end < 0 {
			return "", false
		}
		return rest[1 : 1+end], true
	}
	// 裸词形式：到空白为止
	end := len(rest)
	for i, r := range rest {
		if r == ' ' || r == '\t' || r == ';' || r == '\r' {
			end = i
			break
		}
	}
	word := rest[:end]
	if word == "" {
		return "", false
	}
	for _, r := range word {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_') {
			return "", false
		}
	}
	return word, true
}
