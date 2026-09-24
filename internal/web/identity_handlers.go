package web

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mindloop/internal/identity"
	"mindloop/internal/traj"
)

// liveWindow 是 Live 判定的时间窗：dispatcher 进程死了但 mtime 在
// 这个窗内，仍然认为心智"刚停"——viewer 不会立刻把它标灰。
const liveWindow = 30 * time.Second

// isIdentityLive 判断心智是否在跑：调度器进程活着或 mindlog mtime
// 落在 liveWindow 内。Headlong-web 同款逻辑（headlong_web/liveness.py）。
func isIdentityLive(identityDir string) bool {
	if identityDir == "" {
		return false
	}
	// 调度器进程：<id>/run/dispatcher.lock。属主写入 owner.json：
	// 进程死了我们偷锁；属主死/无主且 lock 过期也算没人了。
	lock := filepath.Join(identityDir, "run", "dispatcher.lock")
	if fi, err := os.Stat(lock); err == nil {
		if !alive(fi) {
			return false
		}
		return true
	}
	// 没锁：轨迹最近被写 = 心智刚醒。
	md := filepath.Join(identityDir, "trajectories")
	entries, err := os.ReadDir(md)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		if time.Since(fi.ModTime()) < liveWindow {
			return true
		}
	}
	return false
}

// alive 锁文件元判断是否"有人持有"——属主 process 仍活 或 lock 尚新鲜。
func alive(fi os.FileInfo) bool {
	return time.Since(fi.ModTime()) < liveWindow
}

// handleChat 渲染对话视图：只返回 message 步骤，按时间排序。
func (s *Server) handleChat(w http.ResponseWriter, _ *http.Request, id *identity.Identity) {
	steps, err := id.Timeline.Steps()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(steps))
	for _, s := range steps {
		if s.Type != "message" {
			continue
		}
		out = append(out, stepToJSON(s))
	}
	writeJSON(w, 200, map[string]any{
		"identity": id.Name,
		"messages": out,
	})
}

// handleMemories 列出身份的记忆库——viewer Memories 视图。
//
// GET /api/identities/{name}/memories?type=fact&q=keyword
//
// 契约：viewer 的 fetchMemories 期望 **裸数组** MemoryInfo[]：
// {name, mtime(毫秒), id, summary, type, created, slug}。
func (s *Server) handleMemories(w http.ResponseWriter, r *http.Request, id *identity.Identity, rest []string) {
	// 子路径 /memories/{filename}：返回单条全文 {name, content}——
	// viewer 点选记忆卡片时拉取。
	if len(rest) > 0 && rest[0] != "" {
		name := rest[0]
		if strings.Contains(name, "..") || strings.Contains(name, "/") || strings.Contains(name, "\\") {
			writeError(w, 400, "非法文件名")
			return
		}
		data, err := os.ReadFile(filepath.Join(id.Dir, "memories", name))
		if err != nil {
			writeError(w, 404, "记忆文件不存在: "+name)
			return
		}
		writeJSON(w, 200, map[string]any{"name": name, "content": string(data)})
		return
	}
	store := memStore(id.Dir)
	all, err := store.List()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	q := r.URL.Query().Get("q")
	tFilter := r.URL.Query().Get("type")
	if q != "" {
		hits, _ := store.Search(q, 20)
		all = hits
	}
	out := make([]map[string]any, 0, len(all))
	for _, m := range all {
		if tFilter != "" && m.Type != tFilter {
			continue
		}
		name := filepath.Base(m.Path)
		slug := strings.TrimSuffix(name, ".md")
		if idx := strings.Index(slug, "_"); idx >= 0 && len(slug) > idx+1+8 {
			slug = slug[idx+9:] // 去掉 "YYYY-MM-DD-HH-MM-SS_" 前缀
		}
		out = append(out, map[string]any{
			"name":    name,
			"mtime":   m.Created.UnixMilli(),
			"id":      m.ID,
			"summary": m.Summary,
			"type":    m.Type,
			"created": m.Created.UTC().Format(traj.TimeFormat),
			"slug":    slug,
		})
	}
	writeJSON(w, 200, out)
}

// handleThinkers 列出 thinker 状态——viewer Thinkers 视图。
// 第 1 轮：返回从轨迹里提炼的 launcher-by 集合。
func (s *Server) handleThinkers(w http.ResponseWriter, _ *http.Request, id *identity.Identity) {
	steps, err := id.Timeline.Steps()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	type summary struct {
		Name      string `json:"name"`
		Runs      int    `json:"runs"`
		WakeCount int    `json:"wake_count"`
	}
	stats := map[string]*summary{}
	var order []string
	for _, s := range steps {
		if by, ok := s.Field("launched_by"); ok && by != "" {
			st, ok := stats[by]
			if !ok {
				st = &summary{Name: by}
				stats[by] = st
				order = append(order, by)
			}
			st.WakeCount++
		}
		if s.Type == "run" {
			by := "monolith"
			if launched, ok := s.Field("launched_by"); ok && launched != "" {
				by = launched
			}
			st, ok := stats[by]
			if !ok {
				st = &summary{Name: by}
				stats[by] = st
				order = append(order, by)
			}
			st.Runs++
		}
	}
	out := make([]summary, 0, len(order))
	for _, n := range order {
		out = append(out, *stats[n])
	}
	writeJSON(w, 200, map[string]any{
		"identity": id.Name,
		"thinkers": out,
	})
}

// handleIdentityHealth 读 llm-health.json + 最近一次失败步骤。
func (s *Server) handleIdentityHealth(w http.ResponseWriter, _ *http.Request, id *identity.Identity) {
	healthPath := filepath.Join(id.Dir, "llm-health.json")
	live := isIdentityLive(id.Timeline.Dir)
	out := map[string]any{
		"identity":   id.Name,
		"live":       live,
		"live_badge": map[bool]string{true: "live", false: "idle"}[live],
		"health":     nil,
	}
	if data, err := os.ReadFile(healthPath); err == nil {
		out["health"] = jsonBytes(data)
	}
	writeJSON(w, 200, out)
}
