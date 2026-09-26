package web

// /api/identities/{id}/replies/stream —— responder 回复的 SSE 流式
// 端点（端到端流式响应的第 3 层）。mind 进程（task-23）在运行锁控制
// 面下写旁路文件，本端点经共享观察器（watcher.go）只读：
//
//	<RunLockDir>/replying            JSON：{"reply_to":"...","thinker":...,"since":...}
//	<Timeline.Dir>/stream/<reply_to>.txt  旁路全文，追加式增长
//
// 事件协议（Lead 固定契约）：
//   - event: status  data: {"replying":true,"reply_to":"..."}
//     订阅快照首帧与每次 replying 状态变化时发送；
//   - event: delta   data: {"reply_to":"...","offset":N,"text":"..."}
//     快照为 offset=0 全量；此后增长只携带新字节（按 UTF-8 字符
//     边界切分）；缩短/替换回退 offset=0——客户端统一按"截断到
//     offset 再追加"处理，快照与增量共用一套客户端逻辑；
//   - event: done    data: {"reply_to":"..."}
//     旁路文件消失时发送一次；
//   - event: working data: {"working":true,"busy":[{"thinker":...}]}
//     订阅快照与 <RunLockDir>/working 内容变化时发送（不存在 = 闲）；
//   - event: step    data: {"step_id","type","ts","excerpt",
//     "task_id?","run_id?","attempt?"}
//     尾随 trajectory.jsonl 新增行（观察器从订阅时刻 EOF 起步，
//     历史不重放），trajectory/run/prompt 类型跳过，excerpt 为
//     content 首行裁 120 rune；任务执行步骤附带 task_id/run_id/
//     attempt 归因字段（无归因不带键）；
//   - 每 15s 发送 ": ping" 注释行保活。
//
// 事件带序号（id: N）。断线重连按 Last-Event-ID（请求头优先，
// ?last_event_id= 查询兜底）在观察器事件缓冲窗口内续发缺口帧，超出
// 窗口回退当前状态快照；同一身份的多个连接共享一个观察器，无订阅者
// 时释放；慢订阅者被弃用（连接关闭，由其重连恢复）而不阻塞广播。
// SSE 单独处理 HTTP 写期限：建立时清除服务器级 WriteTimeout（普通
// API 超时不变），每帧滚动短写期限，写失败立即断开。

import (
	"bytes"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"mindloop/internal/identity"
)

// sseWriteGrace 是 SSE 每帧的写期限：慢链路在帧粒度失败断开，而
// 不是把死连接拖到 TCP 超时；健康连接每帧写前滚动到 now+grace。
const sseWriteGrace = 10 * time.Second

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

// sseWriter 是 SSE 帧写出器：每帧先在内存拼装完整（id/event/data +
// 空行），滚动写期限后一次 Write 并 flush——连接上永远不会出现半帧；
// 写失败如实返回，handler 据此立即结束（对端不可用，继续空转只是
// 浪费）。互斥锁串行化帧间写入。
type sseWriter struct {
	w     http.ResponseWriter
	rc    *http.ResponseController
	grace time.Duration
	mu    sync.Mutex
	buf   bytes.Buffer
}

func newSSEWriter(w http.ResponseWriter, grace time.Duration) *sseWriter {
	return &sseWriter{w: w, rc: http.NewResponseController(w), grace: grace}
}

// writeFrame 写一帧事件；seq>0 时带 id 行——快照帧 seq=0 不带：它是
// 订阅时刻的当前状态而非可续接的历史事件。
func (sw *sseWriter) writeFrame(f sseFrame) error {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	sw.buf.Reset()
	if f.seq > 0 {
		fmt.Fprintf(&sw.buf, "id: %d\n", f.seq)
	}
	fmt.Fprintf(&sw.buf, "event: %s\n", f.event)
	fmt.Fprintf(&sw.buf, "data: %s\n", f.data)
	sw.buf.WriteByte('\n')
	return sw.flushLocked()
}

// writeComment 写注释行（心跳）。
func (sw *sseWriter) writeComment(line string) error {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	sw.buf.Reset()
	fmt.Fprintf(&sw.buf, ": %s\n\n", line)
	return sw.flushLocked()
}

// flushLocked 滚动写期限后整帧写出。SetWriteDeadline 对不支持期限
// 的 ResponseWriter（测试桩）返回 ErrNotSupported——忽略即可。
func (sw *sseWriter) flushLocked() error {
	if sw.grace > 0 {
		_ = sw.rc.SetWriteDeadline(time.Now().Add(sw.grace))
	}
	if _, err := sw.w.Write(sw.buf.Bytes()); err != nil {
		return err
	}
	if f, ok := sw.w.(http.Flusher); ok {
		f.Flush()
	}
	return nil
}

// parseLastEventID 取断线续接锚点：Last-Event-ID 头优先（SSE 规范），
// ?last_event_id= 查询参数兜底（fetch 手写客户端可显式携带）；缺省或
// 非法一律 0——即按快照重建。
func parseLastEventID(r *http.Request) int64 {
	raw := r.Header.Get("Last-Event-ID")
	if raw == "" {
		raw = r.URL.Query().Get("last_event_id")
	}
	if raw == "" {
		return 0
	}
	n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// handleRepliesStream 服务一条 SSE 连接：订阅共享观察器，先写
// 续接缺口帧或状态快照，随后把广播帧按序转发——r.Context() 取消
// （客户端断开）、订阅通道关闭（慢订阅者被弃用）或写失败即退出。
func (s *Server) handleRepliesStream(w http.ResponseWriter, r *http.Request, id *identity.Identity) {
	// SSE 单独处理 HTTP 写期限：清除从 http.Server.WriteTimeout
	// 继承的写期限（普通 API 超时保持不变），随后由 sseWriter 按帧
	// 滚动——长连接不会被服务器级写超时杀死，死连接也会及时断开。
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	// 反向代理场景下禁用响应缓冲，保证逐事件到达。
	w.Header().Set("X-Accel-Buffering", "no")

	// 缺口帧/快照在订阅时一次算好；订阅之后观察器产生的新帧按序
	// 进入 sub.ch（订阅与预热在同一临界区内），写侧顺序不变。
	lastID := parseLastEventID(r)
	sub, warm := s.streams.subscribe(id, lastID)
	defer s.streams.unsubscribe(sub)

	w.WriteHeader(http.StatusOK)

	sw := newSSEWriter(w, sseWriteGrace)
	for _, f := range warm {
		if err := sw.writeFrame(f); err != nil {
			return // 对端不可用：不再空转
		}
	}

	ping := s.replyPingEvery
	if ping <= 0 {
		ping = 15 * time.Second
	}
	ticker := time.NewTicker(ping)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return // 客户端断开：net/http 回收连接
		case f, ok := <-sub.ch:
			if !ok {
				return // 慢订阅者被弃用：重连按 Last-Event-ID 恢复
			}
			if err := sw.writeFrame(f); err != nil {
				return
			}
		case <-ticker.C:
			if err := sw.writeComment("ping"); err != nil {
				return
			}
		}
	}
}
