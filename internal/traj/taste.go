package traj

import "context"

// AppendTasteStep 写一条味觉信号（operator 对产出的归因反馈，
// perception.md Phase 4）：审批决定文件是瞬态的（gate 消费即删），
// 归因必须自己落轨迹才是持久证据。signal ∈ undo|approve|deny；
// cause 是自由词表（undo 侧由 CLI 校验封闭词表）；ref 关联对象
// （run id / 脚本哈希）。落盘失败返回错误——调用方如实呈报，
// 证据是增强不是事务的一部分。
func AppendTasteStep(ctx context.Context, tl *Timeline, signal, cause, ref string) error {
	s := NewStep(TypeTaste)
	s.Fields["signal"] = signal
	if cause != "" {
		s.Fields["cause"] = cause
	}
	if ref != "" {
		s.Fields["ref"] = ref
	}
	return tl.Append(ctx, s)
}
