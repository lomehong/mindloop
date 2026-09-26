package prompt

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestBudgetDefaultsTo128KiB(t *testing.T) {
	b := NewBudget(0)
	if got := b.Remaining(); got != DefaultContextBudget {
		t.Fatalf("NewBudget(0).Remaining() = %d，期望默认 %d", got, DefaultContextBudget)
	}
}

func TestBudgetProtectedOverflowIsAnError(t *testing.T) {
	b := NewBudget(300)
	if err := b.TakeProtected("persona", strings.Repeat("x", 200)); err != nil {
		t.Fatalf("首段未超限不应报错: %v", err)
	}
	err := b.TakeProtected("task", strings.Repeat("y", 150))
	var oe *OverflowError
	if !errors.As(err, &oe) {
		t.Fatalf("受保护内容超限必须报错（不能静默截断），got %v", err)
	}
	if oe.Total != 300 || oe.Used != 350 {
		t.Fatalf("OverflowError = %+v，期望 Total=300 Used=350", oe)
	}
	msg := err.Error()
	if !strings.Contains(msg, "persona") || !strings.Contains(msg, "task") {
		t.Fatalf("错误信息应列出超限段名，got %q", msg)
	}
}

func TestBudgetCappedNeverSplitsRunes(t *testing.T) {
	b := NewBudget(1024)
	text := strings.Repeat("中", 100) // 300 字节
	got := b.TakeCapped("memory", text, 10)
	if !utf8.ValidString(got) {
		t.Fatalf("截断结果必须是合法 UTF-8: %q", got)
	}
	if len(got) != 9 {
		t.Fatalf("10 字节上限下应取 3 个汉字（9 字节），got %d 字节", len(got))
	}
}

func TestBudgetRemainingFlowsToHistory(t *testing.T) {
	b := NewBudget(1000)
	_ = b.TakeProtected("system", strings.Repeat("s", 400))
	b.TakeCapped("memory", strings.Repeat("m", 200), DefaultMemoryBudget)
	b.TakeCapped("digest", strings.Repeat("d", 100), DefaultSummaryBudget)
	if got := b.Remaining(); got != 300 {
		t.Fatalf("Remaining() = %d，期望 300（空余预算回流历史）", got)
	}
	hist := b.TakeCapped("history", strings.Repeat("h", 500), b.Remaining())
	if len(hist) != 300 {
		t.Fatalf("历史应吃满剩余 300 字节，got %d", len(hist))
	}
	if got := b.Remaining(); got != 0 {
		t.Fatalf("装填后 Remaining() = %d，期望 0", got)
	}
}

func TestBudgetCappedRespectsRemaining(t *testing.T) {
	b := NewBudget(100)
	_ = b.TakeProtected("system", strings.Repeat("s", 80))
	got := b.TakeCapped("memory", strings.Repeat("m", 50), DefaultMemoryBudget)
	if len(got) != 20 {
		t.Fatalf("cap 大于剩余时应只吃剩余 20 字节，got %d", len(got))
	}
}

func TestBudgetTrimmedRecordsTruncations(t *testing.T) {
	b := NewBudget(100)
	b.TakeCapped("memory", strings.Repeat("m", 50), 10)
	tr := b.Trimmed()
	if len(tr) != 1 {
		t.Fatalf("Trimmed() = %v，期望记录一条裁剪", tr)
	}
	if !strings.Contains(tr[0], "memory") {
		t.Fatalf("裁剪记录应含段名，got %q", tr[0])
	}
	// 未被裁剪的段不进记录。
	b.TakeCapped("digest", "short", 100)
	if len(b.Trimmed()) != 1 {
		t.Fatalf("未裁剪段不应进 Trimmed()，got %v", b.Trimmed())
	}
}

func TestBudgetCheckPassesWithinBudget(t *testing.T) {
	b := NewBudget(100)
	if err := b.TakeProtected("system", "hello"); err != nil {
		t.Fatalf("预算内不应报错: %v", err)
	}
}
