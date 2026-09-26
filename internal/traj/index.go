package traj

// 侧索引：轨迹 journal 的增量派生视图。Cursor 只负责"读到哪"，
// Index 负责"读到了什么"——步数、首步时间、final 旗标、launched_by
// 集合、消息流与 reply_to 结局。全部可在缓存失效时从 journal 重放
// 重建（日志永远是唯一事实源）。
//
// 缓存（index.json，与 journal 同目录）标记 schema 版本、源文件
// 身份与已消费偏移三项。恢复时三项必须同时成立——崩溃写坏、文件
// 截断、原位替换（哪怕同尺寸）、版本变化或偏移不落在行边界，都会
// 整体重建。查询方法先追平到文件已观测末尾再取数：缓存只加速
// 恢复，绝不让读取方看到过期数据。

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// IndexCacheVersion 是侧索引缓存的 schema 版本；索引布局或消费
// 语义变化时必须递增——旧缓存会因版本不符被整体重建。
const IndexCacheVersion = 2

// IndexCacheName 是侧索引缓存的文件名（轨迹目录内，与 journal 相邻）。
const IndexCacheName = "index.json"

// IndexCachePath 返回轨迹的侧索引缓存路径。
func (t *Timeline) IndexCachePath() string { return filepath.Join(t.Dir, IndexCacheName) }

// IndexMessage 是消息步骤的投影——chat 视图消费的全部字段。
// Status 标记 reply-status 收据：它提供结局事实，但不进消息流。
type IndexMessage struct {
	TS        string `json:"ts"`
	StepID    string `json:"step_id"`
	From      string `json:"from,omitempty"`
	To        string `json:"to,omitempty"`
	Content   string `json:"content,omitempty"`
	ReplyTo   string `json:"reply_to,omitempty"`
	Filename  string `json:"filename,omitempty"`
	SourceURL string `json:"source_url,omitempty"`
	Status    bool   `json:"status,omitempty"`
	State     string `json:"state,omitempty"`
}

// ChatData 是 ChatView 的结果：消息流（不含收据）+ 入站消息结局。
type ChatData struct {
	Messages []IndexMessage
	Outcomes map[string]string
}

// IndexSummary 是一次追平后的轨迹概览。
type IndexSummary struct {
	StepCount  int
	FirstTS    string
	HasFinal   bool
	LaunchedBy []string
}

// ThinkerStat 是单个 thinker 的聚合投影：唤醒步数与最近一次时间戳
// （空序 max，空 TS 不覆盖）。
type ThinkerStat struct {
	Name   string `json:"name"`
	Count  int    `json:"count"`
	LastTS string `json:"last_ts,omitempty"`
}

// Index 是轨迹 journal 的增量侧索引。方法并发安全；查询方法各自
// 先追平，调用方无需关心读取时机。
type Index struct {
	mu        sync.Mutex
	path      string
	cachePath string
	cur       *Cursor

	rewinds int

	stepCount      int
	firstTS        string
	lastTS         string
	hasFinal       bool
	launchedBy     map[string]bool
	lastLaunchedBy string
	thinkers       map[string]*ThinkerStat
	forks          []string
	messages       []IndexMessage
	replies        map[string]bool   // 被回复 step_id → 存在真回复
	receipts       map[string]string // 被回复 step_id → "no-reply" / "failed"
}

// indexCache 是缓存的持久化形态。
type indexCache struct {
	Schema         int               `json:"schema"`
	FileID         string            `json:"file_id"`
	Offset         int64             `json:"offset"`
	StepCount      int               `json:"step_count"`
	FirstTS        string            `json:"first_ts"`
	LastTS         string            `json:"last_ts,omitempty"`
	HasFinal       bool              `json:"has_final"`
	LaunchedBy     []string          `json:"launched_by,omitempty"`
	LastLaunchedBy string            `json:"last_launched_by,omitempty"`
	Thinkers       []ThinkerStat     `json:"thinkers,omitempty"`
	Forks          []string          `json:"forks,omitempty"`
	Replies        []string          `json:"replies,omitempty"`
	Receipts       map[string]string `json:"receipts,omitempty"`
	Messages       []IndexMessage    `json:"messages,omitempty"`
}

// OpenIndex 打开 t 的侧索引：缓存三项标记（schema/身份/偏移）全部
// 成立时从中恢复（不重扫 journal），否则整体重建——重建就是从头
// 追平一次，两边的内存状态最终一致。
func OpenIndex(t *Timeline) (*Index, error) {
	ix := &Index{
		path:       t.Path,
		cachePath:  t.IndexCachePath(),
		cur:        NewCursor(t.Path),
		launchedBy: map[string]bool{},
		thinkers:   map[string]*ThinkerStat{},
		replies:    map[string]bool{},
		receipts:   map[string]string{},
	}
	if st, ok := ix.loadCache(); ok {
		ix.applyCache(st)
		return ix, nil
	}
	if _, err := ix.catchUpLocked(); err != nil {
		return nil, err
	}
	return ix, nil
}

// loadCache 读并验证缓存。任何一项不符（缺失、损坏、版本、身份、
// 截断、行中间偏移）都返回 false，由调用方走重建。
func (ix *Index) loadCache() (indexCache, bool) {
	var st indexCache
	if ix.cachePath == "" || ix.path == "" {
		return st, false
	}
	data, err := os.ReadFile(ix.cachePath)
	if err != nil {
		return st, false
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return st, false
	}
	if st.Schema != IndexCacheVersion || st.Offset < 0 {
		return st, false
	}
	// 身份缺失的缓存保守拒绝：没有身份就无法检测替换。
	if st.FileID == "" {
		return st, false
	}
	f, err := os.Open(ix.path)
	if err != nil {
		return st, false
	}
	defer f.Close()
	if id := fileIdentity(f); id == "" || id != st.FileID {
		return st, false
	}
	fi, err := f.Stat()
	if err != nil || fi.Size() < st.Offset {
		return st, false
	}
	// 偏移必须落在完整行边界——从行中间恢复会产生错位的重放。
	if st.Offset > 0 {
		if _, err := f.Seek(st.Offset-1, io.SeekStart); err != nil {
			return st, false
		}
		var last [1]byte
		if _, err := io.ReadFull(f, last[:]); err != nil || last[0] != '\n' {
			return st, false
		}
	}
	return st, true
}

func (ix *Index) applyCache(st indexCache) {
	ix.cur = &Cursor{path: ix.path, offset: st.Offset, fileID: st.FileID}
	ix.stepCount = st.StepCount
	ix.firstTS = st.FirstTS
	ix.lastTS = st.LastTS
	ix.hasFinal = st.HasFinal
	for _, by := range st.LaunchedBy {
		ix.launchedBy[by] = true
	}
	ix.lastLaunchedBy = st.LastLaunchedBy
	for _, t := range st.Thinkers {
		cp := t
		ix.thinkers[t.Name] = &cp
	}
	ix.forks = st.Forks
	for _, r := range st.Replies {
		ix.replies[r] = true
	}
	for k, v := range st.Receipts {
		ix.receipts[k] = v
	}
	ix.messages = st.Messages
}

// catchUpLocked 增量消费到文件已观测末尾；文件被替换或截断时先
// 清空全部派生状态再重放本批（本批即全量）。
func (ix *Index) catchUpLocked() (int, error) {
	steps, rewound, err := ix.cur.readNew()
	if err != nil {
		return 0, err
	}
	if rewound {
		ix.rewinds++
		ix.reset()
	}
	for _, s := range steps {
		ix.consume(s)
	}
	return len(steps), nil
}

func (ix *Index) reset() {
	ix.stepCount = 0
	ix.firstTS = ""
	ix.lastTS = ""
	ix.hasFinal = false
	ix.launchedBy = map[string]bool{}
	ix.lastLaunchedBy = ""
	ix.thinkers = map[string]*ThinkerStat{}
	ix.forks = nil
	ix.messages = nil
	ix.replies = map[string]bool{}
	ix.receipts = map[string]string{}
}

func (ix *Index) consume(s Step) {
	ix.stepCount++
	if ix.stepCount == 1 {
		ix.firstTS = s.TS
	}
	if s.TS != "" {
		ix.lastTS = s.TS
	}
	if by, ok := s.Field("launched_by"); ok && by != "" {
		ix.launchedBy[by] = true
		ix.lastLaunchedBy = by
		st := ix.thinkers[by]
		if st == nil {
			st = &ThinkerStat{Name: by}
			ix.thinkers[by] = st
		}
		st.Count++
		if s.TS != "" && s.TS > st.LastTS {
			st.LastTS = s.TS
		}
	}
	switch s.Type {
	case TypeFinal:
		ix.hasFinal = true
	case TypeMessage:
		ix.consumeMessage(s)
	case TypeFork:
		if ref, ok := s.Field("child_ref"); ok && ref != "" {
			ix.forks = append(ix.forks, ref)
		}
	}
}

func (ix *Index) consumeMessage(s Step) {
	src, _ := s.Field("source")
	rt, _ := s.Field("reply_to")
	m := IndexMessage{
		TS:      s.TS,
		StepID:  s.StepID,
		Status:  src == "reply-status",
		ReplyTo: rt,
	}
	m.From, _ = s.Field("from")
	m.To, _ = s.Field("to")
	m.Content, _ = s.Field("content")
	m.Filename, _ = s.Field("filename")
	m.SourceURL, _ = s.Field("source_url")
	m.State, _ = s.Field("state")
	ix.messages = append(ix.messages, m)
	if rt == "" {
		return
	}
	if m.Status {
		switch m.State {
		case "no-reply":
			ix.receipts[rt] = "no-reply"
		case "reply-failed":
			ix.receipts[rt] = "failed"
		}
		return
	}
	ix.replies[rt] = true
}

// CatchUp 追平到文件已观测末尾，返回本次消费的新步骤数。
// 查询方法内部各自追平；显式调用用于观察者驱动或预热。
func (ix *Index) CatchUp() (int, error) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	return ix.catchUpLocked()
}

// StepCount 返回已索引的可解析步骤数（坏行跳过，与 Steps 计数一致）。
func (ix *Index) StepCount() (int, error) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if _, err := ix.catchUpLocked(); err != nil {
		return 0, err
	}
	return ix.stepCount, nil
}

// FirstTS 返回第一个可解析步骤的时间戳（空 = 尚无步骤）。
func (ix *Index) FirstTS() (string, error) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if _, err := ix.catchUpLocked(); err != nil {
		return "", err
	}
	return ix.firstTS, nil
}

// HasFinal 报告轨迹是否含 final 步骤。
func (ix *Index) HasFinal() (bool, error) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if _, err := ix.catchUpLocked(); err != nil {
		return false, err
	}
	return ix.hasFinal, nil
}

// LaunchedBy 返回去重排序的 launched_by 集合。
func (ix *Index) LaunchedBy() ([]string, error) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if _, err := ix.catchUpLocked(); err != nil {
		return nil, err
	}
	return ix.launchedByList(), nil
}

// Summary 一次追平后返回计数与旗标——同一请求的多次读取不重复追平。
func (ix *Index) Summary() (IndexSummary, error) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if _, err := ix.catchUpLocked(); err != nil {
		return IndexSummary{}, err
	}
	return IndexSummary{
		StepCount:  ix.stepCount,
		FirstTS:    ix.firstTS,
		HasFinal:   ix.hasFinal,
		LaunchedBy: ix.launchedByList(),
	}, nil
}

// LastTS 返回最后一条带非空时间戳的步骤的 TS（空 = 尚无步骤）。
func (ix *Index) LastTS() (string, error) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if _, err := ix.catchUpLocked(); err != nil {
		return "", err
	}
	return ix.lastTS, nil
}

// LastLaunchedBy 返回最后一条带非空 launched_by 的步骤的值；
// 从未出现过时 ok=false。
func (ix *Index) LastLaunchedBy() (string, bool, error) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if _, err := ix.catchUpLocked(); err != nil {
		return "", false, err
	}
	if ix.lastLaunchedBy == "" {
		return "", false, nil
	}
	return ix.lastLaunchedBy, true, nil
}

// Thinkers 返回按 name 排序的 per-thinker 聚合。
func (ix *Index) Thinkers() ([]ThinkerStat, error) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if _, err := ix.catchUpLocked(); err != nil {
		return nil, err
	}
	return ix.thinkerList(), nil
}

// ForkRefs 返回 fork 步骤的 child_ref 列表（按出现序，空值跳过）。
func (ix *Index) ForkRefs() ([]string, error) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if _, err := ix.catchUpLocked(); err != nil {
		return nil, err
	}
	if ix.forks == nil {
		return nil, nil
	}
	out := make([]string, len(ix.forks))
	copy(out, ix.forks)
	return out, nil
}

func (ix *Index) launchedByList() []string {
	out := make([]string, 0, len(ix.launchedBy))
	for by := range ix.launchedBy {
		out = append(out, by)
	}
	sort.Strings(out)
	return out
}

func (ix *Index) thinkerList() []ThinkerStat {
	out := make([]ThinkerStat, 0, len(ix.thinkers))
	for _, t := range ix.thinkers {
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ChatView 复刻 handleChat 的语义：消息流排除状态收据、with 过滤
// 只留与某人的对话（对方发来的 + 我回给对方的）、tail 截取最近 N
// 条；outcomes 覆盖返回窗口内入站消息的结局——存在真回复则为
// "replied"，responder 收据投影 "no-reply"/"failed"，两者皆无
// 留空 = 未决（诚实而非编造）。逆序窗口扫描：只复制最近的 tail
// 条，到量即停——查询成本不随日志规模增长。
func (ix *Index) ChatView(tail int, with, self string) (ChatData, error) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if _, err := ix.catchUpLocked(); err != nil {
		return ChatData{}, err
	}
	capHint := tail
	if capHint <= 0 || capHint > len(ix.messages) {
		capHint = len(ix.messages)
	}
	data := ChatData{Messages: make([]IndexMessage, 0, capHint), Outcomes: map[string]string{}}
	for i := len(ix.messages) - 1; i >= 0 && (tail <= 0 || len(data.Messages) < tail); i-- {
		m := ix.messages[i]
		if m.Status {
			continue // 状态收据是元事实，不进入消息流
		}
		if with != "" && m.From != with && !(m.From == self && m.To == with) {
			continue
		}
		data.Messages = append(data.Messages, m)
		if m.From != self && m.From != "" {
			if ix.replies[m.StepID] {
				data.Outcomes[m.StepID] = "replied"
			} else if v := ix.receipts[m.StepID]; v != "" {
				data.Outcomes[m.StepID] = v
			}
		}
	}
	// 逆序收集 → 反转回日志序。
	for i, j := 0, len(data.Messages)-1; i < j; i, j = i+1, j-1 {
		data.Messages[i], data.Messages[j] = data.Messages[j], data.Messages[i]
	}
	return data, nil
}

// Save 把索引状态原子写入缓存（同目录临时文件 + 重命名替换）——
// 崩溃写坏最多留下一个孤儿临时文件，正式缓存要么是旧的完整版、
// 要么是新的完整版。
func (ix *Index) Save() error {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	st := indexCache{
		Schema:         IndexCacheVersion,
		FileID:         ix.cur.fileID,
		Offset:         ix.cur.offset,
		StepCount:      ix.stepCount,
		FirstTS:        ix.firstTS,
		LastTS:         ix.lastTS,
		HasFinal:       ix.hasFinal,
		LaunchedBy:     ix.launchedByList(),
		LastLaunchedBy: ix.lastLaunchedBy,
		Thinkers:       ix.thinkerList(),
		Forks:          ix.forks,
		Messages:       ix.messages,
		Receipts:       ix.receipts,
	}
	for r := range ix.replies {
		st.Replies = append(st.Replies, r)
	}
	sort.Strings(st.Replies)
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.%d.tmp", ix.cachePath, os.Getpid())
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, ix.cachePath); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// Rewinds 返回索引因文件被替换或截断而整体重建的次数。
func (ix *Index) Rewinds() int {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	return ix.rewinds
}
