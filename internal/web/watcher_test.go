package web

// 共享流观察器（streamHub/identityWatcher）测试：同一身份的多个 SSE
// 连接共享一套文件观察；无订阅者时观察器释放；事件带序号，断线重连
// 可续发或回退快照；慢订阅者被弃用而不阻塞广播；写失败即断开；
// 短时间反复断线记入降级状态。

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mindloop/internal/identity"
)

// failWriter 是恒失败的 ResponseWriter——SSE handler 首帧写失败即
// 必须退出（写失败=连接不可用，继续轮询只是浪费）。
type failWriter struct{ h http.Header }

func (f *failWriter) Header() http.Header {
	if f.h == nil {
		f.h = http.Header{}
	}
	return f.h
}
func (f *failWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }
func (f *failWriter) WriteHeader(int)           {}

// openStreamCtx 建立 SSE 连接并返回独立的取消函数（测试可主动断开）。
func openStreamCtx(t *testing.T, url string) (*bufio.Reader, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		cancel()
		t.Fatalf("Content-Type = %q，应为 text/event-stream", ct)
	}
	return bufio.NewReader(resp.Body), cancel
}

// hubWatchers 白盒读取共享观察器数量（持 hub 锁）。
func hubWatchers(h *streamHub) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.byKey)
}

// waitUntil 轮询条件直至成立或超时。
func waitUntil(t *testing.T, d time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

// TestStreamWatcherSharedAcrossSubscribers：同一身份两条 SSE 连接共享
// 一个观察器；同一变更以同一序号广播给两条连接。
func TestStreamWatcherSharedAcrossSubscribers(t *testing.T) {
	newIdentityHome(t)
	id, err := identity.Load("ada")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	ts, s := newTestServer(t, identity.Home(), "")
	s.replyPollEvery = 50 * time.Millisecond

	url := ts.URL + "/api/identities/ada/replies/stream"
	rA, _ := openStreamCtx(t, url)
	rB, _ := openStreamCtx(t, url)

	if n := hubWatchers(s.streams); n != 1 {
		t.Fatalf("两条连接应共享 1 个观察器，得到 %d 个", n)
	}

	appendStepLine(t, id, `{"type":"action","step_id":"act-1","ts":"2026-02-01T00:00:00.000Z","content":"共享"}`)

	fA := readEvent(t, rA, "step")
	fB := readEvent(t, rB, "step")
	if fA.id == "" || fA.id != fB.id {
		t.Fatalf("同一变更应带同一序号广播：A id=%q B id=%q", fA.id, fB.id)
	}
	if !strings.Contains(fA.data, "act-1") || !strings.Contains(fB.data, "act-1") {
		t.Fatalf("两连接都应收到 step：A=%q B=%q", fA.data, fB.data)
	}
}

// TestStreamWatcherReleasedWithoutSubscribers：唯一订阅者断开后观察器
// 释放（无订阅者的共享观察不应常驻）。
func TestStreamWatcherReleasedWithoutSubscribers(t *testing.T) {
	newIdentityHome(t)
	ts, s := newTestServer(t, identity.Home(), "")
	s.replyPollEvery = 50 * time.Millisecond

	r, cancel := openStreamCtx(t, ts.URL+"/api/identities/ada/replies/stream")
	readFrame(t, r) // 首帧 status 快照（确认订阅已建立）
	if n := hubWatchers(s.streams); n != 1 {
		t.Fatalf("连接期间应有 1 个观察器，得到 %d", n)
	}

	cancel()
	if !waitUntil(t, 2*time.Second, func() bool { return hubWatchers(s.streams) == 0 }) {
		t.Fatalf("断开后观察器应释放，仍剩 %d 个", hubWatchers(s.streams))
	}
}

// TestStreamResumeFromLastEventID：断线在观察器存活（另一订阅仍在）
// 时重连——按 Last-Event-ID 只补发缺口帧，不发快照、不重放已见帧。
func TestStreamResumeFromLastEventID(t *testing.T) {
	newIdentityHome(t)
	id, err := identity.Load("ada")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	ts, s := newTestServer(t, identity.Home(), "")
	s.replyPollEvery = 50 * time.Millisecond
	url := ts.URL + "/api/identities/ada/replies/stream"

	// B 作为锚订阅保持观察器存活。
	rB, _ := openStreamCtx(t, url)
	appendStepLine(t, id, `{"type":"action","step_id":"l1","ts":"2026-02-02T00:00:00.000Z"}`)
	b1 := readEvent(t, rB, "step")

	// A 连接后共同收到 l2（同序号），随后 A 断开。
	rA, cancelA := openStreamCtx(t, url)
	appendStepLine(t, id, `{"type":"action","step_id":"l2","ts":"2026-02-02T00:00:01.000Z"}`)
	a2 := readEvent(t, rA, "step")
	readEvent(t, rB, "step") // B 也读走 l2
	if a2.id == "" || a2.id == b1.id {
		t.Fatalf("l2 应有递增序号：l1=%q l2=%q", b1.id, a2.id)
	}
	cancelA()
	waitUntil(t, time.Second, func() bool {
		s.streams.mu.Lock()
		defer s.streams.mu.Unlock()
		w := s.streams.byKey[id.Timeline.Dir]
		if w == nil {
			return false
		}
		w.mu.Lock()
		defer w.mu.Unlock()
		return len(w.subs) == 1
	})

	// A 断开期间 l3 发生（B 收走）。
	appendStepLine(t, id, `{"type":"action","step_id":"l3","ts":"2026-02-02T00:00:02.000Z"}`)
	l3 := readEvent(t, rB, "step")

	// A 重连：带 Last-Event-ID=a2.id → 首帧必须是 l3（补缺口），
	// 不得先发 status 快照（那意味着续接失败）。
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Last-Event-ID", a2.id)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	rA2 := bufio.NewReader(resp.Body)

	f := readFrame(t, rA2)
	if f.event != "step" || f.id != l3.id {
		t.Fatalf("续接首帧应为缺口 step（id=%q），得到 event=%q id=%q data=%q",
			l3.id, f.event, f.id, f.data)
	}
	if !strings.Contains(f.data, "l3") {
		t.Fatalf("续接帧应为 l3：%q", f.data)
	}
}

// TestStreamStaleLastEventIDFallsBackToSnapshot：Last-Event-ID 超出
// 事件缓冲窗口（观察器早已越过）时回退快照——首帧是 status（无序号）。
func TestStreamStaleLastEventIDFallsBackToSnapshot(t *testing.T) {
	newIdentityHome(t)
	id, err := identity.Load("ada")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	ts, s := newTestServer(t, identity.Home(), "")
	s.replyPollEvery = 50 * time.Millisecond
	s.streams.ringCap = 2 // 注入小缓冲：4 帧后 ring 只剩最后 2 帧
	url := ts.URL + "/api/identities/ada/replies/stream"

	rB, _ := openStreamCtx(t, url)
	for i, sid := range []string{"s1", "s2", "s3", "s4"} {
		appendStepLine(t, id, `{"type":"action","step_id":"`+sid+`","ts":"2026-02-03T00:00:0`+string(rune('0'+i))+`.000Z"}`)
		readEvent(t, rB, "step")
	}

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Last-Event-ID", "1") // ring 首帧已是 seq 3：1 无从续接
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	rC := bufio.NewReader(resp.Body)

	f := readFrame(t, rC)
	if f.event != "status" || f.id != "" {
		t.Fatalf("陈旧 Last-Event-ID 应回退快照（首帧 status 无序号），得到 event=%q id=%q", f.event, f.id)
	}
}

// TestStreamSlowSubscriberDropped：慢订阅者（缓冲打满）被弃用——其
// 通道关闭，广播继续，其他订阅者不丢帧。
func TestStreamSlowSubscriberDropped(t *testing.T) {
	newIdentityHome(t)
	id, err := identity.Load("ada")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	_, s := newTestServer(t, identity.Home(), "")
	s.replyPollEvery = 50 * time.Millisecond
	s.streams.subCap = 2 // 注入小缓冲：第 3 帧即溢出

	subSlow, _ := s.streams.subscribe(id, 0)
	subFast, _ := s.streams.subscribe(id, 0)

	// 逐帧产生、逐帧确认：快订阅者边产生边消费（缓冲不溢出），
	// 慢订阅者只累积不读——第 3 帧时缓冲（容量 2）打满被弃用。
	var fast []sseFrame
	for _, sid := range []string{"f1", "f2", "f3"} {
		appendStepLine(t, id, `{"type":"action","step_id":"`+sid+`","ts":"2026-02-04T00:00:00.000Z"}`)
		select {
		case f, ok := <-subFast.ch:
			if !ok {
				t.Fatalf("快订阅者不应被丢弃（已收 %d 帧）", len(fast))
			}
			fast = append(fast, f)
		case <-time.After(3 * time.Second):
			t.Fatalf("快订阅者未收到 %s 的帧（已收 %d 帧）", sid, len(fast))
		}
	}

	// 慢订阅者：读尽缓冲后通道关闭（被弃用而非阻塞广播）。
	got := 0
	for range subSlow.ch {
		got++
	}
	if got != 2 {
		t.Fatalf("慢订阅者缓冲应恰有 2 帧后被关闭，得到 %d 帧", got)
	}
	s.streams.mu.Lock()
	w := s.streams.byKey[id.Timeline.Dir]
	s.streams.mu.Unlock()
	if w != nil {
		w.mu.Lock()
		_, still := w.subs[subSlow]
		w.mu.Unlock()
		if still {
			t.Fatal("慢订阅者应已从广播列表移除")
		}
	}
}

// TestStreamWriteFailureEndsHandler：SSE 写失败（对端不可用）时
// handler 必须立即结束——不得继续轮询空转。
func TestStreamWriteFailureEndsHandler(t *testing.T) {
	newIdentityHome(t)
	id, err := identity.Load("ada")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	_, s := newTestServer(t, identity.Home(), "")
	s.replyPollEvery = 50 * time.Millisecond

	r := httptest.NewRequest(http.MethodGet, "/api/identities/ada/replies/stream", nil)
	done := make(chan struct{})
	go func() {
		s.handleRepliesStream(&failWriter{}, r, id)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("写失败后 handler 未退出")
	}
	if !waitUntil(t, time.Second, func() bool {
		return s.streams.stats().Subscribers == 0
	}) {
		t.Fatal("写失败断开后订阅数应归零")
	}
}

// TestStreamChurnDegraded：短时间内反复断线（≥阈值）记入降级状态。
func TestStreamChurnDegraded(t *testing.T) {
	newIdentityHome(t)
	id, err := identity.Load("ada")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	_, s := newTestServer(t, identity.Home(), "")
	s.streams.churnWindow = 500 * time.Millisecond
	s.streams.churnLimit = 3

	for i := 0; i < 3; i++ {
		sub, _ := s.streams.subscribe(id, 0)
		s.streams.unsubscribe(sub)
	}
	if st := s.streams.stats(); !st.Degraded {
		t.Fatalf("短时间 %d 次断线应标记降级：%+v", 3, st)
	}
}

// TestStreamSurvivesServerWriteTimeout：SSE 单独处理写期限——真实
// http.Server 的 WriteTimeout（普通 API 保持 30s 语义）不得杀死
// 长连接；连接跨过 WriteTimeout 后仍持续收到帧。
func TestStreamSurvivesServerWriteTimeout(t *testing.T) {
	newIdentityHome(t)
	s, err := New(Config{Root: identity.Home(), Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s.replyPollEvery = 30 * time.Millisecond
	s.replyPingEvery = 50 * time.Millisecond

	ts := httptest.NewUnstartedServer(s)
	ts.Config.WriteTimeout = 80 * time.Millisecond // 远小于本测试的连接时长
	ts.Start()
	t.Cleanup(ts.Close)

	r, _ := openStreamCtx(t, ts.URL+"/api/identities/ada/replies/stream")
	// 连接跨越 3 个 WriteTimeout 窗口——每帧写期限被单独滚动，
	// 若继承 80ms 的服务器写期限，这里会读不到帧而 Fatal。
	for i := 0; i < 3; i++ {
		for {
			f := readFrame(t, r)
			if f.isComment {
				break // 收到 ": ping"
			}
		}
	}
}

// TestStreamDeltaUTF8Boundary：增量按 UTF-8 字符边界切分——写入侧
// 在字符中间写入时不下发半个字符，补全后才下发。
func TestStreamDeltaUTF8Boundary(t *testing.T) {
	newIdentityHome(t)
	id, err := identity.Load("ada")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	newReplyFiles(t, id, "msg-utf8", "你")
	txt := filepath.Join(id.Timeline.Dir, "stream", "msg-utf8.txt")

	ts, s := newTestServer(t, identity.Home(), "")
	s.replyPollEvery = 50 * time.Millisecond
	s.replyPingEvery = 150 * time.Millisecond
	r := openStream(t, ts.URL+"/api/identities/ada/replies/stream")

	d1 := readEvent(t, r, "delta")
	var first struct {
		Text   string `json:"text"`
		Offset int    `json:"offset"`
	}
	if err := json.Unmarshal([]byte(d1.data), &first); err != nil {
		t.Fatal(err)
	}
	if first.Offset != 0 || first.Text != "你" {
		t.Fatalf("首帧应为全量快照 offset=0 text=你，得到 %+v", first)
	}

	// 字符中间写入（“好”的首字节）——不得产生含替换字符的 delta。
	f, err := os.OpenFile(txt, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte{0xE5}); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()
	for i := 0; i < 4; i++ { // 跨若干轮询周期
		fr := readFrame(t, r)
		if fr.event == "delta" {
			t.Fatalf("半个字符不得下发 delta: %q", fr.data)
		}
	}

	// 补全“好”的剩余两字节 → 增量恰为“好”，offset=3（“你”的字节长）。
	f, err = os.OpenFile(txt, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte{0xA5, 0xBD}); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()
	d2 := readEvent(t, r, "delta")
	var second struct {
		Text   string `json:"text"`
		Offset int    `json:"offset"`
	}
	if err := json.Unmarshal([]byte(d2.data), &second); err != nil {
		t.Fatal(err)
	}
	if second.Offset != 3 || second.Text != "好" {
		t.Fatalf("补全后增量应为 offset=3 text=好，得到 %+v", second)
	}
}
