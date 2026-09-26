package recap

import (
	"context"
	"testing"
	"time"

	"mindloop/internal/llm"
	"mindloop/internal/traj"
)

// attribThinker 捕获摘要调用的归因快照。
type attribThinker struct {
	attrs []llm.Attrib
}

func (f *attribThinker) Think(ctx context.Context, _ string, _ []llm.Message) (string, error) {
	f.attrs = append(f.attrs, llm.AttribFrom(ctx))
	return `{"title":"归因","summary":"摘要"}`, nil
}

// TestSummarizeAttribution：摘要调用归因——thinker/phase=recap（内层
// 覆盖唤醒链路外层挂的 monolith/wake：这是 recap 自己的调用）。
func TestSummarizeAttribution(t *testing.T) {
	t.Setenv("MINDLOOP_HOME", t.TempDir())
	tl, err := traj.Create(context.Background(), "recap-attrib")
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now()
	for i := 0; i < 24; i++ {
		s := traj.NewStep("action")
		s.TS = base.Add(time.Duration(i) * time.Second).UTC().Format(traj.TimeFormat)
		s.Fields["content"] = "动作"
		if err := tl.Append(context.Background(), s); err != nil {
			t.Fatal(err)
		}
	}

	th := &attribThinker{}
	u := &Updater{Timeline: tl, Thinker: th, MaxSteps: 12}
	if _, err := u.Update(context.Background()); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(th.attrs) == 0 {
		t.Fatal("摘要未被调用")
	}
	for _, a := range th.attrs {
		if a.Thinker != "recap" || a.Phase != "recap" {
			t.Fatalf("摘要归因 = %+v", a)
		}
	}
}
