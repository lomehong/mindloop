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
	"math"
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

// Memory 是一条记忆。
type Memory struct {
	ID      string
	Type    string
	Created time.Time
	Summary string
	Content string
	Path    string
}

// 有效期类型集合（Headlong 的类型词汇表的子集）。
var ValidTypes = map[string]bool{
	"fact": true, "belief": true, "value": true, "preference": true,
	"todo": true, "objective": true, "person": true, "note": true,
}

// Add 写入一条记忆，返回其 id。文件名以六位写序号开头——序号在
// 目录锁下取 max+1（Headlong 的 blob 计数器同款模式），跨进程也
// 保证写入顺序可排序：同毫秒写两条不该靠运气排先后。
func (s Store) Add(ctx context.Context, typ, content string) (Memory, error) {
	if err := ctx.Err(); err != nil {
		return Memory{}, err
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return Memory{}, fmt.Errorf("mem: 内容为空")
	}
	if !ValidTypes[typ] {
		return Memory{}, fmt.Errorf("mem: 未知类型 %q（可选 fact/belief/value/preference/todo/objective/person/note）", typ)
	}
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return Memory{}, err
	}
	release, err := traj.AcquireDirLock(ctx, s.Dir+".lock", 5*time.Second)
	if err != nil {
		return Memory{}, err
	}
	defer release()
	seq, err := s.nextSeq()
	if err != nil {
		return Memory{}, err
	}
	id := ids.Short(ids.NewUUID(), 8)
	now := time.Now().UTC()
	summary := firstLine(content)
	if r := []rune(summary); len(r) > 80 {
		summary = string(r[:80]) + "…"
	}
	m := Memory{ID: id, Type: typ, Created: now, Summary: summary, Content: content}
	fname := fmt.Sprintf("%06d_%s.md", seq, id)
	path := filepath.Join(s.Dir, fname)
	body := fmt.Sprintf("---\nid: %s\ntype: %s\ncreated: %s\nsummary: %s\n---\n%s\n",
		id, typ, now.Format(trajTimeFormat), quoteYAML(summary), content)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return Memory{}, err
	}
	m.Path = path
	return m, nil
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
		seq := 0
		if len(name) >= 7 && name[6] == '_' {
			if n, err := strconv.Atoi(name[:6]); err == nil {
				seq = n
			}
		}
		out = append(out, item{m: m, seq: seq})
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

// Search 用 BM25 对查询打分，返回前 topK 条。
func (s Store) Search(query string, topK int) ([]Scored, error) {
	all, err := s.List()
	if err != nil {
		return nil, err
	}
	if len(all) == 0 || strings.TrimSpace(query) == "" {
		return nil, nil
	}
	qTerms := tokenize(query)
	if len(qTerms) == 0 {
		return nil, nil
	}

	// BM25 需要 df 与平均长度：一次扫全库。文档长度是词元总数
	//（含重复）——标准 BM25 的 |D| 定义；用去重词数会让重复词
	// 多的长文归一化失真。
	type doc struct {
		tf     map[string]int
		length int
	}
	docs := make([]doc, len(all))
	df := make(map[string]int)
	totalLen := 0
	for i, m := range all {
		d := doc{tf: make(map[string]int)}
		for _, t := range tokenize(m.Summary + " " + m.Content + " " + m.Type) {
			d.tf[t]++
			d.length++
		}
		for t := range d.tf {
			df[t]++
		}
		docs[i] = d
		totalLen += d.length
	}
	avgLen := float64(totalLen) / float64(len(docs))
	if avgLen == 0 {
		avgLen = 1
	}
	const k1, b = 1.2, 0.75
	n := float64(len(docs))

	var out []Scored
	for i, m := range all {
		var score float64
		for _, t := range qTerms {
			tf := float64(docs[i].tf[t])
			if tf == 0 {
				continue
			}
			idf := math.Log2(1 + (n-float64(df[t])+0.5)/(float64(df[t])+0.5))
			norm := (tf * (k1 + 1)) / (tf + k1*(1-b+0.75*float64(docs[i].length)/avgLen))
			score += idf * norm
		}
		if score > 0 {
			out = append(out, Scored{Memory: m, Score: score})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if topK > 0 && len(out) > topK {
		out = out[:topK]
	}
	return out, nil
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
