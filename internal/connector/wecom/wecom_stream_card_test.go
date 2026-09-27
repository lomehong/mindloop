package wecom

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"mindloop/internal/connector/ws"
	"mindloop/internal/traj"
)

// collectedFrames 按 cmd 分类收集桥发出的业务帧（供断言）。
type collectedFrames struct {
	mu    sync.Mutex
	byCmd map[string][][]byte
}

func (c *collectedFrames) add(cmd string, raw []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.byCmd == nil {
		c.byCmd = map[string][][]byte{}
	}
	c.byCmd[cmd] = append(c.byCmd[cmd], raw)
}

func (c *collectedFrames) count(cmd string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.byCmd[cmd])
}

func (c *collectedFrames) last(cmd string) []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	if n := len(c.byCmd[cmd]); n > 0 {
		return c.byCmd[cmd][n-1]
	}
	return nil
}

// readFrameQuiet 读一帧业务帧；连接关闭或回执帧返回 false。
func readFrameQuiet(br *bufio.Reader) (parsedFrame, bool) {
	_, _, f, ok := readFrameFull(br)
	return f, ok
}

// readFrameFull 同 readFrameQuiet，另带原始线帧（headers.req_id 在
// 断言里要用，Body 里没有）。
func readFrameFull(br *bufio.Reader) (op ws.Opcode, payload []byte, f parsedFrame, ok bool) {
	payload, err := func() ([]byte, error) {
		op, payload, ferr := readMaskedFrame(br)
		if ferr != nil || op != ws.OpText {
			return nil, fmt.Errorf("stop")
		}
		return payload, nil
	}()
	if err != nil {
		return 0, nil, parsedFrame{}, false
	}
	f, err = parseFrame(payload)
	if err != nil || f.IsResponse {
		return op, payload, parsedFrame{}, false
	}
	return op, payload, f, true
}

// writeServerFrameQuiet 空安全写帧（连接关闭 = 正常终局）。
func writeServerFrameQuiet(conn net.Conn, payload []byte) {
	hdr := []byte{0x80 | byte(ws.OpText)}
	if n := len(payload); n <= 125 {
		hdr = append(hdr, byte(n))
	} else if n <= 0xFFFF {
		hdr = append(hdr, 126, byte(n>>8), byte(n))
	} else {
		return
	}
	_, _ = conn.Write(append(hdr, payload...))
}

// bridgeScript 是假网关读循环：所有业务帧回 errcode=0 并按 cmd
// 分类收集；inject 通道把测试要注入的"服务端→桥"帧写到连接上
// （回调/点击事件等）。全部读写都是空安全变体（连接关闭 = 正常终局）。
func bridgeScript(col *collectedFrames, onFrame func(f parsedFrame), inject <-chan []byte) serverScript {
	return func(conn net.Conn, serverFrames chan<- []byte) {
		br := bufio.NewReader(conn)
		var wmu sync.Mutex
		write := func(payload []byte) {
			wmu.Lock()
			defer wmu.Unlock()
			writeServerFrameQuiet(conn, payload)
		}
		f, ok := readFrameQuiet(br)
		if !ok || f.Cmd != "aibot_subscribe" {
			return
		}
		if serverFrames != nil {
			serverFrames <- f.Body
		}
		write(ackFrame(f.ReqID, 0, "ok"))
		time.Sleep(60 * time.Millisecond) // 认证回执先被消费（帧序竞态）
		// 注入泵独立 goroutine：桥静默时 select 会阻塞在读取上，
		// 注入若只在读帧间隙检查会永久饥饿（实测点击送不进去）。
		go func() {
			for frame := range inject {
				write(frame)
			}
		}()
		for {
			_, lastPayload, f, ok := readFrameFull(br)
			if !ok {
				return
			}
			if col != nil {
				col.add(f.Cmd, lastPayload) // 完整线帧（headers.req_id 供断言）
			}
			write(ackFrame(f.ReqID, 0, "ok"))
			if onFrame != nil {
				onFrame(f)
			}
		}
	}
}

// waitCursorCovers 等待出站泵的游标推进越过 offset——此后泵已消费
// 该位置的全部步骤（跳过与否已成事实），断言不再有竞态窗口。
func waitCursorCovers(t *testing.T, cursorPath string, offset int64) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(cursorPath)
		if err == nil {
			var off int64
			if _, err := fmt.Sscanf(string(data), "%d", &off); err == nil && off >= offset {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("游标未推进到 %d", offset)
}

// TestStreamingFlowAndDedup 钉死流式全链路：入站回调建立会话 →
// stream/ 旁路增量经 aibot_respond_msg 推送（req_id 透传原回调、
// finish=false 全量替换）→ 终稿落轨迹 + 旁路消失 → finish=true →
// 出站泵跳过已流式的回复（游标推进过终稿后零 send_msg）。
func TestStreamingFlowAndDedup(t *testing.T) {
	col := &collectedFrames{}
	inject := make(chan []byte, 8)
	b, tl, frames := newTestBridgeOpts(t, bridgeScript(col, nil, inject), func(o *Options) {
		o.StreamInterval = 30 * time.Millisecond
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = b.Run(ctx) }()
	waitFrame(t, frames) // 订阅就绪

	// 1) 入站回调 → 轨迹消息步骤（会话记录 step→req_id）
	const cbReq = "srv-cb-1"
	inject <- []byte(`{"cmd":"aibot_msg_callback","headers":{"req_id":"srv-cb-1"},"body":{"msgid":"sm1","from":{"userid":"zhangsan"},"text":{"content":"讲个故事"}}}`)
	var inStepID string
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && inStepID == "" {
		steps, err := tl.Steps()
		if err == nil {
			for _, s := range steps {
				if cid, _ := s.Field("client_message_id"); cid == "wecom:sm1" {
					inStepID = s.StepID
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if inStepID == "" {
		t.Fatal("入站消息未落轨迹")
	}
	sess, sessOK := b.sessions.get(inStepID)
	t.Logf("session = %+v ok=%v", sess, sessOK)

	// 2) 流式增量：旁路文件全量写入 → respond 帧（透传 req_id）
	streamFile := filepath.Join(tl.Dir, "stream", inStepID+".txt")
	if err := os.MkdirAll(filepath.Dir(streamFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(streamFile, []byte("从前有座山，"), 0o644); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && col.count(cmdRespondMsg) < 1 {
		time.Sleep(10 * time.Millisecond)
	}
	first := col.last(cmdRespondMsg)
	if first == nil || !strings.Contains(string(first), `"finish":false`) || !strings.Contains(string(first), "从前有座山") {
		t.Fatalf("首帧流式不符: %s", first)
	}
	f2, _ := parseFrame(first)
	if f2.ReqID != cbReq {
		t.Fatalf("流式帧应透传原回调 req_id %q，实际 %q", cbReq, f2.ReqID)
	}

	// 3) 终稿：先落轨迹（真实 responder 顺序：Append 后才移除旁路
	// 文件），再移除 → finish=true 全量终稿。
	finalStep := traj.NewStep(traj.TypeMessage)
	finalStep.Fields["from"] = "ada"
	finalStep.Fields["to"] = "wecom:zhangsan"
	finalStep.Fields["reply_to"] = inStepID
	finalStep.Fields["content"] = "从前有座山，山里有座庙。"
	if err := tl.Append(context.Background(), finalStep); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(streamFile); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if last := col.last(cmdRespondMsg); last != nil && strings.Contains(string(last), `"finish":true`) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	lastR := col.last(cmdRespondMsg)
	if lastR == nil || !strings.Contains(string(lastR), `"finish":true`) || !strings.Contains(string(lastR), "山里有座庙") {
		t.Fatalf("终稿帧不符: %s", lastR)
	}

	// 4) 去重（确定性）：tracked 回复归流式泵所有（出站泵 Skip），
	// 游标推进越过终稿步骤后零 send_msg。
	finalSize := finalFileSize(t, tl.Path)
	waitCursorCovers(t, filepath.Join(b.opts.StateDir, "wecom-outbound.cursor"), finalSize)
	time.Sleep(200 * time.Millisecond)
	if n := col.count(cmdSendMsg); n != 0 {
		t.Fatalf("已流式的回复不应再 send_msg（%d 次）", n)
	}
}

func finalFileSize(t *testing.T, path string) int64 {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Size()
}

// TestApprovalCardFlow 钉死审批卡全链路：待批文件 → button_interaction
// 卡（短 token 按钮）→ 白名单内点击 → policy.Decide 落决策 → 卡片
// 更新帧（req_id 透传点击回调）；白名单外点击不落决策。
func TestApprovalCardFlow(t *testing.T) {
	col := &collectedFrames{}
	inject := make(chan []byte, 8)
	var mu sync.Mutex
	var clickToken string
	b, tl, frames := newTestBridgeOpts(t, bridgeScript(col, func(f parsedFrame) {
		// 审批卡发出即模拟白名单内用户点"允许"（提取按钮短 token）
		if f.Cmd == cmdSendMsg && strings.Contains(string(f.Body), "template_card") {
			var body struct {
				TemplateCard struct {
					ButtonList []struct {
						Key string `json:"key"`
					} `json:"button_list"`
				} `json:"template_card"`
			}
			_ = json.Unmarshal(f.Body, &body)
			if len(body.TemplateCard.ButtonList) > 0 {
				mu.Lock()
				clickToken = strings.TrimPrefix(body.TemplateCard.ButtonList[0].Key, keyApprove)
				mu.Unlock()
			}
		}
	}, inject), func(o *Options) {
		o.ApprovalInterval = 100 * time.Millisecond
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = b.Run(ctx) }()
	waitFrame(t, frames)

	// 1) 写待批请求文件（sha256 形态 64 hex）
	hash := strings.Repeat("ab", 32)
	dir := policyDirOf(tl.Dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	req := map[string]any{
		"hash": hash, "script": "rm -rf /tmp/x", "work_dir": "C:/tmp",
		"created": "2026-09-27T07:00:00Z", "expires": "2026-09-27T07:05:00Z",
	}
	reqBody, _ := json.Marshal(req)
	if err := os.WriteFile(filepath.Join(dir, "request-"+hash+".json"), reqBody, 0o644); err != nil {
		t.Fatal(err)
	}

	// 2) 审批卡出现（按钮带 8 位短 token）
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		tok := clickToken
		mu.Unlock()
		if tok != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	token := clickToken
	mu.Unlock()
	if len(token) != 8 {
		t.Fatalf("审批卡未出现或短 token 形态不符: %q", token)
	}

	// 3) 白名单外点击：不落决策
	inject <- []byte(`{"cmd":"aibot_event_callback","headers":{"req_id":"srv-click-1"},"body":{"event":{"eventtype":"template_card_event","template_card_event":{"event_key":"approve:` + token + `"},"from":{"userid":"stranger"}}}}`)
	time.Sleep(300 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(dir, "decision-"+hash+".json")); err == nil {
		dec, _ := os.ReadFile(filepath.Join(dir, "decision-"+hash+".json"))
		t.Fatalf("白名单外点击不应落决策（内容: %s）", dec)
	}

	// 4) 白名单内点击：决策落盘 + 卡片更新帧（req_id 透传点击回调）
	inject <- []byte(`{"cmd":"aibot_event_callback","headers":{"req_id":"srv-click-2"},"body":{"event":{"eventtype":"template_card_event","template_card_event":{"event_key":"approve:` + token + `"},"from":{"userid":"zhangsan"}}}}`)
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(dir, "decision-"+hash+".json")); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	decRaw, err := os.ReadFile(filepath.Join(dir, "decision-"+hash+".json"))
	if err != nil {
		t.Fatalf("决策未落盘: %v", err)
	}
	if !strings.Contains(string(decRaw), "approve") {
		t.Fatalf("决策应为 approve: %s", decRaw)
	}
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && col.count(cmdRespondUpdate) == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	upd := col.last(cmdRespondUpdate)
	if upd == nil || !strings.Contains(string(upd), "update_template_card") {
		t.Fatalf("卡片更新帧缺失: %s", upd)
	}
	f, _ := parseFrame(upd)
	if f.ReqID != "srv-click-2" {
		t.Fatalf("卡片更新应透传点击回调 req_id，实际 %q", f.ReqID)
	}
}

// connForTest 供测试向当前连接直接注入服务端帧（单连接语义下即
// 当前唯一连接）。无连接即测试时序问题，立刻暴露。
func (b *Bridge) connForTest(t *testing.T) net.Conn {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		b.mu.Lock()
		c := b.conn
		b.mu.Unlock()
		if pc, ok := c.(*pipeConn); ok {
			return pc.conn
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("桥没有活动连接")
	return nil
}

// policyDirOf 是审批控制面目录（与 policy.Dir 同一定义；测试侧直接
// 拼路径写文件，生产代码经 policy 包）。
func policyDirOf(tlDir string) string { return filepath.Join(tlDir, "run", "approvals") }
