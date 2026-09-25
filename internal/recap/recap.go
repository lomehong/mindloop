// Package recap 是情节摘要器——Headlong recap 的对应物，解决
// 持久心智的根本矛盾：轨迹无限增长，而上下文窗口有限。策略是把
// 日志按确定性规则切成"情节窗口"，每个窗口用小模型摘要一次并
// 缓存；monolith 的上下文因此变成"旧事看摘要、近事看原文"的
// 分层结构。
//
// 增量与确定性是设计的两根支柱：
//   - 窗口边界只取决于已落盘的步骤（时间间隔/步数/字节三个闭窗
//     条件），追加新步骤只会影响最后一个未闭合窗口——摘要缓存
//     因此可以增量补齐，已摘要的历史永不重算。
//   - 每条摘要盖 provenance 章（model + prompt_version），摘要器
//     升级后新旧混层可检测、可重建。
package recap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mindloop/internal/ids"
	"mindloop/internal/llm"
	"mindloop/internal/traj"
)

// PromptVersion 随摘要提示词的语义变化递增；旧版本缓存将失效
// 重算，而不是被静默混用。
const PromptVersion = 1

// narrativeTypes 参与叙事的步骤类型——执行元数据（run/prompt/
// reasoning/shell-output 的机械部分）不进情节。
var narrativeTypes = map[string]bool{
	traj.TypeMessage: true, traj.TypeAction: true, traj.TypeObservation: true,
	traj.TypeThought: true, traj.TypeFinal: true, traj.TypeMerge: true,
}

// 闭窗参数默认值（Headlong 的 recap.md 参数）。
const (
	DefaultGapClose = 30 * time.Minute
	DefaultMaxSteps = 100
	DefaultMaxBytes = 60 << 10
	// MinStepsForGap：不足这个步数的紧凑段落不按时间切——否则
	// 一段连续工作会被午休切成两半（Headlong 的章节语义）。
	MinStepsForGap = 10
	// LineCap 是单步渲染进摘要提示的字符上限。
	LineCap = 500
)

// NStep 是过滤后的叙事步骤。
type NStep struct {
	Index  int // 叙事序列内的下标（窗口边界引用它）
	StepID string
	TS     time.Time
	Text   string
	Bytes  int
}

// Filter 抽出叙事步骤并渲染为摘要输入行。
func Filter(steps []traj.Step) []NStep {
	var out []NStep
	for _, s := range steps {
		if !narrativeTypes[s.Type] {
			continue
		}
		ts, err := time.Parse(traj.TimeFormat, s.TS)
		if err != nil {
			ts = time.Time{}
		}
		content, _ := s.Field("content")
		from, hasFrom := s.Field("from")
		if hasFrom {
			content = from + ": " + content
		}
		content = strings.ReplaceAll(content, "\n", " ")
		r := []rune(content)
		if len(r) > LineCap {
			content = string(r[:LineCap]) + "…"
		}
		line := fmt.Sprintf("[%s] %s: %s", ids.Short(s.StepID, 8), s.Type, content)
		out = append(out, NStep{
			Index:  len(out),
			StepID: s.StepID,
			TS:     ts,
			Text:   line,
			Bytes:  len(line),
		})
	}
	return out
}

// Window 是 [Start, End) 区间的叙事步骤窗口。Trailing 标记最后
// 一个尚未闭合的窗口——它还会继续增长，现在摘要它是浪费。
type Window struct {
	Start, End int
	Trailing   bool
}

// Windows 按三条件确定性切窗：时间间隔（且窗口已有足够步数）、
// 步数上限、字节上限。边界只依赖窗口内与之前的步骤——追加步骤
// 不改变任何已闭合窗口，这是缓存增量的正确性来源。
func Windows(steps []NStep, gapClose time.Duration, maxSteps int, maxBytes int) []Window {
	if len(steps) == 0 {
		return nil
	}
	var out []Window
	start := 0
	stepsInWin, bytesInWin := 0, 0
	for i, s := range steps {
		stepsInWin++
		bytesInWin += s.Bytes
		if i == len(steps)-1 {
			break
		}
		gap := time.Duration(0)
		if !s.TS.IsZero() && !steps[i+1].TS.IsZero() {
			gap = steps[i+1].TS.Sub(s.TS)
		}
		closeByGap := stepsInWin >= MinStepsForGap && gap >= gapClose
		closeByCount := stepsInWin >= maxSteps
		closeByBytes := bytesInWin >= maxBytes
		if closeByGap || closeByCount || closeByBytes {
			out = append(out, Window{Start: start, End: i + 1})
			start = i + 1
			stepsInWin, bytesInWin = 0, 0
		}
	}
	last := Window{Start: start, End: len(steps), Trailing: true}
	// 计数/字节已达上限的尾窗不可能再增长（下一步只会开新窗），
	// 它事实上已闭合——gap 条件是唯一需要未来步骤才能判定的。
	if stepsInWin >= maxSteps || bytesInWin >= maxBytes {
		last.Trailing = false
	}
	out = append(out, last)
	return out
}

// Episode 是一条缓存的情节摘要。
type Episode struct {
	Start         int    `json:"start"`
	End           int    `json:"end"`
	Title         string `json:"title"`
	Summary       string `json:"summary"`
	Model         string `json:"model"`
	PromptVersion int    `json:"prompt_version"`
	Created       string `json:"created"`
	StepFrom      string `json:"step_from"`
	StepTo        string `json:"step_to"`
}

// Cache 是摘要缓存文件（追加式 JSONL，带目录锁）。
type Cache struct{ Path string }

// Load 读取全部缓存摘要，按窗口起点升序。
func (c Cache) Load() ([]Episode, error) {
	data, err := os.ReadFile(c.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Episode
	for _, ln := range strings.Split(string(data), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		var e Episode
		if json.Unmarshal([]byte(ln), &e) == nil && e.End > e.Start {
			out = append(out, e)
		}
	}
	return out, nil
}

// Append 追加一条摘要（目录锁保护——与轨迹写入同一套纪律）。
func (c Cache) Append(ctx context.Context, e Episode) error {
	if err := os.MkdirAll(filepath.Dir(c.Path), 0o755); err != nil {
		return err
	}
	release, err := traj.AcquireDirLock(ctx, c.Path+".lock", 5*time.Second)
	if err != nil {
		return err
	}
	defer release()
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(c.Path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}

// Updater 负责补齐缺失的摘要。
type Updater struct {
	Timeline *traj.Timeline
	Thinker  interface {
		Think(ctx context.Context, system string, msgs []llm.Message) (string, error)
	}
	GapClose     time.Duration
	MaxSteps     int
	MaxBytes     int
	MaxSummaries int // 单次 Update 最多补几条（成本阀；0 = 不限）
	Flush        bool
	// Flush 为 true 时把尾窗也摘要——人显式触发（CLI recap）而
	// 非唤醒路径的默认行为：尾窗还在增长，摘要它通常浪费。
	Model        string
	SystemPrompt string
}

// Report 汇报一次 Update。
type Report struct {
	Windows     int // 已闭合窗口数
	Cached      int // 缓存命中
	Summarized  int // 本次实际调用模型
	SkippedCost int // 因成本阀未处理的窗口数
}

// SummarySystemPrompt 是摘要调用的系统提示。
const SummarySystemPrompt = `You summarize a slice of an agent's life log for its future self. Write in the same language as the log. Output STRICT JSON: {"title":"≤20字标题","summary":"≤120字，第三人称，记录做了什么、结论是什么（outcome, not temperament）"}. No markdown fences.`

// Update 补齐缺失摘要并返回报告。
func (u *Updater) Update(ctx context.Context) (Report, error) {
	if u.Timeline == nil || u.Thinker == nil {
		return Report{}, errors.New("recap: 缺少 Timeline 或 Thinker")
	}
	gap, maxSteps, maxBytes := u.GapClose, u.MaxSteps, u.MaxBytes
	if gap <= 0 {
		gap = DefaultGapClose
	}
	if maxSteps <= 0 {
		maxSteps = DefaultMaxSteps
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	cache := Cache{Path: u.cachePath()}
	cached, err := cache.Load()
	if err != nil {
		return Report{}, err
	}
	covered := make(map[[2]int]bool)
	for _, e := range cached {
		if e.PromptVersion == PromptVersion {
			covered[[2]int{e.Start, e.End}] = true
		}
	}

	raw, err := timelineSteps(u.Timeline)
	if err != nil {
		return Report{}, fmt.Errorf("recap: %w", err)
	}
	steps := Filter(raw)
	wins := Windows(steps, gap, maxSteps, maxBytes)

	var rep Report
	for _, w := range wins {
		if w.Trailing && !u.Flush {
			continue
		}
		if w.End-w.Start == 0 {
			continue
		}
		rep.Windows++
		key := [2]int{w.Start, w.End}
		if covered[key] {
			rep.Cached++
			continue
		}
		if u.MaxSummaries > 0 && rep.Summarized >= u.MaxSummaries {
			rep.SkippedCost++
			continue
		}
		ep, err := u.summarize(ctx, steps[w.Start:w.End], w)
		if err != nil {
			return rep, err
		}
		if err := cache.Append(ctx, ep); err != nil {
			return rep, err
		}
		rep.Summarized++
	}
	return rep, nil
}

func (u *Updater) cachePath() string {
	return filepath.Join(u.Timeline.Dir, "recap", "episodes.jsonl")
}

func (u *Updater) summarize(ctx context.Context, steps []NStep, w Window) (Episode, error) {
	system := u.SystemPrompt
	if system == "" {
		system = SummarySystemPrompt
	}
	var body strings.Builder
	body.WriteString(fmt.Sprintf("以下是一个 agent 生命日志的第 %d–%d 步：\n\n", w.Start, w.End))
	for _, s := range steps {
		body.WriteString(s.Text)
		body.WriteString("\n")
	}
	text, err := u.Thinker.Think(ctx, system, []llm.Message{{Role: "user", Content: body.String()}})
	if err != nil {
		return Episode{}, fmt.Errorf("recap: 摘要调用: %w", err)
	}
	title, summary := parseSummary(text)
	ep := Episode{
		Start: w.Start, End: w.End,
		Title: title, Summary: summary,
		Model: u.Model, PromptVersion: PromptVersion,
		Created:  traj.NowString(),
		StepFrom: steps[0].StepID, StepTo: steps[len(steps)-1].StepID,
	}
	return ep, nil
}

// parseSummary 宽容解析：优先 JSON，失败则首行当标题、其余当摘要
// ——摘要器坏了不该拖垮记录，降级产物仍可用。
func parseSummary(text string) (title, summary string) {
	trimmed := strings.TrimSpace(text)
	trimmed = strings.TrimPrefix(trimmed, "```json")
	trimmed = strings.TrimPrefix(trimmed, "```")
	trimmed = strings.TrimSuffix(trimmed, "```")
	var parsed struct{ Title, Summary string }
	if err := json.Unmarshal([]byte(trimmed), &parsed); err == nil {
		return strings.TrimSpace(parsed.Title), strings.TrimSpace(parsed.Summary)
	}
	lines := strings.Split(strings.TrimSpace(text), "\n")
	title = strings.TrimSpace(lines[0])
	summary = strings.TrimSpace(strings.Join(lines[1:], " "))
	if summary == "" {
		summary = title
	}
	return title, summary
}

// RenderLife 渲染"人生至今"：分集摘要旧→新。只读缓存，零模型
// 调用——它将作为 monolith 上下文的粗层，细层由 prompt.Render 的
// 原文尾窗承担。
func RenderLife(timelineDir string, maxEpisodes int) (string, error) {
	cached, err := (Cache{Path: filepath.Join(timelineDir, "recap", "episodes.jsonl")}).Load()
	if err != nil {
		return "", err
	}
	if len(cached) == 0 {
		return "", nil
	}
	if maxEpisodes > 0 && len(cached) > maxEpisodes {
		cached = cached[len(cached)-maxEpisodes:]
	}
	var b strings.Builder
	b.WriteString("=== 人生分集摘要（旧→新）===\n")
	for i, e := range cached {
		fmt.Fprintf(&b, "[第%d幕 %s] %s\n", i+1, e.Title, e.Summary)
	}
	return b.String(), nil
}

// timelineSteps 读取轨迹步骤。读取失败必须显式传播——吞掉它会让
// Update 静默报告"没有可摘要的窗口"，把 IO 故障伪装成空闲。
func timelineSteps(tl *traj.Timeline) ([]traj.Step, error) {
	return tl.Steps()
}
