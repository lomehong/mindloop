package wecom

// 流式回复泵：tail responder 的流式旁路目录（<timeline>/stream/
// <reply_to>.txt——全量累积、消失即终稿已落轨迹），把增量内容经
// aibot_respond_msg 推到企微（打字机效果）。req_id 凭证来自会话
// 跟踪（入站回调落盘时记录）；丢失即降级——不流式，最终回复由
// 出站泵按既有路径送达。
//
// 去重契约：finish=true 发送**之前**先把 reply 步骤标记进
// streamedSet（崩溃窗口宁丢终稿不重复推送）；ack 失败则撤销标记，
// 终稿交还出站泵补发。

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/lomehong/mindloop/internal/connector/ws"
	"github.com/lomehong/mindloop/internal/traj"
)

// streamEntry 是一个在途流式文件的处理态。
type streamEntry struct {
	streamID string
	lastSent string // 上次推送的全量内容（含终稿）
	frames   int    // 已成功的增量帧数（不含 finish 终稿）
	done     bool   // 已发 finish=true
}

// runStreamer 轮询流式旁路目录直到 ctx 取消。interval 是轮询与
// 节流共用粒度（首帧延迟 ≈ interval，参考实现为 500ms 量级）。
func (b *Bridge) runStreamer(ctx context.Context, interval time.Duration) {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	active := map[string]*streamEntry{} // replyTo → 处理态
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			b.streamOnce(ctx, active)
		}
	}
}

// streamOnce 处理一轮目录扫描：在途文件推增量；消失的文件收终稿。
func (b *Bridge) streamOnce(ctx context.Context, active map[string]*streamEntry) {
	entries, err := os.ReadDir(streamDirOf(b.opts.Timeline.Dir))
	if err != nil {
		return // 目录不存在 = 没有流式在途（常态）
	}
	seen := map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".txt") {
			continue
		}
		replyTo := strings.TrimSuffix(name, ".txt")
		seen[replyTo] = true
		b.streamFile(ctx, active, replyTo, filepath.Join(streamDirOf(b.opts.Timeline.Dir), name))
	}
	// 消失的在途文件：终稿已落轨迹——补 finish=true（若尚未发）。
	for replyTo, ent := range active {
		if seen[replyTo] || ent.done {
			continue
		}
		b.finishStream(ctx, active, replyTo, ent)
	}
}

// streamDirOf 是流式旁路目录（与 mind.streamDir 同一定义——mind 包
// 未导出，bridge 以文件协议消费，不 import mind 内部符号）。
func streamDirOf(tlDir string) string { return filepath.Join(tlDir, "stream") }

// randomStreamID 生成流式回合 id：前缀 + 纯 hex16（对齐参考实现
// "dsh-"+hex16 的形态——id 会出现在客户端渲染路径上，保持字母
// 数字、长度克制）。
func randomStreamID() string {
	var rnd [8]byte
	_, _ = rand.Read(rnd[:])
	return "ml" + hex.EncodeToString(rnd[:])
}

// streamFile 处理单个在途流式文件：建立会话关联、节流推送增量。
func (b *Bridge) streamFile(ctx context.Context, active map[string]*streamEntry, replyTo, path string) {
	sess, ok := b.sessions.get(replyTo)
	if !ok {
		return // 非企微来源的回复：不流式，走出站泵
	}
	ent := active[replyTo]
	if ent == nil {
		ent = &streamEntry{streamID: randomStreamID()}
		active[replyTo] = ent
	}
	if ent.done {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	content := string(data)
	if content == "" || content == ent.lastSent {
		return
	}
	// 节流：tick 粒度即推送粒度（首帧延迟与增量间隔 ≈ interval）。
	if err := b.respondStream(ctx, sess.ReqID, ent.streamID, content, false); err != nil {
		b.logf("流式推送失败（续写终稿仍会下发）: %v", err)
		return
	}
	if ent.lastSent == "" {
		b.logf("流式开始（reply %s…，%d 字）", shortHash(replyTo), len(content))
	}
	ent.frames++
	ent.lastSent = content
}

// finishStream 发送流式终稿：内容以轨迹上 reply_to 匹配的最终
// message 步骤为准（旁路文件只是投影）。tracked 回复的投递责任在
// 流式泵（出站泵已跳过）——respond 终稿失败必须兜底 sendMessage，
// 否则这条回复就丢了。
func (b *Bridge) finishStream(ctx context.Context, active map[string]*streamEntry, replyTo string, ent *streamEntry) {
	sess, ok := b.sessions.get(replyTo)
	if !ok {
		delete(active, replyTo)
		return
	}
	final := b.findReply(replyTo)
	if final == "" {
		final = ent.lastSent
	}
	if final == "" {
		delete(active, replyTo)
		return
	}
	if err := b.respondStream(ctx, sess.ReqID, ent.streamID, final, true); err != nil {
		b.logf("流式终稿失败，兜底主动推送（%d 字）: %v", len(final), err)
		if err := b.sendFallback(ctx, sess.Userid, final); err != nil {
			b.logf("兜底推送也失败（回复保留在轨迹/对话流）: %v", err)
		}
	} else {
		b.logf("流式完成（%d 字，含终稿共 %d 帧）", len(final), ent.frames+1)
	}
	ent.done = true
	delete(active, replyTo)
}

// findReply 在轨迹尾部找 reply_to 匹配的最终回复正文。
func (b *Bridge) findReply(replyTo string) string {
	steps, err := b.opts.Timeline.Steps()
	if err != nil {
		return ""
	}
	for i := len(steps) - 1; i >= 0; i-- {
		s := steps[i]
		if s.Type != traj.TypeMessage {
			continue
		}
		if rt, ok := s.Field("reply_to"); ok && rt == replyTo {
			content, _ := s.Field("content")
			return content
		}
	}
	return ""
}

// sendFallback 是流式失败时的兜底主动推送（chatid=点击/发信 userid）。
func (b *Bridge) sendFallback(ctx context.Context, userid, content string) error {
	c := b.currentConn()
	if c == nil {
		return errors.New("wecom: 连接未就绪")
	}
	reqID := newReqID(cmdSendMsg)
	if err := c.WriteMessage(ws.OpText, sendMsgFrame(userid, content, reqID)); err != nil {
		return err
	}
	resp, err := b.awaitResponse(ctx, reqID)
	if err != nil {
		return err
	}
	if resp.Errcode != 0 {
		return errors.New("send 失败（errcode=" + strconv.FormatInt(resp.Errcode, 10) + "）: " + resp.Errmsg)
	}
	return nil
}

// respondStream 推送一帧流式内容并等 errcode 回执。
func (b *Bridge) respondStream(ctx context.Context, reqID, streamID, content string, finish bool) error {
	c := b.currentConn()
	if c == nil {
		return errors.New("wecom: 连接未就绪")
	}
	if err := c.WriteMessage(ws.OpText, streamRespondFrame(reqID, streamID, content, finish)); err != nil {
		return err
	}
	resp, err := b.awaitResponse(ctx, reqID)
	if err != nil {
		return err
	}
	if resp.Errcode != 0 {
		return errors.New("respond 失败（errcode=" + strconv.FormatInt(resp.Errcode, 10) + "）: " + resp.Errmsg)
	}
	return nil
}
