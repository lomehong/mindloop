package obs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lomehong/mindloop/internal/llm"
)

// P1-7 观测章钉子：batch/wake_delay_ms 从 Attrib 透传到台账行，
// Derive 派生批大小与唤醒延迟锚点。

func TestUsageRecorderCarriesBatchAndDelay(t *testing.T) {
	dir := t.TempDir()
	rec := UsageRecorder(dir, "m", "p", nil)
	// 归因章经 Complete 收尾由 applyAttrib 写入 Usage——这里直接
	// 构造带章的 Usage（UsageRecorder 只做落盘）。
	rec(llm.Usage{PromptTokens: 10, Known: true, Thinker: "monolith", Phase: "wake", Batch: 3, WakeDelayMS: 1500}, nil)

	b, err := os.ReadFile(filepath.Join(dir, "usage", "llm-usage.jsonl"))
	if err != nil {
		t.Fatalf("读台账: %v", err)
	}
	var row map[string]any
	if err := json.Unmarshal([]byte(strings.SplitN(string(b), "\n", 2)[0]), &row); err != nil {
		t.Fatalf("解析: %v", err)
	}
	if row["batch"] != float64(3) || row["wake_delay_ms"] != float64(1500) {
		t.Fatalf("batch/wake_delay_ms 未落盘: %v", row)
	}
}

func TestDeriveBatchStats(t *testing.T) {
	rows := []UsageRow{
		{TS: "2026-11-03T00:00:00.000Z", Phase: "wake", Wake: "step", Batch: 1},
		{TS: "2026-11-03T00:01:00.000Z", Phase: "wake", Wake: "step", Batch: 5, WakeDelayMS: 2000},
		{TS: "2026-11-03T00:02:00.000Z", Phase: "chat", Batch: 9}, // 非 wake 行不进统计
	}
	st := Derive(rows, 0)
	if st.BatchRows != 2 || st.BatchAvg != 3 || st.BatchMax != 5 {
		t.Fatalf("批统计错: rows=%d avg=%.1f max=%d", st.BatchRows, st.BatchAvg, st.BatchMax)
	}
	if st.DelayRows != 1 || st.WakeDelayAvgMS != 2000 || st.WakeDelayMaxMS != 2000 {
		t.Fatalf("延迟统计错: rows=%d avg=%d max=%d", st.DelayRows, st.WakeDelayAvgMS, st.WakeDelayMaxMS)
	}
}
