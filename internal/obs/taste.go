// taste.go — 味觉投影（舌的证据面，perception.md §5 Phase 4）：
// 视图皆派生——输入是轨迹上已有的三类步骤，不新增存储：
//   - taste 步骤（operator 的归因反馈：undo/approve/deny + cause）；
//   - event 步骤的 S0 沉淀（漏报匹配的对照面）；
//   - message 步骤（operator 的话）。
//
// 校准语义（评审钉死的因果防线）：
//   - undo ≠ 感知错了。只有 cause=proposal-redundant（提案本身
//     多余）算"阈值过紧"的证据；execution-failed/changed-mind/
//     unattributed 都不计入——把执行质量、决策质量、感知质量混进
//     一个标量会系统性污染校准；
//   - 漏报一等化：operator 的话提及了某个 S0 沉淀事件的 subject
//     = 一次"该报没报"的证据（静默棘轮的对冲——只有负反馈可
//     观测的系统必然滑向什么都不报）。
package obs

import (
	"strings"
	"time"

	"github.com/lomehong/mindloop/internal/traj"
)

// tasteLookbackSteps 是投影扫描的步骤上限——味觉是校准证据面不是
// 审计全量，窗口外不回溯（与 schedule.Recover 的 48h 回看同哲学）。
const tasteLookbackSteps = 2000

// TasteSummary 是窗口内的味觉聚合。
type TasteSummary struct {
	Undoes             int      // undo 总数（有归因的）
	UndoThresholdTight int      // 其中 cause=proposal-redundant——阈值过紧证据
	Approves           int      // approve 数（正向：提案被认可）
	Denies             int      // deny 数（提案被拒——提案面的负证据）
	MissedReports      int      // operator 的话提及 S0 沉淀 subject 的次数（漏报证据）
	MissedSubjects     []string // 命中的 subject（去重后，诊断面）
}

// DeriveTaste 从轨迹派生味觉聚合。selfName 用于区分 operator 的
// 话与心智自己的汇报（from=self 的 message 是产出不是反馈）。
func DeriveTaste(tl *traj.Timeline, selfName string, windowStart time.Time) (TasteSummary, error) {
	steps, err := tl.Tail(tasteLookbackSteps, []string{traj.TypeTaste, traj.TypeEvent, traj.TypeMessage})
	if err != nil {
		return TasteSummary{}, err
	}
	var sum TasteSummary
	seenMissed := map[string]bool{}
	var s0Subjects []string
	for i := len(steps) - 1; i >= 0; i-- { // 旧 → 新：s0Subjects 先建全
		s := steps[i]
		ts, err := time.Parse(traj.TimeFormat, s.TS)
		if err == nil && ts.Before(windowStart) {
			continue
		}
		switch s.Type {
		case traj.TypeEvent:
			if sal, _ := s.Field("salience"); sal == "s0" {
				if subject, ok := s.Field("subject"); ok && subject != "" {
					s0Subjects = append(s0Subjects, subject)
				}
			}
		}
	}
	for _, s := range steps {
		ts, err := time.Parse(traj.TimeFormat, s.TS)
		if err == nil && ts.Before(windowStart) {
			continue
		}
		switch s.Type {
		case traj.TypeTaste:
			signal, _ := s.Field("signal")
			cause, _ := s.Field("cause")
			switch signal {
			case "undo":
				sum.Undoes++
				if cause == "proposal-redundant" {
					sum.UndoThresholdTight++
				}
			case "approve":
				sum.Approves++
			case "deny":
				sum.Denies++
			}
		case traj.TypeMessage:
			from, _ := s.Field("from")
			if from == selfName || from == "" {
				continue // 心智自己的产出不是反馈
			}
			content, _ := s.Field("content")
			for _, subject := range s0Subjects {
				if subject == "" || seenMissed[subject] {
					continue
				}
				// 全路径或基名命中都算——人提文件名，不提盘符。
				if strings.Contains(content, subject) || strings.Contains(content, baseOf(subject)) {
					seenMissed[subject] = true
					sum.MissedReports++
					sum.MissedSubjects = append(sum.MissedSubjects, subject)
				}
			}
		}
	}
	return sum, nil
}

// baseOf 取路径形主体的末段（/ 与 \ 都认）；非路径形态原样返回。
func baseOf(subject string) string {
	if i := strings.LastIndexAny(subject, `/\`); i >= 0 && i+1 < len(subject) {
		return subject[i+1:]
	}
	return subject
}
