package obs

import (
	"os"
	"strings"
	"testing"
)

// TestRecallRoundtrip：Record→Load 往返（新→旧排序）；坏行跳过；
// 空目录 nil 而非错误——台账是观测面，不是事实源。
func TestRecallRoundtrip(t *testing.T) {
	dir := t.TempDir()
	RecordRecall(dir, RecallRow{Source: "monolith", Query: "杭州天气",
		HitCount: 2, Injected: 2, ElapsedMS: 3}, nil)
	RecordRecall(dir, RecallRow{Source: "responder", Skipped: "no_hits"}, nil)
	RecordRecall(dir, RecallRow{Source: "responder", Skipped: "error",
		Error: "索引损坏"}, nil)
	// 坏行混入。
	appendGarbage(t, dir)

	rows, err := LoadRecall(dir, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("行数 = %d，要 3（坏行跳过）", len(rows))
	}
	if rows[0].Skipped != "error" || rows[2].Source != "monolith" {
		t.Fatalf("应新→旧排序: %+v", rows)
	}
	if rows[2].Query != "杭州天气" || rows[2].HitCount != 2 {
		t.Fatalf("首行内容不符: %+v", rows[2])
	}

	// limit 截尾（保留最新）。
	limited, err := LoadRecall(dir, 1)
	if err != nil || len(limited) != 1 || limited[0].Skipped != "error" {
		t.Fatalf("limit 截尾应保留最新: %+v err=%v", limited, err)
	}

	// 空目录：nil 而非错误。
	empty, err := LoadRecall(t.TempDir(), 20)
	if err != nil || empty != nil {
		t.Fatalf("空目录应 nil,nil: %+v err=%v", empty, err)
	}
}

// TestRecallQuerySummary：摘要截断 120 字节 + 省略号。
func TestRecallQuerySummary(t *testing.T) {
	if got := RecallQuerySummary("短查询"); got != "短查询" {
		t.Fatalf("短查询不截断: %q", got)
	}
	long := strings.Repeat("长", 200)
	got := RecallQuerySummary(long)
	if len(got) > 130 {
		t.Fatalf("摘要超限: %d 字节", len(got))
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("截断应带省略号: %q", got[len(got)-10:])
	}
}

func appendGarbage(t *testing.T, dir string) {
	t.Helper()
	f, err := os.OpenFile(recallLedgerPath(dir), os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString("not-json\n"); err != nil {
		t.Fatal(err)
	}
}
