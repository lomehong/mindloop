// SendOnce：企微一次性推送（死睡告警等外部检查者用，watchdog v1）。
// 与 Bridge 的分工：Bridge 是常驻会话（订阅 + 入站处理 + 出站泵），
// SendOnce 只做「拨号 → 订阅 → 发一条 → 关」的最小生命周期，不碰
// 轨迹、不收 inbound。协议帧与解析全部复用 protocol.go 的单源实现
// （subscribeFrame/sendMsgFrame/parseFrame），不另立协议知识。
//
// 凭据从调用方传入（操作员在计划任务包装里显式配置——watchdog 是
// 操作员工具，不经身份 .env）；to 形如 wecom:<userid>，由调用方
// 显式给定（--wecom-to），等价于操作员亲手指定告警收件人。
package wecom

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lomehong/mindloop/internal/connector/ws"
)

// SendOnceOptions 是一次性推送的最小凭据面。
type SendOnceOptions struct {
	BotID  string
	Secret string
	// Addr 缺省官方网关（与 Bridge 同源）。
	Addr string
}

// SendOnce 建立短会话推送一条文本到 to（wecom:<userid>），完成即关。
// 任何一步失败返回错误——调用方（watchdog）如实记录，不重试（下次
// 定时触发自然重试，与「告警去重」状态机解耦）。
func SendOnce(ctx context.Context, opts SendOnceOptions, to, text string) error {
	if strings.TrimSpace(opts.BotID) == "" || strings.TrimSpace(opts.Secret) == "" {
		return errors.New("wecom: SendOnce 缺少 BotID/Secret")
	}
	userid := strings.TrimPrefix(to, "wecom:")
	if userid == "" || userid == to {
		return fmt.Errorf("wecom: SendOnce 收件人应为 wecom:<userid>，得 %q", to)
	}
	if opts.Addr == "" {
		opts.Addr = "wss://openws.work.weixin.qq.com"
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	conn, err := dialReal(ctx, opts.Addr)
	if err != nil {
		return fmt.Errorf("wecom: SendOnce 拨号失败: %w", err)
	}
	defer conn.Close()

	// 读循环先行（认证回执也要经它进来——与 runOnce 同一条教训）。
	frames := make(chan []byte, 4)
	readErr := make(chan error, 1)
	go func() {
		defer close(frames)
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				readErr <- err
				return
			}
			frames <- data
		}
	}()

	authReq := newReqID(cmdSubscribe)
	if err := conn.WriteMessage(ws.OpText, subscribeFrame(opts.BotID, opts.Secret, authReq)); err != nil {
		return fmt.Errorf("wecom: SendOnce 订阅发送失败: %w", err)
	}
	sendReq := newReqID(cmdSendMsg)

	// 帧循环：等认证回执 → 发送 → 等 errcode 回执 → 收线。
	await := func(reqID string) (parsedFrame, error) {
		for {
			select {
			case <-ctx.Done():
				return parsedFrame{}, ctx.Err()
			case err := <-readErr:
				return parsedFrame{}, fmt.Errorf("wecom: SendOnce 连接断开: %w", err)
			case data, ok := <-frames:
				if !ok {
					return parsedFrame{}, errors.New("wecom: SendOnce 读循环退出")
				}
				f, err := parseFrame(data)
				if err != nil {
					continue // 坏帧跳过——与 Bridge 读循环同一容错
				}
				if f.IsResponse && f.ReqID == reqID {
					return f, nil
				}
				// 其余响应帧（心跳等）忽略。
			}
		}
	}
	auth, err := await(authReq)
	if err != nil {
		return err
	}
	if auth.Errcode != 0 {
		return fmt.Errorf("wecom: SendOnce 订阅失败（errcode=%d）: %s", auth.Errcode, auth.Errmsg)
	}
	if err := conn.WriteMessage(ws.OpText, sendMsgFrame(userid, text, sendReq)); err != nil {
		return fmt.Errorf("wecom: SendOnce 写帧失败: %w", err)
	}
	resp, err := await(sendReq)
	if err != nil {
		return err
	}
	if resp.Errcode != 0 {
		return fmt.Errorf("wecom: SendOnce 发送失败（errcode=%d）: %s", resp.Errcode, resp.Errmsg)
	}
	return nil
}
