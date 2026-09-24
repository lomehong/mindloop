package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"mindloop/internal/identity"
)

// recapView 是 viewer Recap 契约——id/available/refreshing 必有，
// 后面三个字段在缓存可用时填充。
type recapView struct {
	Identity   recapIdentity       `json:"identity"`
	Available  bool                `json:"available"`
	Refreshing bool                `json:"refreshing"`
	NewSteps   *int                `json:"new_steps,omitempty"`
	Themes     *recapThemesBlock   `json:"themes,omitempty"`
	Episodes   *[]recapEpisodeView `json:"episodes,omitempty"`
}

type recapIdentity struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type recapThemesBlock struct {
	GeneratedAt string           `json:"generated_at"`
	Model       string           `json:"model"`
	Arc         string           `json:"arc"`
	Themes      []recapThemeView `json:"themes"`
}

type recapThemeView struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Episodes    []int          `json:"episodes"`
	KeySteps    []recapStepRef `json:"key_steps"`
}

type recapStepRef struct {
	Step string `json:"step"`
	Note string `json:"note"`
}

type recapEpisodeView struct {
	Title        string `json:"title"`
	Summary      string `json:"summary"`
	Themes       []int  `json:"themes"`
	NotableSteps []int  `json:"notable_steps"`
}

// recapEpisode 是 cache/episodes.jsonl 的 JSON 行。
type recapEpisode struct {
	Start    int    `json:"start"`
	End      int    `json:"end"`
	Title    string `json:"title"`
	Summary  string `json:"summary"`
	Model    string `json:"model"`
	StepFrom string `json:"step_from"`
	StepTo   string `json:"step_to"`
	// "themes" 数组 + "notable_steps" 数组（headlong 里有，简化版忽略）
}

// handleRecap 返回身份的人生分集——viewer Recap 页契约。缓存
// 文件 recap/episodes.jsonl 不存在时 available=false（这是常态）：
// 真实分集要等心智空闲时由 recap 命令异步生成。viewer 据此区分。
func (s *Server) handleRecap(w http.ResponseWriter, _ *http.Request, id *identity.Identity, _ []string) {
	view := recapView{
		Identity:   recapIdentity{ID: id.Name, Name: id.Name},
		Available:  false,
		Refreshing: false,
	}
	cachePath := filepath.Join(id.Dir, "recap", "episodes.jsonl")
	data, err := os.ReadFile(cachePath)
	if err != nil {
		if !os.IsNotExist(err) {
			writeError(w, 500, fmt.Errorf("recap 读取失败: %w", err).Error())
			return
		}
		writeJSON(w, 200, view)
		return
	}
	var themes recapThemesBlock
	themes.Model = "bundled-recap"
	themes.GeneratedAt = "imported"
	var episodes []recapEpisodeView
	for _, ln := range splitNonEmptyLines(string(data)) {
		var ep recapEpisode
		if err := json.Unmarshal([]byte(ln), &ep); err != nil {
			continue
		}
		// themes 与 notable_steps 暂未持久化（headlong 0.3.x 才有），
		// 留空数组让 viewer 显示"无主题分类"的标题与摘要即可。
		episodes = append(episodes, recapEpisodeView{
			Title:   ep.Title,
			Summary: ep.Summary,
		})
		themes.Themes = append(themes.Themes, recapThemeView{Name: ep.Title})
		themes.Themes[len(themes.Themes)-1].Episodes = []int{len(episodes) - 1}
	}
	if len(episodes) > 0 {
		view.Available = true
		view.Episodes = &episodes
		view.Themes = &themes
	}
	writeJSON(w, 200, view)
}

// handleLlmHealth 是 viewer Health 页的探测信号。v0.1 用 llm-health.json
// 透传即可——更深的健康指标（如 429 频率、按 provider 聚合）留给后续轮次。
type llmHealthView struct {
	Status      string              `json:"status"`
	Failures15m int                 `json:"failures_15m"`
	Failures1h  int                 `json:"failures_1h"`
	CadenceSlow bool                `json:"cadence_slow"`
	CheckedAt   string              `json:"checked_at"`
	Identities  []llmHealthIdentity `json:"identities"`
	LastCall    *llmHealthLastCall  `json:"last_call,omitempty"`
}

type llmHealthIdentity struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Live        bool              `json:"live"`
	Failures1h  int               `json:"failures_1h"`
	Failures15m int               `json:"failures_15m"`
	LastFailure *llmHealthFailure `json:"last_failure,omitempty"`
	Cadence     *llmHealthCadence `json:"cadence,omitempty"`
}

type llmHealthFailure struct {
	TS      string `json:"ts"`
	Content string `json:"content"`
}

type llmHealthCadence struct {
	RecentMedianS   int  `json:"recent_median_s"`
	BaselineMedianS *int `json:"baseline_median_s,omitempty"`
	RecentN         int  `json:"recent_n"`
}

type llmHealthLastCall struct {
	OK       bool    `json:"ok"`
	TS       *string `json:"ts,omitempty"`
	Provider *string `json:"provider,omitempty"`
	Model    *string `json:"model,omitempty"`
	Kind     *string `json:"kind,omitempty"`
	HTTPCode any     `json:"http_code"`
	Message  *string `json:"message,omitempty"`
}

// handleLlmHealth 读取每个身份目录下的 llm-health.json 聚合返回。
// 单一身份是 viewer 的正常使用场景——只回该身份。
func (s *Server) handleLlmHealth(w http.ResponseWriter, _ *http.Request, id *identity.Identity, _ []string) {
	out := llmHealthView{
		Status:      "ok",
		Failures15m: 0,
		Failures1h:  0,
		CadenceSlow: false,
		CheckedAt:   nowISO(),
	}
	hp := filepath.Join(id.Dir, "llm-health.json")
	data, err := os.ReadFile(hp)
	if err != nil {
		// 文件不存在（心智从未运行或无失败）= 一切健康
		if !os.IsNotExist(err) {
			writeError(w, 500, err.Error())
			return
		}
		// 健康身份=无历史故障
		ent := llmHealthIdentity{ID: id.Name, Name: id.Name, Live: isIdentityLive(id.Timeline.Dir)}
		out.Identities = append(out.Identities, ent)
		writeJSON(w, 200, out)
		return
	}
	var h struct {
		ConsecutiveErrors int    `json:"consecutive_errors"`
		LastError         string `json:"last_error,omitempty"`
		LastErrorAt       string `json:"last_error_at,omitempty"`
		LastOK            string `json:"last_ok,omitempty"`
		LastCheck         string `json:"last_check,omitempty"`
	}
	if err := json.Unmarshal(data, &h); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	ent := llmHealthIdentity{
		ID: id.Name, Name: id.Name, Live: isIdentityLive(id.Timeline.Dir),
	}
	if h.ConsecutiveErrors > 0 {
		ent.Failures1h = h.ConsecutiveErrors // 近似：本地缓存不切窗口
		ent.Failures15m = h.ConsecutiveErrors
		if h.LastErrorAt != "" {
			ent.LastFailure = &llmHealthFailure{TS: h.LastErrorAt, Content: h.LastError}
		}
		out.Failures15m = h.ConsecutiveErrors
		out.Failures1h = h.ConsecutiveErrors
	}
	if h.ConsecutiveErrors == 0 && h.LastOK != "" {
		// 探针成功=ok 状态
	}
	if h.ConsecutiveErrors > 2 {
		out.Status = "degraded"
		// last_call 不强解为 type，因为我们没有 marker；留空
	}
	out.Identities = append(out.Identities, ent)
	writeJSON(w, 200, out)
}

// splitNonEmptyLines 按 \n 分割并丢弃空行（trim 后）。
func splitNonEmptyLines(s string) []string {
	var out []string
	for _, ln := range splitLines(s) {
		if ln = trimSpace(ln); ln != "" {
			out = append(out, ln)
		}
	}
	return out
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func trimSpace(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t' || s[0] == '\r') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
