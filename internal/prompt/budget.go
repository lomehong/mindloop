// budget.go 是"一次模型请求的输入文本预算"的账本——字节是防线，
// 不是精确 token 窗口的等价物（多字节 CJK 每字 3 字节，token 密度
// 与 ASCII 完全不同；供应商的真实窗口由模型与输出上限共同决定）。
//
// 记账分两层：
//   - 受保护段（persona/执行规则、当前完整任务、最新用户消息）只
//     累加、绝不截断——超限时报错，绝不静默删掉用户的要求。
//   - 低优先段（记忆、摘要、旧历史）按上限与剩余预算裁剪，裁剪
//     事实记进 Trimmed()，供调用方落盘审计。
package prompt

import (
	"fmt"
	"strings"
)

// 默认预算。DefaultContextBudget 是整次请求的输入文本防线；
// 记忆与摘要的上限独立可配（0 = 用默认）。
const (
	DefaultContextBudget = 128 * 1024
	DefaultMemoryBudget  = 8 * 1024
	DefaultSummaryBudget = 16 * 1024
)

// Budget 是单次请求的预算账本。非并发安全——一次请求一份。
type Budget struct {
	total   int
	used    int
	segs    []segment
	trimmed []string
}

type segment struct {
	name  string
	bytes int
}

// OverflowError 报告受保护内容合计超出总预算：这是配置错误
// （persona/任务文本本身过大），调用方必须显式处理而不是继续。
type OverflowError struct {
	Total    int
	Used     int
	Segments []segment
}

func (e *OverflowError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "上下文受保护内容超出预算：%d/%d 字节（", e.Used, e.Total)
	for i, s := range e.Segments {
		if i > 0 {
			b.WriteString("、")
		}
		fmt.Fprintf(&b, "%s %d", s.name, s.bytes)
	}
	b.WriteString("）——请缩小 persona 或任务文本，不会静默截断")
	return b.String()
}

// NewBudget 建立账本。totalBytes <= 0 取 DefaultContextBudget。
func NewBudget(totalBytes int) *Budget {
	if totalBytes <= 0 {
		totalBytes = DefaultContextBudget
	}
	return &Budget{total: totalBytes}
}

// TakeProtected 登记受保护段。受保护合计超出总预算时返回
// *OverflowError（先登记的部分保留在账本里，错误信息含全部分段）。
func (b *Budget) TakeProtected(name, text string) error {
	b.segs = append(b.segs, segment{name: name, bytes: len(text)})
	b.used += len(text)
	if b.used > b.total {
		return &OverflowError{Total: b.total, Used: b.used, Segments: append([]segment(nil), b.segs...)}
	}
	return nil
}

// TakeCapped 装入低优先段：实际取 min(capBytes, 剩余预算) 字节
// （rune 安全截断），返回被采用的部分；截断事实记进 Trimmed()。
// capBytes <= 0 表示只受剩余预算限制。
func (b *Budget) TakeCapped(name, text string, capBytes int) string {
	remain := b.Remaining()
	if remain <= 0 {
		if text != "" {
			b.record(name, len(text), 0)
		}
		return ""
	}
	limit := remain
	if capBytes > 0 && capBytes < limit {
		limit = capBytes
	}
	if len(text) > limit {
		before := len(text)
		text, _ = cutRunes(text, limit)
		b.record(name, before, len(text))
	}
	b.used += len(text)
	return text
}

// record 记录一次裁剪（供 Trimmed 审计）。
func (b *Budget) record(name string, before, after int) {
	b.trimmed = append(b.trimmed, fmt.Sprintf("%s: %d → %d 字节", name, before, after))
}

// Remaining 是尚未占用的字节数——空余回流近期历史。
func (b *Budget) Remaining() int {
	remain := b.total - b.used
	if remain < 0 {
		return 0
	}
	return remain
}

// Trimmed 返回被裁剪段的审计记录（段名、原长、采用长）。
func (b *Budget) Trimmed() []string {
	return append([]string(nil), b.trimmed...)
}
