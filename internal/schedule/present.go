package schedule

// 本文件是条目展示文本的唯一出处：CLI list 与 web 日程页显示同一
// 句话——两处各写一份必然漂移。

import (
	"fmt"
	"strings"
	"time"
)

// TriggerText 触发器列文本。
func TriggerText(p Parsed) string {
	if p.Kind == KindTask {
		at := fmt.Sprintf("at %02d:%02d", p.AtMinute/60, p.AtMinute%60)
		if p.HasDays {
			var names []string
			for i, on := range p.Days {
				if on {
					names = append(names, weekdayNames[i])
				}
			}
			at += " (" + strings.Join(names, ",") + ")"
		}
		return at
	}
	return "every " + p.Every.String()
}

// ActionText 动作列文本。
func ActionText(p Parsed) string {
	if p.Kind == KindExec {
		return "exec " + oneLine(p.Item.Exec, 60)
	}
	return "task " + oneLine(p.Item.Task, 60)
}

// NextText 下次触发的展示文本：优先状态投影；投影缺失时 task 条目
// 可精确推算（at 条目含 quiet 顺延——先取下一次计划点再过窗口；
// every 条目没有运行时就不知道下一次）。展示的是有效触发时刻，
// 与 writeState 投影同序。
func NextText(p Parsed, st ItemState, now time.Time) string {
	if st.NextRun != "" {
		return st.NextRun
	}
	if p.Kind == KindTask {
		planned := atTimeOn(now, p.AtMinute)
		if !planned.After(now) || !p.dayAllowed(planned) {
			planned = p.nextAllowedDay(planned.AddDate(0, 0, 1))
		}
		return shiftOutOfQuiet(planned, p).Format("2006-01-02 15:04:05")
	}
	return ""
}

// QuietText quiet 窗口的展示后缀（未配置返回空串）。
func QuietText(p Parsed) string {
	if !p.HasQuiet {
		return ""
	}
	return fmt.Sprintf("（quiet %02d:%02d-%02d:%02d）",
		p.QuietStart/60, p.QuietStart%60, p.QuietEnd/60, p.QuietEnd%60)
}
