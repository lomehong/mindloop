// judge.go — 判定层：显著性判级与反射状态。判定签名带显式状态与
// 关联快照（perception.md §4.3 评审修正：不是无状态纯函数——判据
// 要事务关联，状态要去重冷却）：
//
//	judge(cfg, state, view, event, now) → Decision
//
// 判定契约：S 分档允许出错、错档本身是校准素材；判定以可解释优先
// ——每个 Decision 带 reason（规则名/关联来源），落进 event 步骤的
// reason 必填字段，Phase 4 味觉归因与"为什么没醒"的诊断全靠它。
package sensor

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// 判定窗口的缺省值（配置可逐感官覆盖，见 SensorConfig）。
const (
	DefaultDedupWindow  = 10 * time.Minute
	DefaultRuleCooldown = 30 * time.Minute
)

// Decision 是一次判定的结论。Duplicate 为 true 表示同指纹去重窗口
// 内的重复事件——调用方应整体丢弃（不落盘不叫醒），抑制计数折叠
// 进同源下一条事件。
type Decision struct {
	Salience  Salience
	Reason    string // 命中规则名 / agenda-match / default；可解释性字段
	Duplicate bool
}

// State 是一个感官的反射运行态。判定由装配层的单 goroutine 串行
// 调用，方法不加锁；Clone/Rebuild 供热加载与重启重建。
type State struct {
	// lastFired 记录去重指纹的最近触发时刻（去重 + 冷却共用一张表：
	// 冷却键是 "rule:<name>"，去重键是 "<kind>|<dedup>"）。
	lastFired map[string]time.Time
	// seenSubjects 是主体的首次出现时刻（首次出现语义 + 鼻的
	// "新主体"检测共用）。
	seenSubjects map[string]time.Time
	// window 是速率统计的滚动窗（ts, kind），判定与缺席检测共用；
	// 上限 512 条，裁剪即丢弃最旧。
	window []windowEntry
}

type windowEntry struct {
	ts   time.Time
	kind string
}

// NewState 构造空状态。
func NewState() *State {
	return &State{
		lastFired:    map[string]time.Time{},
		seenSubjects: map[string]time.Time{},
	}
}

// Clone 复制运行态（热加载按 sensor-id diff：配置变了重建感官，
// 状态跟着走）。
func (s *State) Clone() *State {
	c := NewState()
	for k, v := range s.lastFired {
		c.lastFired[k] = v
	}
	for k, v := range s.seenSubjects {
		c.seenSubjects[k] = v
	}
	c.window = append(c.window, s.window...)
	return c
}

// Rebuild 从轨迹事件重建运行态（启动时回读冷却窗口内的 event/alert
// 步骤——视图皆派生，与 schedule.Recover 同构：重启不重复唤醒）。
// olderThan 之前的记录直接忽略（去重/冷却窗口外的历史对判定无用）。
func (s *State) Rebuild(records []EventRecord, olderThan time.Time) {
	for _, r := range records {
		if r.TS.Before(olderThan) {
			continue
		}
		if r.Salience != string(S0) && r.Salience != "" {
			// 非沉淀级事件才算"触发过"：S0 只沉淀，不占去重窗。
			key := dedupKey(r.Kind, r.Dedup, r.Subject)
			if t, ok := s.lastFired[key]; !ok || r.TS.After(t) {
				s.lastFired[key] = r.TS
			}
			if name, ok := ruleNameOf(r.Reason); ok {
				ck := "rule:" + name
				if t, ok := s.lastFired[ck]; !ok || r.TS.After(t) {
					s.lastFired[ck] = r.TS
				}
			}
		}
		if _, ok := s.seenSubjects[r.Subject]; !ok {
			s.seenSubjects[r.Subject] = r.TS
		}
		s.window = append(s.window, windowEntry{ts: r.TS, kind: r.Kind})
	}
	s.prune(olderThan)
}

// ruleNameOf 从判定理由反解规则名（"rule:<名>" 形态）；取不到就
// 放弃冷却重建——漏一条冷却只是多叫醒一次，不是正确性问题。
func ruleNameOf(reason string) (string, bool) {
	const p = "rule:"
	if !strings.HasPrefix(reason, p) {
		return "", false
	}
	name := strings.TrimPrefix(reason, p)
	if i := strings.Index(name, " "); i >= 0 {
		name = name[:i] // 剥掉 " (冷却中)" 等后缀
	}
	return name, true
}

func (s *State) prune(cutoff time.Time) {
	w := s.window[:0]
	for _, e := range s.window {
		if e.ts.After(cutoff) {
			w = append(w, e)
		}
	}
	s.window = w
	if len(s.window) > 512 {
		s.window = s.window[len(s.window)-512:]
	}
}

// Rate 报告窗口内某 kind 的事件数（缺席检测与速率升档共用）。
func (s *State) Rate(since time.Time, kind string) int {
	n := 0
	for _, e := range s.window {
		if !e.ts.Before(since) && (kind == "" || e.kind == kind) {
			n++
		}
	}
	return n
}

// FirstSeen 返回主体的首次出现时刻；没有则记录 now 并返回
// (now, true)——"新主体"语义。
func (s *State) FirstSeen(subject string, now time.Time) (time.Time, bool) {
	if t, ok := s.seenSubjects[subject]; ok {
		return t, false
	}
	s.seenSubjects[subject] = now
	return now, true
}

func dedupKey(kind, dedup, subject string) string {
	if dedup == "" {
		dedup = subject
	}
	return kind + "|" + dedup
}

// Judge 是判定入口。state 原地更新（调用方串行调用）。
func Judge(cfg *SensorConfig, st *State, view ReflexView, e PEvent, now time.Time) Decision {
	kind := e.Kind
	if kind == "" {
		kind = KindChanged
	}

	// 1. 去重：同指纹窗口内 = 重复（不更新窗口——重复不占速率账）。
	dedupWin := DefaultDedupWindow
	if d, err := time.ParseDuration(cfg.DedupWindow); err == nil && d > 0 {
		dedupWin = d
	}
	key := dedupKey(kind, e.Dedup, e.Subject)
	if t, ok := st.lastFired[key]; ok && now.Sub(t) < dedupWin {
		return Decision{Duplicate: true}
	}
	st.lastFired[key] = now
	st.window = append(st.window, windowEntry{ts: now, kind: kind})
	st.prune(now.Add(-30 * time.Minute))

	sal := salienceOf(cfg)
	reason := "default"
	ruleNote := "" // 冷却中的规则名（进 reason，不掩盖）

	// 2. 规则表：按声明序，第一条命中生效；冷却期内同规则对同主体
	// 不再升档——冷却键含 subject：同一网页变五次（同主体变体）被
	// 压住，观察目录里不同的命中文件（不同主体）各自升档。
	cooldown := DefaultRuleCooldown
	if d, err := time.ParseDuration(cfg.RuleCooldown); err == nil && d > 0 {
		cooldown = d
	}
	hay := strings.ToLower(e.Subject + "\n" + e.Digest)
	for _, r := range cfg.Salience.Rules {
		if !containsAny(hay, r.Keywords) {
			continue
		}
		if r.Min > 0 && !digestHasNumberGE(e.Digest, r.Min) {
			continue // 阈值越界未达：词面命中但数值不够
		}
		ck := "rule:" + r.Name + "|" + e.Subject
		if t, ok := st.lastFired[ck]; ok && now.Sub(t) < cooldown {
			ruleNote = "rule:" + r.Name + " 冷却中"
			break
		}
		st.lastFired[ck] = now
		sal = r.Salience
		reason = "rule:" + r.Name
		break
	}

	// 3. 议程关联（ReflexView）：主体命中事务路径前缀，或关键词
	// 命中——保底升到 S1（不覆盖规则已给的更高档）。
	if sal < S1 && (matchesPath(e.Subject, view.Paths) || containsAny(hay, view.Keywords)) {
		sal = S1
		reason = "agenda-match"
	}
	// 冷却信息不掩盖：议程升档时把"哪条规则在冷却"留在 reason 里
	//（校准与"为什么这次没升档"的诊断都靠 reason）。
	if ruleNote != "" && reason != ruleNote && !strings.Contains(reason, "rule:") {
		reason += "(" + ruleNote + ")"
	}

	// 4. 感官自带档位建议（内感受的严重性是信号的一部分，不归
	// 词面规则管）：判定低于建议时取建议。
	if e.Hint != "" && sal < e.Hint {
		sal = e.Hint
		reason = "hint:" + string(e.Hint)
	}

	// 5. 鼻三件套（perception.md §5 Phase 3 补齐，便宜统计）：
	// 首次出现——kind=appeared 的新主体保底 s1（"新东西出现"值得
	// 知道；changed 类首见只做状态登记不抬档）。
	_, isNew := st.FirstSeen(e.Subject, now)
	if isNew && e.Kind == KindAppeared && sal < S1 {
		sal = S1
		reason += "+first-seen"
	}
	// 速率突增——同 kind 最近 5 分钟 ≥5 条且 ≥ 此前 25 分钟的 3 倍
	// → 保底 s2（窗口含当前事件；零星事件永不触发）。
	recent := st.Rate(now.Add(-5*time.Minute), kind)
	older := st.Rate(now.Add(-30*time.Minute), kind) - recent
	if recent >= 5 && recent >= 3*older {
		if sal < S2 {
			sal = S2
		}
		reason += "+spike"
	}

	return Decision{Salience: sal, Reason: reason}
}

func salienceOf(cfg *SensorConfig) Salience {
	if cfg.Salience.Default != "" {
		return cfg.Salience.Default
	}
	return DefaultSalience(cfg.Type)
}

// digestHasNumberGE 报告 digest 中是否存在 ≥ min 的数值（阈值越界
// 规则的原料）。
func digestHasNumberGE(digest string, min float64) bool {
	for _, m := range numRe.FindAllString(digest, 32) {
		if v, err := strconv.ParseFloat(m, 64); err == nil && v >= min {
			return true
		}
	}
	return false
}

// numRe 提取十进制数。
var numRe = regexp.MustCompile(`[0-9]+(?:\.[0-9]+)?`)

func containsAny(hay string, needles []string) bool {
	for _, n := range needles {
		if n = strings.TrimSpace(strings.ToLower(n)); n != "" && strings.Contains(hay, n) {
			return true
		}
	}
	return false
}

// matchesPath 报告 subject 是否落在任一事务路径前缀之下（分隔符
// 归一到 /，Windows 路径直接可比）。
func matchesPath(subject string, paths []string) bool {
	s := strings.ReplaceAll(strings.ToLower(subject), "\\", "/")
	for _, p := range paths {
		p = strings.ReplaceAll(strings.ToLower(strings.TrimSpace(p)), "\\", "/")
		if p == "" {
			continue
		}
		p = strings.TrimSuffix(p, "/")
		if s == p || strings.HasPrefix(s, p+"/") {
			return true
		}
	}
	return false
}
