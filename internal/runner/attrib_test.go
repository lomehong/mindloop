package runner

import (
	"context"
	"testing"
	"time"

	"mindloop/internal/llm"
)

// attribThinker 捕获每轮 Think 的归因快照。
type attribThinker struct {
	attrs []llm.Attrib
}

func (f *attribThinker) Think(ctx context.Context, _ string, _ []llm.Message) (string, error) {
	f.attrs = append(f.attrs, llm.AttribFrom(ctx))
	return fence("echo attribution\nFINAL=\"done\""), nil
}

// TestRunThinkerAttribution：运行循环给每轮 Think 的 ctx 挂上
// TaskID/RunID/Attempt——用量台账据此归因到任务运行代次。
func TestRunThinkerAttribution(t *testing.T) {
	requireBash(t)
	tl := newTestTimeline(t)
	thinker := &attribThinker{}
	res, err := Run(context.Background(), Options{
		Timeline: tl, Thinker: thinker, Task: "归因任务",
		TaskID: "task-9", Attempt: 2, RunID: "run-9",
		IdleTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(thinker.attrs) == 0 {
		t.Fatal("Think 未被调用")
	}
	for i, a := range thinker.attrs {
		if a.Task != "task-9" || a.Run != res.RunID || a.Attempt != 2 {
			t.Fatalf("第 %d 轮归因 = %+v", i, a)
		}
	}
	if res.RunID != "run-9" {
		t.Fatalf("RunID = %q（预分配 run 应原样使用）", res.RunID)
	}
}
