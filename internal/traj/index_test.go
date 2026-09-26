package traj

// 侧索引（Index）的测试：增量消费、缓存恢复与各类重建触发。
// 手工构造 journal 字节以精确控制半行、替换与截断场景。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	tsA = "2026-01-01T00:00:00.000Z"
	tsB = "2026-01-01T00:01:00.000Z"
	tsC = "2026-01-01T00:02:00.000Z"
	tsD = "2026-01-01T00:03:00.000Z"
)

// rawTimeline 手工构造一条只有文件的轨迹——测试直接控制 journal
// 字节（半行、替换、截断），不走 Create/Append 的高层路径。
func rawTimeline(t *testing.T) *Timeline {
	t.Helper()
	dir := t.TempDir()
	return &Timeline{ID: "tid00000000", Slug: "t", Dir: dir, Path: filepath.Join(dir, "trajectory.jsonl")}
}

// journalLine 构造一行扁平 JSONL 步骤。
func journalLine(typ, stepID, ts string, fields map[string]string) string {
	m := map[string]any{"type": typ, "step_id": stepID, "ts": ts}
	for k, v := range fields {
		m[k] = v
	}
	b, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func writeJournal(t *testing.T, tl *Timeline, lines ...string) {
	t.Helper()
	var b strings.Builder
	for _, ln := range lines {
		b.WriteString(ln)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(tl.Path, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("写入 journal: %v", err)
	}
}

func appendJournal(t *testing.T, tl *Timeline, raw string) {
	t.Helper()
	f, err := os.OpenFile(tl.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("打开 journal 追加: %v", err)
	}
	defer f.Close()
	if _, err := f.WriteString(raw); err != nil {
		t.Fatalf("追加 journal: %v", err)
	}
}

func openIndex(t *testing.T, tl *Timeline) *Index {
	t.Helper()
	ix, err := OpenIndex(tl)
	if err != nil {
		t.Fatalf("OpenIndex: %v", err)
	}
	return ix
}

func TestIndexCountAndFlags(t *testing.T) {
	tl := rawTimeline(t)
	head := journalLine(TypeTrajectory, "tid00000000", tsA, map[string]string{"slug": "t"})
	m1 := journalLine(TypeMessage, "msg00000001", tsB, map[string]string{"from": "operator", "to": "ada", "content": "hi"})
	a1 := journalLine(TypeAction, "act00000001", tsC, map[string]string{"launched_by": "monolith", "content": "x"})
	a2 := journalLine(TypeAction, "act00000002", tsC, map[string]string{"launched_by": "monolith", "content": "y"})
	a3 := journalLine(TypeAction, "act00000003", tsC, map[string]string{"launched_by": "responder", "content": "z"})
	writeJournal(t, tl, head, m1, a1, a2, a3)

	ix := openIndex(t, tl) // 无缓存：全量重建
	if n, err := ix.StepCount(); err != nil || n != 5 {
		t.Fatalf("StepCount = %d, %v，应为 5", n, err)
	}
	if got, err := ix.FirstTS(); err != nil || got != tsA {
		t.Fatalf("FirstTS = %q, %v，应为 %q", got, err, tsA)
	}
	if got, _ := ix.HasFinal(); got {
		t.Fatal("HasFinal 应为 false")
	}
	by, err := ix.LaunchedBy()
	if err != nil || len(by) != 2 || by[0] != "monolith" || by[1] != "responder" {
		t.Fatalf("LaunchedBy = %v, %v，应为 [monolith responder]", by, err)
	}

	// 增量：追加 final 后无需显式 CatchUp——读请求先追平。
	appendJournal(t, tl, journalLine(TypeFinal, "fin00000001", tsD, map[string]string{"content": "done"})+"\n")
	if n, _ := ix.StepCount(); n != 6 {
		t.Fatalf("追平后 StepCount = %d，应为 6", n)
	}
	if got, _ := ix.HasFinal(); !got {
		t.Fatal("追加 final 后 HasFinal 应为 true")
	}
}

func TestIndexChatView(t *testing.T) {
	tl := rawTimeline(t)
	head := journalLine(TypeTrajectory, "tid00000000", tsA, map[string]string{"slug": "t"})
	m1 := journalLine(TypeMessage, "msg00000001", tsB, map[string]string{"from": "operator", "to": "ada", "content": "c1"})
	s0 := journalLine(TypeMessage, "msg00000002", tsB, map[string]string{"from": "ada", "to": "operator", "content": "", "source": "reply-status", "state": "no-reply", "reply_to": "msg00000001"})
	m2 := journalLine(TypeMessage, "msg00000003", tsB, map[string]string{"from": "ada", "to": "operator", "content": "r1", "reply_to": "msg00000001"})
	m3 := journalLine(TypeMessage, "msg00000004", tsC, map[string]string{"from": "bob", "to": "ada", "content": "c2"})
	s1 := journalLine(TypeMessage, "msg00000005", tsC, map[string]string{"from": "ada", "to": "bob", "content": "", "source": "reply-status", "state": "reply-failed", "reply_to": "msg00000004"})
	m4 := journalLine(TypeMessage, "msg00000006", tsC, map[string]string{"from": "operator", "to": "ada", "content": "c3"})
	m5 := journalLine(TypeMessage, "msg00000007", tsC, map[string]string{"from": "ada", "to": "bob", "content": "r2"})
	writeJournal(t, tl, head, m1, s0, m2, m3, s1, m4, m5)

	ix := openIndex(t, tl)

	// 全量：收据不进消息流；replied 强于收据（m1 既有 no-reply 收据又有真回复）。
	chat, err := ix.ChatView(200, "", "ada")
	if err != nil {
		t.Fatalf("ChatView: %v", err)
	}
	if len(chat.Messages) != 5 {
		t.Fatalf("消息流 = %d 条，应为 5（排除 2 条收据）", len(chat.Messages))
	}
	if chat.Messages[0].StepID != "msg00000001" || chat.Messages[0].Content != "c1" {
		t.Fatalf("首条消息 = %+v", chat.Messages[0])
	}
	if got := chat.Outcomes["msg00000001"]; got != "replied" {
		t.Fatalf("m1 outcome = %q，应为 replied（真回复强于收据）", got)
	}
	if got := chat.Outcomes["msg00000004"]; got != "failed" {
		t.Fatalf("m3 outcome = %q，应为 failed", got)
	}
	if _, ok := chat.Outcomes["msg00000006"]; ok {
		t.Fatal("无结局的入站消息不应出现在 outcomes")
	}
	if _, ok := chat.Outcomes["msg00000003"]; ok {
		t.Fatal("出站回复不应有 outcome")
	}

	// tail：只取最近 3 条；outcomes 只覆盖返回窗口内的入站消息——
	// 尾窗读取（O(tail)）不随日志规模重建整份结局表；窗口外的
	// 结局对消费方（只渲染返回消息）无用，且代价随日志线性增长。
	chatTail, _ := ix.ChatView(3, "", "ada")
	if len(chatTail.Messages) != 3 || chatTail.Messages[0].StepID != "msg00000004" {
		t.Fatalf("tail=3 消息 = %+v", chatTail.Messages)
	}
	if got := chatTail.Outcomes["msg00000004"]; got != "failed" {
		t.Fatalf("tail=3 时窗口内 m3 outcome = %q，应为 failed", got)
	}
	if _, ok := chatTail.Outcomes["msg00000001"]; ok {
		t.Fatal("尾窗外的消息不应出现在 outcomes")
	}

	// with：只留与某人的对话（对方发来的 + 我回给对方的）。
	chatBob, _ := ix.ChatView(200, "bob", "ada")
	if len(chatBob.Messages) != 2 || chatBob.Messages[0].StepID != "msg00000004" || chatBob.Messages[1].StepID != "msg00000007" {
		t.Fatalf("with=bob 消息 = %+v", chatBob.Messages)
	}
	if got := chatBob.Outcomes["msg00000004"]; got != "failed" {
		t.Fatalf("with=bob 时 m3 outcome = %q", got)
	}
	chatOp, _ := ix.ChatView(200, "operator", "ada")
	if len(chatOp.Messages) != 3 {
		t.Fatalf("with=operator 消息 = %d 条，应为 3", len(chatOp.Messages))
	}
}

func TestIndexHalfLine(t *testing.T) {
	tl := rawTimeline(t)
	head := journalLine(TypeTrajectory, "tid00000000", tsA, map[string]string{"slug": "t"})
	m1 := journalLine(TypeMessage, "msg00000001", tsB, map[string]string{"from": "operator", "to": "ada", "content": "hi"})
	m2 := journalLine(TypeMessage, "msg00000002", tsC, map[string]string{"from": "operator", "to": "ada", "content": "full"})
	// 半行：只有 JSON 前缀，没有换行。
	half := m2[:len(m2)/2]
	writeJournal(t, tl, head, m1)
	appendJournal(t, tl, half)

	ix := openIndex(t, tl)
	if n, _ := ix.StepCount(); n != 2 {
		t.Fatalf("半行未完成时 StepCount = %d，应为 2", n)
	}

	// 写者补全：半行的剩余部分 + 换行。
	appendJournal(t, tl, m2[len(m2)/2:]+"\n")
	if n, _ := ix.StepCount(); n != 3 {
		t.Fatalf("补全后 StepCount = %d，应为 3", n)
	}
	chat, _ := ix.ChatView(200, "", "ada")
	if len(chat.Messages) != 2 || chat.Messages[1].Content != "full" {
		t.Fatalf("补全后消息 = %+v", chat.Messages)
	}
}

func TestIndexBadLinesSkipped(t *testing.T) {
	tl := rawTimeline(t)
	head := journalLine(TypeTrajectory, "tid00000000", tsA, map[string]string{"slug": "t"})
	m1 := journalLine(TypeMessage, "msg00000001", tsB, map[string]string{"from": "operator", "to": "ada", "content": "hi"})
	writeJournal(t, tl, head, "{broken json", m1, `{"no_type":"x"}`)

	ix := openIndex(t, tl)
	n, err := ix.StepCount()
	if err != nil {
		t.Fatalf("StepCount: %v", err)
	}
	steps, err := tl.Steps()
	if err != nil {
		t.Fatalf("Steps: %v", err)
	}
	if n != len(steps) || n != 2 {
		t.Fatalf("StepCount = %d，Steps 计数 = %d，应同为 2（坏行跳过）", n, len(steps))
	}
}

func TestIndexSaveLoadRoundTrip(t *testing.T) {
	tl := rawTimeline(t)
	head := journalLine(TypeTrajectory, "tid00000000", tsA, map[string]string{"slug": "t"})
	m1 := journalLine(TypeMessage, "msg00000001", tsB, map[string]string{"from": "operator", "to": "ada", "content": "c1"})
	a1 := journalLine(TypeAction, "act00000001", tsC, map[string]string{"launched_by": "monolith"})
	writeJournal(t, tl, head, m1, a1)

	ix := openIndex(t, tl)
	if err := ix.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// 新实例：从缓存恢复（无新内容 → CatchUp 零消费）。
	ix2 := openIndex(t, tl)
	if n, err := ix2.CatchUp(); err != nil || n != 0 {
		t.Fatalf("缓存恢复后 CatchUp = %d, %v，应为 0（未重放）", n, err)
	}
	if n, _ := ix2.StepCount(); n != 3 {
		t.Fatalf("恢复后 StepCount = %d，应为 3", n)
	}
	if got, _ := ix2.FirstTS(); got != tsA {
		t.Fatalf("恢复后 FirstTS = %q", got)
	}
	if by, _ := ix2.LaunchedBy(); len(by) != 1 || by[0] != "monolith" {
		t.Fatalf("恢复后 LaunchedBy = %v", by)
	}
	chat, _ := ix2.ChatView(200, "", "ada")
	if len(chat.Messages) != 1 || chat.Messages[0].Content != "c1" {
		t.Fatalf("恢复后消息 = %+v", chat.Messages)
	}

	// 缓存不牺牲新鲜度：追加后读请求自己追平。
	appendJournal(t, tl, journalLine(TypeMessage, "msg00000002", tsD, map[string]string{"from": "bob", "to": "ada", "content": "c2"})+"\n")
	chat2, _ := ix2.ChatView(200, "", "ada")
	if len(chat2.Messages) != 2 || chat2.Messages[1].Content != "c2" {
		t.Fatalf("追平后消息 = %+v", chat2.Messages)
	}
}

func TestIndexCorruptCacheRebuild(t *testing.T) {
	tl := rawTimeline(t)
	head := journalLine(TypeTrajectory, "tid00000000", tsA, map[string]string{"slug": "t"})
	m1 := journalLine(TypeMessage, "msg00000001", tsB, map[string]string{"from": "operator", "to": "ada", "content": "hi"})
	writeJournal(t, tl, head, m1)

	ix := openIndex(t, tl)
	if err := ix.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// 缓存写坏（崩溃现场）：JSON 截断。
	if err := os.WriteFile(tl.IndexCachePath(), []byte(`{"schema":1,"step_c`), 0o644); err != nil {
		t.Fatalf("写坏缓存: %v", err)
	}

	ix2 := openIndex(t, tl)
	if n, err := ix2.StepCount(); err != nil || n != 2 {
		t.Fatalf("坏缓存重建后 StepCount = %d, %v，应为 2", n, err)
	}
}

func TestIndexVersionMismatchRebuild(t *testing.T) {
	tl := rawTimeline(t)
	head := journalLine(TypeTrajectory, "tid00000000", tsA, map[string]string{"slug": "t"})
	m1 := journalLine(TypeMessage, "msg00000001", tsB, map[string]string{"from": "operator", "to": "ada", "content": "hi"})
	writeJournal(t, tl, head, m1)

	ix := openIndex(t, tl)
	if err := ix.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// 篡改 schema 版本与偏移（指向行中间 → 也必须重建）。
	data, err := os.ReadFile(tl.IndexCachePath())
	if err != nil {
		t.Fatalf("读缓存: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("解析缓存: %v", err)
	}
	m["schema"] = 999
	m["offset"] = 1
	out, _ := json.Marshal(m)
	if err := os.WriteFile(tl.IndexCachePath(), out, 0o644); err != nil {
		t.Fatalf("写回缓存: %v", err)
	}

	ix2 := openIndex(t, tl)
	if n, err := ix2.StepCount(); err != nil || n != 2 {
		t.Fatalf("版本不符重建后 StepCount = %d, %v，应为 2", n, err)
	}
}

func TestIndexMidLineOffsetRebuild(t *testing.T) {
	tl := rawTimeline(t)
	head := journalLine(TypeTrajectory, "tid00000000", tsA, map[string]string{"slug": "t"})
	m1 := journalLine(TypeMessage, "msg00000001", tsB, map[string]string{"from": "operator", "to": "ada", "content": "hi"})
	writeJournal(t, tl, head, m1)

	ix := openIndex(t, tl)
	if err := ix.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// 合法 schema/身份，但偏移落在行中间（手工篡改或写坏）→ 重建。
	// 偏移选在头行内部：若从行中间恢复，头行残片被容错跳过、
	// 后续消息行被重复消费，计数量必然偏离。
	data, _ := os.ReadFile(tl.IndexCachePath())
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	m["offset"] = float64(3)
	out, _ := json.Marshal(m)
	if err := os.WriteFile(tl.IndexCachePath(), out, 0o644); err != nil {
		t.Fatalf("写回缓存: %v", err)
	}

	ix2 := openIndex(t, tl)
	if n, err := ix2.StepCount(); err != nil || n != 2 {
		t.Fatalf("行中间偏移重建后 StepCount = %d, %v，应为 2", n, err)
	}
}

func TestIndexReplaceSameSizeRebuild(t *testing.T) {
	tl := rawTimeline(t)
	head := journalLine(TypeTrajectory, "tid00000000", tsA, map[string]string{"slug": "t"})
	oldMsg := journalLine(TypeMessage, "msg00000001", tsB, map[string]string{"from": "operator", "to": "ada", "content": "old-old-old"})
	newMsg := journalLine(TypeMessage, "msg00000002", tsB, map[string]string{"from": "operator", "to": "ada", "content": "new-new-new"})
	if len(oldMsg) != len(newMsg) {
		t.Fatalf("测试构造错误：两版本行长度 %d != %d", len(oldMsg), len(newMsg))
	}
	writeJournal(t, tl, head, oldMsg)

	ix := openIndex(t, tl)
	if err := ix.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// 同尺寸替换：临时文件 + rename 覆盖（真实替换场景，文件身份必变）。
	swap := tl.Path + ".swap"
	if err := os.WriteFile(swap, []byte(head+"\n"+newMsg+"\n"), 0o644); err != nil {
		t.Fatalf("写替换文件: %v", err)
	}
	if err := os.Rename(swap, tl.Path); err != nil {
		t.Fatalf("rename 替换: %v", err)
	}

	ix2 := openIndex(t, tl)
	chat, err := ix2.ChatView(200, "", "ada")
	if err != nil {
		t.Fatalf("ChatView: %v", err)
	}
	if len(chat.Messages) != 1 || chat.Messages[0].Content != "new-new-new" {
		t.Fatalf("同尺寸替换后应看到新内容，得到 %+v", chat.Messages)
	}

	// 运行中再次替换：同一实例的下一轮追平检测到身份变化，整体重建。
	third := journalLine(TypeMessage, "msg00000003", tsB, map[string]string{"from": "operator", "to": "ada", "content": "3rd-3rd-3rd"})
	if len(third) != len(newMsg) {
		t.Fatalf("测试构造错误：第三版本行长度 %d != %d", len(third), len(newMsg))
	}
	swap2 := tl.Path + ".swap2"
	if err := os.WriteFile(swap2, []byte(head+"\n"+third+"\n"), 0o644); err != nil {
		t.Fatalf("写第二份替换文件: %v", err)
	}
	if err := os.Rename(swap2, tl.Path); err != nil {
		t.Fatalf("rename 二次替换: %v", err)
	}
	chat3, err := ix2.ChatView(200, "", "ada")
	if err != nil {
		t.Fatalf("ChatView: %v", err)
	}
	if len(chat3.Messages) != 1 || chat3.Messages[0].Content != "3rd-3rd-3rd" {
		t.Fatalf("运行中替换后应重建看到新内容，得到 %+v", chat3.Messages)
	}
}

func TestIndexTruncateRebuild(t *testing.T) {
	tl := rawTimeline(t)
	head := journalLine(TypeTrajectory, "tid00000000", tsA, map[string]string{"slug": "t"})
	m1 := journalLine(TypeMessage, "msg00000001", tsB, map[string]string{"from": "operator", "to": "ada", "content": "hi"})
	m2 := journalLine(TypeMessage, "msg00000002", tsC, map[string]string{"from": "operator", "to": "ada", "content": "yo"})
	writeJournal(t, tl, head, m1, m2)

	ix := openIndex(t, tl)
	if err := ix.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// 运行中截断：同一实例的下一轮追平必须整体重建（size < offset），
	// 不能让游标停在越界偏移上。
	writeJournal(t, tl, head, m1)
	if n, err := ix.StepCount(); err != nil || n != 2 {
		t.Fatalf("运行中截断后 StepCount = %d, %v，应为 2", n, err)
	}
	chat, _ := ix.ChatView(200, "", "ada")
	if len(chat.Messages) != 1 || chat.Messages[0].Content != "hi" {
		t.Fatalf("运行中截断后消息 = %+v", chat.Messages)
	}

	// 新实例：缓存偏移超出文件大小同样触发重建。
	ix2 := openIndex(t, tl)
	if n, err := ix2.StepCount(); err != nil || n != 2 {
		t.Fatalf("截断重建后 StepCount = %d, %v，应为 2", n, err)
	}
	chat2, _ := ix2.ChatView(200, "", "ada")
	if len(chat2.Messages) != 1 || chat2.Messages[0].Content != "hi" {
		t.Fatalf("截断后消息 = %+v", chat2.Messages)
	}
}

func TestIndexMissingFile(t *testing.T) {
	tl := rawTimeline(t) // 文件尚未创建
	ix := openIndex(t, tl)
	if n, err := ix.StepCount(); err != nil || n != 0 {
		t.Fatalf("文件缺失时 StepCount = %d, %v，应为 0", n, err)
	}
	// 文件随后出现：追平即见。
	head := journalLine(TypeTrajectory, "tid00000000", tsA, map[string]string{"slug": "t"})
	writeJournal(t, tl, head)
	if n, _ := ix.StepCount(); n != 1 {
		t.Fatalf("文件出现后 StepCount = %d，应为 1", n)
	}
}

// TestIndexProjections 覆盖 d2 消费方（activity/tree/thinkers）需要的
// 新增投影：_last_ts、最后 launched_by、per-thinker 聚合与 fork refs，
// 以及它们的缓存往返。
func TestIndexProjections(t *testing.T) {
	tl := rawTimeline(t)
	head := journalLine(TypeTrajectory, "tid00000000", tsA, map[string]string{"slug": "t"})
	// 空串 TS 的步骤（外部写入）放在末尾：若空串覆盖 lastTS，最终值会从
	// tsD 变成空串——它是“不覆盖”守卫的有效哨兵。
	noTS := `{"type":"action","step_id":"act00000000","launched_by":"solo","ts":""}`
	a1 := journalLine(TypeAction, "act00000001", tsB, map[string]string{"launched_by": "solo"})
	f1 := journalLine(TypeFork, "frk00000001", tsB, map[string]string{"child_ref": "sub1/trajectory.jsonl"})
	a2 := journalLine(TypeAction, "act00000002", tsC, map[string]string{"launched_by": "fast"})
	f2 := journalLine(TypeFork, "frk00000002", tsC, map[string]string{"child_ref": "sub2/trajectory.jsonl"})
	f3 := journalLine(TypeFork, "frk00000003", tsC, map[string]string{}) // 无 child_ref：跳过
	f4 := journalLine(TypeFork, "frk00000004", tsC, map[string]string{"child_ref": ""})
	a3 := journalLine(TypeAction, "act00000003", tsD, map[string]string{"launched_by": "solo"})
	writeJournal(t, tl, head, a1, f1, a2, f2, f3, f4, a3, noTS)

	ix := openIndex(t, tl)

	// LastTS：最后一条非空 TS——末尾的空串步骤不得覆盖 tsD。
	if got, err := ix.LastTS(); err != nil || got != tsD {
		t.Fatalf("LastTS = %q, %v，应为 %q（空串 TS 不得覆盖）", got, err, tsD)
	}
	// LastLaunchedBy：最后一条非空 launched_by。
	if got, ok, err := ix.LastLaunchedBy(); err != nil || !ok || got != "solo" {
		t.Fatalf("LastLaunchedBy = %q, %v, %v，应为 solo", got, ok, err)
	}
	// Thinkers：按 name 排序；Count 含无 ts 步骤；LastTS 取空序 max。
	thinkers, err := ix.Thinkers()
	if err != nil {
		t.Fatalf("Thinkers: %v", err)
	}
	if len(thinkers) != 2 || thinkers[0].Name != "fast" || thinkers[1].Name != "solo" {
		t.Fatalf("Thinkers = %+v，应为排序 [fast solo]", thinkers)
	}
	if thinkers[0].Count != 1 || thinkers[0].LastTS != tsC {
		t.Fatalf("fast = %+v，应为 count=1 last=%s", thinkers[0], tsC)
	}
	if thinkers[1].Count != 3 || thinkers[1].LastTS != tsD {
		t.Fatalf("solo = %+v，应为 count=3 last=%s（空串 TS 不覆盖）", thinkers[1], tsD)
	}
	// ForkRefs：按出现序、空 child_ref 跳过。
	forks, err := ix.ForkRefs()
	if err != nil {
		t.Fatalf("ForkRefs: %v", err)
	}
	if len(forks) != 2 || forks[0] != "sub1/trajectory.jsonl" || forks[1] != "sub2/trajectory.jsonl" {
		t.Fatalf("ForkRefs = %v", forks)
	}

	// 缓存 round-trip：四个投影全部恢复。
	if err := ix.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	ix2 := openIndex(t, tl)
	if got, _ := ix2.LastTS(); got != tsD {
		t.Fatalf("恢复后 LastTS = %q", got)
	}
	if got, ok, _ := ix2.LastLaunchedBy(); !ok || got != "solo" {
		t.Fatalf("恢复后 LastLaunchedBy = %q, %v", got, ok)
	}
	th2, _ := ix2.Thinkers()
	if len(th2) != 2 || th2[1].Count != 3 || th2[1].LastTS != tsD {
		t.Fatalf("恢复后 Thinkers = %+v", th2)
	}
	fk2, _ := ix2.ForkRefs()
	if len(fk2) != 2 || fk2[0] != "sub1/trajectory.jsonl" {
		t.Fatalf("恢复后 ForkRefs = %v", fk2)
	}

	// 空轨迹：从未有 launched_by 步骤 → ok=false、Thinkers 为空。
	tl2 := rawTimeline(t)
	writeJournal(t, tl2, journalLine(TypeTrajectory, "tid00000001", tsA, map[string]string{"slug": "u"}))
	ix3 := openIndex(t, tl2)
	if _, ok, err := ix3.LastLaunchedBy(); err != nil || ok {
		t.Fatalf("无 launched_by 时 LastLaunchedBy ok = %v, %v，应为 false", ok, err)
	}
	if got, _ := ix3.Thinkers(); len(got) != 0 {
		t.Fatalf("无 launched_by 时 Thinkers = %v，应为空", got)
	}
}
