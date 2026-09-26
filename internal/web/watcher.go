package web

// 共享流观察器：每个身份在 web 进程内共享一套文件观察与广播——
// status（replying）、working、旁路增量文本、轨迹尾随四类事件由
// 一个 goroutine 采集，带序号广播给全部订阅者；无订阅者时观察器
// 释放。订阅者各持有有界缓冲，慢订阅者被弃用而不是阻塞广播；
// 断线重连按 Last-Event-ID 从事件环形缓冲续发，超出窗口则回退
// 到"当前状态快照"（status/working/每个旁路全量，delta offset=0
// 即重建语义——复用既有事件类型，客户端无需新协议分支）。

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"mindloop/internal/identity"
	"mindloop/internal/mind"
	"mindloop/internal/traj"
)

// sseFrame 是一帧待写出的 SSE 事件。seq=0 表示快照帧——它是订阅
// 时刻的当前状态而非历史事件，不占用事件序号、不进环形缓冲。
type sseFrame struct {
	seq   int64
	event string
	data  string
}

// streamHub 是 Server 级的订阅中心：按身份共享观察器，并记录
// 订阅者的断线节律以识别"短时间反复断线"的降级状态。
type streamHub struct {
	srv *Server

	mu    sync.Mutex
	byKey map[string]*identityWatcher
	churn map[string][]time.Time // key → 断线时刻（滚动窗口）

	// 以下参数在 New 时取默认值；测试可收窄加速。
	ringCap     int
	subCap      int
	churnWindow time.Duration
	churnLimit  int
}

// streamSub 是单个订阅者：有界通道承载广播帧。缓冲打满 = 订阅者
// 消费不过来（慢客户端/断网），观察器弃用它而不是等待。
type streamSub struct {
	w      *identityWatcher
	ch     chan sseFrame
	lagged bool // 被弃用（emit 已关闭 ch）；unsubscribe 不重复关闭
}

// streamStats 汇总观察器状态（/health 与测试使用）。
type streamStats struct {
	Watchers    int
	Subscribers int
	Degraded    bool // 任一身份在窗口内反复断线
}

func newStreamHub(srv *Server) *streamHub {
	return &streamHub{
		srv:         srv,
		byKey:       map[string]*identityWatcher{},
		churn:       map[string][]time.Time{},
		ringCap:     1024,
		subCap:      256,
		churnWindow: 60 * time.Second,
		churnLimit:  3,
	}
}

// pollEvery 取轮询周期：Server 的 replyPollEvery 为准（测试收窄）。
func (h *streamHub) pollEvery() time.Duration {
	if d := h.srv.replyPollEvery; d > 0 {
		return d
	}
	return 200 * time.Millisecond
}

// subscribe 挂载一个订阅者并返回预热帧：Last-Event-ID 落在事件缓冲
// 窗口内时返回缺口帧（续接，不发快照）；否则返回当前状态快照。
func (h *streamHub) subscribe(id *identity.Identity, lastID int64) (*streamSub, []sseFrame) {
	for {
		h.mu.Lock()
		w := h.byKey[id.Timeline.Dir]
		if w == nil {
			w = newIdentityWatcher(h, id)
			h.byKey[w.key] = w
			go w.run()
		}
		h.mu.Unlock()

		w.mu.Lock()
		if w.stopped {
			// 竞态：观察器恰在释放中——重建后重试。
			w.mu.Unlock()
			continue
		}
		sub := &streamSub{w: w, ch: make(chan sseFrame, w.subCap)}
		w.subs[sub] = true
		warm := w.warmFramesLocked(lastID)
		w.mu.Unlock()
		return sub, warm
	}
}

// unsubscribe 摘除订阅者；最后一个订阅者离开时释放观察器。断线
// 计入 churn 窗口（含正常断开——"短时间反复断线"本身就是信号）。
func (h *streamHub) unsubscribe(sub *streamSub) {
	w := sub.w
	if w == nil {
		return
	}
	w.mu.Lock()
	if _, ok := w.subs[sub]; ok {
		delete(w.subs, sub)
		if !sub.lagged {
			close(sub.ch)
		}
	}
	empty := len(w.subs) == 0
	if empty && !w.stopped {
		w.stopped = true
		close(w.done)
	}
	w.mu.Unlock()

	h.noteDisconnect(w.key)
	if empty {
		h.mu.Lock()
		if h.byKey[w.key] == w {
			delete(h.byKey, w.key)
		}
		h.mu.Unlock()
	}
}

// noteDisconnect 记录一次断线并识别降级：窗口内达到阈值时打日志
// （恰跨过阈值时一次）。降级是动态判定（stats 按窗口重算），窗口
// 滑过后自动恢复，无需显式清除。
func (h *streamHub) noteDisconnect(key string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	ds := h.churn[key]
	cut := 0
	for cut < len(ds) && now.Sub(ds[cut]) > h.churnWindow {
		cut++
	}
	ds = append(ds[cut:], now)
	h.churn[key] = ds
	if len(ds) == h.churnLimit {
		log.Printf("web: 身份 %q 的流订阅在 %v 内断开 %d 次——记入降级状态",
			key, h.churnWindow, len(ds))
	}
}

// stats 汇总当前观察器与订阅数；Degraded 为任一身份在 churn 窗口内
// 断线达到阈值。
func (h *streamHub) stats() streamStats {
	h.mu.Lock()
	now := time.Now()
	ws := make([]*identityWatcher, 0, len(h.byKey))
	degraded := false
	for _, w := range h.byKey {
		ws = append(ws, w)
	}
	for _, ds := range h.churn {
		n := 0
		for _, t := range ds {
			if now.Sub(t) <= h.churnWindow {
				n++
			}
		}
		if n >= h.churnLimit {
			degraded = true
			break
		}
	}
	h.mu.Unlock()

	st := streamStats{Watchers: len(ws), Degraded: degraded}
	for _, w := range ws {
		w.mu.Lock()
		st.Subscribers += len(w.subs)
		w.mu.Unlock()
	}
	return st
}

// identityWatcher 是单身份的共享观察状态与广播器。
type identityWatcher struct {
	hub *streamHub
	id  *identity.Identity
	key string // Timeline.Dir

	replyPath   string
	workingPath string
	streamDir   string
	tlPath      string

	mu      sync.Mutex
	subs    map[*streamSub]bool
	stopped bool
	done    chan struct{}

	// 观察状态（基线采样后仅"变化"才成为事件）
	lastStatus  string // replying 的 reply_to；"" = 无
	lastWorking string // working 文件原文；"" = 无
	sizes       map[string]int64
	tlOffset    int64
	tlPending   []byte

	// 事件日志：序号 + 环形缓冲（断线续接的窗口）
	seq     int64
	ring    []sseFrame
	ringCap int
	subCap  int
}

func newIdentityWatcher(h *streamHub, id *identity.Identity) *identityWatcher {
	ctl := mind.RunLockDir(id.Timeline.Dir)
	w := &identityWatcher{
		hub:         h,
		id:          id,
		key:         id.Timeline.Dir,
		replyPath:   filepath.Join(ctl, "replying"),
		workingPath: filepath.Join(ctl, "working"),
		streamDir:   filepath.Join(id.Timeline.Dir, "stream"),
		tlPath:      id.Timeline.Path,
		subs:        map[*streamSub]bool{},
		done:        make(chan struct{}),
		sizes:       map[string]int64{},
		ringCap:     h.ringCap,
		subCap:      h.subCap,
	}
	if w.ringCap <= 0 {
		w.ringCap = 1024
	}
	if w.subCap <= 0 {
		w.subCap = 256
	}
	w.sampleBaseline()
	return w
}

// sampleBaseline 在不广播的前提下采一次当前状态：已存在的内容由
// 订阅时刻的快照交付，观察器只把后续"变化"作为事件——轨迹尾随
// 同样从当前 EOF 起步（历史不重放）。
func (w *identityWatcher) sampleBaseline() {
	w.lastStatus = readReplyingReplyTo(w.replyPath)
	if data, err := os.ReadFile(w.workingPath); err == nil {
		w.lastWorking = string(data)
	}
	if entries, err := os.ReadDir(w.streamDir); err == nil {
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".txt") {
				continue
			}
			if fi, err := e.Info(); err == nil {
				w.sizes[strings.TrimSuffix(e.Name(), ".txt")] = fi.Size()
			}
		}
	}
	if fi, err := os.Stat(w.tlPath); err == nil {
		w.tlOffset = fi.Size()
	}
}

// run 是观察循环：周期追平文件变化并广播，直到最后一个订阅者
// 离开（unsubscribe 关闭 done）。
func (w *identityWatcher) run() {
	t := time.NewTicker(w.hub.pollEvery())
	defer t.Stop()
	for {
		select {
		case <-w.done:
			return
		case <-t.C:
			w.pollOnce()
		}
	}
}

func (w *identityWatcher) pollOnce() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped {
		return
	}
	w.pollStatusLocked()
	w.pollStreamsLocked()
	w.pollWorkingLocked()
	w.pollTrailLocked()
}

func (w *identityWatcher) pollStatusLocked() {
	cur := readReplyingReplyTo(w.replyPath)
	if cur == w.lastStatus {
		return
	}
	w.lastStatus = cur
	payload, _ := json.Marshal(map[string]any{"replying": cur != "", "reply_to": cur})
	w.emitLocked("status", string(payload))
}

// pollStreamsLocked 处理旁路文本：增长只发新增字节（切到完整
// UTF-8 前缀——写入侧的部分字符留到下轮）；缩短/替换回退全量
// （offset=0 的重建语义）；消失发 done。
func (w *identityWatcher) pollStreamsLocked() {
	present := map[string]bool{}
	entries, err := os.ReadDir(w.streamDir)
	if err == nil {
		for _, e := range entries {
			name := e.Name()
			if !strings.HasSuffix(name, ".txt") {
				continue
			}
			rt := strings.TrimSuffix(name, ".txt")
			present[rt] = true
			fi, ierr := e.Info()
			if ierr != nil {
				continue
			}
			prev, size := w.sizes[rt], fi.Size()
			if size == prev {
				continue
			}
			path := filepath.Join(w.streamDir, name)
			if size < prev {
				data, rerr := os.ReadFile(path)
				if rerr != nil {
					continue // 读取竞态：下个周期重试
				}
				w.sizes[rt] = size
				w.emitDeltaLocked(rt, 0, string(data))
				continue
			}
			f, oerr := os.Open(path)
			if oerr != nil {
				continue
			}
			chunk := make([]byte, size-prev)
			n, rerr := f.ReadAt(chunk, prev)
			f.Close()
			if rerr != nil && rerr != io.EOF {
				continue
			}
			chunk = chunk[:n]
			chunk = chunk[:utf8PrefixLen(chunk)]
			if len(chunk) == 0 {
				continue // 只有半个字符：留到下轮拼接
			}
			w.sizes[rt] = prev + int64(len(chunk))
			w.emitDeltaLocked(rt, prev, string(chunk))
		}
	}
	for rt := range w.sizes {
		if !present[rt] {
			delete(w.sizes, rt)
			payload, _ := json.Marshal(map[string]any{"reply_to": rt})
			w.emitLocked("done", string(payload))
		}
	}
}

func (w *identityWatcher) emitDeltaLocked(rt string, offset int64, text string) {
	payload, _ := json.Marshal(map[string]any{"reply_to": rt, "offset": offset, "text": text})
	w.emitLocked("delta", string(payload))
}

func (w *identityWatcher) pollWorkingLocked() {
	raw, err := os.ReadFile(w.workingPath)
	sig := string(raw)
	if err != nil {
		sig = ""
	}
	if sig == w.lastWorking {
		return
	}
	w.lastWorking = sig
	w.emitLocked("working", w.workingPayloadLocked())
}

// workingPayloadLocked 组装 working 事件载荷：原文按对象解析，
// 解析失败或 working=false 一律按闲；busy 空数组而非 null。
func (w *identityWatcher) workingPayloadLocked() string {
	var parsed struct {
		Working bool          `json:"working"`
		Busy    []workingBusy `json:"busy"`
	}
	working := false
	var busy []workingBusy
	if w.lastWorking != "" && json.Unmarshal([]byte(w.lastWorking), &parsed) == nil {
		working = parsed.Working
		busy = parsed.Busy
	}
	if busy == nil {
		busy = []workingBusy{}
	}
	payload, _ := json.Marshal(map[string]any{"working": working, "busy": busy})
	return string(payload)
}

// pollTrailLocked 尾随 trajectory.jsonl：读新增字节、按 \n 切完整行
// （末尾不完整行留到下轮），逐行下发 step 事件。文件收缩（理论不
// 发生）：重置到新 EOF，宁可少发不误发。
func (w *identityWatcher) pollTrailLocked() {
	fi, err := os.Stat(w.tlPath)
	if err != nil {
		return
	}
	size := fi.Size()
	if size == w.tlOffset {
		return
	}
	if size < w.tlOffset {
		w.tlOffset = size
		w.tlPending = nil
		return
	}
	f, err := os.Open(w.tlPath)
	if err != nil {
		return
	}
	chunk := make([]byte, size-w.tlOffset)
	_, rerr := f.ReadAt(chunk, w.tlOffset)
	f.Close()
	if rerr != nil && rerr != io.EOF {
		return
	}
	w.tlOffset = size
	data := append(w.tlPending, chunk...)
	w.tlPending = nil
	for {
		idx := bytes.IndexByte(data, '\n')
		if idx < 0 {
			w.tlPending = append(w.tlPending, data...)
			break
		}
		w.emitStepLocked(data[:idx])
		data = data[idx+1:]
	}
}

// emitStepLocked 解析一行轨迹 JSON 并下发 step 事件；解析失败跳过。
// trajectory/run/prompt 是结构性步骤，不进活动流；任务执行步骤按
// 需附带 task_id/run_id/attempt 归因（无归因不带键）。
func (w *identityWatcher) emitStepLocked(line []byte) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return
	}
	var raw struct {
		StepID  string          `json:"step_id"`
		Type    string          `json:"type"`
		TS      string          `json:"ts"`
		Content json.RawMessage `json:"content"`
		TaskID  string          `json:"task_id"`
		RunID   string          `json:"run_id"`
		Attempt json.RawMessage `json:"attempt"`
	}
	if json.Unmarshal(line, &raw) != nil {
		return
	}
	switch raw.Type {
	case traj.TypeTrajectory, traj.TypeRun, traj.TypePrompt:
		return
	}
	content := ""
	if len(raw.Content) > 0 {
		// 仅当 content 是 JSON 字符串时取值；缺失或非字符串
		// （数字/对象）按契约保持空串。
		_ = json.Unmarshal(raw.Content, &content)
	}
	payload := map[string]any{
		"step_id": raw.StepID,
		"type":    raw.Type,
		"ts":      raw.TS,
		"excerpt": stepExcerpt(content),
	}
	if raw.TaskID != "" {
		payload["task_id"] = raw.TaskID
	}
	if raw.RunID != "" {
		payload["run_id"] = raw.RunID
	}
	if len(raw.Attempt) > 0 {
		payload["attempt"] = raw.Attempt
	}
	data, _ := json.Marshal(payload)
	w.emitLocked("step", string(data))
}

// emitLocked 递增序号、记入环形缓冲并广播。持 w.mu 调用。
func (w *identityWatcher) emitLocked(event, data string) {
	w.seq++
	f := sseFrame{seq: w.seq, event: event, data: data}
	w.ring = append(w.ring, f)
	if len(w.ring) > w.ringCap {
		w.ring = w.ring[len(w.ring)-w.ringCap:]
	}
	for sub := range w.subs {
		select {
		case sub.ch <- f:
		default:
			// 慢订阅者：弃用而不等待——客户端重连后由续接/快照恢复。
			delete(w.subs, sub)
			sub.lagged = true
			close(sub.ch)
		}
	}
}

// warmFramesLocked 生成订阅预热帧：可续接时给出缓冲中的缺口帧；
// 否则给出当前状态快照（status/working/每个现存旁路全量）。
func (w *identityWatcher) warmFramesLocked(lastID int64) []sseFrame {
	if lastID > 0 && w.canResumeLocked(lastID) {
		out := make([]sseFrame, 0, 8)
		for _, f := range w.ring {
			if f.seq > lastID {
				out = append(out, f)
			}
		}
		return out
	}
	return w.snapshotFramesLocked()
}

// canResumeLocked 报告 lastID 之后的帧是否都还在缓冲窗口内。
func (w *identityWatcher) canResumeLocked(lastID int64) bool {
	if lastID > w.seq {
		return false // 超前（观察器已重建）：无从续接
	}
	if lastID == w.seq {
		return true // 无缺口
	}
	if len(w.ring) == 0 {
		return false
	}
	return lastID+1 >= w.ring[0].seq
}

// snapshotFramesLocked 组装当前状态快照。status 恒为首帧（连接建立
// 契约）；delta 一律 offset=0 全量——客户端以"截断到 offset 再
// 追加"处理，快照与增量的客户端逻辑是同一套。
func (w *identityWatcher) snapshotFramesLocked() []sseFrame {
	out := make([]sseFrame, 0, 8)
	statusPayload, _ := json.Marshal(map[string]any{"replying": w.lastStatus != "", "reply_to": w.lastStatus})
	out = append(out, sseFrame{event: "status", data: string(statusPayload)})
	out = append(out, sseFrame{event: "working", data: w.workingPayloadLocked()})
	if entries, err := os.ReadDir(w.streamDir); err == nil {
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".txt") {
				continue
			}
			data, rerr := os.ReadFile(filepath.Join(w.streamDir, e.Name()))
			if rerr != nil {
				continue
			}
			rt := strings.TrimSuffix(e.Name(), ".txt")
			payload, _ := json.Marshal(map[string]any{"reply_to": rt, "offset": 0, "text": string(data)})
			out = append(out, sseFrame{event: "delta", data: string(payload)})
		}
	}
	return out
}

// readReplyingReplyTo 解析 <RunLockDir>/replying 的 reply_to；缺失、
// 坏 JSON 一律按"未在回复"（""）。
func readReplyingReplyTo(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var st struct {
		ReplyTo string `json:"reply_to"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(string(data))), &st) != nil {
		return ""
	}
	return st.ReplyTo
}

// utf8PrefixLen 返回 b 的最长完整 UTF-8 前缀长度。写入侧把多字节
// 字符写成一半时（部分写），半个字符留待下轮——若按字节原样下发，
// JSON 序列化会把无效序列变成 U+FFFD，内容永久损坏。
func utf8PrefixLen(b []byte) int {
	n := len(b)
	for n > 0 {
		r, size := utf8.DecodeLastRune(b[:n])
		if r != utf8.RuneError || size > 1 {
			return n
		}
		n-- // 不完整尾序列（DecodeLastRune 返回 RuneError,1）：回退
	}
	return 0
}
