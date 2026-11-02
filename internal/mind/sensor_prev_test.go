package mind

import (
	"strings"
	"testing"

	"github.com/lomehong/mindloop/internal/connector/sensor"
	"github.com/lomehong/mindloop/internal/traj"
)

// P1-9 落点 a：同源第二条事件附 prev 锚（链式召回一跳）；首条无锚。
func TestSensorRunnerPrevAnchor(t *testing.T) {
	h := newSensorHarness(t, `{"version":1,"sensors":[
		{"id":"web1","type":"web","url":"https://x.example","learning_days":-1}
	]}`, 0)
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
	_ = strings.TrimSpace("")
}
