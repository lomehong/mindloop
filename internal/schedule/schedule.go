// Package schedule 是身份级日程原语：持久时间表驱动的三类动作——
// every+exec（周期执行命令，不叫醒模型、零 API 成本）、at+task
// （每天定时提交任务）与 every+task（周期巡检任务，新增的组合），
// 后两类由心智领取执行。
//
// quiet 窗口只约束"提交任务"的条目：at 条目到点落在窗口内则顺延到
// 窗口终点（跨午夜合法），巡检条目窗口内直接蒸发（错过的检查不补
// ——补提交 N 小时前的巡检没有意义）；exec 条目不受 quiet 限制，
// 巡检与采集本来就安静。
//
// 调度检查挂在调度器心跳上（Tick），到点精度即心跳粒度——"到点"
// 由代码保证，不依赖模型自觉。边界：进程不在线则不执行（这正是
// "Agent 不在线"的自然语义）；重启时 at 条目在 48h 回看窗口内的
// 有效触发时刻会幂等补提交（靠任务幂等键兜底），exec/巡检不回补。
package schedule

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// SourceName 是日程提交任务的发起者标识（幂等键的一半）。
const SourceName = "schedule"

// DefaultTimeout 是 exec 条目的默认执行时限。
const DefaultTimeout = time.Minute

// recoverLookback 是 Recover 补提交的回看窗口：覆盖"昨天被 quiet
// 顺延到今天早上"的最近一次可顺延机会；更早的错过保持错过——
// 补提交不是重放历史（与"一个月前的你在吗不需要回答"同向）。
const recoverLookback = 48 * time.Hour

const (
	// minEvery 是周期条目的最小间隔——低于它的循环属于别的东西。
	minEvery = time.Second
	// mtimeEvery 是日程文件热加载检查的最小间隔。
	mtimeEvery = 5 * time.Second
	// maxLogBytes 是 schedule.log 的轮转阈值。
	maxLogBytes = 1 << 20
)

// Item 是日程条目的文件形态（schedule.json 的 items 元素）。
type Item struct {
	ID      string `json:"id"`
	Enabled *bool  `json:"enabled,omitempty"`
	Every   string `json:"every,omitempty"`
	At      string `json:"at,omitempty"`
	Exec    string `json:"exec,omitempty"`
	Task    string `json:"task,omitempty"`
	Timeout string `json:"timeout,omitempty"`
	// Quiet 是安静窗口 "HH:MM-HH:MM"（允许跨午夜，如 23:00-08:00）：
	// 到点落在窗口内的 task 条目按类别顺延或跳过（见包注释）。
	Quiet string `json:"quiet,omitempty"`
	// Days 限定星期（逗号分隔 mon/tue/wed/thu/fri/sat/sun，如
	// "mon"）；空 = 每天。周报类条目用它与 at 组成"每周一 HH:MM"。
	Days string `json:"days,omitempty"`
}

// File 是 schedule.json 的结构。
type File struct {
	Items []Item `json:"items"`
}

// Kind 是校验后的条目类别。校验矩阵是"触发器×动作"的三格：
// every+exec / at+task / every+task（巡检）；at+exec 维持拒绝——
// 周期跑命令是 exec 的本职，"命令产出触发心智"由巡检承担，
// at+exec 没有不被这两格覆盖的用例。
type Kind string

const (
	// KindExec：every + exec——周期执行命令，不叫醒模型。
	KindExec Kind = "exec"
	// KindTask：at + task——每天定时提交任务，由心智领取执行。
	KindTask Kind = "task"
	// KindPatrol：every + task——周期巡检任务，由心智领取执行。
	KindPatrol Kind = "patrol"
)

// Parsed 是校验后的条目。
type Parsed struct {
	Item     Item
	Kind     Kind
	Enabled  bool
	Every    time.Duration // KindExec / KindPatrol：触发周期
	Timeout  time.Duration // KindExec：执行时限
	AtMinute int           // KindTask：每天触发时刻（0..1439，本地时区）
	// HasQuiet 与窗口端点（当天分钟数 0..1439）。窗口允许跨午夜。
	HasQuiet   bool
	QuietStart int
	QuietEnd   int
	// HasDays 与星期位（下标 0=周日…6=周六）：task 条目限定只在
	// 指定星期触发（周报类）；空 = 每天。
	HasDays bool
	Days    [7]bool
}

// Parse 解析并逐条校验日程文件：文件级语法错误返回 err；条目级问题
// 记入 warnings 并跳过该条——一条坏日程不能拖垮整张表。quiet 字段
// 的 misuse（配在 exec 上、格式坏）降级为警告并忽略该字段，条目
// 本身保留——字段级缺陷不该抹掉一条合法日程。
func Parse(data []byte) ([]Parsed, []string, error) {
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, nil, fmt.Errorf("schedule: JSON 解析失败: %w", err)
	}
	var out []Parsed
	var warns []string
	seen := map[string]bool{}
	for i, it := range f.Items {
		label := strings.TrimSpace(it.ID)
		if label == "" {
			label = fmt.Sprintf("#%d", i+1)
		}
		if !validID(it.ID) {
			warns = append(warns, fmt.Sprintf("条目 %s: id 非法（允许字母数字与 -_.，长度 ≤64）", label))
			continue
		}
		if seen[it.ID] {
			warns = append(warns, fmt.Sprintf("条目 %s: id 重复，仅保留首次出现", label))
			continue
		}
		seen[it.ID] = true

		p := Parsed{Item: it, Enabled: it.Enabled == nil || *it.Enabled}
		hasEvery := strings.TrimSpace(it.Every) != ""
		hasAt := strings.TrimSpace(it.At) != ""
		hasExec := strings.TrimSpace(it.Exec) != ""
		hasTask := strings.TrimSpace(it.Task) != ""

		if hasEvery == hasAt {
			warns = append(warns, fmt.Sprintf("条目 %s: 触发器必须恰好二选一（every 或 at）", label))
			continue
		}
		if hasExec == hasTask {
			warns = append(warns, fmt.Sprintf("条目 %s: 动作必须恰好二选一（exec 或 task）", label))
			continue
		}
		if hasEvery {
			switch {
			case hasExec:
				d, err := time.ParseDuration(it.Every)
				if err != nil || d < minEvery {
					warns = append(warns, fmt.Sprintf("条目 %s: every 非法（Go 时长语法且 ≥1s，如 2m）", label))
					continue
				}
				p.Kind = KindExec
				p.Every = d
				p.Timeout = DefaultTimeout
				if strings.TrimSpace(it.Timeout) != "" {
					td, err := time.ParseDuration(it.Timeout)
					if err != nil || td <= 0 {
						warns = append(warns, fmt.Sprintf("条目 %s: timeout 非法（Go 时长语法，如 60s）", label))
						continue
					}
					p.Timeout = td
				}
			default: // hasTask：巡检
				d, err := time.ParseDuration(it.Every)
				if err != nil || d < minEvery {
					warns = append(warns, fmt.Sprintf("条目 %s: every 非法（Go 时长语法且 ≥1s，如 2m）", label))
					continue
				}
				p.Kind = KindPatrol
				p.Every = d
			}
		} else {
			if !hasTask {
				warns = append(warns, fmt.Sprintf("条目 %s: at 只能与 task 组合（定时提问类）", label))
				continue
			}
			h, m, ok := parseClock(it.At)
			if !ok {
				warns = append(warns, fmt.Sprintf("条目 %s: at 非法（本地时刻 HH:MM，如 21:00）", label))
				continue
			}
			p.Kind = KindTask
			p.AtMinute = h*60 + m
		}

		// days 字段：只对提交任务的条目有意义；exec 配了即警告忽略。
		if ds := strings.TrimSpace(it.Days); ds != "" {
			if p.Kind == KindExec {
				warns = append(warns, fmt.Sprintf("条目 %s: days 只对 task 条目生效（exec 每天照跑），已忽略", label))
			} else if days, ok := parseDays(ds); !ok {
				warns = append(warns, fmt.Sprintf("条目 %s: days 非法（逗号分隔 mon/tue/wed/thu/fri/sat/sun），已忽略", label))
			} else {
				p.HasDays = true
				p.Days = days
			}
		}

		// quiet 字段：只对提交任务的条目有意义；exec 配了即警告忽略。
		if qs := strings.TrimSpace(it.Quiet); qs != "" {
			if p.Kind == KindExec {
				warns = append(warns, fmt.Sprintf("条目 %s: quiet 只对 task 条目生效（exec 本来就不叫醒模型），已忽略", label))
			} else if start, end, ok := parseQuiet(qs); !ok {
				warns = append(warns, fmt.Sprintf("条目 %s: quiet 非法（HH:MM-HH:MM 且两端不等，如 23:00-08:00），已忽略", label))
			} else {
				p.HasQuiet = true
				p.QuietStart = start
				p.QuietEnd = end
			}
		}
		out = append(out, p)
	}
	return out, warns, nil
}

// Load 读取并解析日程文件；不存在时返回空日程（不是错误）。
func Load(path string) ([]Parsed, []string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	return Parse(data)
}

// DateKey 是任务幂等键（sched-<id>-<计划时刻>）：由计划触发时刻导出，
// 与顺延、重启、热加载全部无关——at 条目的计划时刻是当天的 at 点
// （顺延提交与当天直接触发共享一键），巡检条目是触发网格点。
// 旧格式 sched-<id>-<date> 已弃用：升级当天旧键去重失效可能双提交
// 一次，量级为一行日志。
func DateKey(id string, planned time.Time) string {
	return "sched-" + id + "-" + planned.Format("2006-01-02-1504")
}

// Fire 是一次已越过的有效触发：Planned 是计划触发时刻（幂等键由它
// 导出），At 是有效触发时刻（quiet 顺延后的实际到点，无 quiet 时与
// Planned 相同）。
type Fire struct {
	Planned time.Time
	At      time.Time
}

// effectiveFires 返回 (lastSeen, now] 内已越过的有效触发时刻集合
// （仅 KindTask）。它是触发评估的唯一出处：Tick 用内存 lastSeen
// 逐拍评估，Recover 用 48h 回看窗口补提交——两处共用同一函数，
// "什么时候该触发"只有一个答案。纯函数，无锁无 IO。
func effectiveFires(p Parsed, lastSeen, now time.Time) []Fire {
	if p.Kind != KindTask {
		return nil
	}
	if lastSeen.IsZero() || lastSeen.After(now) {
		lastSeen = now
	}
	// 从 lastSeen 的前一天扫起：被 quiet 顺延到今天的计划点属于昨天。
	day := time.Date(lastSeen.Year(), lastSeen.Month(), lastSeen.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, -1)
	endDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	var fires []Fire
	for ; !day.After(endDay); day = day.AddDate(0, 0, 1) {
		if !p.dayAllowed(day) {
			continue // 星期限定（如周报只在周一）：非触发日直接跳过
		}
		planned := time.Date(day.Year(), day.Month(), day.Day(), p.AtMinute/60, p.AtMinute%60, 0, 0, now.Location())
		at := shiftOutOfQuiet(planned, p)
		if lastSeen.Before(at) && !at.After(now) {
			fires = append(fires, Fire{Planned: planned, At: at})
		}
	}
	return fires
}

// shiftOutOfQuiet 把落在 quiet 窗口内的时刻顺延到窗口终点；不在
// 窗口内则原样返回。跨午夜窗口的晚间部分（m ≥ start）顺延到明天
// 终点，早间部分（m < end）顺延到今天终点。
func shiftOutOfQuiet(t time.Time, p Parsed) time.Time {
	if !p.HasQuiet || !inQuiet(t, p.QuietStart, p.QuietEnd) {
		return t
	}
	day := t
	if p.QuietStart > p.QuietEnd && t.Hour()*60+t.Minute() >= p.QuietStart {
		day = t.AddDate(0, 0, 1)
	}
	return time.Date(day.Year(), day.Month(), day.Day(), p.QuietEnd/60, p.QuietEnd%60, 0, 0, t.Location())
}

// inQuiet 报告 t 是否落在 [start, end) 窗口内（跨午夜合法；
// start == end 视为空窗口，Parse 已拒绝，这里兜底）。
func inQuiet(t time.Time, start, end int) bool {
	if start == end {
		return false
	}
	m := t.Hour()*60 + t.Minute()
	if start < end {
		return m >= start && m < end
	}
	return m >= start || m < end
}

// weekdayNames 是 days 字段的星期名（下标=time.Weekday 值）。
var weekdayNames = []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}

// parseDays 解析逗号分隔的星期名列表为 7 位命中表。
func parseDays(s string) ([7]bool, bool) {
	var days [7]bool
	for _, part := range strings.Split(s, ",") {
		name := strings.ToLower(strings.TrimSpace(part))
		if name == "" {
			continue
		}
		hit := false
		for i, n := range weekdayNames {
			if name == n {
				days[i] = true
				hit = true
				break
			}
		}
		if !hit {
			return days, false
		}
	}
	// 全部命中位为零视为非法（等价于没配）。
	any := false
	for _, d := range days {
		if d {
			any = true
		}
	}
	return days, any
}

// dayAllowed 报告 planned 所在星期是否在 days 限定内（未配置恒真）。
func (p Parsed) dayAllowed(t time.Time) bool {
	if !p.HasDays {
		return true
	}
	return p.Days[int(t.Weekday())]
}

// nextAllowedDay 从 day 起向后找第一个允许星期（含 day 本身）。
func (p Parsed) nextAllowedDay(day time.Time) time.Time {
	for i := 0; i < 7 && !p.dayAllowed(day); i++ {
		day = day.AddDate(0, 0, 1)
	}
	return day
}

// parseQuiet 严格解析 "HH:MM-HH:MM"（两端时刻且不相等）。
func parseQuiet(s string) (int, int, bool) {
	a, b, ok := strings.Cut(s, "-")
	if !ok {
		return 0, 0, false
	}
	h1, m1, ok1 := parseClock(strings.TrimSpace(a))
	h2, m2, ok2 := parseClock(strings.TrimSpace(b))
	if !ok1 || !ok2 || (h1 == h2 && m1 == m2) {
		return 0, 0, false
	}
	return h1*60 + m1, h2*60 + m2, true
}

// PlannedFor 返回手动触发（SubmitNow）视作的计划触发时刻：at 条目
// 取当天的 at 点——与到点自动触发同键，同一天手动+自动不双发；
// 巡检条目取当前时刻（手动巡检就是"现在查一次"）。
func PlannedFor(p Parsed, now time.Time) time.Time {
	if p.Kind == KindTask {
		return atTimeOn(now, p.AtMinute)
	}
	return now
}

// RenderTask 渲染任务文本模板：{{date}} 替换为当天本地日期，
// {{report_to}} 替换为默认外发地址（缺省 operator）。
func RenderTask(content string, date time.Time) string {
	return strings.ReplaceAll(content, "{{date}}", dateString(date))
}

// reportTo 折算生效的外发地址（空 = operator）。
func (r *Runtime) reportTo() string {
	if v := strings.TrimSpace(r.opts.ReportTo); v != "" {
		return v
	}
	return "operator"
}

// renderTaskText 是 submitFire/SubmitNow 共用的任务文本出口：
// 模板渲染 + at 条目的完成回执注入。注入是教学不是机制——模型
// 自行决定回执的语气与时机；巡检条目不注入（"无事不报"的纪律
// 与"完成必回执"矛盾，巡检的汇报义务由模板自带）。
func (r *Runtime) renderTaskText(p Parsed, now time.Time) string {
	content := RenderTask(p.Item.Task, now)
	content = strings.ReplaceAll(content, "{{report_to}}", r.reportTo())
	if p.Kind == KindTask {
		content += "\n\n（本任务由 schedule 定时触发；完成后向 " + r.reportTo() +
			" 发一条 ≤100 字的完成回执，含结果要点与产物路径。若模板另有汇报要求，以模板为准并兼顾本条。）"
	}
	return content
}

// validID 约束条目 id：字母数字与 -_.，长度 1..64。
func validID(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
		default:
			return false
		}
	}
	return true
}

// parseClock 严格解析 HH:MM（两位小时与分钟，本地时刻）。
func parseClock(s string) (int, int, bool) {
	if len(s) != 5 || s[2] != ':' {
		return 0, 0, false
	}
	h, err1 := strconv.Atoi(s[:2])
	m, err2 := strconv.Atoi(s[3:])
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, 0, false
	}
	return h, m, true
}

// Options 是 Runtime 的构造参数。
type Options struct {
	// Path 是 schedule.json 路径。
	Path string
	// Dir 是 exec 条目工作目录（身份目录）。
	Dir string
	// LogPath 是执行日志；空 = 不落盘。
	LogPath string
	// StatePath 是状态投影（schedule list 读取）；空 = 不落盘。
	StatePath string
	// Env 是 exec 的显式业务环境值（childenv 白名单之外的补充）。
	Env []string
	// Submit 提交 task 类条目的任务（幂等键 = clientMessageID）；
	// note 供日志与状态。装配层负责把 task.ErrConflict 视为
	// "计划已提交"的良性跳过。
	Submit func(ctx context.Context, content, clientMessageID string) (note string, err error)
	// Alert 是 exec 失败告警的写出口（nil = 现状只落日志）。参数是
	// 纯数据（本包不 import traj）；装配层闭包持有轨迹写入职责，
	// 负责构造 alert 步骤落盘——失败必须叫醒人，不能只躺在日志里。
	Alert func(kind, entryID, content string)
	// ReportTo 是任务模板 {{report_to}} 的替换值与完成回执的投递
	// 目标（"operator" = 对话流，"wecom:<uid>" 等渠道地址由 bridge
	// 出站泵认领投递）。空值取缺省 operator。本包不读环境变量——
	// 装配层从 REPORT_TO（显式环境变量 > 身份 .env）取值传入。
	ReportTo string
	// Execute 是 exec 条目的执行器；nil 时用沙箱默认实现（测试注入用）。
	Execute func(ctx context.Context, p Parsed) ExecOutcome
	// Logger 注入重要事件（失败、警告）；成功执行只进文件。
	Logger func(format string, args ...any)
}

// ExecOutcome 是一次 exec 执行的结果。
type ExecOutcome struct {
	ExitCode int
	Duration time.Duration
	Err      error
	Detail   string // 失败时的输出尾部（单行化）
}

// ItemState 是单个条目的状态投影。
type ItemState struct {
	LastRun      string `json:"last_run,omitempty"`
	NextRun      string `json:"next_run,omitempty"`
	LastError    string `json:"last_error,omitempty"`
	LastExitCode int    `json:"last_exit_code,omitempty"`
	LastNote     string `json:"last_note,omitempty"`
}

// State 是 schedule-state.json 的结构。
type State struct {
	UpdatedAt string               `json:"updated_at"`
	Items     map[string]ItemState `json:"items"`
}

// pendingSubmit 是提交失败的重试记录：同一天内逐拍重试（幂等键
// 不变，重复提交由任务层兜底）；跨天放弃——错过就是错过。登记粒度
// 是幂等键而不是条目：一次 Recover 补提交两天时，一天的失败不能被
// 另一天的成功顺手清掉。
type pendingSubmit struct {
	key     string
	planned time.Time
	day     string
}

// Runtime 是身份日程的运行时：装载、热加载、到点检查与执行。
// 全部导出方法都可由调度器心跳直接调用，不阻塞。
type Runtime struct {
	opts Options

	mu           sync.Mutex
	items        []Parsed
	modTime      time.Time
	mtimeChecked time.Time
	lastSeen     time.Time
	next         map[string]time.Time       // every 条目（exec/巡检）下次触发
	running      map[string]bool            // exec 条目在跑标记（防重叠）
	pendingAt    map[string][]pendingSubmit // 条目 → 按幂等键登记的失败重试
	state        map[string]ItemState
}

// New 构造日程运行时；执行器缺省为沙箱实现。
func New(opts Options) *Runtime {
	r := &Runtime{
		opts:      opts,
		next:      map[string]time.Time{},
		running:   map[string]bool{},
		pendingAt: map[string][]pendingSubmit{},
		state:     map[string]ItemState{},
	}
	if r.opts.Execute == nil {
		r.opts.Execute = r.executeSandbox
	}
	if r.opts.LogPath != "" {
		_ = os.MkdirAll(filepath.Dir(r.opts.LogPath), 0o755)
	}
	if r.opts.StatePath != "" {
		_ = os.MkdirAll(filepath.Dir(r.opts.StatePath), 0o755)
	}
	return r
}

// Recover 在调度器取得运行锁后调用：装载日程、补提交回看窗口内
// 已越过的有效触发时刻。补提交靠任务幂等键兜底，重复调用无害。
func (r *Runtime) Recover(ctx context.Context, now time.Time) {
	r.mu.Lock()
	reloaded := r.load(now, true)
	items := append([]Parsed(nil), r.items...)
	r.lastSeen = now
	r.mtimeChecked = now
	r.mu.Unlock()
	if reloaded {
		r.writeState(now)
	}
	for _, p := range items {
		if !p.Enabled || p.Kind != KindTask {
			continue
		}
		for _, fire := range effectiveFires(p, now.Add(-recoverLookback), now) {
			r.submitFire(ctx, p, fire, now)
		}
	}
}

// Tick 是一次心跳：按节流热加载，然后检查三类条目的到点。调用方是
// 调度器主循环——本方法不阻塞（exec 在工作 goroutine 里跑，任务
// 提交只在提交点短暂经过任务锁）。
func (r *Runtime) Tick(ctx context.Context, now time.Time) {
	r.mu.Lock()
	reloaded := false
	if now.Sub(r.mtimeChecked) >= mtimeEvery {
		r.mtimeChecked = now
		reloaded = r.load(now, false)
	}
	items := append([]Parsed(nil), r.items...)
	lastSeen := r.lastSeen
	r.mu.Unlock()
	if reloaded {
		r.writeState(now)
	}

	for _, p := range items {
		if !p.Enabled {
			continue
		}
		switch p.Kind {
		case KindExec:
			r.tickExec(ctx, p, now)
		case KindPatrol:
			r.tickPatrol(ctx, p, now)
		case KindTask:
			for _, fire := range effectiveFires(p, lastSeen, now) {
				r.submitFire(ctx, p, fire, now)
			}
			// 提交失败的同日重试：跨天放弃（错过就是错过）。
			r.mu.Lock()
			var retries []Fire
			for _, ps := range r.pendingAt[p.Item.ID] {
				if ps.day == dateString(now) {
					retries = append(retries, Fire{Planned: ps.planned})
				}
			}
			r.mu.Unlock()
			for _, fire := range retries {
				r.submitFire(ctx, p, fire, now)
			}
		}
	}
	r.mu.Lock()
	r.lastSeen = now
	r.mu.Unlock()
}

// tickExec 检查一个周期条目：到点则在工作 goroutine 里执行；上一次
// 未结束则跳过本次（next 照常推进，节奏不漂移）。
func (r *Runtime) tickExec(ctx context.Context, p Parsed, now time.Time) {
	r.mu.Lock()
	next := r.next[p.Item.ID]
	if next.IsZero() {
		// 首次见到：注册即开始（下一拍触发）。
		r.next[p.Item.ID] = now
		r.mu.Unlock()
		return
	}
	if now.Before(next) {
		r.mu.Unlock()
		return
	}
	r.next[p.Item.ID] = advanceNext(next, p.Every, now)
	busy := r.running[p.Item.ID]
	if !busy {
		r.running[p.Item.ID] = true
	}
	r.mu.Unlock()

	if busy {
		r.log("[%s] 跳过本次（上次执行仍在进行）", p.Item.ID)
		return
	}
	go func() {
		outcome := r.opts.Execute(ctx, p)
		r.mu.Lock()
		delete(r.running, p.Item.ID)
		r.mu.Unlock()
		r.recordExec(p, now, outcome)
	}()
}

// tickPatrol 检查一个巡检条目：网格机制与 tickExec 同源（next 推进、
// 节奏不漂移），动作是提交任务而非执行命令。quiet 窗口内的网格点
// 直接蒸发——错过的检查不补（与 exec 不回补同向），窗口后的第一
// 个网格点照常触发，键由各自的计划网格点导出、互不干扰。
func (r *Runtime) tickPatrol(ctx context.Context, p Parsed, now time.Time) {
	r.mu.Lock()
	next := r.next[p.Item.ID]
	if next.IsZero() {
		r.next[p.Item.ID] = now
		r.mu.Unlock()
		return
	}
	if now.Before(next) {
		r.mu.Unlock()
		return
	}
	planned := next
	r.next[p.Item.ID] = advanceNext(next, p.Every, now)
	r.mu.Unlock()

	if p.HasQuiet && !shiftOutOfQuiet(planned, p).Equal(planned) {
		r.log("[%s] 网格点 %s 落在 quiet 窗口内，跳过", p.Item.ID, fmtTime(planned))
		return
	}
	r.submitFire(ctx, p, Fire{Planned: planned, At: now}, now)
}

// recordExec 记一次 exec 执行的结果（日志 + 状态投影 + 失败告警）。
func (r *Runtime) recordExec(p Parsed, now time.Time, o ExecOutcome) {
	if o.Err != nil {
		suffix := ""
		if o.Detail != "" {
			suffix = "：" + o.Detail
		}
		r.logf("[%s] 执行失败: %v%s", p.Item.ID, o.Err, suffix)
		// 失败叫醒：alert 是代码写入的系统事实，monolith 订阅它评估
		// 升级——失败只躺在日志里等于没人被叫醒。
		if r.opts.Alert != nil {
			r.opts.Alert("exec-failure", p.Item.ID,
				fmt.Sprintf("[%s] 执行失败: %v%s", p.Item.ID, o.Err, suffix))
		}
	} else {
		r.log("[%s] 执行完成（exit %d，%s）", p.Item.ID, o.ExitCode, o.Duration.Round(time.Millisecond))
	}
	r.mu.Lock()
	st := r.state[p.Item.ID]
	st.LastRun = fmtTime(now)
	st.LastExitCode = o.ExitCode
	if o.Err != nil {
		st.LastError = o.Err.Error()
	} else {
		st.LastError = ""
	}
	r.state[p.Item.ID] = st
	r.mu.Unlock()
	r.writeState(now)
}

// submitFire 提交一次触发的任务；失败按幂等键登记重试，后续心跳
// 重试（键不变，重复提交由任务层兜底）。planned 是计划触发时刻——
// 键由它导出，与提交动作发生的时刻无关。
func (r *Runtime) submitFire(ctx context.Context, p Parsed, fire Fire, now time.Time) {
	if r.opts.Submit == nil {
		r.logf("[%s] 未配置任务提交通道，跳过", p.Item.ID)
		return
	}
	content := r.renderTaskText(p, now)
	key := DateKey(p.Item.ID, fire.Planned)
	note, err := r.opts.Submit(ctx, content, key)
	r.mu.Lock()
	st := r.state[p.Item.ID]
	if err != nil {
		r.pendingAt[p.Item.ID] = upsertPending(r.pendingAt[p.Item.ID], pendingSubmit{
			key: key, planned: fire.Planned, day: dateString(now),
		})
		st.LastError = err.Error()
	} else {
		r.pendingAt[p.Item.ID] = dropPending(r.pendingAt[p.Item.ID], key)
		st.LastRun = fmtTime(now)
		st.LastNote = note
		st.LastError = ""
	}
	r.state[p.Item.ID] = st
	r.mu.Unlock()
	if err != nil {
		r.logf("[%s] 任务提交失败: %v", p.Item.ID, err)
	} else if note != "" {
		r.log("[%s] 已提交任务（%s） %s", p.Item.ID, key, note)
	} else {
		r.log("[%s] 已提交任务（%s）", p.Item.ID, key)
	}
	r.writeState(now)
}

// upsertPending 按键替换或追加一条失败记录。
func upsertPending(list []pendingSubmit, p pendingSubmit) []pendingSubmit {
	for i := range list {
		if list[i].key == p.key {
			list[i] = p
			return list
		}
	}
	return append(list, p)
}

// dropPending 摘除一条已成功（或已放弃）的失败记录。
func dropPending(list []pendingSubmit, key string) []pendingSubmit {
	for i := range list {
		if list[i].key == key {
			return append(list[:i], list[i+1:]...)
		}
	}
	return list
}

// SubmitNow 立即提交一个 task 条目（CLI schedule run）：幂等键与到点
// 触发相同（键由 PlannedFor 的计划时刻导出）——at 条目同一天不会因
// 手动+自动产生两个任务；只落一行日志，不动调度状态投影。
func (r *Runtime) SubmitNow(ctx context.Context, p Parsed, now time.Time) (string, error) {
	if r.opts.Submit == nil {
		return "", errors.New("schedule: 未配置任务提交通道")
	}
	content := r.renderTaskText(p, now)
	key := DateKey(p.Item.ID, PlannedFor(p, now))
	note, err := r.opts.Submit(ctx, content, key)
	if err != nil {
		r.logf("[%s] 手动提交失败: %v", p.Item.ID, err)
		return "", err
	}
	r.log("[%s] 手动提交（%s）%s", p.Item.ID, key, note)
	return note, nil
}

// load 按 mtime 装载日程文件；force 时忽略 mtime 判定。调用方持锁。
// 文件不存在 = 空日程；解析失败保留旧条目（只记警告，不拦调度）。
// 返回值表示条目集合是否被刷新。
func (r *Runtime) load(now time.Time, force bool) bool {
	if r.opts.Path == "" {
		return false
	}
	fi, err := os.Stat(r.opts.Path)
	if err != nil {
		if os.IsNotExist(err) {
			r.modTime = time.Time{}
			if len(r.items) != 0 {
				r.items = nil
				r.next = map[string]time.Time{}
				r.log("日程文件已删除，日程清空")
				return true
			}
			return false
		}
		r.log("日程文件不可读: %v", err)
		return false
	}
	if !force && fi.ModTime().Equal(r.modTime) {
		return false
	}
	data, err := os.ReadFile(r.opts.Path)
	if err != nil {
		r.log("日程文件读取失败: %v", err)
		return false
	}
	parsed, warns, err := Parse(data)
	for _, w := range warns {
		r.logf("日程警告: %s", w)
	}
	if err != nil {
		r.logf("日程解析失败（保留旧日程）: %v", err)
		return false
	}
	r.items = parsed
	r.modTime = fi.ModTime()
	// every 条目（exec/巡检）首次出现立即生效；已存在的保留节奏。
	alive := map[string]bool{}
	for _, p := range parsed {
		alive[p.Item.ID] = true
		if p.Kind == KindExec || p.Kind == KindPatrol {
			if _, ok := r.next[p.Item.ID]; !ok {
				r.next[p.Item.ID] = now
			}
		}
	}
	for id := range r.next {
		if !alive[id] {
			delete(r.next, id)
		}
	}
	for id := range r.pendingAt {
		if !alive[id] {
			delete(r.pendingAt, id)
		}
	}
	for id := range r.state {
		if !alive[id] {
			delete(r.state, id)
		}
	}
	return true
}

// writeState 原子写状态投影（供 schedule list 读取）。
func (r *Runtime) writeState(now time.Time) {
	if r.opts.StatePath == "" {
		return
	}
	r.mu.Lock()
	st := State{UpdatedAt: fmtTime(now), Items: map[string]ItemState{}}
	for _, p := range r.items {
		s := r.state[p.Item.ID]
		switch p.Kind {
		case KindExec, KindPatrol:
			if n := r.next[p.Item.ID]; !n.IsZero() {
				s.NextRun = fmtTime(n)
			}
		case KindTask:
			// 展示有效触发时刻：先按"计划时刻已过推到明天"取下一次
			// 允许星期的计划点，再过 quiet 顺延——与 effectiveFires
			// 的评估同序。
			planned := atTimeOn(now, p.AtMinute)
			if !planned.After(now) || !p.dayAllowed(planned) {
				planned = p.nextAllowedDay(planned.AddDate(0, 0, 1))
			}
			s.NextRun = fmtTime(shiftOutOfQuiet(planned, p))
		}
		st.Items[p.Item.ID] = s
	}
	r.mu.Unlock()
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return
	}
	tmp := r.opts.StatePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, r.opts.StatePath)
}

// runningCount 报告正在执行的 exec 条目数（测试与诊断用）。
func (r *Runtime) runningCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.running)
}

// advanceNext 推进周期条目的下次触发：追赶落后多拍时只补跑一次，
// 然后把节奏对齐到 now + every，避免醒来后连发。
func advanceNext(next time.Time, every time.Duration, now time.Time) time.Time {
	n := next.Add(every)
	if !n.After(now) {
		n = now.Add(every)
	}
	return n
}

// atTimeOn 返回 now 当天 minute 时刻（本地时区）。
func atTimeOn(now time.Time, minute int) time.Time {
	return time.Date(now.Year(), now.Month(), now.Day(), minute/60, minute%60, 0, 0, now.Location())
}

func dateString(t time.Time) string { return t.Format("2006-01-02") }

func fmtTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02 15:04:05")
}
