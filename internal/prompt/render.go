package prompt

import (
	"fmt"
	"unicode/utf8"
)

// cutRunes 按字节上限截断字符串，绝不切断 UTF-8 多字节序列的中间
// ——中文内容是这里的第一等公民。返回截后的文本与省略的字节数。
func cutRunes(s string, limit int) (string, int) {
	if limit <= 0 || len(s) <= limit {
		return s, 0
	}
	cut := 0
	for _, r := range s {
		size := utf8.RuneLen(r)
		if cut+size > limit {
			return s[:cut], len(s) - cut
		}
		cut += size
	}
	return s, 0
}

// cutMarked 截断并在截断处留下可执行的取回命令。分级是索引而不
// 是证词：模型（和人）必须能沿着标记走回原文。
func cutMarked(s string, limit int, stepID string, inline bool) string {
	text, omitted := cutRunes(s, limit)
	if omitted == 0 {
		return text
	}
	marker := fmt.Sprintf("《已截断：省略 %d 字节；完整内容: mindloop traj show %s --full》", omitted, stepID)
	if inline {
		return text + "…" + marker
	}
	return text + "\n" + marker
}
