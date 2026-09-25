package mind

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"mindloop/internal/llm"
	"mindloop/internal/traj"
)

// stubThinker 是一次性补全的桩：记录被调用次数（流式路径不应回退
// 到它），返回固定回复。
type stubThinker struct {
	reply string
	calls int
}

func (s *stubThinker) Think(ctx context.Context, system string, msgs []llm.Message) (string, error) {
	s.calls++
	return s.reply, nil
}

// newStreamResponder 按给定开关构造 responder；thinker 为 nil 时用
// 不该被调到的桩；minDwell 传负值禁用驻留（测试直通），需要验证
// 驻留语义的用例显式传正值。经 NewResponder 构造以获得与生产一致
// 的默认值（RecallWindow/MaxHistory 归一化）——直接拼 Responder{}
// 会让 RecallWindow=0，所有消息都被"过于陈旧"守卫跳过。
func newStreamResponder(t *testing.T, tl *traj.Timeline, streaming bool, streamFn func(context.Context, string, []llm.Message, func(string)) (string, error), thinker runnerThinker, minDwell time.Duration) *Responder {
	t.Helper()
	if thinker == nil {
		thinker = &stubThinker{reply: "不应被调用"}
	}
	r := NewResponder(ResponderOptions{
		Timeline:  tl,
		Thinker:   thinker,
		SelfName:  "ada",
		Persona:   "test persona",
		Streaming: streaming,
		StreamFn:  streamFn,
		MinDwell:  minDwell,
	})
	return r.(*Responder)
}

// inboundMessage 复用 responder_test.go 里的同名助手（from/content
// 参数形态）。

func assertAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("%s 应已消失（err=%v）", path, err)
	}
}

// TestResponderStreamingLifecycle：流式旁路的完整生命周期——delta
// 期间旁路文件与 replying 状态在场且形状正确；正式消息一次性落轨
// 迹；之后旁路与状态一起消失。
func TestResponderStreamingLifecycle(t *testing.T) {
	tl := newMsgTimeline(t)
	msg := inboundMessage(t, tl, "operator", "在吗")
	sidecar := filepath.Join(streamDir(tl.Dir), msg.StepID+".txt")
	replying := replyingPath(tl.Dir)

	r := newStreamResponder(t, tl, true, func(ctx context.Context, system string, msgs []llm.Message, onDelta func(string)) (string, error) {
		onDelta("第一段，")
		// delta 时刻：旁路文件在场，内容是到此刻为止的累积全文；
		// replying 状态在场且 reply_to 指向本条消息。
		data, err := os.ReadFile(sidecar)
		if err != nil || string(data) != "第一段，" {
			t.Errorf("delta 时旁路全文 = %q, %v，应为累积的「第一段，」", data, err)
		}
		st, err := os.ReadFile(replying)
		if err != nil {
			t.Errorf("delta 时 replying 不在场: %v", err)
		} else {
			var rs replyingState
			if json.Unmarshal(st, &rs) != nil || rs.ReplyTo != msg.StepID || rs.Thinker != responderName || rs.Since == "" {
				t.Errorf("replying 形状不符: %s", st)
			}
		}
		onDelta("第二段")
		return "第一段，第二段", nil
	}, nil, -time.Second)

	out := r.Wake(context.Background(), Wake{Step: msg, Kind: WakeStep})
	if out.Note == "" || strings.Contains(out.Note, "失败") || strings.Contains(out.Note, "错误") {
		t.Fatalf("唤醒应成功，Note = %q", out.Note)
	}

	// 旁路与状态都已删除。
	assertAbsent(t, sidecar)
	assertAbsent(t, replying)

	// 正式消息照旧一次性落轨迹：全文 + reply_to 盖章。
	steps, err := tl.Steps()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range steps {
		if rt, ok := s.Field("reply_to"); ok && rt == msg.StepID {
			found = true
			if c, _ := s.Field("content"); c != "第一段，第二段" {
				t.Fatalf("正式消息全文 = %q", c)
			}
		}
	}
	if !found {
		t.Fatal("正式消息未落轨迹")
	}
}

func containsFailureNote(note string) bool {
	return strings.Contains(note, "失败") || strings.Contains(note, "错误")
}

// TestResponderStreamingErrorDiscards：流中断（首 delta 之后出错）
// → 旁路与 replying 一起消失、无正式消息、Note 照旧报失败。
func TestResponderStreamingErrorDiscards(t *testing.T) {
	tl := newMsgTimeline(t)
	msg := inboundMessage(t, tl, "operator", "在吗")
	sidecar := filepath.Join(streamDir(tl.Dir), msg.StepID+".txt")

	r := newStreamResponder(t, tl, true, func(ctx context.Context, system string, msgs []llm.Message, onDelta func(string)) (string, error) {
		onDelta("半截")
		return "", errors.New("llm: 流被中断")
	}, nil, -time.Second)

	out := r.Wake(context.Background(), Wake{Step: msg, Kind: WakeStep})
	if !containsFailureNote(out.Note) {
		t.Fatalf("Note 应报失败，得到 %q", out.Note)
	}
	assertAbsent(t, sidecar)
	assertAbsent(t, replyingPath(tl.Dir))

	steps, _ := tl.Steps()
	for _, s := range steps {
		if rt, ok := s.Field("reply_to"); ok && rt == msg.StepID {
			t.Fatal("失败回复不应落轨迹")
		}
	}
}

// TestResponderStreamingDisabled：Streaming=false 时完全不写旁路
// （连 stream/ 目录都不创建），回复走一次性补全。
func TestResponderStreamingDisabled(t *testing.T) {
	tl := newMsgTimeline(t)
	msg := inboundMessage(t, tl, "operator", "在吗")
	thinker := &stubThinker{reply: "普通回复"}

	r := newStreamResponder(t, tl, false, func(ctx context.Context, system string, msgs []llm.Message, onDelta func(string)) (string, error) {
		t.Error("Streaming=false 时 StreamFn 不应被调用")
		return "", nil
	}, thinker, -time.Second)

	out := r.Wake(context.Background(), Wake{Step: msg, Kind: WakeStep})
	if containsFailureNote(out.Note) {
		t.Fatalf("回复失败: %q", out.Note)
	}
	assertAbsent(t, streamDir(tl.Dir))
	assertAbsent(t, replyingPath(tl.Dir))
	if thinker.calls != 1 {
		t.Fatalf("一次性补全应被调用 1 次，实际 %d", thinker.calls)
	}
	steps, _ := tl.Steps()
	found := false
	for _, s := range steps {
		if rt, ok := s.Field("reply_to"); ok && rt == msg.StepID {
			if c, _ := s.Field("content"); c == "普通回复" {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("非流式回复未落轨迹")
	}
}

// TestResponderStreamingFallbackWithoutStreamFn：开关开着但装配层
// 未提供 StreamFn（无流式能力）→ 回退一次性补全，无旁路。
func TestResponderStreamingFallbackWithoutStreamFn(t *testing.T) {
	tl := newMsgTimeline(t)
	msg := inboundMessage(t, tl, "operator", "在吗")
	thinker := &stubThinker{reply: "回退回复"}

	r := newStreamResponder(t, tl, true, nil, thinker, -time.Second)
	out := r.Wake(context.Background(), Wake{Step: msg, Kind: WakeStep})
	if containsFailureNote(out.Note) {
		t.Fatalf("回退回复失败: %q", out.Note)
	}
	assertAbsent(t, streamDir(tl.Dir))
	assertAbsent(t, replyingPath(tl.Dir))
	if thinker.calls != 1 {
		t.Fatalf("回退应调用一次性补全 1 次，实际 %d", thinker.calls)
	}
}

// TestResponderStreamingMinDwellKeepsSidecarVisible：lead 端到端
// 实测的协议缺陷回归钉子——快生成器（echo ~80ms 完成）的旁路存活
// 短于读方（web SSE 200ms 轮询）采样周期，SSE 零 delta/零 done，
// 流式协议静默失效。MinDwell 强制旁路可观测驻留足额，读方至少能
// 采到一轮。
func TestResponderStreamingMinDwellKeepsSidecarVisible(t *testing.T) {
	tl := newMsgTimeline(t)
	msg := inboundMessage(t, tl, "operator", "在吗")
	sidecar := filepath.Join(streamDir(tl.Dir), msg.StepID+".txt")

	const dwell = 300 * time.Millisecond
	r := newStreamResponder(t, tl, true, func(ctx context.Context, system string, msgs []llm.Message, onDelta func(string)) (string, error) {
		onDelta("快生成器的瞬时全文") // 整个"生成"瞬间完成——正是 echo 的形态
		return "快生成器的瞬时全文", nil
	}, nil, dwell)

	// 监视者以 5ms 周期采样旁路文件（比 web 的 200ms 更苛刻），
	// 记录可见窗口的起止。
	var mu sync.Mutex
	var firstSeen, lastSeen time.Time
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := os.Stat(sidecar); err == nil {
				now := time.Now()
				mu.Lock()
				if firstSeen.IsZero() {
					firstSeen = now
				}
				lastSeen = now
				mu.Unlock()
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()

	out := r.Wake(context.Background(), Wake{Step: msg, Kind: WakeStep})
	close(stop)
	if containsFailureNote(out.Note) {
		t.Fatalf("回复失败: %q", out.Note)
	}
	// Wake 返回后文件必已删除（dwell 在 finish 内同步补足）。
	assertAbsent(t, sidecar)
	assertAbsent(t, replyingPath(tl.Dir))

	mu.Lock()
	visible := lastSeen.Sub(firstSeen)
	mu.Unlock()
	if visible < dwell*3/4 {
		t.Fatalf("旁路可见时长 %v，应 ≥ MinDwell %v 的四分之三（读方轮询完全错过瞬态旁路）", visible, dwell)
	}
}

// TestBeginReplyStreamRejectsUnsafeStepID：reply_to 直接拼文件名，
// 非 UUID 字符集的 id（穿越段、空串）必须被拒且不留任何文件。
func TestBeginReplyStreamRejectsUnsafeStepID(t *testing.T) {
	tl := newMsgTimeline(t)
	for _, bad := range []string{"", "../evil", `..\evil`, "abc/../x", "大写与大写"} {
		if s := beginReplyStream(tl.Dir, bad, 0); s != nil {
			s.finish()
			t.Fatalf("不合规 reply_to %q 应被拒绝", bad)
		}
	}
	if _, err := os.Stat(streamDir(tl.Dir)); !os.IsNotExist(err) {
		t.Fatalf("被拒的 id 不应留下 stream 目录: %v", err)
	}
}

// TestDispatcherStartupGCClearsTransients：崩溃模拟——上一任调度器
// 死后残留的 sidecar 与 replying，必须在新调度器 Run 启动瞬间清空。
func TestDispatcherStartupGCClearsTransients(t *testing.T) {
	d, tl := newTestDispatcher(t)
	if err := os.MkdirAll(streamDir(tl.Dir), 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(streamDir(tl.Dir), "deadbeefdeadbeefdeadbeefdeadbeef.txt")
	if err := os.WriteFile(stale, []byte("崩溃残留的半截回复"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(replyingPath(tl.Dir)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(replyingPath(tl.Dir), []byte(`{"reply_to":"x","thinker":"responder","since":"2026-01-01T00:00:00Z"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(workingPath(tl.Dir)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(workingPath(tl.Dir), []byte(`{"working":true,"busy":[{"thinker":"monolith","wake":"step","since":"2026-01-01T00:00:00Z"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)
	waitFor(t, 2*time.Second, func() bool {
		_, e1 := os.Stat(streamDir(tl.Dir))
		_, e2 := os.Stat(replyingPath(tl.Dir))
		_, e3 := os.Stat(workingPath(tl.Dir))
		return os.IsNotExist(e1) && os.IsNotExist(e2) && os.IsNotExist(e3)
	})
}
