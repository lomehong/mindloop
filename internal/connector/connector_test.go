package connector

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lomehong/mindloop/internal/traj"
)

// fakeDelivery 记录投递；failN 次失败注入（每次消耗一次）。
type fakeDelivery struct {
	mu    sync.Mutex
	sent  []sendCall
	failN int
}

type sendCall struct{ to, text string }

func (f *fakeDelivery) Send(ctx context.Context, to, text string) error {
	f.mu.Lock()
	shouldFail := f.failN > 0
	if shouldFail {
		f.failN--
	}
	f.mu.Unlock()
	if shouldFail {
		return errors.New("注入的投递失败")
	}
	f.mu.Lock()
	f.sent = append(f.sent, sendCall{to, text})
	f.mu.Unlock()
	return nil
}

func (f *fakeDelivery) snapshot() []sendCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]sendCall(nil), f.sent...)
}

// appendMessage 落一条 message 步骤（bridge 的消费对象）。
func appendMessage(t *testing.T, tl *traj.Timeline, from, to, content string) traj.Step {
	t.Helper()
	s := traj.NewStep(traj.TypeMessage)
	s.Fields["from"] = from
	s.Fields["to"] = to
	s.Fields["source"] = "chat"
	s.Fields["content"] = content
	if err := tl.Append(context.Background(), s); err != nil {
		t.Fatalf("Append: %v", err)
	}
	return s
}

// startOutbound 启动出站泵并等它过起跑线（Options.Ready：游标
// 三态定位完成）。不等待的话"首启不重放"的 EOF 位置不确定——
// 慢机器上主 goroutine 的 appendMessage 可能先于泵的 LoadCursorAtEnd
// 完成，测试消息被当成历史跳过（90s 零投递假红的真根因，CI 实证）；
// 本地快机 goroutine 先跑所以从不复现。收尾语义：defer cancel 先于
// t.Cleanup 执行，泵收到取消后返回并落盘游标（不等会与 TempDir
// 清理赛跑）；中途意外死亡（非取消错误）报真因。
func startOutbound(t *testing.T, ob *Outbound, ready <-chan struct{}, ctx context.Context) func() {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- ob.Run(ctx) }()
	select {
	case <-ready:
	case <-time.After(30 * time.Second):
		t.Fatal("等待出站泵就绪超时")
	}
	t.Cleanup(func() {
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Fatalf("出站泵意外退出: %v", err)
			}
		default:
		}
	})
	// stopAndWait 取消泵并等 Run 完全退出（含游标落盘）。测试中段
	// 对轨迹文件做截断/替换等外科操作前必须先等前一任泵退出——
	// 否则前一任的退出落盘（旧 offset）可能落在新文件状态之后，
	// 下一任读到的游标与新文件错位（CI 实证：race 下 Save 慢于
	// 截断，rewound 弃批语义与投递断言互相踩踏）。
	return func() {
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Fatalf("出站泵意外退出: %v", err)
			}
		case <-time.After(30 * time.Second):
			t.Fatal("等待出站泵退出超时")
		}
	}
}

// waitForDelivery 轮询等待投递数达到 n。
func waitForDelivery(t *testing.T, d *fakeDelivery, n int) {
	t.Helper()
	// 90s：CI（2 核 + -race 拖慢 3-5 倍 + 全量并行包）下出站泵会被
	// 调度饥饿到完全无投递——93c1507 曾把 10s 提到 30s，-race 后仍
	// 不够（2026-10-02 CI 实证），再加一档。断言的是投递语义，不是
	// 调度时延。
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		if len(d.snapshot()) >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("等待 %d 条投递超时，实际 %d", n, len(d.snapshot()))
}

// waitCursor 轮询等待游标文件出现（Run 的落盘点可观察）。
func waitCursor(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("等待游标文件超时")
}

// TestOutboundDeliversClaimedOnly 钉死过滤谓词：只投 from=self 且
// to ∈ 认领地址的 message；对话流地址、他人消息、非 message 类型
// 一律不投（alert 不直接出站）。
func TestOutboundDeliversClaimedOnly(t *testing.T) {
	dir := t.TempDir()
	tl, err := traj.Create(context.Background(), "conn-test")
	if err != nil {
		t.Fatal(err)
	}
	d := &fakeDelivery{}
	ready := make(chan struct{})
	ob, err := NewOutbound(OutboundOptions{
		Path: tl.Path, CursorPath: filepath.Join(dir, "out.cursor"),
		Self: "ada", Addresses: []string{"wecom:zhangsan"}, Deliver: d, Poll: 20 * time.Millisecond,
		Ready: func() { close(ready) },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = startOutbound(t, ob, ready, ctx)

	appendMessage(t, tl, "ada", "wecom:zhangsan", "你好")   // 认领：投
	appendMessage(t, tl, "ada", "operator", "本地对话流")      // 不认领：不投
	appendMessage(t, tl, "wecom:zhangsan", "ada", "入站不投") // from 不是 self：不投
	al := traj.NewStep(traj.TypeAlert)                    // alert 不直接出站
	al.Fields["from"] = "ada"
	al.Fields["to"] = "wecom:zhangsan"
	al.Fields["content"] = "告警"
	if err := tl.Append(context.Background(), al); err != nil {
		t.Fatal(err)
	}

	waitForDelivery(t, d, 1)
	time.Sleep(100 * time.Millisecond) // 负向观察窗
	sent := d.snapshot()
	if len(sent) != 1 || sent[0].to != "wecom:zhangsan" || sent[0].text != "你好" {
		t.Fatalf("投递结果 = %+v，应只含认领的一条", sent)
	}
}

// TestOutboundCursorThreeStatesAndRewound 钉死出站泵的三条游标安全
// 前提：首启不重放历史；重启续读；轨迹被替换（rewound）弃批跳 EOF。
func TestOutboundCursorThreeStatesAndRewound(t *testing.T) {
	dir := t.TempDir()
	cursor := filepath.Join(dir, "out.cursor")

	// 首启：历史消息（写在启动前）绝不重投。
	tl, err := traj.Create(context.Background(), "conn-replay")
	if err != nil {
		t.Fatal(err)
	}
	appendMessage(t, tl, "ada", "wecom:zhangsan", "历史消息")

	first := &fakeDelivery{}
	ready := make(chan struct{})
	ob, _ := NewOutbound(OutboundOptions{
		Path: tl.Path, CursorPath: cursor,
		Self: "ada", Addresses: []string{"wecom:zhangsan"}, Deliver: first, Poll: 20 * time.Millisecond,
		Ready: func() { close(ready) },
	})
	ctx, cancel := context.WithCancel(context.Background())
	go ob.Run(ctx)
	time.Sleep(200 * time.Millisecond)
	cancel()
	waitCursor(t, cursor) // Run 的退出落盘完成后才进入下一态
	if n := len(first.snapshot()); n != 0 {
		t.Fatalf("首启重投了 %d 条历史消息，应为 0", n)
	}

	// 重启：游标有效 → 续读，只投新消息。同样等起跑线——append 在
	// LoadCursorAtEnd 之前完成的话消息会被续读 offset 跳过。
	restart := &fakeDelivery{}
	ready2 := make(chan struct{})
	ob2, _ := NewOutbound(OutboundOptions{
		Path: tl.Path, CursorPath: cursor,
		Self: "ada", Addresses: []string{"wecom:zhangsan"}, Deliver: restart, Poll: 20 * time.Millisecond,
		Ready: func() { close(ready2) },
	})
	ctx2, cancel2 := context.WithCancel(context.Background())
	stopOb2 := startOutbound(t, ob2, ready2, ctx2)
	appendMessage(t, tl, "ada", "wecom:zhangsan", "重启后的新消息")
	waitForDelivery(t, restart, 1)
	sent := restart.snapshot()
	if len(sent) != 1 || sent[0].text != "重启后的新消息" {
		t.Fatalf("重启续读不符: %+v", sent)
	}
	// 完全退出（含游标落盘）后再截断：退出落盘与截断的次序颠倒会让
	// 下一任读到大 offset 游标（race 下的 CI 实证窗口）。
	cancel2()
	stopOb2()

	// 轨迹被替换（rewound）：本批是重放，必须弃批——不投任何旧
	// 消息。先让泵消费掉"空文件的 rewound 批"再追加新消息，保证
	// 新消息落在弃批之后的正常读取里。
	if err := os.WriteFile(tl.Path, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	after := &fakeDelivery{}
	ready3 := make(chan struct{})
	ob3, _ := NewOutbound(OutboundOptions{
		Path: tl.Path, CursorPath: cursor,
		Self: "ada", Addresses: []string{"wecom:zhangsan"}, Deliver: after, Poll: 20 * time.Millisecond,
		Ready: func() { close(ready3) },
	})
	ctx3, cancel3 := context.WithCancel(context.Background())
	defer cancel3()
	_ = startOutbound(t, ob3, ready3, ctx3)
	time.Sleep(300 * time.Millisecond) // rewound 弃批消费窗
	appendMessage(t, tl, "ada", "wecom:zhangsan", "替换后的新消息")
	waitForDelivery(t, after, 1)
	time.Sleep(100 * time.Millisecond)
	sent = after.snapshot()
	if len(sent) != 1 || sent[0].text != "替换后的新消息" {
		t.Fatalf("rewound 后应只投替换后的 1 条: %+v", sent)
	}
}

// TestOutboundRetryExhaustedDrops 钉死漏发档：重试耗尽即丢弃并推进
// 游标——不能卡住出站泵，后续消息照常投递。
func TestOutboundRetryExhaustedDrops(t *testing.T) {
	dir := t.TempDir()
	tl, err := traj.Create(context.Background(), "conn-drop")
	if err != nil {
		t.Fatal(err)
	}
	d := &fakeDelivery{failN: 1}
	ready := make(chan struct{})
	ob, _ := NewOutbound(OutboundOptions{
		Path: tl.Path, CursorPath: filepath.Join(dir, "out.cursor"),
		Self: "ada", Addresses: []string{"wecom:zhangsan"}, Deliver: d,
		Retry: 1, Poll: 20 * time.Millisecond,
		Ready: func() { close(ready) },
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = startOutbound(t, ob, ready, ctx)

	appendMessage(t, tl, "ada", "wecom:zhangsan", "第一条（将失败丢弃）")
	waitForDelivery(t, d, 1)
	appendMessage(t, tl, "ada", "wecom:zhangsan", "第二条（照常投递）")
	waitForDelivery(t, d, 2)
	sent := d.snapshot()
	if sent[0].text != "第一条（将失败丢弃）" || sent[1].text != "第二条（照常投递）" {
		t.Fatalf("投递序列不符: %+v", sent)
	}
}

// TestOutboundNoAddressesFailsClosed 认领地址为空拒绝启动。
func TestOutboundNoAddressesFailsClosed(t *testing.T) {
	if _, err := NewOutbound(OutboundOptions{Self: "ada", Deliver: &fakeDelivery{}}); !errors.Is(err, ErrNoAddresses) {
		t.Fatalf("空认领地址应拒绝启动: %v", err)
	}
}

// TestSegment 钉死分段的三条规则：短文不分段；段落边界折叠；
// 未闭合代码块内不落刀。
func TestSegment(t *testing.T) {
	// 短文：原样。
	if got := Segment("短文", 100); len(got) != 1 || got[0] != "短文" {
		t.Fatalf("短文应原样: %v", got)
	}
	// 段落边界折叠：段数 >1 且每段不超限。
	long := strings.Repeat("段落A内容。\n\n", 10) + strings.Repeat("段落B内容。", 10)
	segs := Segment(long, 30)
	if len(segs) < 2 {
		t.Fatalf("长文应分段: %d 段", len(segs))
	}
	for i, s := range segs {
		if len([]rune(s)) > 30 {
			t.Fatalf("第 %d 段超限（%d rune）: %q", i, len([]rune(s)), s)
		}
	}
	// 代码块场景：有界性优先——所有段都不超限，超长代码块走硬切
	// 兜底（干净切仅在 fence 已闭合的段落边界发生）。
	fenced := "intro\n\n```go\n" + strings.Repeat("x := 1\n", 20) + "```\n\noutro"
	segs = Segment(fenced, 40)
	if len(segs) < 2 {
		t.Fatalf("超限代码块应被硬切分段: %d 段", len(segs))
	}
	for i, s := range segs {
		if len([]rune(s)) > 40 {
			t.Fatalf("fenced 第 %d 段超限（%d rune）", i, len([]rune(s)))
		}
	}
	// 短代码块不触发分段：fence 完整保留在同一段内。
	short := "intro\n\n```go\nprintln(1)\n```\n\noutro"
	if got := Segment(short, 200); len(got) != 1 || got[0] != short {
		t.Fatalf("限内短文应原样: %v", got)
	}
}

// TestOutboundSavesCursorEvenWithoutDir 游标目录缺失时优雅退出也必须
// 落盘——否则重启后从 EOF 起步，停机期间的主动汇报全部漏发。
func TestOutboundSavesCursorEvenWithoutDir(t *testing.T) {
	dir := t.TempDir()
	tl, err := traj.Create(context.Background(), "conn-cursor")
	if err != nil {
		t.Fatal(err)
	}
	cursor := filepath.Join(dir, "not-created-subdir", "out.cursor")
	ready := make(chan struct{})
	ob, err := NewOutbound(OutboundOptions{
		Path: tl.Path, CursorPath: cursor,
		Self: "ada", Addresses: []string{"wecom:u"}, Deliver: &fakeDelivery{}, Poll: 20 * time.Millisecond,
		Ready: func() { close(ready) },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go ob.Run(ctx)
	time.Sleep(150 * time.Millisecond)
	cancel()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(cursor); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("优雅退出后游标文件未落盘")
}
