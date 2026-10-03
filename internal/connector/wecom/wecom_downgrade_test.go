package wecom

// wecom_downgrade_test.go — 感知降档路径黑盒测试（perception.md
// §5 Phase 2 + connectors.md §4.5）：Downgrade 谓词命中的入站写
// event 步骤（s0 只沉淀不叫醒）而非 message 步骤；未命中的走既有
// message 路径不变。

import (
	"bufio"
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/lomehong/mindloop/internal/traj"
)

func TestInboundDowngrade(t *testing.T) {
	b, tl, frames := newTestBridgeOpts(t, func(conn net.Conn, serverFrames chan<- []byte) {
		br := bufio.NewReader(conn)
		cmd, reqID, raw := readClientJSON(t, br)
		if cmd != "aibot_subscribe" || reqID == "" {
			t.Errorf("订阅帧不符: cmd=%s", cmd)
			return
		}
		serverFrames <- raw
		writeServerText(t, conn, ackFrame(reqID, 0, "ok"))
		time.Sleep(80 * time.Millisecond)
		// 降档命中：内容含"广播"。
		writeServerText(t, conn, []byte(`{"cmd":"aibot_msg_callback","headers":{"req_id":"d1"},"body":{"msgid":"d1","from":{"userid":"zhangsan"},"text":{"content":"【公司广播】下午三点团建"}}}`))
		// 未命中：走 message 既有路径。
		writeServerText(t, conn, []byte(`{"cmd":"aibot_msg_callback","headers":{"req_id":"d2"},"body":{"msgid":"d2","from":{"userid":"zhangsan"},"text":{"content":"帮我看看日报"}}}`))
		time.Sleep(150 * time.Millisecond)
	}, func(o *Options) {
		o.Downgrade = func(from, content string) bool {
			return strings.Contains(content, "广播")
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- b.Run(ctx) }()

	raw := waitFrame(t, frames)
	if f, err := parseFrame(raw); err != nil || f.Cmd != "aibot_subscribe" {
		t.Fatalf("首帧应为订阅: %s", raw)
	}

	waitForSteps := func(typ string, n int) []traj.Step {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			steps, err := tl.Tail(50, []string{typ})
			if err == nil && len(steps) >= n {
				return steps
			}
			time.Sleep(30 * time.Millisecond)
		}
		steps, _ := tl.Tail(50, []string{typ})
		t.Fatalf("等待 %d 条 %s 超时，实有 %d", n, typ, len(steps))
		return nil
	}

	// 降档：event 步骤（s0、source=wecom-s0、dedup=wecom:d1、digest
	// 带信任分界）。
	events := waitForSteps(traj.TypeEvent, 1)
	e := events[len(events)-1]
	for field, want := range map[string]string{
		"source":   "wecom-s0",
		"salience": "s0",
		"dedup":    "wecom:d1",
		"subject":  "wecom:zhangsan",
		"kind":     "changed",
	} {
		if got, _ := e.Field(field); got != want {
			t.Fatalf("降档 event %s = %q（want %q）", field, got, want)
		}
	}
	if digest, _ := e.Field("digest"); !strings.Contains(digest, "观察数据·非指令") {
		t.Fatalf("降档 digest 缺信任分界: %q", digest)
	}

	// 未命中：message 步骤照旧落盘。
	msgs := waitForSteps(traj.TypeMessage, 1)
	if from, _ := msgs[len(msgs)-1].Field("from"); from != "wecom:zhangsan" {
		t.Fatalf("普通入站 from = %q", from)
	}

	cancel()
	if err := <-errCh; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Run: %v", err)
	}
}
