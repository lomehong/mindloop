package ws

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ---------- 测试侧服务端原语 ----------

// hijack 握手并接管连接：计算 Accept、回 101，返回原始连接与读端。
func hijack(t *testing.T, w http.ResponseWriter, r *http.Request) (net.Conn, *bufio.ReadWriter) {
	t.Helper()
	if v := r.Header.Get("Sec-WebSocket-Version"); v != "13" {
		http.Error(w, "bad version", http.StatusBadRequest)
		t.Fatal("版本头缺失或不对")
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		t.Fatal("无法 hijack")
	}
	conn, brw, err := hj.Hijack()
	if err != nil {
		t.Fatal(err)
	}
	accept := acceptKey(r.Header.Get("Sec-WebSocket-Key"))
	resp := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + accept + "\r\n\r\n"
	if _, err := brw.WriteString(resp); err != nil {
		t.Fatal(err)
	}
	if err := brw.Flush(); err != nil {
		t.Fatal(err)
	}
	return conn, brw
}

// writeServerFrame 服务端发帧（可选掩码位——用于协议违规测试）。
func writeServerFrame(t *testing.T, conn net.Conn, op Opcode, payload []byte, mask bool) {
	t.Helper()
	hdr := []byte{0x80 | byte(op), byte(len(payload))}
	if mask {
		hdr[1] |= 0x80
		hdr = append(hdr, 0, 0, 0, 0) // 全零掩码键
	}
	if _, err := conn.Write(append(hdr, payload...)); err != nil {
		t.Fatal(err)
	}
}

// readClientFrame 读客户端帧并解掩码（客户端帧必须掩码）。长度走
// 完整的 7/16/64 位解码——大帧测试恰在被测路径上。
func readClientFrame(t *testing.T, br *bufio.Reader) (Opcode, []byte) {
	t.Helper()
	var hdr [2]byte
	if _, err := ioReadFull(br, hdr[:]); err != nil {
		t.Fatalf("读帧头: %v", err)
	}
	op := Opcode(hdr[0] & 0x0F)
	masked := hdr[1]&0x80 != 0
	length := int(hdr[1] & 0x7F)
	switch length {
	case 126:
		var ext [2]byte
		if _, err := ioReadFull(br, ext[:]); err != nil {
			t.Fatal(err)
		}
		length = int(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := ioReadFull(br, ext[:]); err != nil {
			t.Fatal(err)
		}
		length = int(binary.BigEndian.Uint64(ext[:]))
	}
	if !masked {
		t.Fatal("客户端帧未掩码（服务端应拒绝）")
	}
	var key [4]byte
	if _, err := ioReadFull(br, key[:]); err != nil {
		t.Fatal(err)
	}
	payload := make([]byte, length)
	if _, err := ioReadFull(br, payload); err != nil {
		t.Fatal(err)
	}
	for i := range payload {
		payload[i] ^= key[i%4]
	}
	return op, payload
}

// ioReadFull 避免测试文件 import 冲突的最小读满实现。
func ioReadFull(br *bufio.Reader, buf []byte) (int, error) {
	n := 0
	for n < len(buf) {
		m, err := br.Read(buf[n:])
		n += m
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

// wsURL 把 httptest 的 http(s) URL 换成 ws(s)。
func wsURL(serverURL string) string {
	return "ws" + strings.TrimPrefix(serverURL, "http") + "/ws"
}

// ---------- 测试 ----------

func TestDialEchoRoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, brw := hijack(t, w, r)
		defer conn.Close()
		// 期待一条掩码文本帧，回显（服务端帧不掩码）。
		op, payload := readClientFrame(t, brw.Reader)
		if op != OpText {
			t.Errorf("op = %d", op)
		}
		hdr := []byte{0x80 | byte(OpText), byte(len(payload))}
		if _, err := conn.Write(append(hdr, payload...)); err != nil {
			t.Error(err)
		}
	}))
	defer srv.Close()

	c, err := Dial(context.Background(), wsURL(srv.URL))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()
	if err := c.WriteMessage(OpText, []byte("你好，websocket")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	op, data, err := c.ReadMessage()
	if err != nil || op != OpText || string(data) != "你好，websocket" {
		t.Fatalf("echo = op=%d data=%q err=%v", op, data, err)
	}
}

func TestDialLargePayloadLengths(t *testing.T) {
	// 126（16 位长度）与 127（64 位长度）两条编码路径各来一发。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, brw := hijack(t, w, r)
		defer conn.Close()
		for i := 0; i < 2; i++ {
			op, payload := readClientFrame(t, brw.Reader)
			if op != OpBinary {
				t.Errorf("op = %d", op)
			}
			hdr := []byte{0x80 | byte(OpBinary)}
			n := len(payload)
			switch {
			case n <= 125:
				hdr = append(hdr, byte(n))
			case n <= 0xFFFF:
				hdr = append(hdr, 126, byte(n>>8), byte(n))
			default:
				ext := make([]byte, 8)
				binary.BigEndian.PutUint64(ext, uint64(n))
				hdr = append(append(hdr, 127), ext...)
			}
			if _, err := conn.Write(append(hdr, payload...)); err != nil {
				t.Error(err)
			}
		}
	}))
	defer srv.Close()

	c, err := Dial(context.Background(), wsURL(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	mid := make([]byte, 300)   // 126 路径
	big := make([]byte, 70000) // 127 路径
	rand.Read(mid)
	rand.Read(big)
	if err := c.WriteMessage(OpBinary, mid); err != nil {
		t.Fatalf("写 16 位长度帧: %v", err)
	}
	if err := c.WriteMessage(OpBinary, big); err != nil {
		t.Fatalf("写 64 位长度帧: %v", err)
	}
	for _, want := range [][]byte{mid, big} {
		op, data, err := c.ReadMessage()
		if err != nil || op != OpBinary || !bytes.Equal(data, want) {
			t.Fatalf("大帧往返不符: op=%d len=%d err=%v", op, len(data), err)
		}
	}
}

func TestPingAutoPong(t *testing.T) {
	pongSeen := make(chan []byte, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, brw := hijack(t, w, r)
		defer conn.Close()
		writeServerFrame(t, conn, OpPing, []byte("hb"), false)
		op, payload := readClientFrame(t, brw.Reader)
		if op == OpPong {
			pongSeen <- payload
		}
	}))
	defer srv.Close()

	c, err := Dial(context.Background(), wsURL(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	go c.ReadMessage() // 读循环内部消化 ping 并回 pong
	select {
	case payload := <-pongSeen:
		if string(payload) != "hb" {
			t.Fatalf("pong 载荷 = %q", payload)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("未观察到 pong")
	}
}

func TestFragmentedReassembly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _ := hijack(t, w, r)
		defer conn.Close()
		// 分片：text FIN=0 + continuation FIN=0 + continuation FIN=1。
		conn.Write([]byte{0x01, 3, 'a', 'b', 'c'})
		conn.Write([]byte{0x00, 1, 'd'})
		conn.Write([]byte{0x80, 1, 'e'})
	}))
	defer srv.Close()

	c, err := Dial(context.Background(), wsURL(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	op, data, err := c.ReadMessage()
	if err != nil || op != OpText || string(data) != "abcde" {
		t.Fatalf("分片重组 = op=%d %q err=%v", op, data, err)
	}
}

func TestFragmentedOversizeRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _ := hijack(t, w, r)
		defer conn.Close()
		// 两片合计超 maxMessage（测试里收紧到 16）。
		conn.Write([]byte{0x01, 12})
		conn.Write(make([]byte, 12))
		conn.Write([]byte{0x00, 12})
		conn.Write(make([]byte, 12))
	}))
	defer srv.Close()

	c, err := Dial(context.Background(), wsURL(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.maxMessage = 16
	if _, _, err := c.ReadMessage(); err == nil {
		t.Fatal("超限分片应被拒绝")
	}
}

func TestCloseHandshake(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, brw := hijack(t, w, r)
		defer conn.Close()
		writeServerFrame(t, conn, OpClose, nil, false)
		op, _ := readClientFrame(t, brw.Reader)
		if op != OpClose {
			t.Errorf("close 回执 op = %d", op)
		}
	}))
	defer srv.Close()

	c, err := Dial(context.Background(), wsURL(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, _, err := c.ReadMessage(); !errors.Is(err, ErrClosed) {
		t.Fatalf("close 后 ReadMessage = %v，应 ErrClosed", err)
	}
}

func TestDialRejectsBadAccept(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, _ := w.(http.Hijacker)
		conn, brw, _ := hj.Hijack()
		defer conn.Close()
		resp := "HTTP/1.1 101 Switching Protocols\r\n" +
			"Upgrade: websocket\r\nConnection: Upgrade\r\n" +
			"Sec-WebSocket-Accept: 伪造的key\r\n\r\n"
		brw.WriteString(resp)
		brw.Flush()
	}))
	defer srv.Close()
	if _, err := Dial(context.Background(), wsURL(srv.URL)); err == nil || !strings.Contains(err.Error(), "Accept") {
		t.Fatalf("Accept 不匹配应拒绝: %v", err)
	}
}

func TestAcceptKeyMatchesRFCExample(t *testing.T) {
	// RFC 6455 §1.3 的官方示例值。
	if got := acceptKey("dGhlIHNhbXBsZSBub25jZQ=="); got != "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=" {
		t.Fatalf("acceptKey = %s", got)
	}
}

func TestHostOnly(t *testing.T) {
	cases := map[string]string{
		"example.com:443": "example.com",
		"example.com":     "example.com",
		"[::1]:8080":      "::1",
	}
	for in, want := range cases {
		if got := hostOnly(in); got != want {
			t.Fatalf("hostOnly(%q) = %q，应 %q", in, got, want)
		}
	}
}
