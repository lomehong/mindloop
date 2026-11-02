package mind

import (
	"strings"
	"testing"

	"github.com/lomehong/mindloop/internal/connector/sensor"
	"github.com/lomehong/mindloop/internal/thread"
	"github.com/lomehong/mindloop/internal/traj"
)

// P1-9 落点 a：同源第二条事件附 prev 锚（链式召回一跳）；首条无锚。
// P1-10：subject 锚定到开放线索时附 thread 归属。
func TestSensorRunnerPrevAnchorAndThread(t *testing.T) {
	h := newSensorHarness(t, `{"version":1,"sensors":[
		{"id":"web1","type":"web","url":"https://x.example","learning_days":-1}
	]}`, 0)
	// 开一条带 subject 锚的线索（归属判定经 SensorRunner 的 threads 缓存）。
	if _, err := thread.Add(h.dir, "盯价格", "https://x.example"); err != nil {
		t.Fatalf("thread.Add: %v", err)
	}
	h.fire("web1", sensor.PEvent{Kind: sensor.KindChanged, Subject: "https://x.example", Dedup: "p1", Digest: "第一条"})
	h.fire("web1", sensor.PEvent{Kind: sensor.KindChanged, Subject: "https://x.example", Dedup: "p2", Digest: "第二条"})

	steps := waitForSteps(t, h, traj.TypeEvent, 2)
	first, second := steps[0], steps[1]
	if p, _ := first.Field("prev"); p != "" {
		t.Fatalf("首条不应带 prev: %q", p)
	}
	prev, _ := second.Field("prev")
	if prev != first.StepID {
		t.Fatalf("第二条 prev = %q，应为首条 %q", prev, first.StepID)
	}
	// 线索归属：两条都带 thread 字段（锚定的 subject 命中开放线索）。
	for i, s := range steps {
		tid, _ := s.Field("thread")
		if !strings.HasPrefix(tid, "th-") {
			t.Fatalf("第 %d 条事件应带线索归属，得 %q", i+1, tid)
		}
	}
}
