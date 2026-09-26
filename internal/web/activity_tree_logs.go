package web

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"mindloop/internal/identity"
	"mindloop/internal/mind"
	"mindloop/internal/traj"
)

// handleActivity 返回主页面的 IdentityActivity 契约：心智是否运行、
// 哪些 thinker 忙、最近一次活动的延迟、消息队列长度。
//
// live 状态从心智运行锁存在性近似（dispatcher.pid 文件）。其他
// 字段先取心智的实际信息，再补 0 值——viewer 缺字段只是显示空。
// activityMap 组装 IdentityActivity 数据（/activity 与 /health 共用）。
// busy_thinkers 取自共享侧索引的最后一条 launched_by 投影。
func (s *Server) activityMap(id *identity.Identity) map[string]any {
	live := isIdentityLive(id.Timeline.Dir)
	state := "idle"
	if live {
		state = "working"
	}
	// 契约漂移清理：steps_in_flight/pending_total 是调度器内存态，
	// 仪表盘侧永远无法知晓——硬编码 0 的死字段，与前端同步删除。
	out := map[string]any{
		"state":              state,
		"dispatcher_running": live,
		"busy_thinkers":      []string{},
		"last_step_ts":       nil,
		"last_step_age_s":    nil,
		"run_seconds":        nil,
		"stall_after_s":      300,
		"cadence_s":          nil,
	}
	if fi, err := os.Stat(id.Timeline.Path); err == nil {
		out["last_step_ts"] = fi.ModTime().UTC().Format(traj.TimeFormat)
		out["last_step_age_s"] = int(time.Since(fi.ModTime()).Seconds())
	}
	// 索引不可用时保持空数组——与无记录等价。
	if ix, err := s.indexes.get(id.Timeline); err == nil {
		if by, ok, err := ix.LastLaunchedBy(); err == nil && ok {
			out["busy_thinkers"] = []string{by}
		}
	}
	return out
}

// handleActivity 返回主页面的 IdentityActivity 契约。
func (s *Server) handleActivity(w http.ResponseWriter, _ *http.Request, id *identity.Identity, _ []string) {
	writeJSON(w, 200, s.activityMap(id))
}

// handleTree 返回 fork-tree 侧栏——headlong viewer 用它画子轨迹的
// 树状图。我们的 MindlogJSONL 末尾的"fork"步骤正是 tree 边。
//
// Query: ?depth=N（默认 1，最浅层）
func (s *Server) handleTree(w http.ResponseWriter, r *http.Request, id *identity.Identity, _ []string) {
	depth := 1
	if d := r.URL.Query().Get("depth"); d != "" {
		// 0 是合法值（仅当前节点），只拒绝负数与非数字。
		if n, err := strconv.Atoi(d); err == nil && n >= 0 {
			depth = n
		}
	}
	// 根节点的计数/旗标来自共享侧索引；空轨迹或首步无时间戳时
	// StartedTs 退回文件 mtime（与旧 identityCreateTime 的兜底一致）。
	var started string
	var count int
	var hasFinal bool
	if ix, err := s.indexes.get(id.Timeline); err == nil {
		if sum, err := ix.Summary(); err == nil {
			started, count, hasFinal = sum.FirstTS, sum.StepCount, sum.HasFinal
		}
	}
	if started == "" {
		if fi, err := os.Stat(id.Timeline.Path); err == nil {
			started = fi.ModTime().UTC().Format(traj.TimeFormat)
		}
	}
	root := treeNode{
		TrajID:     id.Timeline.ID,
		Slug:       id.Name,
		StartedTs:  started,
		LastTs:     identityLastTime(id),
		StepCount:  count,
		HasFinal:   hasFinal,
		ChildCount: 0,
	}
	// 一层深度：列出直接子轨迹。深度 0 = 仅当前。
	if depth >= 1 {
		kids := s.childTrajectories(id)
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

// childTrajectories 列出 id 的直接子轨迹——经共享侧索引读根轨迹的
// fork 步骤 child_ref（headlong 同款机制）；子轨迹的步数/末步/final
// 由其各自的侧索引给出（同一 store，同尺寸替换/截断由 Index 重建覆盖）。
func (s *Server) childTrajectories(id *identity.Identity) []childTrajInfo {
	ix, err := s.indexes.get(id.Timeline)
	if err != nil {
		return nil
	}
	refs, err := ix.ForkRefs()
	if err != nil {
		return nil
	}
	tlDir := filepath.Dir(id.Timeline.Path)
	seen := map[string]bool{}
	var out []childTrajInfo
	for _, ref := range refs {
		// child_ref 是相对 tlDir 的路径；解绝对路径。ref 来自轨迹
		// 内容（traj append --field 可任意写入），解析结果必须仍在
		// 轨迹目录之内——越界引用直接跳过，绝不 Stat/读文件。
		childPath := filepath.Join(tlDir, ref)
		if rel, rerr := filepath.Rel(tlDir, childPath); rerr != nil ||
			rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			continue
		}
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
		childTL := &traj.Timeline{Dir: childDir, Path: filepath.Join(childDir, "trajectory.jsonl")}
		var steps int
		var hasFinal bool
		if cix, err := s.indexes.get(childTL); err == nil {
			if sum, err := cix.Summary(); err == nil {
				steps = sum.StepCount
				hasFinal = sum.HasFinal
			}
			if lt, err := cix.LastTS(); err == nil && lt != "" {
				last = lt
			}
		}
		if last == "" && ts != nil {
			last = started
		}
		out = append(out, childTrajInfo{
			id: filepath.Base(childDir), slug: filepath.Base(childDir),
			started: started, last: last,
			steps: steps, hasFinal: hasFinal,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].started < out[j].started })
	return out
}

// handleThinkers 返回 ThinkersStatus 契约：dispatcher 状态、所有
// thinker 列表、busy/disabled 统计。
func (s *Server) handleThinkers(w http.ResponseWriter, _ *http.Request, id *identity.Identity, _ []string) {
	live := isIdentityLive(id.Timeline.Dir)
	// thinkers 列表：从侧索引的 launched_by 聚合提炼（计数与最近
	// 活跃时间戳），状态据最近活跃时间推断；索引不可用时空列表。
	var stats []traj.ThinkerStat
	if ix, err := s.indexes.get(id.Timeline); err == nil {
		if list, err := ix.Thinkers(); err == nil {
			stats = list
		}
	}
	var thinkInfos []map[string]any
	now := time.Now().UTC()
	total := len(stats)
	disabled := 0
	for _, t := range stats {
		state := "idle"
		if mind.IsThinkerDisabled(id.Timeline.Dir, t.Name) {
			state = "disabled"
			disabled++
		}
		if live && state != "disabled" && t.LastTS != "" {
			// disabled 优先：禁用的 thinker 不因最近有轨迹事实而
			// 显示 active（此前 active 分支会覆盖 disabled）。
			last, _ := time.Parse("2006-01-02T15:04:05Z07:00", t.LastTS)
			if last.IsZero() {
				last, _ = time.Parse(traj.TimeFormat, t.LastTS)
			}
			if !last.IsZero() && now.Sub(last) < 5*time.Minute {
				state = "active"
			}
		}
		// 契约漂移清理（与前端同步删除）：steps_in_flight 与 pending
		// 是仪表盘侧永远无法知晓的调度器内存态——恒 0/恒空的死字段，
		// 展示它们等于展示谎言，两边一起删。
		thinkInfos = append(thinkInfos, map[string]any{
			"name":         t.Name,
			"state":        state,
			"pid":          nil,
			"types":        []string{},
			"trigger_self": false,
			"log_bytes":    nil,
			"log_mtime":    t.LastTS,
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
		// 顶层 steps_in_flight/pending_total 同为硬编码 0 的死字段，
		// 与条目级字段一并删除（契约漂移清理的延伸，前端同步）。
		"thinkers": thinkInfos,
	})
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

// handleTrajectoryBlob 读取旧轨迹中保留的完整输出；不接受任意文件或跨轨迹路径。
func (s *Server) handleTrajectoryBlob(w http.ResponseWriter, r *http.Request, id *identity.Identity, rest []string) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	if len(rest) != 3 || rest[0] != id.Timeline.ID {
		writeError(w, 404, "输出文件不存在")
		return
	}
	name := rest[2]
	if name == "" || strings.ContainsAny(name, `/\:`) || strings.HasPrefix(name, ".") ||
		(!strings.HasSuffix(name, ".stdout") && !strings.HasSuffix(name, ".stderr")) {
		writeError(w, 404, "输出文件不存在")
		return
	}
	rel, err := filepath.Rel(id.Dir, id.Timeline.Dir)
	if err != nil || !filepath.IsLocal(rel) {
		writeError(w, 404, "输出文件不存在")
		return
	}
	// Root 在打开时约束路径和符号链接，避免检查后替换链接的逃逸窗口。
	root, err := os.OpenRoot(id.Dir)
	if err != nil {
		writeError(w, 404, "输出文件不存在")
		return
	}
	defer root.Close()
	blobs, err := root.OpenRoot(filepath.Join(rel, "blobs"))
	if err != nil {
		writeError(w, 404, "输出文件不存在")
		return
	}
	defer blobs.Close()
	f, err := blobs.Open(name)
	if err != nil {
		writeError(w, 404, "输出文件不存在")
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		writeError(w, 404, "输出文件不存在")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, name, info.ModTime(), f)
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

// identityLastTime 返回轨迹文件 mtime。
func identityLastTime(id *identity.Identity) string {
	if fi, err := os.Stat(id.Timeline.Path); err == nil {
		return fi.ModTime().UTC().Format(traj.TimeFormat)
	}
	return ""
}
