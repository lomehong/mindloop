package web

// d5：SSE 长存活场景。默认跳过（运行约 125s）——设置
// MINDLOOP_SSE_LONG=1 启用，覆盖计划验收"至少 2 分钟 SSE 存活"。

import (
	"bufio"
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"mindloop/internal/identity"
)

// TestStreamLongSurvival：空闲 SSE 连接存活 ≥2 分钟——心跳持续
// 到达，连接不被服务器写期限或空闲清理杀死；半程处追加一行轨迹，
// 确认长命连接仍在同一连接上实时投递真实事件（而非只剩心跳的
// 僵尸连接）。不复用 openStream（其 ctx 5s 兜底会掐断长连接），
// 自建 ctx 上限 140s：即使服务端僵死（无帧可读），读阻塞也有界。
func TestStreamLongSurvival(t *testing.T) {
	if os.Getenv("MINDLOOP_SSE_LONG") != "1" {
		t.Skip("设置 MINDLOOP_SSE_LONG=1 运行 ≥2 分钟 SSE 存活验证")
	}
	_, id := newIdentityHome(t)
	ts, s := newTestServer(t, identity.Home(), "")
	s.replyPollEvery = 100 * time.Millisecond
	s.replyPingEvery = time.Second

	ctx, cancel := context.WithTimeout(context.Background(), 140*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		ts.URL+"/api/identities/ada/replies/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q，应为 text/event-stream", ct)
	}
	r := bufio.NewReader(resp.Body)

	const total = 125 * time.Second
	start := time.Now()
	deadline := start.Add(total)
	mid := start.Add(total / 2)

	pings := 0
	wroteMid := false
	gotMidStep := false
	lastFrame := start
	for time.Now().Before(deadline) {
		f := readFrame(t, r)
		lastFrame = time.Now()
		if f.isComment {
			pings++
		}
		if f.event == "step" && wroteMid {
			gotMidStep = true
		}
		if !wroteMid && time.Now().After(mid) {
			appendStepLine(t, id, `{"type":"action","step_id":"actlong0001","ts":"2026-02-04T00:00:00.000Z","launched_by":"monolith","content":"long-survival probe"}`)
			wroteMid = true
		}
	}
	if pings < 100 {
		t.Fatalf("%v 内心跳仅 %d 个（每 %v 一个，应 ≥100）", total, pings, time.Second)
	}
	if !wroteMid || !gotMidStep {
		t.Fatalf("半程追加的轨迹未在长命连接上投递 step 事件（写入=%v 收到=%v）", wroteMid, gotMidStep)
	}
	if gap := time.Since(lastFrame); gap > 5*time.Second {
		t.Fatalf("结束时最后一帧已过去 %v，连接疑似僵死", gap)
	}
	t.Logf("空闲 SSE 存活 %v：心跳 %d 个；半程追加的行在同一连接上实时投递",
		total.Round(time.Second), pings)
}
