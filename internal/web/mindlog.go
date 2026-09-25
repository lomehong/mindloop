package web

import (
	"net/http"
	"net/url"
	"sort"
	"strings"

	"mindloop/internal/identity"
	"mindloop/internal/traj"
)

// mindlogNormalized 是 headlong viewer Mindlog 契约的 Go 形态。
// 字段名与 web/static/app/lib/types.ts 的 Mindlog 接口一一对齐。
type mindlogNormalized struct {
	TrajID    string           `json:"traj_id"`
	DirRel    string           `json:"dir_rel"`
	StepCount int              `json:"step_count"`
	Steps     []normalizedStep `json:"steps"`
	Runs      []runGroup       `json:"runs"`
	Live      bool             `json:"live"`
	Since     *int             `json:"since,omitempty"`
	Identity  identityRef      `json:"identity"`
}

type identityRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type normalizedStep struct {
	StepID  string         `json:"step_id"`
	TS      string         `json:"ts"`
	Type    string         `json:"type"`
	Source  *string        `json:"source"`
	Preview string         `json:"preview"`
	Raw     map[string]any `json:"raw"`
	RunID   *string        `json:"run_id"`
	Fork    *forkLink      `json:"fork,omitempty"`
	// writeback 对齐 headlong 的 WritebackLink
	Writeback *writebackLink `json:"writeback,omitempty"`
}

type forkLink struct {
	ChildTrajID string `json:"child_traj_id"`
	Slug        string `json:"slug"`
	Resolved    bool   `json:"resolved"`
}

type writebackLink struct {
	FromTraj string  `json:"from_traj"`
	FromStep *string `json:"from_step"`
}

type runGroup struct {
	RunID         string   `json:"run_id"`
	TriggerStepID *string  `json:"trigger_step_id"`
	LaunchedBy    *string  `json:"launched_by"`
	StepIDs       []string `json:"step_ids"`
	StartedTS     string   `json:"started_ts"`
	EndedTS       *string  `json:"ended_ts"`
	Status        string   `json:"status"` // "running" | "done"
	Command       string   `json:"command"`
	CommandTrunc  bool     `json:"command_truncated,omitempty"`
	Model         *string  `json:"model,omitempty"`
	Tldr          *string  `json:"tldr,omitempty"`
	LastTouch     int      `json:"last_touch"`
}

// stepSource 推断 viewer 的 source 语义：
//   - launched_by 显式优先（monolith/responder 等）
//   - message from=operator → "chat"
//   - 其他 → nil（viewer 显示 gear 图标 = 机械步骤）
func stepSource(s traj.Step) *string {
	if by, ok := s.Field("launched_by"); ok && by != "" {
		return &by
	}
	if s.Type == traj.TypeMessage {
		if from, ok := s.Field("from"); ok && from != "" {
			return &from
		}
	}
	return nil
}

// stepPreview 取 content 截断到 240 字符（viewer 的 preview 约定）。
func stepPreview(s traj.Step) string {
	c, _ := s.Field("content")
	c = strings.ReplaceAll(c, "\n", " ")
	r := []rune(c)
	if len(r) > 240 {
		return string(r[:240]) + "…"
	}
	return c
}

// normalizeStep 把轨迹步骤转为 viewer 的 NormalizedStep。
func normalizeStep(s traj.Step) normalizedStep {
	n := normalizedStep{
		StepID:  s.StepID,
		TS:      s.TS,
		Type:    s.Type,
		Source:  stepSource(s),
		Preview: stepPreview(s),
		Raw:     s.Fields,
	}
	if rid, ok := s.Field("run_id"); ok && rid != "" {
		rid := rid
		n.RunID = &rid
	}
	if s.Type == traj.TypeFork {
		child, _ := s.Field("child")
		ref, _ := s.Field("child_ref")
		slug := ref
		if idx := strings.LastIndex(slug, "-"); idx >= 0 {
			slug = slug[idx+1:]
		}
		n.Fork = &forkLink{ChildTrajID: child, Slug: slug, Resolved: true}
	}
	if s.Type == traj.TypeMerge {
		ft, _ := s.Field("from_traj")
		fs, hasFS := s.Field("from_step")
		wb := writebackLink{FromTraj: ft}
		if hasFS {
			wb.FromStep = &fs
		}
		n.Writeback = &wb
	}
	return n
}

// groupRuns 按 run_id 聚合 machinery 步骤为 RunGroup——viewer 的
// run-group 折叠视图数据源。边界：run_id 首次出现开组、消失即闭合
// （我们的写入是顺序的，不会交错 run）。
func groupRuns(steps []traj.Step, norm []normalizedStep) []runGroup {
	index := map[string]*runGroup{}
	var order []string
	for i, s := range steps {
		rid, ok := s.Field("run_id")
		if !ok || rid == "" || s.Type == traj.TypeTrajectory {
			continue
		}
		g, ok := index[rid]
		if !ok {
			g = &runGroup{RunID: rid, Status: "running", StepIDs: []string{}, StartedTS: s.TS}
			index[rid] = g
			order = append(order, rid)
		}
		g.StepIDs = append(g.StepIDs, s.StepID)
		g.LastTouch = i
		if s.Type == traj.TypePrompt {
			if c, ok := s.Field("content"); ok {
				g.Command = c
			}
		}
		// TLDR 从日志派生（"视图皆派生"）：final 步骤的正文就是
		// 模型自己写的"这一觉干了什么"——不需要 run-summary 那样
		// 为标签额外烧一次模型调用。
		if s.Type == traj.TypeFinal {
			if c, ok := s.Field("content"); ok && c != "" {
				one := traj.OneLine(c, 120)
				g.Tldr = &one
			}
		}
		if s.Type == traj.TypeFinal || s.Type == traj.TypeError {
			g.Status = "done"
			e := s.TS
			g.EndedTS = &e
		}
		if lb, ok := s.Field("launched_by"); ok && (g.LaunchedBy == nil || *g.LaunchedBy == "") {
			lb := lb
			g.LaunchedBy = &lb
		}
	}
	out := make([]runGroup, 0, len(order))
	sort.Slice(order, func(i, j int) bool {
		return index[order[i]].StartedTS < index[order[j]].StartedTS
	})
	for _, rid := range order {
		out = append(out, *index[rid])
	}
	_ = norm
	return out
}

// handleMindlog 返回 viewer Mindlog 契约：
//
//	GET /mindlog?tail=N            最近 N 步
//	GET /mindlog?since=I           从第 I 步到末尾（轮询增量）
//	GET /mindlog?since=I&until=J   历史窗口 [I,J)
func (s *Server) handleMindlog(w http.ResponseWriter, r *http.Request, id *identity.Identity, _ []string) {
	q := r.URL.Query()
	all, err := id.Timeline.Steps()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	total := len(all)

	start, end := windowBounds(q, total)
	window := all[start:end]

	norm := make([]normalizedStep, len(window))
	for i, s := range window {
		norm[i] = normalizeStep(s)
	}
	runs := groupRuns(window, norm)

	since := start
	var sinceP *int
	if start > 0 {
		sinceP = &since
	}
	out := mindlogNormalized{
		TrajID:    id.Timeline.ID,
		DirRel:    relTrajDir(s.cfg.Root, id.Timeline.Dir),
		StepCount: len(norm),
		Steps:     norm,
		Runs:      runs,
		Live:      isIdentityLive(id.Timeline.Dir),
		Since:     sinceP,
		Identity:  identityRef{ID: id.Name, Name: id.Name},
	}
	writeJSON(w, 200, out)
}

// windowBounds 解析 since/until/tail 三种窗口语义，返回 [start,end)。
// 优先级：since(+until) > tail > 全量。
func windowBounds(q url.Values, total int) (int, int) {
	sv, uv, tv := q.Get("since"), q.Get("until"), q.Get("tail")
	if sv != "" {
		s := atoiOr(sv, 0)
		if s < 0 {
			s = 0
		}
		e := total
		if uv != "" {
			if u := atoiOr(uv, total); u >= s && u <= total {
				e = u
			}
		}
		if s > total {
			s = total
		}
		return s, e
	}
	if tv != "" {
		n := atoiOr(tv, 0)
		if n <= 0 {
			return 0, total
		}
		s := total - n
		if s < 0 {
			s = 0
		}
		return s, total
	}
	return 0, total
}

func atoiOr(s string, def int) int {
	n := 0
	neg := false
	for i, c := range s {
		if c == '-' && i == 0 {
			neg = true
			continue
		}
		if c < '0' || c > '9' {
			return def
		}
		n = n*10 + int(c-'0')
	}
	if neg {
		return -n
	}
	return n
}

// relTrajDir 计算轨迹目录相对 root 的路径（viewer 的 dir_rel 字段，
// 用于拼接子轨迹请求）。
func relTrajDir(root, trajDir string) string {
	r := strings.TrimRight(root, "/\\")
	t := trajDir
	if strings.HasPrefix(strings.ToLower(t), strings.ToLower(r)) && len(t) > len(r) {
		return strings.ReplaceAll(t[len(r)+1:], "\\", "/")
	}
	return t
}

// handleMindlogSearch 实现搜索框：在 content/类型字段里做大小写
// 不敏感的子串匹配，返回 viewer SearchHit 列表。
func (s *Server) handleMindlogSearch(w http.ResponseWriter, r *http.Request, id *identity.Identity, _ []string) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	scope := r.URL.Query().Get("scope")
	out := mindlogSearchResult{Q: q, Scope: "all", Hits: []searchHit{}}
	if q == "" {
		writeJSON(w, 200, out)
		return
	}
	if scope == "thoughts" {
		out.Scope = "thoughts"
	}
	needle := strings.ToLower(q)
	steps, err := id.Timeline.Steps()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	for i, s := range steps {
		if out.Scope == "thoughts" && !mindLevelStep(s.Type) {
			continue
		}
		content, _ := s.Field("content")
		hay := strings.ToLower(content)
		if idx := strings.Index(hay, needle); idx >= 0 {
			snippet := snippetAround(content, idx, 160)
			src, _ := s.Field("launched_by")
			rid, _ := s.Field("run_id")
			out.Hits = append(out.Hits, searchHit{
				Index: i, StepID: s.StepID, TS: s.TS, Type: s.Type,
				Source: src, RunID: rid, Field: "content", Snippet: snippet,
			})
		}
	}
	out.Total = len(out.Hits)
	out.StepCount = len(steps)
	writeJSON(w, 200, out)
}

// mindLevelStep 是 "thoughts" 搜索范围的白名单：心智层面的步骤（想法、
// 消息、观察、行动），排除运行机械（prompt、模型推理、shell 输出等，
// 这些归 "everything" 范围）。与 viewer 搜索框的 scope 说明一致。
//
// tp-thought / human-msg / agent-msg / feedback 是 headlong 兼容别名
// ——mindloop 不产出这些类型，仅为读取旧日志保留；词表常量不覆盖
// 它们，它们也不得出现在任何 mindloop 的写入路径上。
func mindLevelStep(t string) bool {
	switch t {
	case traj.TypeThought, "tp-thought", traj.TypeMessage, "human-msg", "agent-msg",
		traj.TypeObservation, traj.TypeAction, "feedback", traj.TypeFinal:
		return true
	}
	return false
}

type searchHit struct {
	Index   int    `json:"index"`
	StepID  string `json:"step_id"`
	TS      string `json:"ts"`
	Type    string `json:"type"`
	Source  string `json:"source"`
	RunID   string `json:"run_id"`
	Field   string `json:"field"`
	Snippet string `json:"snippet"`
}

type mindlogSearchResult struct {
	Q         string      `json:"q"`
	Scope     string      `json:"scope"`
	Total     int         `json:"total"`
	Hits      []searchHit `json:"hits"`
	StepCount int         `json:"step_count"`
}

func snippetAround(s string, idx, width int) string {
	r := []rune(s)
	ir := len([]rune(s[:idx]))
	start := ir - width/2
	if start < 0 {
		start = 0
	}
	end := ir + width/2
	if end > len(r) {
		end = len(r)
	}
	out := string(r[start:end])
	out = strings.ReplaceAll(out, "\n", " ")
	if start > 0 {
		out = "…" + out
	}
	if end < len(r) {
		out += "…"
	}
	return out
}

// handleRunCommand 返回某次 run 的 prompt 全文（viewer 的
// command_truncated 展开按钮用）。
func (s *Server) handleRunCommand(w http.ResponseWriter, r *http.Request, id *identity.Identity, rest []string) {
	runID := rest[0]
	steps, err := id.Timeline.Steps()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	for _, s := range steps {
		if rid, ok := s.Field("run_id"); ok && rid == runID && s.Type == traj.TypePrompt {
			if c, ok := s.Field("content"); ok {
				writeJSON(w, 200, map[string]any{"run_id": runID, "command": c})
				return
			}
		}
	}
	writeError(w, 404, "未找到该 run 的 prompt")
}
