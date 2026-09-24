package runner

import (
	"strings"
)

// extraction 是从模型回复中提取可执行代码的结果。Notice 是给模型
// 的教学反馈，会在下一轮以用户消息回灌——Headlong 的 self-teaching
// stderr 提示思路：第一次犯错不受罚，但要被教会。
type extraction struct {
	Code   string
	Notice string
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

	flush := func() extraction {
		if len(code) == 0 {
			// 无 fence：整段当命令执行（Headlong 同款兜底），
			// 但要教会模型正确的格式。
			return extraction{
				Code:   strings.TrimSpace(text),
				Notice: "你的回复里没有 ```bash 代码块，整段文本已被当作命令执行。之后请把要执行的命令放进恰好一个 ```bash 代码块。",
			}
		}
		notice := ""
		if fenceCount > 1 {
			notice = "你的回复包含多个代码块，只有第一个被执行。请每轮只输出一个 ```bash 代码块。"
		}
		return extraction{Code: strings.Join(code, "\n"), Notice: notice}
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// 第一个块已经闭合：其余内容不进代码，但继续数 fence——
		// 多块提示据此产生（Headlong 的 TRUNCATED 信号语义）。
		if closed {
			if strings.HasPrefix(trimmed, "```") {
				fenceCount++
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
		}
	}
	return flush()
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
