package web

// /api/identities/{id}/replies/stream —— responder 回复的 SSE 流式
// 端点（端到端流式响应的第 3 层）。mind 进程（task-23）在运行锁控制
// 面下写旁路文件，本端点只读：
//
//	<RunLockDir>/replying            JSON：{"reply_to":"...","thinker":...,"since":...}
//	<Timeline.Dir>/stream/<reply_to>.txt  旁路全文，追加式增长
//
// 事件协议（Lead 固定契约）：
//   - event: status  data: {"replying":true,"reply_to":"..."}
//     连接建立时与每次 replying 状态变化时发送；
//   - event: delta   data: {"reply_to":"...","text":"<累积全文>"}
//     旁路文件增长时发送，text 恒为文件全量内容——幂等下发，客户端
//     免偏移管理；每连接 200ms 轮询一次；
//   - event: done    data: {"reply_to":"..."}
//     旁路文件消失时发送一次；
//   - event: working data: {"working":true,"busy":[{"thinker":...}]}
//     连接建立时与 <RunLockDir>/working 内容变化时发送（不存在 = 闲）；
//   - event: step    data: {"step_id","type","ts","excerpt"}
//     尾随 trajectory.jsonl 新增行（每连接 EOF 起步，历史不重放），
//     trajectory/run/prompt 类型跳过，excerpt 为 content 首行裁 120 rune；
//   - 每 15s 发送 ": ping" 注释行保活；客户端断开（r.Context().Done）
//     即停止轮询。无活跃旁路且非 replying 时保持心跳即可。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"mindloop/internal/identity"
	"mindloop/internal/mind"
	"mindloop/internal/traj"
)

// workingBusy 是 <RunLockDir>/working 文件中单个忙碌条目的形态。
type workingBusy struct {
	Thinker string `json:"thinker"`
	Wake    string `json:"wake"`
	Since   string `json:"since"`
}

// stepExcerpt 取 content 的首行，裁到 120 rune——超出部分以 "…" 截尾。
func stepExcerpt(content string) string {
	if i := strings.IndexByte(content, '\n'); i >= 0 {
		content = content[:i]
	}
	content = strings.TrimSuffix(content, "\r")
	r := []rune(content)
	if len(r) > 120 {
		return string(r[:120]) + "…"
	}
	return content
}

// routeReplies 分流 /api/identities/{id}/replies 子树。读端点按现行
// 风格做方法路由：stream 只收 GET，其余方法 405。
func (s *Server) routeReplies(w http.ResponseWriter, r *http.Request, id *identity.Identity, rest []string) {
	if len(rest) == 0 || rest[0] != "stream" {
		writeError(w, 404, "未知子路径: replies/"+strings.Join(rest, "/"))
		return
	}
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	s.handleRepliesStream(w, r, id)
}

// sseWriter 是 SSE 帧写出器：双缓冲防部分写——每帧先在内存拼装完整
// （event + data + 空行），再一次 Write 到 ResponseWriter 并 flush，
// 连接上永远不会出现半帧。互斥锁串行化帧间写入。
type sseWriter struct {
	w   http.ResponseWriter
	mu  sync.Mutex
	buf bytes.Buffer
}

func (sw *sseWriter) writeEvent(event, data string) {
	sw.buf.Reset()
	fmt.Fprintf(&sw.buf, "event: %s\n", event)
	fmt.Fprintf(&sw.buf, "data: %s\n", data)
	sw.buf.WriteByte('\n')
	sw.writeFrame()
}

func (sw *sseWriter) writeComment(line string) {
	sw.buf.Reset()
	fmt.Fprintf(&sw.buf, ": %s\n\n", line)
	sw.writeFrame()
}

func (sw *sseWriter) writeFrame() {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	_, _ = sw.w.Write(sw.buf.Bytes())
	if f, ok := sw.w.(http.Flusher); ok {
		f.Flush()
	}
}

// handleRepliesStream 服务一条 SSE 连接：每连接一个 goroutine，在
// r.Context() 取消（客户端断开）时退出。轮询只读旁路文件，无共享
// 状态；连接数不需限制。
func (s *Server) handleRepliesStream(w http.ResponseWriter, r *http.Request, id *identity.Identity) {
	ctl := mind.RunLockDir(id.Timeline.Dir)
	replyPath := filepath.Join(ctl, "replying")
	// 旁路目录与 mind 写入侧一致：<Timeline.Dir>/stream（不是运行锁
	// 目录下的 stream——mind 的 replyStream 写在轨迹目录本体下）。
	streamDir := filepath.Join(id.Timeline.Dir, "stream")

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	// 反向代理场景下禁用响应缓冲，保证逐事件到达。
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	sw := &sseWriter{w: w}
	poll, ping := s.replyPollEvery, s.replyPingEvery
	if poll <= 0 {
		poll = 200 * time.Millisecond
	}
	if ping <= 0 {
		ping = 15 * time.Second
	}

	// lastStatus 以空字节为哨兵：连接建立的第一轮必须发出 status
	// 帧（契约：连接建立时发送），之后仅在变化时发送。
	lastStatus := "\x00"
	sizes := map[string]int64{} // reply_to → 上次下发时的文件大小
	lastPing := time.Now()

	// working 事件状态：文件原文作为变化信号（哨兵模式同 status）。
	workingPath := filepath.Join(ctl, "working")
	lastWorking := "\x00"

	// 轨迹尾随状态：首次连接从当前 EOF 起步——只推新步骤，不重放
	// 历史（与调度器 cursor 冷启动同一铁律）。stepPending 是半行
	// 缓冲：末尾无换行的不完整行留到下轮拼接。
	tlPath := id.Timeline.Path
	stepOffset := int64(0)
	if fi, err := os.Stat(tlPath); err == nil {
		stepOffset = fi.Size()
	}
	var stepPending []byte

	// emitStep 解析一行轨迹 JSON 并下发 step 事件；解析失败跳过。
	// trajectory/run/prompt 是结构性步骤，不进活动流。
	emitStep := func(line []byte) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			return
		}
		var raw struct {
			StepID  string          `json:"step_id"`
			Type    string          `json:"type"`
			TS      string          `json:"ts"`
			Content json.RawMessage `json:"content"`
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
		payload, _ := json.Marshal(map[string]any{
			"step_id": raw.StepID,
			"type":    raw.Type,
			"ts":      raw.TS,
			"excerpt": stepExcerpt(content),
		})
		sw.writeEvent("step", string(payload))
	}

	// trailSteps 尾随 trajectory.jsonl：读新增字节、按 \n 切完整行
	// （末尾不完整行留到下轮），逐行下发 step 事件。
	trailSteps := func() {
		fi, err := os.Stat(tlPath)
		if err != nil {
			return
		}
		size := fi.Size()
		if size == stepOffset {
			return
		}
		if size < stepOffset {
			// 文件收缩（理论不发生）：重置到新 EOF，宁可少发不误发。
			stepOffset = size
			stepPending = nil
			return
		}
		f, err := os.Open(tlPath)
		if err != nil {
			return
		}
		chunk := make([]byte, size-stepOffset)
		_, rerr := f.ReadAt(chunk, stepOffset)
		f.Close()
		if rerr != nil && rerr != io.EOF {
			return
		}
		stepOffset = size
		data := append(stepPending, chunk...)
		stepPending = nil
		for {
			idx := bytes.IndexByte(data, '\n')
			if idx < 0 {
				stepPending = append(stepPending, data...)
				break
			}
			emitStep(data[:idx])
			data = data[idx+1:]
		}
	}

	pollOnce := func() {
		// status：replying 文件在即回复中，内容为 JSON
		// {"reply_to":"...","thinker":...,"since":...}——解析后取
		// reply_to 作为事件字段，而不是把文件原文当字符串塞进事件。
		cur := ""
		if data, err := os.ReadFile(replyPath); err == nil {
			var st struct {
				ReplyTo string `json:"reply_to"`
			}
			if json.Unmarshal([]byte(strings.TrimSpace(string(data))), &st) == nil {
				cur = st.ReplyTo
			}
		}
		if cur != lastStatus {
			lastStatus = cur
			payload, _ := json.Marshal(map[string]any{"replying": cur != "", "reply_to": cur})
			sw.writeEvent("status", string(payload))
		}

		// delta：旁路文件增长即重发全量（幂等）。
		present := map[string]bool{}
		entries, err := os.ReadDir(streamDir)
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
				if prev, seen := sizes[rt]; seen && fi.Size() <= prev {
					continue
				}
				data, rerr := os.ReadFile(filepath.Join(streamDir, name))
				if rerr != nil {
					continue // 读取竞态：下个轮询周期重试
				}
				sizes[rt] = fi.Size()
				payload, _ := json.Marshal(map[string]any{"reply_to": rt, "text": string(data)})
				sw.writeEvent("delta", string(payload))
			}
		}

		// done：曾下发过内容的旁路文件消失时发送一次。
		for rt := range sizes {
			if !present[rt] {
				payload, _ := json.Marshal(map[string]any{"reply_to": rt})
				sw.writeEvent("done", string(payload))
				delete(sizes, rt)
			}
		}

		// working：monolith/thinker 忙闲状态，文件原文变化即重发
		// （哨兵模式）。文件不存在 = 闲。
		workingRaw, werr := os.ReadFile(workingPath)
		workingSig := string(workingRaw)
		if werr != nil {
			workingSig = ""
		}
		if workingSig != lastWorking {
			lastWorking = workingSig
			// 文件是对象形态 {"working":true,"busy":[...]}——按对象
			// 解析；解析失败或 working=false 一律按闲处理。
			var parsed struct {
				Working bool          `json:"working"`
				Busy    []workingBusy `json:"busy"`
			}
			working := false
			var busy []workingBusy
			if werr == nil && json.Unmarshal(workingRaw, &parsed) == nil {
				working = parsed.Working
				busy = parsed.Busy
			}
			if busy == nil {
				busy = []workingBusy{}
			}
			payload, _ := json.Marshal(map[string]any{"working": working, "busy": busy})
			sw.writeEvent("working", string(payload))
		}

		// step：轨迹尾随，新增行逐条下发（历史不重放、半行缓冲）。
		trailSteps()
	}

	pollOnce() // 连接建立：立即发出首帧 status（与已存在的旁路 delta）
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return // 客户端断开：停止轮询，连接由 net/http 回收
		case <-ticker.C:
			pollOnce()
			if time.Since(lastPing) >= ping {
				lastPing = time.Now()
				sw.writeComment("ping")
			}
		}
	}
}
