package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"

	"mindloop/internal/identity"
	"mindloop/internal/traj"
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

// handleLlmHealth 返回 IdentityHealth 契约：
// {identity, activity: IdentityActivity, responses: ResponseStats}。
// responses 大部分从对话步骤实算：replied 有 reply_to 章并可配对
// 计算响应延迟（入站消息 ts → 回复 ts），recent 为最近对话事件；
// model/p90/paths 是 LLM 指标——用量账本未落盘前为诚实的 0/null。
func (s *Server) handleLlmHealth(w http.ResponseWriter, _ *http.Request, id *identity.Identity, _ []string) {
	replied, undecided := 0, 0
	var recent []map[string]any
	var latencies []float64
	if steps, err := id.Timeline.Steps(); err == nil {
		// 回复索引：reply_to → 回复 ts
		replyAt := map[string]string{}
		for _, st := range steps {
			if st.Type == "message" {
				if rt, ok := st.Field("reply_to"); ok && rt != "" {
					replyAt[rt] = st.TS
				}
			}
		}
		for _, st := range steps {
			if st.Type != "message" {
				continue
			}
			from, _ := st.Field("from")
			if from == id.Name {
				continue
			}
			outcome := "declined"
			var respS float64
			if rts, ok := replyAt[st.StepID]; ok && rts != "" && st.TS != "" {
				outcome = "replied"
				replied++
				if t0, e0 := time.Parse(traj.TimeFormat, st.TS); e0 == nil {
					if t1, e1 := time.Parse(traj.TimeFormat, rts); e1 == nil {
						respS = t1.Sub(t0).Seconds()
						latencies = append(latencies, respS)
					}
				}
			} else {
				undecided++
			}
			recent = append(recent, map[string]any{
				"ts": st.TS, "from": from, "outcome": outcome,
				"path": nil, "response_s": respS,
			})
		}
	}
	if recent == nil {
		recent = []map[string]any{}
	}
	if len(recent) > 20 {
		recent = recent[len(recent)-20:]
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	var median, p90 *float64
	if n := len(latencies); n > 0 {
		m := latencies[n/2]
		median = &m
		p := latencies[(n*9)/10]
		p90 = &p
	}
	pathStats := map[string]any{"n": 0, "median_s": nil, "p90_s": nil}
	writeJSON(w, 200, map[string]any{
		"identity": map[string]string{"id": id.Name, "name": id.Name},
		"activity": activityMap(id),
		"responses": map[string]any{
			"window_days": 7,
			"replied":     replied,
			"declined":    0,
			"undecided":   undecided,
			"duplicates":  0,
			"median_s":    median,
			"p90_s":       p90,
			"max_s":       nil,
			"paths":       map[string]any{"fast": pathStats, "inline": pathStats},
			"injections":  []any{},
			"model": map[string]any{
				"calls": 0, "llm_p50_s": nil, "llm_p90_s": nil,
				"in_tok": 0, "out_tok": 0, "think_tok": 0,
				"daily": []any{},
			},
			"recent": recent,
		},
	})
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
