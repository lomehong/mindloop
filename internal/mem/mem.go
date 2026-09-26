// Package mem 是文件记忆库——Headlong 的 mem 的 Go 对应物，但检索
// 是真正的 BM25（k1=1.2, b=0.75，含 CJK 二元组切词），不再需要
// awk 预筛 + LLM 重排的两段式：纯本地打分，毫秒级，零成本。
//
// 记忆是 markdown 文件（YAML frontmatter + 正文），位于身份目录的
// memories/ 下——可 cat、可 grep、可 git。沙箱里的 agent 通过
// $MINDLOOP_EXE mem add ... 自己写记忆：记忆工具同时是 agent 的
// 工具和人手里的工具，同一套 CLI。
package mem

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"mindloop/internal/traj"

	"mindloop/internal/ids"
)

// Store 是一个记忆目录的句柄。
type Store struct{ Dir string }

// 记忆状态。空串按有效处理——旧文件没有 status 字段，必须继续
// 被检索看见（有效旧格式），失效是显式动作而不是默认。
const (
	StatusActive     = "active"
	StatusInvalid    = "invalid"
	StatusSuperseded = "superseded"
)

// Memory 是一条记忆。可选字段：来源步骤（provenance）、最近修订
// 时间、状态、失效时间与替代链——旧文件缺字段时全部取零值。
type Memory struct {
	ID           string
	Type         string
	Created      time.Time
	Updated      time.Time // 可选：最近一次修订时间
	Source       string    // 可选：来源轨迹步骤 ID
	Status       string    // 可选：""/active | invalid | superseded
	Expires      time.Time // 可选：失效时间（零值 = 不过期）
	Supersedes   string    // 可选：本修订替代的旧记忆 ID
	SupersededBy string    // 可选：替代本条的修订 ID
	Summary      string
	Content      string
	Path         string
}

// Active 判断记忆在 now 时刻是否有效：显式失效与被替代永不参与
// 检索，过期时间到点后自动退出。
func (m Memory) Active(now time.Time) bool {
	if m.Status != "" && m.Status != StatusActive {
		return false
	}
	if !m.Expires.IsZero() && !now.Before(m.Expires) {
		return false
	}
	return true
}

// 有效期类型集合（Headlong 的类型词汇表的子集）。
var ValidTypes = map[string]bool{
	"fact": true, "belief": true, "value": true, "preference": true,
	"todo": true, "objective": true, "person": true, "note": true,
}

// AddOpts 是写记忆的可选参数。
type AddOpts struct {
	Source  string    // 来源步骤 ID（可选，供追溯）
	Expires time.Time // 失效时间（可选，零值 = 不过期）
}

// Conflict 是一条与新内容高度相似的活动记忆：展示候选、要求显式
// 修订（revise），而不是让新写入无声盖过用户事实。
type Conflict struct {
	Memory     Memory
	Similarity float64
}

// Added 是一次写入的结果。Duplicate 为真表示完全相同内容已存在
// （未写盘）；Conflicts 列出需要人工/显式修订的相似候选。
type Added struct {
	Memory    Memory
	Duplicate bool
	Conflicts []Conflict
}

// Add 写入一条记忆（无可选字段的便捷形式）。
func (s Store) Add(ctx context.Context, typ, content string) (Memory, error) {
	a, err := s.AddWith(ctx, typ, content, AddOpts{})
	return a.Memory, err
}

// AddWith 写入一条记忆，返回写入结果。文件名以六位写序号开头
// ——序号在目录锁下取 max+1（Headlong 的 blob 计数器同款模式），
// 跨进程也保证写入顺序可排序：同毫秒写两条不该靠运气排先后。
func (s Store) AddWith(ctx context.Context, typ, content string, opts AddOpts) (Added, error) {
	if err := ctx.Err(); err != nil {
		return Added{}, err
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return Added{}, fmt.Errorf("mem: 内容为空")
	}
	if !ValidTypes[typ] {
		return Added{}, fmt.Errorf("mem: 未知类型 %q（可选 fact/belief/value/preference/todo/objective/person/note）", typ)
	}
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return Added{}, err
	}
	release, err := traj.AcquireDirLock(ctx, s.Dir+".lock", 5*time.Second)
	if err != nil {
		return Added{}, err
	}
	defer release()
	// 去重与冲突检查在锁内完成：与并发写入互斥。完全相同的
	// 内容不重复写盘；高度相似的返回候选提示（修改应走显式
	// Revise——新写入不会无声盖过已有事实）。
	all, _, err := s.ListDetailed()
	if err != nil {
		return Added{}, err
	}
	dup, conflicts := analyzeNew(all, content)
	if dup != nil {
		return Added{Memory: *dup, Duplicate: true}, nil
	}
	seq, err := s.nextSeq()
	if err != nil {
		return Added{}, err
	}
	id := ids.Short(ids.NewUUID(), 8)
	now := time.Now().UTC()
	summary := summarize(content)
	m := Memory{
		ID: id, Type: typ, Created: now, Summary: summary, Content: content,
		Source: opts.Source, Expires: opts.Expires.UTC(),
	}
	fname := fmt.Sprintf("%06d_%s.md", seq, id)
	path := filepath.Join(s.Dir, fname)
	if err := os.WriteFile(path, []byte(renderMemory(m)), 0o644); err != nil {
		return Added{}, err
	}
	m.Path = path
	return Added{Memory: m, Conflicts: conflicts}, nil
}

// conflictThreshold 是 token 集合 Jaccard 相似度阈值。校准样本
// （中文二元组）：“操作员张伟住在杭州” vs “操作员张伟住在上海”
// = 0.6；话题不同的记忆通常远低于 0.35。
const conflictThreshold = 0.5

// conflictLimit 单次提示最多几条候选。
const conflictLimit = 3

// analyzeNew 对照现有活动记忆检查新内容：完全相同的返回命中
// 条目（调用方不再写盘）；高度相似的按相似度降序返回候选。
// 失效/被替代/过期条目不参与——它们已退出判断集合。
func analyzeNew(all []Memory, content string) (*Memory, []Conflict) {
	now := time.Now()
	q := tokenSet(content)
	var conflicts []Conflict
	for i := range all {
		m := &all[i]
		if !m.Active(now) {
			continue
		}
		if strings.TrimSpace(m.Content) == content {
			return m, nil
		}
		if sim := jaccard(q, tokenSet(m.Summary+" "+m.Content+" "+m.Type)); sim >= conflictThreshold {
			conflicts = append(conflicts, Conflict{Memory: *m, Similarity: sim})
		}
	}
	sort.SliceStable(conflicts, func(i, j int) bool { return conflicts[i].Similarity > conflicts[j].Similarity })
	if len(conflicts) > conflictLimit {
		conflicts = conflicts[:conflictLimit]
	}
	return nil, conflicts
}

// tokenSet 把文本切成去重 token 集合（与 BM25 同一套切词）。
func tokenSet(s string) map[string]bool {
	set := make(map[string]bool)
	for _, t := range tokenize(s) {
		set[t] = true
	}
	return set
}

// jaccard 计算两个 token 集合的 Jaccard 相似度。
func jaccard(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	for t := range a {
		if b[t] {
			inter++
		}
	}
	return float64(inter) / float64(len(a)+len(b)-inter)
}

// summarize 取首行做摘要并按 80 字符截断。
func summarize(content string) string {
	summary := firstLine(content)
	if r := []rune(summary); len(r) > 80 {
		summary = string(r[:80]) + "…"
	}
	return summary
}

// renderMemory 渲染记忆文件全文。可选字段仅在非零时写入——
// 旧解析器跳过不认识的键，互不干扰。
func renderMemory(m Memory) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "id: %s\ntype: %s\ncreated: %s\nsummary: %s\n",
		m.ID, m.Type, m.Created.Format(trajTimeFormat), quoteYAML(m.Summary))
	if !m.Updated.IsZero() {
		fmt.Fprintf(&b, "updated: %s\n", quoteYAML(m.Updated.Format(trajTimeFormat)))
	}
	if m.Source != "" {
		fmt.Fprintf(&b, "source: %s\n", quoteYAML(m.Source))
	}
	if m.Status != "" && m.Status != StatusActive {
		fmt.Fprintf(&b, "status: %s\n", m.Status)
	}
	if !m.Expires.IsZero() {
		fmt.Fprintf(&b, "expires: %s\n", quoteYAML(m.Expires.Format(trajTimeFormat)))
	}
	if m.Supersedes != "" {
		fmt.Fprintf(&b, "supersedes: %s\n", m.Supersedes)
	}
	if m.SupersededBy != "" {
		fmt.Fprintf(&b, "superseded_by: %s\n", m.SupersededBy)
	}
	b.WriteString("---\n")
	b.WriteString(m.Content)
	b.WriteString("\n")
	return b.String()
}

// Revise 显式修订：写入新版本（supersedes 旧 id），并把旧版本
// 标记为被替代（superseded + superseded_by）。旧文件保留在盘上
// 作为修订记录；新版本继承类型与来源步骤，有效期重新起算。
// 只有活动条目能被修订——二次修订同一旧条会让修订链分叉。
func (s Store) Revise(ctx context.Context, id, content string) (Added, error) {
	if err := ctx.Err(); err != nil {
		return Added{}, err
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return Added{}, fmt.Errorf("mem: 内容为空")
	}
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return Added{}, err
	}
	release, err := traj.AcquireDirLock(ctx, s.Dir+".lock", 5*time.Second)
	if err != nil {
		return Added{}, err
	}
	defer release()
	old, err := s.mustActive(id)
	if err != nil {
		return Added{}, err
	}
	seq, err := s.nextSeq()
	if err != nil {
		return Added{}, err
	}
	newID := ids.Short(ids.NewUUID(), 8)
	now := time.Now().UTC()
	m := Memory{
		ID: newID, Type: old.Type, Created: now, Updated: now,
		Source: old.Source, Supersedes: id,
		Summary: summarize(content), Content: content,
	}
	path := filepath.Join(s.Dir, fmt.Sprintf("%06d_%s.md", seq, newID))
	if err := os.WriteFile(path, []byte(renderMemory(m)), 0o644); err != nil {
		return Added{}, err
	}
	m.Path = path
	// 标记旧版本：先写新文件再原子替换旧文件；替换失败则回滚
	// 新文件——盘上宁可没有新版本，也不要未标记的双活动版本。
	old.Status = StatusSuperseded
	old.SupersededBy = newID
	old.Updated = now
	if err := replaceFile(old.Path, []byte(renderMemory(old))); err != nil {
		os.Remove(path)
		return Added{}, err
	}
	return Added{Memory: m}, nil
}

// Invalidate 显式失效一条记忆：文件保留（审计），但退出检索。
func (s Store) Invalidate(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	release, err := traj.AcquireDirLock(ctx, s.Dir+".lock", 5*time.Second)
	if err != nil {
		return err
	}
	defer release()
	m, err := s.mustActive(id)
	if err != nil {
		return err
	}
	m.Status = StatusInvalid
	m.Updated = time.Now().UTC()
	return replaceFile(m.Path, []byte(renderMemory(m)))
}

// mustActive 在目录锁内按 id 定位有效记忆；不存在或已失效/被替代
// 时报错——显式操作需要明确对象，拒绝二次失效与链外修订。
func (s Store) mustActive(id string) (Memory, error) {
	all, _, err := s.ListDetailed()
	if err != nil {
		return Memory{}, err
	}
	now := time.Now()
	for _, m := range all {
		if m.ID != id {
			continue
		}
		if !m.Active(now) {
			return Memory{}, fmt.Errorf("mem: %s 已失效（status=%s），不能再次修订/失效", id, m.Status)
		}
		return m, nil
	}
	return Memory{}, fmt.Errorf("mem: 没有记忆 %q", id)
}

// replaceFile 原子替换文件内容：同目录临时文件 + rename。
// rename 在 Windows/NTFS 上覆盖已存在文件是原子操作。
func replaceFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // rename 成功后为 no-op
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// seqOf 从文件名解析六位写序号（无前缀或非数字前缀返回 0）。
func seqOf(name string) int {
	if len(name) >= 7 && name[6] == '_' {
		if n, err := strconv.Atoi(name[:6]); err == nil {
			return n
		}
	}
	return 0
}

// nextSeq 返回目录内最大写序号 + 1。调用方必须持有目录锁。
func (s Store) nextSeq() (int, error) {
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 1, nil
		}
		return 0, err
	}
	max := 0
	for _, e := range entries {
		name := e.Name()
		if len(name) < 7 || name[6] != '_' {
			continue
		}
		if n, err := strconv.Atoi(name[:6]); err == nil && n > max {
			max = n
		}
	}
	return max + 1, nil
}

// List 返回全部记忆，新的在前。排序键是文件名里的写序号——
// 跨进程单调递增（见 Add），不受时间戳精度限制。坏文件被静默
// 跳过（web 复用本签名）；需要感知坏文件时用 ListDetailed。
func (s Store) List() ([]Memory, error) {
	items, _, err := s.ListDetailed()
	return items, err
}

// ListDetailed 是 List 的诊断形态：解析失败的文件逐个记入 errs
// （含路径），不拖垮列表——记忆库坏一条不该掩盖其余全部。
func (s Store) ListDetailed() (items []Memory, errs []error, err error) {
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	type item struct {
		m   Memory
		seq int
	}
	var out []item
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".md") {
			continue
		}
		path := filepath.Join(s.Dir, name)
		m, perr := parseFile(path)
		if perr != nil {
			errs = append(errs, fmt.Errorf("mem: %s: %w", path, perr))
			continue // 坏文件跳过，不拖垮列表
		}
		out = append(out, item{m: m, seq: seqOf(name)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].seq > out[j].seq })
	memories := make([]Memory, len(out))
	for i, it := range out {
		memories[i] = it.m
	}
	return memories, errs, nil
}

// Scored 是一条检索命中。
type Scored struct {
	Memory
	Score float64
}

// Search 用 BM25 对查询打分，返回前 topK 条。分词统计按目录
// 增量缓存（仅 size+mtime 变化的文件重新切词）；检索默认排除
// 失效、过期与被替代项——记忆库是长期仓库，失效条目保留在盘上
// 供审计，但不应再影响判断。外部手工编辑在下一次查询重新扫描
// 时被发现。
func (s Store) Search(query string, topK int) ([]Scored, error) {
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}
	qTerms := tokenize(query)
	if len(qTerms) == 0 {
		return nil, nil
	}
	ix := indexFor(s.Dir)
	ix.mu.Lock()
	defer ix.mu.Unlock()
	ix.sync(s.Dir)
	if len(ix.files) == 0 {
		return nil, nil
	}
	return ix.search(qTerms, topK), nil
}

// Forget 按 id 删除记忆。
func (s Store) Forget(id string) error {
	all, err := s.List()
	if err != nil {
		return err
	}
	for _, m := range all {
		if m.ID == id {
			return os.Remove(m.Path)
		}
	}
	return fmt.Errorf("mem: 没有记忆 %q", id)
}

// tokenize 切词：ASCII 词元 + CJK 单字二元组。中文没有空格边界，
// 字二元组是检索实践中简单且有效的近似。
func tokenize(s string) []string {
	s = strings.ToLower(s)
	var out []string
	var word []rune
	var cjk []rune
	flushWord := func() {
		if len(word) > 1 { // 丢单字符 ASCII 词
			out = append(out, string(word))
		}
		word = word[:0]
	}
	flushCJK := func() {
		for i := 0; i+1 < len(cjk); i++ {
			out = append(out, string(cjk[i:i+2]))
		}
		if len(cjk) == 1 {
			out = append(out, string(cjk))
		}
		cjk = cjk[:0]
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_':
			flushCJK()
			word = append(word, r)
		case isCJK(r):
			flushWord()
			cjk = append(cjk, r)
			if len(cjk) > 64 { // 超长 CJK 段 flush 防内存放大
				tail := cjk[len(cjk)-2:]
				flushCJK()
				cjk = append(cjk, tail...)
			}
		default:
			flushWord()
			flushCJK()
		}
	}
	flushWord()
	flushCJK()
	return out
}

func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r)
}
