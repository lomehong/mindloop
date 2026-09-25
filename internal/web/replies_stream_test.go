package web

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mindloop/internal/identity"
	"mindloop/internal/mind"
)

// newReplyFiles 手写 mind 进程（task-23）的旁路文件，路径与内容形状
// 均与其真实写入侧一致：replying 是 <RunLockDir>/replying 的 JSON
// （{"reply_to":...}），旁路全文在 <Timeline.Dir>/stream/<reply_to>.txt。
func newReplyFiles(t *testing.T, id *identity.Identity, replyTo, text string) {
	t.Helper()
	ctl := mind.RunLockDir(id.Timeline.Dir)
	if err := os.MkdirAll(ctl, 0o755); err != nil { // 生产中由 TryRunLock 创建
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(id.Timeline.Dir, "stream"), 0o755); err != nil {
		t.Fatal(err)
	}
	replying, err := json.Marshal(map[string]any{
		"reply_to": replyTo,
		"thinker":  "responder",
		"since":    time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ctl, "replying"), replying, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(id.Timeline.Dir, "stream", replyTo+".txt"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// sseFrame 是客户端侧解析出的一个 SSE 帧。
type sseFrame struct {
	event     string
	data      string
	isComment bool
}

// readEvent 持续读帧直到事件类型命中 want 之一——测试只关心目标
// 事件，对插在其他位置的 status/working/ping 帧宽容跳过。
func readEvent(t *testing.T, r *bufio.Reader, want ...string) sseFrame {
	t.Helper()
	for {
		f := readFrame(t, r)
		for _, w := range want {
			if f.event == w {
				return f
			}
		}
	}
}

// readFrame 从 SSE 流读取下一帧（以空行终结）。整体受 ctx 超时保护，
// 读不到帧即 Fatal——流式测试不允许挂死。
func readFrame(t *testing.T, r *bufio.Reader) sseFrame {
	t.Helper()
	var f sseFrame
	var sb strings.Builder
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("读 SSE 流: %v（已收: %q）", err, sb.String())
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if sb.Len() > 0 {
				return f
			}
			continue
		}
		sb.WriteString(line)
		sb.WriteByte('\n')
		switch {
		case strings.HasPrefix(line, ":"):
			f.isComment = true
		case strings.HasPrefix(line, "event: "):
			f.event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			f.data = strings.TrimPrefix(line, "data: ")
		}
	}
}

// openStream 建立 SSE 连接（ctx 5s 兜底，防测试挂死）。
func openStream(t *testing.T, url string) *bufio.Reader {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q，应为 text/event-stream", ct)
	}
	return bufio.NewReader(resp.Body)
}

// TestRepliesStreamStatusAndDelta：连接建立即收到 status（replying
// true），随后 delta 携带旁路文件累积全文——UTF-8 中文完整无截断。
func TestRepliesStreamStatusAndDelta(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	home := identity.Home()
	id, err := identity.Create(context.Background(), "ada")
	if err != nil {
		t.Fatal(err)
	}
	const (
		replyTo = "msg-1"
		full    = "你好，世界——这是旁路文件的累积全文，含 UTF-8 中文与标点。"
	)
	newReplyFiles(t, id, replyTo, full)

	ts, s := newTestServer(t, home, "")
	s.replyPollEvery = 50 * time.Millisecond
	r := openStream(t, ts.URL+"/api/identities/ada/replies/stream")

	st := readFrame(t, r)
	if st.event != "status" {
		t.Fatalf("首帧应为 status，得到 %q (%q)", st.event, st.data)
	}
	var status struct {
		Replying bool   `json:"replying"`
		ReplyTo  string `json:"reply_to"`
	}
	if err := json.Unmarshal([]byte(st.data), &status); err != nil {
		t.Fatalf("status data 不是 JSON: %v (%q)", err, st.data)
	}
	if !status.Replying || status.ReplyTo != replyTo {
		t.Fatalf("status 契约错位: %+v", status)
	}

	dl := readEvent(t, r, "delta")
	var delta struct {
		ReplyTo string `json:"reply_to"`
		Text    string `json:"text"`
	}
	if err := json.Unmarshal([]byte(dl.data), &delta); err != nil {
		t.Fatalf("delta data 不是 JSON: %v (%q)", err, dl.data)
	}
	if delta.ReplyTo != replyTo {
		t.Fatalf("delta.reply_to = %q，应为 %q", delta.ReplyTo, replyTo)
	}
	if delta.Text != full {
		t.Fatalf("delta.text 应为累积全文（UTF-8 中文无截断）:\n got %q\nwant %q", delta.Text, full)
	}
}

// TestRepliesStreamGrowsAndDone：旁路文件增长 → delta 重发全量；
// 文件删除 → done 恰一次。
func TestRepliesStreamGrowsAndDone(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	home := identity.Home()
	id, err := identity.Create(context.Background(), "ada")
	if err != nil {
		t.Fatal(err)
	}
	newReplyFiles(t, id, "msg-2", "第一段")
	txt := filepath.Join(id.Timeline.Dir, "stream", "msg-2.txt")

	ts, s := newTestServer(t, home, "")
	s.replyPollEvery = 50 * time.Millisecond
	s.replyPingEvery = 200 * time.Millisecond // 心跳推进"done 不重复"读取
	r := openStream(t, ts.URL+"/api/identities/ada/replies/stream")

	readEvent(t, r, "status") // 首帧 status
	d1 := readEvent(t, r, "delta")
	if !strings.Contains(d1.data, "第一段") {
		t.Fatalf("首个 delta 错位: %q", d1.data)
	}

	// 追加增长 → delta 再次下发，text 仍为全量。
	if err := os.WriteFile(txt, []byte("第一段\n第二段追加"), 0o644); err != nil {
		t.Fatal(err)
	}
	d2 := readEvent(t, r, "delta")
	var delta struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(d2.data), &delta); err != nil {
		t.Fatal(err)
	}
	if delta.Text != "第一段\n第二段追加" {
		t.Fatalf("增长后 delta 应携带全量，得到 %q", delta.Text)
	}

	// 删除旁路 → done 恰一次（后续不再重复）。
	if err := os.Remove(txt); err != nil {
		t.Fatal(err)
	}
	done := readEvent(t, r, "done")
	if !strings.Contains(done.data, `"reply_to":"msg-2"`) {
		t.Fatalf("done.data 应含 reply_to: %q", done.data)
	}
	// 再读若干帧，确认 done 不重复（最多等 1s 的帧里没有第二个 done）。
	for i := 0; i < 3; i++ {
		f := readFrame(t, r)
		if f.event == "done" {
			t.Fatal("done 重复下发")
		}
	}
}

// TestRepliesStreamPing：无任何旁路活动时，心跳注释帧按间隔到达。
func TestRepliesStreamPing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	home := identity.Home()
	identity.Create(context.Background(), "ada")

	ts, s := newTestServer(t, home, "")
	s.replyPollEvery = 50 * time.Millisecond
	s.replyPingEvery = 300 * time.Millisecond
	r := openStream(t, ts.URL+"/api/identities/ada/replies/stream")

	st := readFrame(t, r)
	if st.event != "status" {
		t.Fatalf("首帧应为 status，得到 %q", st.event)
	}
	var status struct {
		Replying bool `json:"replying"`
	}
	if err := json.Unmarshal([]byte(st.data), &status); err != nil {
		t.Fatal(err)
	}
	if status.Replying {
		t.Fatalf("无 replying 文件时 replying 应为 false: %q", st.data)
	}
	for {
		f := readFrame(t, r)
		if f.isComment {
			return // 收到 ": ping" 心跳
		}
		// 空闲连接的合法帧：status 与 working 的首帧/状态帧。
		if f.event != "status" && f.event != "working" {
			t.Fatalf("空闲连接只应有 status/working/心跳，得到 %q (%q)", f.event, f.data)
		}
	}
}

// TestRepliesStreamRejectsPOST：GET 专属——POST 必须 405（方法路由）。
func TestRepliesStreamRejectsPOST(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	home := identity.Home()
	identity.Create(context.Background(), "ada")
	ts, _ := newTestServer(t, home, "")

	resp, err := http.Post(ts.URL+"/api/identities/ada/replies/stream", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST 流端点应 405，得到 %d", resp.StatusCode)
	}
}

// TestRepliesStreamCrossOriginRejected：跨源请求由既有同源守卫拦截
// （403，先于 mux 与流式 handler）。
func TestRepliesStreamCrossOriginRejected(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	home := identity.Home()
	identity.Create(context.Background(), "ada")
	ts, _ := newTestServer(t, home, "")

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/identities/ada/replies/stream", nil)
	req.Header.Set("Origin", "https://evil.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("跨源流请求应 403，得到 %d", resp.StatusCode)
	}
}

// appendStepLine 向 trajectory.jsonl 追加一行步骤 JSON。
func appendStepLine(t *testing.T, id *identity.Identity, line string) {
	t.Helper()
	f, err := os.OpenFile(id.Timeline.Path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(line + "\n"); err != nil {
		t.Fatal(err)
	}
}

// TestRepliesStreamStepEvents：尾随 trajectory.jsonl——连接前已存在
// 的历史行不重放（EOF 起步）；连接后追加的 action/message 下发 step
// 事件（excerpt 取 content 首行、长行裁 120 rune 加 …），prompt 被
// 跳过。
func TestRepliesStreamStepEvents(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	home := identity.Home()
	id, err := identity.Create(context.Background(), "ada")
	if err != nil {
		t.Fatal(err)
	}
	// 连接前写入历史行——绝不能出现在事件流里。
	appendStepLine(t, id, `{"type":"action","step_id":"hist-1","ts":"2026-01-01T00:00:00.000Z","content":"历史步骤"}`)

	ts, s := newTestServer(t, home, "")
	s.replyPollEvery = 50 * time.Millisecond
	s.replyPingEvery = 200 * time.Millisecond
	r := openStream(t, ts.URL+"/api/identities/ada/replies/stream")

	long := strings.Repeat("好", 150)
	appendStepLine(t, id, `{"type":"action","step_id":"act-1","ts":"2026-01-02T00:00:00.000Z","content":"`+long+`\n第二行不应出现在 excerpt"}`)
	appendStepLine(t, id, `{"type":"message","step_id":"msg-1","ts":"2026-01-02T00:00:01.000Z","content":"你好"}`)
	appendStepLine(t, id, `{"type":"prompt","step_id":"pr-1","ts":"2026-01-02T00:00:02.000Z","content":"机密任务原文"}`)

	wantExcerpt := strings.Repeat("好", 120) + "…"
	var seen []map[string]any
	for len(seen) < 2 {
		f := readFrame(t, r)
		if f.event != "step" {
			continue // status/working/ping 帧
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(f.data), &m); err != nil {
			t.Fatalf("step data 不是 JSON: %v (%q)", err, f.data)
		}
		seen = append(seen, m)
	}
	if seen[0]["step_id"] != "act-1" {
		t.Fatalf("第一个 step 应为 act-1（历史 hist-1 不重放），得到 %v", seen[0]["step_id"])
	}
	if seen[0]["type"] != "action" || seen[0]["ts"] != "2026-01-02T00:00:00.000Z" {
		t.Fatalf("act-1 契约错位: %v", seen[0])
	}
	if seen[0]["excerpt"] != wantExcerpt {
		t.Fatalf("excerpt 应为首行裁 120 rune 加 …:\n got %q\nwant %q", seen[0]["excerpt"], wantExcerpt)
	}
	if seen[1]["step_id"] != "msg-1" || seen[1]["excerpt"] != "你好" {
		t.Fatalf("msg-1 契约错位: %v", seen[1])
	}
	// prompt 类型被跳过：再读若干帧确认 pr-1 永不出现。
	for i := 0; i < 3; i++ {
		f := readFrame(t, r)
		if f.event == "step" && strings.Contains(f.data, "pr-1") {
			t.Fatal("prompt 类型不应下发 step 事件")
		}
	}
}

// TestRepliesStreamStepPartialLine：末尾不完整行（无换行符）不产生
// 事件，补全换行后才下发——半行缓冲正确性。
func TestRepliesStreamStepPartialLine(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	home := identity.Home()
	id, err := identity.Create(context.Background(), "ada")
	if err != nil {
		t.Fatal(err)
	}

	ts, s := newTestServer(t, home, "")
	s.replyPollEvery = 50 * time.Millisecond
	s.replyPingEvery = 150 * time.Millisecond
	r := openStream(t, ts.URL+"/api/identities/ada/replies/stream")

	// 追加半行（无换行符）——不应产生 step 事件。
	half := `{"type":"action","step_id":"half-1","ts":"2026-01-03T00:00:00.000Z","content":"半行"`
	f, err := os.OpenFile(id.Timeline.Path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(half); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()
	for i := 0; i < 4; i++ { // 跨若干轮询周期（含心跳帧）确认无 step
		fr := readFrame(t, r)
		if fr.event == "step" {
			t.Fatalf("半行不应产生 step 事件: %q", fr.data)
		}
	}

	// 补全换行 → 半行缓冲拼接，step 事件到达且内容完整。
	f, err = os.OpenFile(id.Timeline.Path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("}\n"); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()
	st := readEvent(t, r, "step")
	if !strings.Contains(st.data, `"step_id":"half-1"`) || !strings.Contains(st.data, `"excerpt":"半行"`) {
		t.Fatalf("补全后的 step 契约错位: %q", st.data)
	}
}

// TestRepliesStreamWorkingEvents：working 文件写入/删除驱动事件变化
// ——不存在 = 闲（working:false 空数组），存在时透传 busy 条目。
func TestRepliesStreamWorkingEvents(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	home := identity.Home()
	id, err := identity.Create(context.Background(), "ada")
	if err != nil {
		t.Fatal(err)
	}

	ts, s := newTestServer(t, home, "")
	s.replyPollEvery = 50 * time.Millisecond
	r := openStream(t, ts.URL+"/api/identities/ada/replies/stream")

	// 连接建立即发：无 working 文件 → 闲。
	idle := readEvent(t, r, "working")
	var idleState struct {
		Working bool             `json:"working"`
		Busy    []map[string]any `json:"busy"`
	}
	if err := json.Unmarshal([]byte(idle.data), &idleState); err != nil {
		t.Fatal(err)
	}
	if idleState.Working || len(idleState.Busy) != 0 {
		t.Fatalf("无 working 文件应为闲: %q", idle.data)
	}

	// 写入 working → busy 状态事件（文件形态与 mind 写入侧一致：对象）。
	workingPath := filepath.Join(mind.RunLockDir(id.Timeline.Dir), "working")
	if err := os.MkdirAll(filepath.Dir(workingPath), 0o755); err != nil {
		t.Fatal(err)
	}
	busy := `{"working":true,"busy":[{"thinker":"monolith","wake":"step","since":"2026-01-04T00:00:00.000Z"}]}`
	if err := os.WriteFile(workingPath, []byte(busy), 0o644); err != nil {
		t.Fatal(err)
	}
	busyFrame := readEvent(t, r, "working")
	var busyState struct {
		Working bool `json:"working"`
		Busy    []struct {
			Thinker string `json:"thinker"`
			Wake    string `json:"wake"`
			Since   string `json:"since"`
		} `json:"busy"`
	}
	if err := json.Unmarshal([]byte(busyFrame.data), &busyState); err != nil {
		t.Fatal(err)
	}
	if !busyState.Working || len(busyState.Busy) != 1 ||
		busyState.Busy[0].Thinker != "monolith" || busyState.Busy[0].Wake != "step" {
		t.Fatalf("working 契约错位: %q", busyFrame.data)
	}

	// 删除 working → 回到闲。
	if err := os.Remove(workingPath); err != nil {
		t.Fatal(err)
	}
	idle2 := readEvent(t, r, "working")
	if strings.Contains(idle2.data, `"working":true`) {
		t.Fatalf("删除 working 文件后应为闲: %q", idle2.data)
	}
}

// TestStepExcerpt：excerpt 裁剪规则——首行、120 rune 上限加 …、
// 无换行/短行原样。
func TestStepExcerpt(t *testing.T) {
	if got := stepExcerpt("第一行\n第二行\n第三行"); got != "第一行" {
		t.Fatalf("应取首行，得到 %q", got)
	}
	if got := stepExcerpt("短行"); got != "短行" {
		t.Fatalf("短行应原样，得到 %q", got)
	}
	long := strings.Repeat("字", 200)
	want := strings.Repeat("字", 120) + "…"
	if got := stepExcerpt(long); got != want {
		t.Fatalf("长行应裁 120 rune 加 …，得到 %d rune", len([]rune(got)))
	}
	if got := stepExcerpt(""); got != "" {
		t.Fatalf("空串应原样，得到 %q", got)
	}
}
