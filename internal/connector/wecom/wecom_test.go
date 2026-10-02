package wecom

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lomehong/mindloop/internal/connector/ws"
	"github.com/lomehong/mindloop/internal/traj"
)

// ---------- 测试侧帧原语（客户端掩码编解码，与服务端裸帧） ----------

func writeMaskedFrame(conn net.Conn, op ws.Opcode, payload []byte) error {
	hdr := []byte{0x80 | byte(op), 0x80}
	n := len(payload)
	switch {
	case n <= 125:
		hdr[1] |= byte(n)
	case n <= 0xFFFF:
		hdr[1] |= 126
		var ext [2]byte
		binary.BigEndian.PutUint16(ext[:], uint16(n))
		hdr = append(hdr, ext[:]...)
	default:
		hdr[1] |= 127
		var ext [8]byte
		binary.BigEndian.PutUint64(ext[:], uint64(n))
		hdr = append(hdr, ext[:]...)
	}
	key := []byte{1, 2, 3, 4}
	masked := make([]byte, n)
	for i := range payload {
		masked[i] = payload[i] ^ key[i%4]
	}
	buf := append(append(hdr, key...), masked...)
	_, err := conn.Write(buf)
	return err
}

func readMaskedFrame(br *bufio.Reader) (ws.Opcode, []byte, error) {
	var hdr [2]byte
	if _, err := io.ReadFull(br, hdr[:]); err != nil {
		return 0, nil, err
	}
	op := ws.Opcode(hdr[0] & 0x0F)
	length := int(hdr[1] & 0x7F)
	switch length {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(br, ext[:]); err != nil {
			return 0, nil, err
		}
		length = int(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(br, ext[:]); err != nil {
			return 0, nil, err
		}
		length = int(binary.BigEndian.Uint64(ext[:]))
	}
	key := make([]byte, 4)
	if _, err := io.ReadFull(br, key); err != nil {
		return 0, nil, err
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(br, payload); err != nil {
		return 0, nil, err
	}
	for i := range payload {
		payload[i] ^= key[i%4]
	}
	return op, payload, nil
}

// pipeConn 把 net.Conn 适配成桥要的 wsConn：读写都走测试帧原语
// （客户端侧：写掩码、读裸）。
type pipeConn struct {
	conn net.Conn
	br   *bufio.Reader
}

func (p *pipeConn) ReadMessage() (ws.Opcode, []byte, error) {
	var hdr [2]byte
	if _, err := io.ReadFull(p.br, hdr[:]); err != nil {
		return 0, nil, err
	}
	op := ws.Opcode(hdr[0] & 0x0F)
	length := int(hdr[1] & 0x7F)
	switch length {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(p.br, ext[:]); err != nil {
			return 0, nil, err
		}
		length = int(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(p.br, ext[:]); err != nil {
			return 0, nil, err
		}
		length = int(binary.BigEndian.Uint64(ext[:]))
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(p.br, payload); err != nil {
		return 0, nil, err
	}
	return op, payload, nil
}

func (p *pipeConn) WriteMessage(op ws.Opcode, data []byte) error {
	return writeMaskedFrame(p.conn, op, data)
}

func (p *pipeConn) WritePing([]byte) error { return nil }
func (p *pipeConn) Close() error           { return p.conn.Close() }

// ackFrame 构造一帧回执（无 cmd，按 req_id 对账）。
func ackFrame(reqID string, errcode int64, errmsg string) []byte {
	out, _ := json.Marshal(map[string]any{
		"headers": map[string]string{"req_id": reqID},
		"errcode": errcode,
		"errmsg":  errmsg,
	})
	return out
}

// readClientJSON 读一帧并解析：返回 cmd / req_id / 原始字节。
func readClientJSON(t *testing.T, br *bufio.Reader) (string, string, []byte) {
	t.Helper()
	op, payload, err := readMaskedFrame(br)
	if err != nil || op != ws.OpText {
		t.Fatalf("读客户端帧: op=%d err=%v", op, err)
	}
	f, err := parseFrame(payload)
	if err != nil || f.IsResponse {
		t.Fatalf("解析客户端帧: %+v %v", f, err)
	}
	return f.Cmd, f.ReqID, payload
}

// writeServerText 服务端→桥的裸帧（不掩码），支持 16 位长度。
func writeServerText(t *testing.T, conn net.Conn, payload []byte) {
	t.Helper()
	hdr := []byte{0x80 | byte(ws.OpText)}
	if n := len(payload); n <= 125 {
		hdr = append(hdr, byte(n))
	} else if n <= 0xFFFF {
		hdr = append(hdr, 126, byte(n>>8), byte(n))
	} else {
		t.Fatal("测试帧保持 ≤64KiB")
	}
	if _, err := conn.Write(append(hdr, payload...)); err != nil {
		t.Fatal(err)
	}
}

// serverScript 是假网关的一次连接剧本：拿到连接后执行；serverFrames
// 收集桥发出的帧（订阅/出站），供测试断言。
type serverScript func(conn net.Conn, serverFrames chan<- []byte)

// dialFor 把剧本包成 Options.Dial：每次拨号经本地 TCP 对接一对真
// 连接（劫持握手环节——传输层已由 ws 包自己的帧级测试覆盖）。
func dialFor(t *testing.T, script serverScript, frames chan<- []byte) func(ctx context.Context, addr string) (wsConn, error) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go script(conn, frames)
		}
	}()
	return func(ctx context.Context, addr string) (wsConn, error) {
		d := net.Dialer{Timeout: 3 * time.Second}
		conn, err := d.DialContext(ctx, "tcp", l.Addr().String())
		if err != nil {
			return nil, err
		}
		return &pipeConn{conn: conn, br: bufio.NewReader(conn)}, nil
	}
}

func newTestBridge(t *testing.T, script serverScript) (*Bridge, *traj.Timeline, chan []byte) {
	t.Helper()
	t.Setenv("MINDLOOP_HOME", t.TempDir())
	tl, err := traj.Create(context.Background(), "wecom-test")
	if err != nil {
		t.Fatal(err)
	}
	frames := make(chan []byte, 32)
	b, err := New(Options{
		Timeline: tl, Self: "ada",
		BotID: "ww1", Secret: "s",
		Allow:    []string{"zhangsan", "lisi"},
		StateDir: t.TempDir(),
		Addr:     "wss://test.invalid",
		Dial:     dialFor(t, script, frames),
		// 1 小时心跳 + 毫秒级退避：测试里排除 ping 干扰、加速重连
		PingInterval:     time.Hour,
		ReconnectBackoff: time.Millisecond,
		Logger:           func(format string, args ...any) { t.Logf("bridge: "+format, args...) },
	})
	if err != nil {
		t.Fatal(err)
	}
	return b, tl, frames
}

// waitFrame 从服务端视角等一帧。
func waitFrame(t *testing.T, frames chan []byte) []byte {
	t.Helper()
	select {
	case f := <-frames:
		return f
	case <-time.After(10 * time.Second):
		t.Fatal("等待桥帧超时")
		return nil
	}
}

// ---------- 测试 ----------

func TestNewFailClosed(t *testing.T) {
	tl, err := traj.Create(context.Background(), "x")
	if err != nil {
		t.Skip("需要 MINDLOOP_HOME 环境")
	}
	for name, opts := range map[string]Options{
		"缺 BotID":  {Timeline: tl, Self: "ada", Secret: "s", Allow: []string{"u"}, StateDir: t.TempDir()},
		"缺 Secret": {Timeline: tl, Self: "ada", BotID: "b", Allow: []string{"u"}, StateDir: t.TempDir()},
		"缺白名单":     {Timeline: tl, Self: "ada", BotID: "b", Secret: "s", StateDir: t.TempDir()},
		"缺游标目录":    {Timeline: tl, Self: "ada", BotID: "b", Secret: "s", Allow: []string{"u"}},
	} {
		if _, err := New(opts); err == nil {
			t.Fatalf("%s 应拒绝启动", name)
		}
	}
}

// TestInboundIdempotencyAndKick 钉死入站全链路：订阅帧带 req_id 且
// 认证回执达成、白名单内回调落盘（幂等键 wecom:<msgid>）、服务端
// 重推被幂等吸收、白名单外不落轨迹、disconnected_event 以 ErrKicked
// 终止（事件帧嵌套形态）。
func TestInboundIdempotencyAndKick(t *testing.T) {
	b, tl, frames := newTestBridge(t, func(conn net.Conn, serverFrames chan<- []byte) {
		br := bufio.NewReader(conn)
		// 1) 订阅帧：带 req_id → 回 errcode=0 认证回执
		cmd, reqID, raw := readClientJSON(t, br)
		if cmd != "aibot_subscribe" || reqID == "" {
			t.Errorf("订阅帧不符: cmd=%s req_id=%q", cmd, reqID)
			return
		}
		if !strings.Contains(string(raw), `"bot_id":"ww1"`) || !strings.Contains(string(raw), `"secret":"s"`) {
			t.Errorf("订阅载荷缺凭据: %s", raw)
		}
		serverFrames <- raw
		writeServerText(t, conn, ackFrame(reqID, 0, "ok"))
		// 真实网关不会在认证回执被消费前推送消息；这里的延迟同构
		// 地避免帧序竞态（ack 与回调同拍到达时认证 select 随机
		// 先读回调会被"认证前忽略"）。
		time.Sleep(80 * time.Millisecond)
		// 2) 白名单内回调 + 同 msgid 重推
		writeServerText(t, conn, []byte(`{"cmd":"aibot_msg_callback","headers":{"req_id":"srv1"},"body":{"msgid":"m1","from":{"userid":"zhangsan"},"text":{"content":"帮我看看今天的日报"}}}`))
		writeServerText(t, conn, []byte(`{"cmd":"aibot_msg_callback","headers":{"req_id":"srv2"},"body":{"msgid":"m1","from":{"userid":"zhangsan"},"text":{"content":"帮我看看今天的日报"}}}`))
		// 3) 白名单外：不落轨迹
		writeServerText(t, conn, []byte(`{"cmd":"aibot_msg_callback","headers":{"req_id":"srv3"},"body":{"msgid":"m2","from":{"userid":"stranger"},"text":{"content":"骗子"}}}`))
		// 4) 互踢：事件帧嵌套形态（SDK 线上事实）
		writeServerText(t, conn, []byte(`{"cmd":"aibot_event_callback","headers":{"req_id":"srv4"},"body":{"event":{"eventtype":"disconnected_event"}}}`))
		time.Sleep(100 * time.Millisecond)
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- b.Run(ctx) }()

	raw := waitFrame(t, frames)
	f, err := parseFrame(raw)
	if err != nil || f.Cmd != "aibot_subscribe" {
		t.Fatalf("首帧应为订阅: %s", raw)
	}
	select {
	case err := <-errCh:
		if !errors.Is(err, ErrKicked) {
			t.Fatalf("互踢应返回 ErrKicked: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("等待互踢退出超时")
	}

	steps, err := tl.Steps()
	if err != nil {
		t.Fatal(err)
	}
	var msgs []traj.Step
	for _, s := range steps {
		if s.Type == traj.TypeMessage {
			msgs = append(msgs, s)
		}
	}
	if len(msgs) != 1 {
		t.Fatalf("应恰好 1 条入站消息（重推吸收+白名单外拒收）: %d", len(msgs))
	}
	from, _ := msgs[0].Field("from")
	cid, _ := msgs[0].Field("client_message_id")
	src, _ := msgs[0].Field("source")
	if from != "wecom:zhangsan" || cid != "wecom:m1" || src != "wecom" {
		t.Fatalf("消息字段不符: from=%s cid=%s src=%s", from, cid, src)
	}
}

// TestAuthFailureExhausted 钉死凭证判死：连续 5 次认证回执
// errcode!=0 → Run 以 ErrAuthFailed 终止（不再无限重连）。
func TestAuthFailureExhausted(t *testing.T) {
	var mu sync.Mutex
	n := 0
	b, _, _ := newTestBridge(t, func(conn net.Conn, _ chan<- []byte) {
		br := bufio.NewReader(conn)
		_, reqID, _ := readClientJSON(t, br)
		writeServerText(t, conn, ackFrame(reqID, 40001, "invalid secret"))
		mu.Lock()
		n++
		mu.Unlock()
		time.Sleep(50 * time.Millisecond)
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- b.Run(ctx) }()
	select {
	case err := <-errCh:
		if !errors.Is(err, ErrAuthFailed) {
			t.Fatalf("应以 ErrAuthFailed 终止: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("等待认证判死超时")
	}
	mu.Lock()
	defer mu.Unlock()
	if n != authFailureLimit {
		t.Fatalf("应恰好尝试 %d 次，实际 %d", authFailureLimit, n)
	}
}

// TestOutboundMapsToChatidAndSegments 钉死出站链路：回复步骤 →
// aibot_send_msg（chatid=userid、markdown、req_id 对账、回执
// errcode=0）；超长文本分段。
func TestOutboundMapsToChatidAndSegments(t *testing.T) {
	var mu sync.Mutex
	var sent [][]byte
	b, tl, frames := newTestBridge(t, func(conn net.Conn, serverFrames chan<- []byte) {
		br := bufio.NewReader(conn)
		cmd, reqID, raw := readClientJSON(t, br)
		if cmd != "aibot_subscribe" {
			t.Errorf("首帧应为订阅: %s", cmd)
			return
		}
		serverFrames <- raw
		writeServerText(t, conn, ackFrame(reqID, 0, "ok"))
		for {
			// EOF = 测试结束、桥正常关闭连接——终局而非错误。
			op, payload, err := readMaskedFrame(br)
			if err != nil {
				return
			}
			if op != ws.OpText {
				continue
			}
			f, err := parseFrame(payload)
			if err != nil || f.IsResponse {
				continue
			}
			cmd, reqID, payload := f.Cmd, f.ReqID, payload
			t.Logf("server 收到帧 cmd=%s req=%s", cmd, reqID)
			if cmd != "aibot_send_msg" {
				continue
			}
			mu.Lock()
			sent = append(sent, payload)
			mu.Unlock()
			writeServerText(t, conn, ackFrame(reqID, 0, "ok"))
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = b.Run(ctx) }()
	waitFrame(t, frames) // 订阅帧就绪：桥已连上

	// 短回复先行（隔离定位：s2 暂不发）。
	s1 := traj.NewStep(traj.TypeMessage)
	s1.Fields["from"] = "ada"
	s1.Fields["to"] = "wecom:zhangsan"
	s1.Fields["content"] = "短回复"
	if err := tl.Append(context.Background(), s1); err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("段落内容。\n\n", 3000) // ~21000 rune
	s2 := traj.NewStep(traj.TypeMessage)
	s2.Fields["from"] = "ada"
	s2.Fields["to"] = "wecom:zhangsan"
	s2.Fields["content"] = long
	if err := tl.Append(context.Background(), s2); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(sent)
		mu.Unlock()
		if n >= 3 { // 1 短 + ≥2 分段
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(sent) < 3 {
		t.Fatalf("应发出 ≥3 帧（1 短 + 2 分段），实际 %d", len(sent))
	}
	f, err := parseFrame(sent[0])
	if err != nil || f.Cmd != cmdSendMsg || f.ReqID == "" {
		t.Fatalf("出站帧应为带 req_id 的 aibot_send_msg: %s %v", sent[0], err)
	}
	if !strings.Contains(string(sent[0]), `"chatid":"zhangsan"`) {
		t.Fatalf("chatid 映射缺失: %s", sent[0])
	}
	for _, p := range sent[1:] {
		pf, err := parseFrame(p)
		if err != nil {
			t.Fatalf("分段帧解析: %v", err)
		}
		var seg struct {
			Markdown struct {
				Content string `json:"content"`
			} `json:"markdown"`
		}
		if err := json.Unmarshal(pf.Body, &seg); err != nil {
			t.Fatalf("分段 markdown: %v", err)
		}
		if len([]rune(seg.Markdown.Content)) > maxMarkdownBytes {
			t.Fatalf("分段超限: %d > %d", len([]rune(seg.Markdown.Content)), maxMarkdownBytes)
		}
	}
}

// newTestBridgeOpts 是 newTestBridge 的可注入变体：测试按需调整
// Options（流式/审批轮询间隔等）。
func newTestBridgeOpts(t *testing.T, script serverScript, mutate func(*Options)) (*Bridge, *traj.Timeline, chan []byte) {
	t.Helper()
	b, tl, frames := newTestBridge(t, script)
	if mutate != nil {
		mutate(&b.opts)
	}
	return b, tl, frames
}

// newTestBridgeOpts 是 newTestBridge 的可注入变体：测试按需调整
// Options（流式/审批轮询间隔等）。
