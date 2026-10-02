package obs

import (
	"context"
	"testing"
	"time"

	"github.com/lomehong/mindloop/internal/traj"
)

func appendTestStep(t *testing.T, tl *traj.Timeline, s traj.Step) {
	t.Helper()
	if err := tl.Append(context.Background(), s); err != nil {
		t.Fatalf("Append: %v", err)
	}
}

// TestDeriveTaste：味觉投影的校准语义——只有 proposal-redundant 算
// "阈值过紧"证据；漏报匹配只认 operator 的话（心智自己的产出不算）。
func TestDeriveTaste(t *testing.T) {
	t.Setenv("MINDLOOP_HOME", t.TempDir())
	tl, err := traj.Create(context.Background(), "taste-test")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// S0 沉淀：两份报表事件。
	for _, subject := range []string{"D:/work/report-q3.xlsx", "D:/work/report-budget.xlsx"} {
		s := traj.NewStep(traj.TypeEvent)
		s.Fields["source"] = "fs1"
		s.Fields["subject"] = subject
		s.Fields["salience"] = "s0"
		appendTestStep(t, tl, s)
	}
	// 味觉：撤销两条（一条提案多余、一条执行失败）、批准、拒绝。
	mkTaste := func(signal, cause string) traj.Step {
		s := traj.NewStep(traj.TypeTaste)
		s.Fields["signal"] = signal
		if cause != "" {
			s.Fields["cause"] = cause
		}
		return s
	}
	appendTestStep(t, tl, mkTaste("undo", "proposal-redundant"))
	appendTestStep(t, tl, mkTaste("undo", "execution-failed"))
	appendTestStep(t, tl, mkTaste("approve", ""))
	appendTestStep(t, tl, mkTaste("deny", ""))
	// operator 的话提及一个 S0 subject → 漏报证据。
	msg := traj.NewStep(traj.TypeMessage)
	msg.Fields["from"] = "operator"
	msg.Fields["to"] = "ada"
	msg.Fields["content"] = "report-q3.xlsx 那边改了你怎么没说？"
	appendTestStep(t, tl, msg)
	// 心智自己的汇报提及另一 subject：是产出不是反馈，不计漏报。
	self := traj.NewStep(traj.TypeMessage)
	self.Fields["from"] = "ada"
	self.Fields["to"] = "operator"
	self.Fields["content"] = "report-budget.xlsx 有更新"
	appendTestStep(t, tl, self)

	sum, err := DeriveTaste(tl, "ada", time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("DeriveTaste: %v", err)
	}
	if sum.Undoes != 2 {
		t.Fatalf("撤销应 2，得 %d", sum.Undoes)
	}
	if sum.UndoThresholdTight != 1 {
		t.Fatalf("阈值过紧证据应 1（只有 proposal-redundant），得 %d", sum.UndoThresholdTight)
	}
	if sum.Approves != 1 || sum.Denies != 1 {
		t.Fatalf("批准/拒绝应 1/1，得 %d/%d", sum.Approves, sum.Denies)
	}
	if sum.MissedReports != 1 || len(sum.MissedSubjects) != 1 || sum.MissedSubjects[0] != "D:/work/report-q3.xlsx" {
		t.Fatalf("漏报应只命中 operator 提及的那条，得 %d %+v", sum.MissedReports, sum.MissedSubjects)
	}
}
