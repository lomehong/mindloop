// 增量 BM25 索引：按目录缓存每个记忆文件的分词统计，文件以
// size+mtime 指纹复用——只有变化的文件重新切词，外部手工编辑
// （内容或时间戳变化）会被下一次查询的重新扫描发现。
package mem

import (
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// docStat 是一个文档的分词统计：词频与词元总长（含重复词——
// 标准 BM25 的 |D| 定义；用去重词数会让重复词多的长文归一化失真）。
type docStat struct {
	tf     map[string]int
	length int
}

// tokenCounts 对文档文本切词并统计词频与总长。文档文本 =
// summary + 正文 + 类型（与既有检索口径一致）。
func tokenCounts(m Memory) (map[string]int, int) {
	tf := make(map[string]int)
	length := 0
	for _, t := range tokenize(m.Summary + " " + m.Content + " " + m.Type) {
		tf[t]++
		length++
	}
	return tf, length
}

// fileEntry 是一个被索引的文件：磁盘指纹、解析结果与分词统计。
type fileEntry struct {
	size   int64
	mod    time.Time
	mem    Memory
	tf     map[string]int
	length int
}

// bm25Index 是一个记忆目录的增量索引。df 与 totalLen 是语料库级
// 的增量聚合（与 files 一一对应）；查询用它做 BM25 统计，df ≤ n
// 恒成立（n = 全库文件数），idf 不会被退出检索的条目挤成负数。
type bm25Index struct {
	mu       sync.Mutex
	files    map[string]*fileEntry
	df       map[string]int
	totalLen int
}

// bm25Indices 按目录绝对路径缓存索引：同一目录的不同写法共享。
var bm25Indices sync.Map // string -> *bm25Index

// indexFor 返回目录的索引，首次访问时创建。
func indexFor(dir string) *bm25Index {
	key, err := filepath.Abs(dir)
	if err != nil {
		key = dir
	}
	if v, ok := bm25Indices.Load(key); ok {
		return v.(*bm25Index)
	}
	ix := &bm25Index{files: map[string]*fileEntry{}, df: map[string]int{}}
	v, _ := bm25Indices.LoadOrStore(key, ix)
	return v.(*bm25Index)
}

// install 把一个文件挂入索引：df 每文件每词只记一次（document
// frequency 语义），totalLen 加文档长。
func (ix *bm25Index) install(name string, ent *fileEntry) {
	ix.files[name] = ent
	for t := range ent.tf {
		ix.df[t]++
	}
	ix.totalLen += ent.length
}

// drop 移除一个文件并回退它的统计（df 归零的键删掉，不残留）。
func (ix *bm25Index) drop(name string) {
	ent, ok := ix.files[name]
	if !ok {
		return
	}
	delete(ix.files, name)
	for t := range ent.tf {
		if ix.df[t]--; ix.df[t] <= 0 {
			delete(ix.df, t)
		}
	}
	ix.totalLen -= ent.length
}

// sync 重扫目录：size+mtime 指纹相同的文件复用缓存（零重切词），
// 变化与新增的重建，消失的移除，解析失败的退出索引——与
// ListDetailed 的跳过语义一致。目录不存在时清空不报错。
func (ix *bm25Index) sync(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		for name := range ix.files {
			ix.drop(name)
		}
		return
	}
	seen := make(map[string]bool, len(entries))
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".md") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		seen[name] = true
		if old, ok := ix.files[name]; ok && old.size == info.Size() && old.mod.Equal(info.ModTime()) {
			continue
		}
		m, perr := parseFile(filepath.Join(dir, name))
		if perr != nil {
			ix.drop(name)
			continue
		}
		tf, length := tokenCounts(m)
		ix.drop(name) // 先撤旧统计再装新
		ix.install(name, &fileEntry{
			size: info.Size(), mod: info.ModTime(), mem: m, tf: tf, length: length,
		})
	}
	for name := range ix.files {
		if !seen[name] {
			ix.drop(name)
		}
	}
}

// search 在索引上打分。候选是有效条目（失效/被替代/过期退出
// 检索），语料统计（df / n / avgLen）用全库——统计是语料级的，
// 不因条目退出而漂移；候选顺序与 ListDetailed 相同（写序号降序，
// 序号相同按文件名升序保持确定）。
func (ix *bm25Index) search(qTerms []string, topK int) []Scored {
	type cand struct {
		name string
		seq  int
	}
	cands := make([]cand, 0, len(ix.files))
	for name := range ix.files {
		cands = append(cands, cand{name: name, seq: seqOf(name)})
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].seq != cands[j].seq {
			return cands[i].seq > cands[j].seq
		}
		return cands[i].name < cands[j].name
	})
	now := time.Now()
	all := make([]Memory, 0, len(cands))
	stats := make([]docStat, 0, len(cands))
	for _, c := range cands {
		ent := ix.files[c.name]
		if !ent.mem.Active(now) {
			continue
		}
		all = append(all, ent.mem)
		stats = append(stats, docStat{tf: ent.tf, length: ent.length})
	}
	if len(all) == 0 {
		return nil
	}
	avgLen := float64(ix.totalLen) / float64(len(ix.files))
	return scoreBM25(all, stats, ix.df, len(ix.files), avgLen, qTerms, topK)
}

// scoreBM25 对候选文档打 BM25 分并取前 topK。df/n/avgLen 描述
// 语料库（全库），all/stats 是实际打分的候选——两者解耦保证
// df ≤ n，idf 恒正。查询词在候选中的 tf 全为零时无分（score>0
// 才输出）。
func scoreBM25(all []Memory, stats []docStat, df map[string]int, n int, avgLen float64, qTerms []string, topK int) []Scored {
	if avgLen == 0 {
		avgLen = 1
	}
	const k1, b = 1.2, 0.75
	nf := float64(n)
	var out []Scored
	for i, m := range all {
		var score float64
		for _, t := range qTerms {
			tf := float64(stats[i].tf[t])
			if tf == 0 {
				continue
			}
			idf := math.Log2(1 + (nf-float64(df[t])+0.5)/(float64(df[t])+0.5))
			norm := (tf * (k1 + 1)) / (tf + k1*(1-b+0.75*float64(stats[i].length)/avgLen))
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
	return out
}
