package obs

// stats.go — 评估基线：用量台账的派生指标。视图皆派生：这里不写
// 任何新存储，台账（UsageRecorder 落的 llm-usage.jsonl）是唯一
// 事实源，读方宽容（坏行跳过、缺文件 = 合法的从未运行）。
//
// v1 三个指标（roadmap §1.4）：
//   - 调用与 token 分账：按唤醒（watchdog/scheduled/step/manual）、
//     按阶段（wake/chat/task）、按模型——自发行为与响应人类各烧
//     多少，一眼可辨；
//   - 空转唤醒率：watchdog 合成唤醒的思考轮中，窗口内未跟随新
//     运行的占比。这是台账级代理——"有效 FINAL"的精确判定需要
//     计划步骤（roadmap §2.4）落地后才有锚点；
//   - token 效率的原料：按模型与按任务章的 token 分账；"每完成
//     任务消耗"的 join 在 cli 层用任务系统做。

import (
	"encoding/json"
	"os"
	"strings"
	"time"

	"mindloop/internal/traj"
)

// UsageRow 是台账行的只读解码形态（写方结构见 UsageRecorder）。
type UsageRow struct {
	TS               string `json:"ts"`
	PromptTokens     int    `json:"prompt_tokens"`
	CompletionTokens int    `json:"completion_tokens"`
	UsageKnown       bool   `json:"usage_known"`
	LatencyMS        int64  `json:"latency_ms,omitempty"`
	Model            string `json:"model,omitempty"`
	Provider         string `json:"provider,omitempty"`
	Task             string `json:"task,omitempty"`
	Run              string `json:"run,omitempty"`
	Thinker          string `json:"thinker,omitempty"`
	Wake             string `json:"wake,omitempty"`
	Phase            string `json:"phase,omitempty"`
	Error            string `json:"error,omitempty"`
}

// LoadUsage 读台账。文件不存在返回 (nil, nil)——从未运行过是合法
// 状态；单行损坏跳过（账本是追加式日志，读方按能读多少读多少）。
func LoadUsage(ledgerPath string) ([]UsageRow, error) {
	data, err := os.ReadFile(ledgerPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var rows []UsageRow
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var r UsageRow
		if json.Unmarshal([]byte(line), &r) != nil {
			continue
		}
		rows = append(rows, r)
	}
	return rows, nil
}

// Bucket 是一个分账维度上的聚合计数。
type Bucket struct {
	Calls            int
	Errors           int
	PromptTokens     int64
	CompletionTokens int64
}

func (b *Bucket) add(r UsageRow) {
	if r.Error != "" {
		b.Errors++
		return
	}
	b.Calls++
	b.PromptTokens += int64(r.PromptTokens)
	b.CompletionTokens += int64(r.CompletionTokens)
}

// Stats 是一个窗口内的派生指标集。
type Stats struct {
	Rows             int // 台账行总数（含错误行）
	Calls            int
	Errors           int
	PromptTokens     int64
	CompletionTokens int64

	ByWake  map[string]*Bucket // watchdog / scheduled / step / manual / ""（无章旧行）
	ByPhase map[string]*Bucket // wake / chat / task / ""（无章旧行）
	ByModel map[string]*Bucket

	WatchdogWakes int // watchdog 合成唤醒的思考轮
	IdleNoRun     int // 其中窗口内未跟随新 run 的（空转代理）

	FirstTS string
	LastTS  string
}

// IdleRate 返回空转唤醒率（0~1）；没有 watchdog 唤醒时返回 false
// ——"没醒过"和"醒过全在干活"是两种健康，不该共用一个 0。
func (s Stats) IdleRate() (float64, bool) {
	if s.WatchdogWakes == 0 {
		return 0, false
	}
	return float64(s.IdleNoRun) / float64(s.WatchdogWakes), true
}

// Derive 从台账行派生指标。idleWindow 是空转判定的跟随窗口：唤醒
// 调用之后这么长时间内出现任何新 run 章即视为"这轮唤醒干了活"。
func Derive(rows []UsageRow, idleWindow time.Duration) Stats {
	st := Stats{
		ByWake:  map[string]*Bucket{},
		ByPhase: map[string]*Bucket{},
		ByModel: map[string]*Bucket{},
	}
	// run 章 → 首次出现时刻：唤醒是否"干了活"的锚点。
	runFirst := map[string]time.Time{}
	var firstTS, lastTS time.Time
	for _, r := range rows {
		st.Rows++
		if r.Error != "" {
			st.Errors++
		} else {
			st.Calls++
			st.PromptTokens += int64(r.PromptTokens)
			st.CompletionTokens += int64(r.CompletionTokens)
		}
		bucketFor(st.ByWake, wakeBucket(r.Wake)).add(r)
		bucketFor(st.ByPhase, phaseBucket(r.Phase)).add(r)
		bucketFor(st.ByModel, r.Model).add(r)
		if r.Run != "" {
			if ts, ok := parseTS(r.TS); ok {
				if _, seen := runFirst[r.Run]; !seen {
					runFirst[r.Run] = ts
				}
			}
		}
		if ts, ok := parseTS(r.TS); ok {
			if firstTS.IsZero() || ts.Before(firstTS) {
				firstTS = ts
			}
			if ts.After(lastTS) {
				lastTS = ts
			}
		}
	}
	if !firstTS.IsZero() {
		st.FirstTS = firstTS.Format(traj.TimeFormat)
		st.LastTS = lastTS.Format(traj.TimeFormat)
	}

	for _, r := range rows {
		if r.Phase != "wake" || r.Wake != "watchdog" {
			continue
		}
		st.WatchdogWakes++
		idle := r.Run == ""
		if idle {
			if ts, ok := parseTS(r.TS); ok {
				for _, t0 := range runFirst {
					if !t0.Before(ts) && t0.Sub(ts) <= idleWindow {
						idle = false
						break
					}
				}
			}
		}
		if idle {
			st.IdleNoRun++
		}
	}
	return st
}

// bucketFor 取桶，没有就建——调用方只管加。
func bucketFor(m map[string]*Bucket, key string) *Bucket {
	if m[key] == nil {
		m[key] = &Bucket{}
	}
	return m[key]
}

// wakeBucket / phaseBucket 把无章旧行归到显式命名桶——"没有章"
// 是事实，不能默默并进别的桶。
func wakeBucket(w string) string {
	if w == "" {
		return "(无章)"
	}
	return w
}

func phaseBucket(p string) string {
	if p == "" {
		return "(无章)"
	}
	return p
}

func parseTS(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(traj.TimeFormat, s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}
