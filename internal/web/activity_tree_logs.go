package web

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"mindloop/internal/identity"
	"mindloop/internal/traj"
)

// handleActivity 返回主页面的 IdentityActivity 契约：心智是否运行、
// 哪些 thinker 忙、最近一次活动的延迟、消息队列长度。
//
// live 状态从心智运行锁存在性近似（dispatcher.pid 文件）。其他
// 字段先取心智的实际信息，再补 0 值——viewer 缺字段只是显示空。
// activityMap 组装 IdentityActivity 数据（/activity 与 /health 共用）。
func activityMap(id *identity.Identity) map[string]any {
	live := isIdentityLive(id.Timeline.Dir)
	state := "idle"
	if live {
		state = "working"
	}
	out := map[string]any{
		"state":              state,
		"dispatcher_running": live,
		"steps_in_flight":    0,
		"busy_thinkers":      []string{},
		"last_step_ts":       nil,
		"last_step_age_s":    nil,
		"run_seconds":        nil,
		"stall_after_s":      300,
		"cadence_s":          nil,
		"queued_messages":    []map[string]any{},
		"pending_total":      0,
	}
	if fi, err := os.Stat(id.Timeline.Path); err == nil {
		out["last_step_ts"] = fi.ModTime().UTC().Format(traj.TimeFormat)
		out["last_step_age_s"] = int(time.Since(fi.ModTime()).Seconds())
	}
	if steps, err := id.Timeline.Steps(); err == nil {
		for i := len(steps) - 1; i >= 0; i-- {
			if by, ok := steps[i].Field("launched_by"); ok && by != "" {
				out["busy_thinkers"] = []string{by}
				break
			}
		}
	}
	return out
}

// handleActivity 返回主页面的 IdentityActivity 契约。
func (s *Server) handleActivity(w http.ResponseWriter, _ *http.Request, id *identity.Identity, _ []string) {
	writeJSON(w, 200, activityMap(id))
}

// handleTree 返回 fork-tree 侧栏——headlong viewer 用它画子轨迹的
// 树状图。我们的 MindlogJSONL 末尾的"fork"步骤正是 tree 边。
//
// Query: ?depth=N（默认 1，最浅层）
func (s *Server) handleTree(w http.ResponseWriter, r *http.Request, id *identity.Identity, _ []string) {
	depth := 1
	if d := r.URL.Query().Get("depth"); d != "" {
		if n, err := strconv.Atoi(d); err == nil && n > 0 {
			depth = n
		}
	}
	root := treeNode{
		TrajID:     id.Timeline.ID,
		Slug:       id.Name,
		StartedTs:  identityCreateTime(id),
		LastTs:     identityLastTime(id),
		StepCount:  stepCount(id),
		HasFinal:   hasFinalStep(id),
		ChildCount: 0,
	}
	// 一层深度：列出直接子轨迹。深度 0 = 仅当前。
	if depth >= 1 {
		kids := childTrajectories(id)
		root.ChildCount = len(kids)
		if len(kids) > 0 {
			cs := make([]treeNode, 0, len(kids))
			for _, k := range kids {
				cs = append(cs, treeNode{
					TrajID: k.id, Slug: k.slug,
					StartedTs: k.started, LastTs: k.last,
					StepCount: k.steps, HasFinal: k.hasFinal,
					ChildCount: 0,
				})
			}
			root.Children = &cs
		}
	}
	writeJSON(w, 200, root)
}

type treeNode struct {
	TrajID       string      `json:"traj_id"`
	Slug         string      `json:"slug"`
	ParentStepID *string     `json:"parent_step_id"`
	StartedTs    string      `json:"started_ts"`
	LastTs       string      `json:"last_ts"`
	StepCount    int         `json:"step_count"`
	HasFinal     bool        `json:"has_final"`
	Tldr         *string     `json:"tldr,omitempty"`
	ChildCount   int         `json:"child_count"`
	Children     *[]treeNode `json:"children,omitempty"`
}

type childTrajInfo struct {
	id, started, last string
	slug              string
	steps             int
	hasFinal          bool
}

// childTrajectories 列出 id 的直接子轨迹——通过读根轨迹里
// fork 步骤的 child_ref 字段获得相对路径（headlong 同款机制）。
func childTrajectories(id *identity.Identity) []childTrajInfo {
	steps, err := id.Timeline.Steps()
	if err != nil {
		return nil
	}
	tlDir := filepath.Dir(id.Timeline.Path)
	seen := map[string]bool{}
	var out []childTrajInfo
	for _, s := range steps {
		if s.Type != "fork" {
			continue
		}
		ref, _ := s.Field("child_ref")
		if ref == "" {
			continue
		}
		// child_ref 是相对 tlDir 的路径；解绝对路径。
		childPath := filepath.Join(tlDir, ref)
		// childPath/trajectory.jsonl
		childDir := filepath.Dir(childPath)
		if seen[childDir] {
			continue
		}
		seen[childDir] = true
		ts, _ := os.Stat(childPath)
		var started, last string
		if ts != nil {
			started = ts.ModTime().UTC().Format("2006-01-02T15:04:05Z")
		}
		ci := readTrajectoryMeta(childDir)
		if ci.last != "" {
			last = ci.last
		}
		if last == "" && ts != nil {
			last = started
		}
		out = append(out, childTrajInfo{
			id: filepath.Base(childDir), slug: filepath.Base(childDir),
			started: started, last: last,
			steps: ci.steps, hasFinal: ci.hasFinal,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].started < out[j].started })
	return out
}

type trajMeta struct {
	steps    int
	last     string
	hasFinal bool
}

func readTrajectoryMeta(dir string) trajMeta {
	data, err := os.ReadFile(filepath.Join(dir, "trajectory.jsonl"))
	if err != nil {
		return trajMeta{}
	}
	m := trajMeta{}
	for _, ln := range splitNonEmptyLines(string(data)) {
		var s struct {
			Type string `json:"type"`
			TS   string `json:"ts"`
		}
		if err := json.Unmarshal([]byte(ln), &s); err != nil {
			continue
		}
		if s.TS != "" {
			m.last = s.TS
		}
		m.steps++
		if s.Type == "final" {
			m.hasFinal = true
		}
	}
	return m
}

// handleThinkers 返回 ThinkersStatus 契约：dispatcher 状态、所有
// thinker 列表、busy/disabled 统计。
func (s *Server) handleThinkers(w http.ResponseWriter, _ *http.Request, id *identity.Identity, _ []string) {
	steps, _ := id.Timeline.Steps()
	live := isIdentityLive(id.Timeline.Dir)
	// thinkers 列表：从轨迹的 launched_by 提炼，状态据最近活跃时间推断。
	seen := map[string]*thinkerLiveInfo{}
	for _, s := range steps {
		by, ok := s.Field("launched_by")
		if !ok || by == "" {
			continue
		}
		t, exists := seen[by]
		if !exists {
			t = &thinkerLiveInfo{Name: by}
			seen[by] = t
		}
		t.WakeCount++
		if s.TS != "" && s.TS > t.LastTS {
			t.LastTS = s.TS
		}
	}
	var thinkInfos []map[string]any
	now := time.Now().UTC()
	total := len(seen)
	disabled := 0
	for _, t := range seen {
		state := "idle"
		if live && t.LastTS != "" {
			last, _ := time.Parse("2006-01-02T15:04:05Z07:00", t.LastTS)
			if last.IsZero() {
				last, _ = time.Parse(traj.TimeFormat, t.LastTS)
			}
			if !last.IsZero() && now.Sub(last) < 5*time.Minute {
				state = "active"
			}
		}
		thinkInfos = append(thinkInfos, map[string]any{
			"name":            t.Name,
			"state":           state,
			"steps_in_flight": 0,
			"pid":             nil,
			"types":           []string{},
			"trigger_self":    false,
			"pending":         []string{},
			"log_bytes":       nil,
			"log_mtime":       t.LastTS,
		})
	}
	active := 0
	for _, ti := range thinkInfos {
		if ti["state"] == "active" {
			active++
		}
	}
	writeJSON(w, 200, map[string]any{
		"identity":          map[string]string{"id": id.Name, "name": id.Name},
		"dispatcher":        map[string]any{"running": live, "pid": nil},
		"active_thinkers":   active,
		"thinkers_total":    total,
		"thinkers_disabled": disabled,
		"steps_in_flight":   0,
		"pending_total":     0,
		"thinkers":          thinkInfos,
	})
}

type thinkerLiveInfo struct {
	Name      string
	LastTS    string
	WakeCount int
}

// handleLogs 返回日志文件列表——viewer Thinkers 页用。
// 契约：LogInfo[]（裸数组），mtime 为 epoch 毫秒，bytes 为字节数。
func (s *Server) handleLogs(w http.ResponseWriter, _ *http.Request, id *identity.Identity, _ []string) {
	logs := []map[string]any{}
	rootDir := filepath.Join(id.Timeline.Dir, "runs")
	entries, err := os.ReadDir(rootDir)
	if err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			fi, _ := e.Info()
			var mtimeMs int64
			var size int64
			if fi != nil {
				mtimeMs = fi.ModTime().UnixMilli()
				size = fi.Size()
			}
			logs = append(logs, map[string]any{
				"name":  e.Name(),
				"bytes": size,
				"mtime": mtimeMs,
			})
		}
	}
	sort.Slice(logs, func(i, j int) bool {
		return logs[i]["name"].(string) < logs[j]["name"].(string)
	})
	writeJSON(w, 200, logs)
}

// handleLogTail 返回某一日志文件的尾部字节——viewer 的 Logs 子页用。
// 真实数据源：磁盘上 <identity>/runs/<name>.log。
// Query: ?tail_bytes=N（默认 4096）
func (s *Server) handleLogTail(w http.ResponseWriter, r *http.Request, id *identity.Identity, rest []string) {
	if len(rest) == 0 || rest[0] == "" {
		writeError(w, 400, "缺少日志名")
		return
	}
	if strings.Contains(rest[0], "..") || strings.Contains(rest[0], "/") || strings.Contains(rest[0], "\\") {
		writeError(w, 400, "非法日志名")
		return
	}
	path := filepath.Join(id.Timeline.Dir, "runs", rest[0])
	data, err := os.ReadFile(path)
	if err != nil {
		writeError(w, 404, "未找到日志: "+rest[0])
		return
	}
	tail := 4096
	if t := r.URL.Query().Get("tail_bytes"); t != "" {
		if n, err := strconv.Atoi(t); err == nil && n > 0 {
			tail = n
		}
	}
	if len(data) > tail {
		data = data[len(data)-tail:]
	}
	writeJSON(w, 200, map[string]any{"name": rest[0], "content": string(data)})
}

// handleSubTrajectory 返回子轨迹的 mindlog + 面包屑——viewer
// SubTrajectory 页契约。我们目前没有子轨迹的深度路径，但路径字段
// 必须返回（即便为空数组），否则页面崩在 TypeError。
func (s *Server) handleSubTrajectory(w http.ResponseWriter, _ *http.Request, id *identity.Identity, rest []string) {
	if len(rest) == 0 || rest[0] == "" {
		writeError(w, 400, "缺少轨迹 id")
		return
	}
	// 与 viewer Mindlog 子集：traj_id、steps、live、identity。
	view := mindlogNormalized{
		TrajID:    rest[0],
		DirRel:    relTrajDir(s.cfg.Root, id.Timeline.Dir),
		StepCount: 0,
		Steps:     []normalizedStep{},
		Runs:      []runGroup{},
		Live:      false,
		Identity:  identityRef{ID: id.Name, Name: id.Name},
	}
	out := subTrajectory{
		Mindlog: view,
	}
	writeJSON(w, 200, out)
}

type subTrajectory struct {
	Mindlog    mindlogNormalized `json:"mindlog"`
	Breadcrumb []subCrumb        `json:"breadcrumb,omitempty"`
	Parent     *subParentRef     `json:"parent,omitempty"`
}

type subCrumb struct {
	TrajID string `json:"traj_id"`
	Slug   string `json:"slug"`
}

type subParentRef struct {
	TrajID string  `json:"traj_id"`
	StepID *string `json:"step_id"`
}

// identityCreateTime 返回身份创建时间（轨迹头行 ts）。
func identityCreateTime(id *identity.Identity) string {
	if steps, err := id.Timeline.Steps(); err == nil && len(steps) > 0 {
		return steps[0].TS
	}
	if fi, err := os.Stat(id.Timeline.Path); err == nil {
		return fi.ModTime().UTC().Format(traj.TimeFormat)
	}
	return ""
}

// identityLastTime 返回轨迹文件 mtime。
func identityLastTime(id *identity.Identity) string {
	if fi, err := os.Stat(id.Timeline.Path); err == nil {
		return fi.ModTime().UTC().Format(traj.TimeFormat)
	}
	return ""
}

// stepCount 返回轨迹的步数（含头行）。
func stepCount(id *identity.Identity) int {
	if steps, err := id.Timeline.Steps(); err == nil {
		return len(steps)
	}
	return 0
}

// hasFinalStep 检查轨迹是否含 final 步骤。
func hasFinalStep(id *identity.Identity) bool {
	if steps, err := id.Timeline.Steps(); err == nil {
		for _, s := range steps {
			if s.Type == "final" {
				return true
			}
		}
	}
	return false
}
