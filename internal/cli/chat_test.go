package cli

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuffer 是并发安全的输出缓冲：兜底计时器在后台 goroutine 写，
// 测试在主 goroutine 读。
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// TestSilenceFallbackOnlyOnSilence 钉住静默兜底的判据方向：只有窗口
// 内没有任何输出（提示符未挂起）才告警；回复到达（Linef→Show 挂起
// 提示符）后必须保持沉默——条件写反时每条正常回复之后都会弹一条
// "无回复"假警报。
func TestSilenceFallbackOnlyOnSilence(t *testing.T) {
	t.Run("回复到达后不告警", func(t *testing.T) {
		var out syncBuffer
		term := newPromptWriter(&out, promptWorking)
		term.Linef("%s> 示例回复", "ada") // 回复到达：提示符已挂起
		armSilenceFallback(term, 20*time.Millisecond)
		time.Sleep(80 * time.Millisecond)
		if strings.Contains(out.String(), "未见回复") {
			t.Fatalf("回复到达后触发了静默假警报:\n%s", out.String())
		}
	})
	t.Run("真静默时告警并恢复提示符", func(t *testing.T) {
		var out syncBuffer
		term := newPromptWriter(&out, promptWorking)
		term.InputConsumed() // 用户刚按过回车：提示符未挂起
		armSilenceFallback(term, 20*time.Millisecond)
		deadline := time.Now().Add(2 * time.Second)
		for !strings.Contains(out.String(), "未见回复") && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		if got := out.String(); !strings.Contains(got, "未见回复") {
			t.Fatalf("静默窗口未告警:\n%s", got)
		}
		if !term.Pending() {
			t.Fatal("告警后应恢复提示符，用户才能继续输入")
		}
	})
}

// TestReplyLineKeepsFullText 钉住回复的显示契约：全文原样（含换行），
// 不截断、不压平——400 字截断曾把 444 字的回复截成"……命…"。
func TestReplyLineKeepsFullText(t *testing.T) {
	content := strings.Repeat("长", 444) + "\n第二段"
	got := replyLine("ada", content)
	if !strings.Contains(got, content) {
		t.Fatalf("回复未原样呈现: %q", got)
	}
	if strings.Contains(got, "…") {
		t.Fatal("回复出现了截断省略号")
	}
}
