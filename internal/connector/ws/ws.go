// Package ws 是最小 RFC 6455 WebSocket 客户端，纯标准库——国内
// 渠道（企微智能机器人/钉钉 Stream/飞书）的长连接共用这一个传输层。
//
// 只实现客户端必需子集：Upgrade 握手（含 Accept 校验）、文本/二进制
// 帧、ping/pong（对端 ping 自动回 pong）、close 握手、客户端掩码、
// 分片重组（带上限）。明确不做：扩展协商（响应带 Sec-WebSocket-
// Extensions 即拒绝）、服务端、超限消息（拒绝）。若协议边缘 case
// 失控，按设计文档的升级路径切换为子包局部破例（coder/websocket），
// 本包的调用面即为替换边界。
package ws

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Opcode 是帧操作码（RFC 6455 §5.2）。
type Opcode byte

const (
	OpContinuation Opcode = 0x0
	OpText         Opcode = 0x1
	OpBinary       Opcode = 0x2
	OpClose        Opcode = 0x8
	OpPing         Opcode = 0x9
	OpPong         Opcode = 0xA
)

// ErrClosed 是对端关闭（close 握手或连接断开）的哨兵——调用方以
// errors.Is 区分"该重连了"与其他失败。
var ErrClosed = errors.New("ws: 连接已关闭")

// DefaultMaxMessage 是分片重组的字节上限（渠道消息远小于此）。
const DefaultMaxMessage = 1 << 20

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// Conn 是一条 WebSocket 客户端连接。
type Conn struct {
	conn       net.Conn
	br         *bufio.Reader
	wmu        sync.Mutex
	maxMessage int64
	closed     bool
}

// Dial 建立 WebSocket 连接（ws:// 或 wss://）。TLS 采用系统默认
// 配置（SNI/系统根证书）；握手超时 15s。
func Dial(ctx context.Context, rawURL string) (*Conn, error) {
	return DialWithTLS(ctx, rawURL, nil)
}

// DialWithTLS 同 Dial，允许注入 TLS 配置（测试与内网场景）。
func DialWithTLS(ctx context.Context, rawURL string, tlscfg *tls.Config) (*Conn, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("ws: URL 解析失败: %w", err)
	}
	var secure bool
	switch u.Scheme {
	case "wss":
		secure = true
	case "ws":
	default:
		return nil, fmt.Errorf("ws: 不支持的协议 %q（ws/wss）", u.Scheme)
	}
	host := u.Host
	addr := host
	if u.Port() == "" {
		if secure {
			addr = host + ":443"
		} else {
			addr = host + ":80"
		}
	}
	d := net.Dialer{Timeout: 15 * time.Second}
	raw, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("ws: 建连失败: %w", err)
	}
	if secure {
		cfg := tlscfg
		if cfg == nil {
			cfg = &tls.Config{}
		}
		if cfg.ServerName == "" {
			cfg = cfg.Clone()
			cfg.ServerName = hostOnly(host)
		}
		raw = tls.Client(raw, cfg)
	}
	// 握手deadline：建连+升级全程限时，之后交给长连接语义。
	_ = raw.SetDeadline(time.Now().Add(15 * time.Second))

	keyBytes := make([]byte, 16)
	if _, err := rand.Read(keyBytes); err != nil {
		raw.Close()
		return nil, err
	}
	key := base64.StdEncoding.EncodeToString(keyBytes)
	path := u.RequestURI()
	if path == "" {
		path = "/"
	}
	req := "GET " + path + " HTTP/1.1\r\n" +
		"Host: " + host + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + key + "\r\n" +
		"Sec-WebSocket-Version: 13\r\n\r\n"
	if _, err := raw.Write([]byte(req)); err != nil {
		raw.Close()
		return nil, fmt.Errorf("ws: 握手请求写入失败: %w", err)
	}
	br := bufio.NewReader(raw)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		raw.Close()
		return nil, fmt.Errorf("ws: 握手响应解析失败: %w", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		raw.Close()
		return nil, fmt.Errorf("ws: 握手被拒（%s）", resp.Status)
	}
	if !strings.EqualFold(resp.Header.Get("Upgrade"), "websocket") ||
		!strings.Contains(strings.ToLower(resp.Header.Get("Connection")), "upgrade") {
		raw.Close()
		return nil, errors.New("ws: 响应不是 WebSocket 升级")
	}
	// Accept 校验：base64(sha1(key+GUID))——不校验等于接受任何
	// 101 响应，WebSocket 伪装服务可注入任意帧。
	want := acceptKey(key)
	if resp.Header.Get("Sec-WebSocket-Accept") != want {
		raw.Close()
		return nil, errors.New("ws: Sec-WebSocket-Accept 不匹配")
	}
	if resp.Header.Get("Sec-WebSocket-Extensions") != "" {
		// 未协商任何扩展：服务端强推（如 permessage-deflate）时
		// 帧语义会超出本实现，直接拒绝而不是静默误解。
		raw.Close()
		return nil, errors.New("ws: 服务端要求扩展，本客户端未协商")
	}
	_ = raw.SetDeadline(time.Time{})
	return &Conn{conn: raw, br: br, maxMessage: DefaultMaxMessage}, nil
}

// ReadMessage 读取下一条完整消息（分片重组；对端 ping 自动回 pong，
// close 自动回执并返回 ErrClosed）。只返回 Text/Binary，控制帧在
// 内部消化。
func (c *Conn) ReadMessage() (Opcode, []byte, error) {
	var msg []byte
	var msgOp Opcode
	inMsg := false
	for {
		op, payload, fin, err := c.readFrame()
		if err != nil {
			return 0, nil, err
		}
		switch op {
		case OpText, OpBinary:
			if inMsg {
				return 0, nil, errors.New("ws: 分片中途出现新数据帧")
			}
			if fin {
				return op, payload, nil
			}
			msgOp, msg, inMsg = op, append(msg, payload...), true
		case OpContinuation:
			if !inMsg {
				return 0, nil, errors.New("ws: 无开始的 continuation 帧")
			}
			if int64(len(msg))+int64(len(payload)) > c.maxMessage {
				return 0, nil, errors.New("ws: 消息超限")
			}
			msg = append(msg, payload...)
			if fin {
				return msgOp, msg, nil
			}
		case OpPing:
			if err := c.writeFrame(OpPong, payload); err != nil {
				return 0, nil, err
			}
		case OpPong:
			// 心跳回执：本客户端不做单向 ping 校验，忽略。
		case OpClose:
			_ = c.writeFrame(OpClose, nil)
			_ = c.conn.Close()
			return 0, nil, ErrClosed
		default:
			return 0, nil, fmt.Errorf("ws: 未知操作码 %d", op)
		}
	}
}

// WriteMessage 写一条数据帧（文本/二进制）。本实现的写入不分片
// ——渠道消息远小于一帧上限，一帧即一条完整消息。
func (c *Conn) WriteMessage(op Opcode, data []byte) error {
	return c.writeFrame(op, data)
}

// WritePing 发送 ping（保活；载荷 ≤125 字节）。
func (c *Conn) WritePing(data []byte) error {
	return c.writeFrame(OpPing, data)
}

// Close 执行 close 握手并关闭底层连接（幂等）。
func (c *Conn) Close() error {
	c.wmu.Lock()
	if !c.closed {
		c.closed = true
		_ = c.writeFrameLocked(OpClose, nil)
	}
	c.wmu.Unlock()
	return c.conn.Close()
}

// readFrame 读一帧（服务端→客户端不掩码；掩码位为 1 是协议违规）。
func (c *Conn) readFrame() (op Opcode, payload []byte, fin bool, err error) {
	var hdr [2]byte
	if _, err = io.ReadFull(c.br, hdr[:]); err != nil {
		return 0, nil, false, err
	}
	op = Opcode(hdr[0] & 0x0F)
	fin = hdr[0]&0x80 != 0
	if hdr[0]&0x70 != 0 {
		return 0, nil, false, errors.New("ws: RSV 位非零（扩展未协商）")
	}
	masked := hdr[1]&0x80 != 0
	length := int64(hdr[1] & 0x7F)
	switch length {
	case 126:
		var ext [2]byte
		if _, err = io.ReadFull(c.br, ext[:]); err != nil {
			return 0, nil, false, err
		}
		length = int64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err = io.ReadFull(c.br, ext[:]); err != nil {
			return 0, nil, false, err
		}
		length = int64(binary.BigEndian.Uint64(ext[:]))
	}
	if length > c.maxMessage {
		return 0, nil, false, errors.New("ws: 帧超限")
	}
	if masked {
		return 0, nil, false, errors.New("ws: 服务端帧不得掩码")
	}
	payload = make([]byte, length)
	if _, err = io.ReadFull(c.br, payload); err != nil {
		return 0, nil, false, err
	}
	return op, payload, fin, nil
}

func (c *Conn) writeFrame(op Opcode, payload []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.closed {
		return ErrClosed
	}
	return c.writeFrameLocked(op, payload)
}

func (c *Conn) writeFrameLocked(op Opcode, payload []byte) error {
	if op != OpBinary && op != OpText && len(payload) > 125 {
		return errors.New("ws: 控制帧载荷超 125 字节")
	}
	// 客户端帧必须掩码（RFC 6455 §5.1）：首字节 FIN=1 单帧，第二
	// 字节掩码位恒置 1，载荷逐字节异或随机密钥。
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
	var key [4]byte
	if _, err := rand.Read(key[:]); err != nil {
		return err
	}
	masked := make([]byte, n)
	for i := range payload {
		masked[i] = payload[i] ^ key[i%4]
	}
	buf := make([]byte, 0, len(hdr)+4+n)
	buf = append(buf, hdr...)
	buf = append(buf, key[:]...)
	buf = append(buf, masked...)
	_, err := c.conn.Write(buf)
	return err
}

// acceptKey 计算 RFC 6455 §4.2.2 的 Sec-WebSocket-Accept 期望值。
func acceptKey(key string) string {
	h := sha1.Sum([]byte(key + wsGUID))
	return base64.StdEncoding.EncodeToString(h[:])
}

// hostOnly 剥掉端口的 host（TLS SNI 用）：IPv6 先按 "]" 截断再去
// 括号（SNI ServerName 不含方括号），否则按最后一个冒号截断。
func hostOnly(hostport string) string {
	if i := strings.LastIndex(hostport, "]"); i >= 0 {
		return strings.Trim(hostport[:i+1], "[]")
	}
	if i := strings.LastIndex(hostport, ":"); i >= 0 {
		return hostport[:i]
	}
	return hostport
}
