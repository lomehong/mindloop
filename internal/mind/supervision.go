// 监督快照（Super-agent 论文 Watchdog 的 mindloop 形态，2026-11-02
// 用户批准的两项可借之一）：从轨迹错误步骤聚合出"工作健康度"读数，
// 注入自发唤醒——补两个真实缺口：尾窗渲染只看 50 步，更早的错误
// 滑进省略区；单条错误可见但"同一件事反复失败"的重复模式不可见。
//
// 机制账：
//   - 数据 = 轨迹 error 步骤（事实源），无新状态；
//   - 安静日零成本——健康（窗口内错误 < 2 且无重复失败）时本段
//     不物化，段的存在本身就是信号（DeepSeek："不需要介入时保持
//     安静"）；
//   - 研判归模型（外部控制回路的模型判断半场），升级走既有报文
//     通道（[alert] 前缀），代码只负责把证据摆到眼前。
package mind

import (
	"fmt"
	"strings"
	"time"

	"github.com/lomehong/mindloop/internal/traj"
)

// supervisionWindow 是监督快照的统计窗口：24h 内的错误模式值得研判，
// 更早的属于 recap 分集的历史。
const supervisionWindow = 24 * time.Hour

// supervisionMinErrors 是物化阈值：窗口内错误总数 ≥2 才成段——单次
// 瞬态错误是噪音（投机纪律要求先安静核实，而不是每次闪失都摆上台）。
const supervisionMinErrors = 2

// supervisionMaxLines 摘要行上限：快照是证据索引不是日志转储，模型
// 需要全文时用 traj show 取。
const supervisionMaxLines = 4

// supervisionSnapshot 从轨迹步骤聚合监督快照段。健康返回空串。
// 纯函数：时间由调用方传入，测试确定性。
func supervisionSnapshot(steps []traj.Step, now time.Time) string {
	type errItem struct {
		ts      time.Time
		taskID  string
		attempt string
		content string
	}
	var errs []errItem
	for i := len(steps) - 1; i >= 0; i-- {
		s := steps[i]
		if s.Type != traj.TypeError {
			continue
		}
		at, err := time.Parse(traj.TimeFormat, s.TS)
		if err != nil || now.Sub(at) > supervisionWindow {
			continue
		}
		content, _ := s.Field("content")
		taskID, _ := s.Field("task_id")
		attempt, _ := s.Field("attempt")
		errs = append(errs, errItem{ts: at, taskID: taskID, attempt: attempt, content: content})
	}
	if len(errs) < supervisionMinErrors {
		return ""
	}

	// 重复失败检测：同一 task_id 在窗口内 ≥2 次错误 = 打转的证据。
	taskCounts := map[string]int{}
	for _, e := range errs {
		if e.taskID != "" {
			taskCounts[e.taskID]++
		}
	}
	var repeats []string
	for tid, n := range taskCounts {
		if n >= 2 {
			repeats = append(repeats, fmt.Sprintf("任务 %s 窗口内失败 %d 次", traj.OneLine(tid, 24), n))
		}
	}

	var b strings.Builder
	b.WriteString("监督快照（近 24h 错误聚合，供研判；数据=轨迹 error 步骤）：\n")
	fmt.Fprintf(&b, "- 错误共 %d 次（最近 %s 前）\n", len(errs),
		time.Duration(now.Sub(errs[0].ts).Truncate(time.Minute)).String())
	for _, r := range repeats {
		fmt.Fprintf(&b, "- %s ——同一件事反复失败，优先研判是否在打转\n", r)
	}
	for i, e := range errs {
		if i >= supervisionMaxLines {
			fmt.Fprintf(&b, "- ……另有 %d 次，traj show 按步取全文\n", len(errs)-supervisionMaxLines)
			break
		}
		fmt.Fprintf(&b, "- [%s] %s\n", e.ts.Local().Format("01-02 15:04"), traj.OneLine(e.content, 100))
	}
	b.WriteString("研判纪律：先判断这些失败是可自行修复的模式（修它）、已确认需操作员决策的问题（报告一次，[alert] 前缀），还是已处理过的噪音（安静）——不重复报告已报告过的事。")
	return b.String()
}
