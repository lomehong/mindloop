package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"mindloop/internal/identity"
	"mindloop/internal/traj"
)

// IdentityInfo 是 viewer Identity 形态的子集（viewer 需要 root_trajectory
// 与 mindlog_path）；dashboard 真正要的全部字段这里。
type IdentityInfo struct {
	Name            string   `json:"name"`
	Dir             string   `json:"dir"`
	RootTrajectory  string   `json:"root_trajectory"`
	MindlogPath     string   `json:"mindlog_path"`
	PersonaPath     string   `json:"persona_path"`
	StepCount       int      `json:"step_count"`
	Live            bool     `json:"live"`
	LastModified    string   `json:"last_modified"`
	LastStepSummary string   `json:"last_step_summary"`
	LiveBadge       string   `json:"live_badge"`
	Routes          []string `json:"routes"`
}

// handleConfig 返回 viewer 期望的 Config 形态——viewer 类型要求
// root、version、controls_enabled 等字段，本后端能产的字段就产，
// 不能产的就给合理空值（viewer 渲染时按 falsy 处理）。
func (s *Server) handleConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{
		"root":                s.cfg.Root,
		"version":             "0.1.0",
		"controls_enabled":    true,
		"self_update_enabled": false,
		"default_send_from":   nil,
		"git_commit":          nil,
		"git_branch":          nil,
	})
}

// handleIdentities 扫描 cfg.Root 下的身份目录，返回列表。
func (s *Server) handleIdentities(w http.ResponseWriter, _ *http.Request) {
	infos, err := scanIdentities(s.cfg.Root)
	if infos == nil {
		infos = []IdentityInfo{}
	}
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	for i := range infos {
		infos[i].Routes = []string{
			"/i/" + infos[i].Name,
			"/i/" + infos[i].Name + "/mindlog",
			"/i/" + infos[i].Name + "/chat",
			"/i/" + infos[i].Name + "/memories",
			"/i/" + infos[i].Name + "/thinkers",
			"/i/" + infos[i].Name + "/health",
		}
	}
	writeJSON(w, 200, map[string]any{"identities": infos})
}

// routeIdentity 把 /api/identities/{name}/{sub,...} 路由到具体 handler。
func (s *Server) routeIdentity(w http.ResponseWriter, r *http.Request) {
	name, sub, rest, ok := splitIdentityPath(r.URL.Path)
	if !ok {
		writeError(w, 400, "身份路径格式错误，期望 /api/identities/{name}[/{sub}...]")
		return
	}
	if isBadIdentSegment(name) || !isSafeIdentName(name) {
		writeError(w, 400, "身份名含非法字符或路径穿越段")
		return
	}
	id, err := identity.Load(name)
	if err != nil {
		writeError(w, 404, "身份不存在: "+name)
		return
	}
	switch sub {
	case "":
		writeJSON(w, 200, identitySummary(id))
	case "mindlog":
		if len(rest) > 0 && rest[0] == "search" {
			s.handleMindlogSearch(w, r, id, rest[1:])
			return
		}
		s.handleMindlog(w, r, id, rest)
	case "step":
		s.handleStepDetail(w, r, id, rest)
	case "runs":
		s.handleRunCommand(w, r, id, rest)
	case "status":
		s.handleIdentityStatus(w, r, id)
	case "chat":
		s.handleChat(w, r, id)
	case "memories":
		s.handleMemories(w, r, id, rest)
	case "thinkers":
		s.handleThinkers(w, r, id)
	case "health":
		s.handleIdentityHealth(w, r, id)
	default:
		writeError(w, 404, "未知子路径: "+sub)
	}
}

// handleIdentityStatus 返回 viewer IdentityStatus 契约：live/pid/
// mindlog mtime/bytes/step_count。pid 探测经由运行锁存在性近似——
// 锁目录活着即 pid_alive（精确 pid 由 mind run 写 owner.json 提供）。
func (s *Server) handleIdentityStatus(w http.ResponseWriter, r *http.Request, id *identity.Identity) {
	live := isIdentityLive(id.Timeline.Dir)
	var mtime *string
	var bytesCount *int
	if fi, err := os.Stat(id.Timeline.Path); err == nil {
		t := fi.ModTime().UTC().Format(traj.TimeFormat)
		b := int(fi.Size())
		mtime, bytesCount = &t, &b
	}
	steps, err := id.Timeline.Steps()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"live":           live,
		"pid_alive":      live,
		"dispatcher_pid": nil,
		"mindlog_mtime":  mtime,
		"mindlog_bytes":  bytesCount,
		"step_count":     len(steps),
	})
}

// splitIdentityPath 解析 "/api/identities/{name}[/{sub}[/{rest}...]]"。
func splitIdentityPath(path string) (string, string, []string, bool) {
	const prefix = "/api/identities/"
	if !strings.HasPrefix(path, prefix) {
		return "", "", nil, false
	}
	rest := strings.Trim(path[len(prefix):], "/")
	if rest == "" {
		return "", "", nil, false
	}
	parts := strings.Split(rest, "/")
	if len(parts) < 1 {
		return "", "", nil, false
	}
	if len(parts) == 1 {
		return parts[0], "", nil, true
	}
	return parts[0], parts[1], parts[2:], true
}

// isSafeIdentName 是身份名的字符白名单——防路径穿越。
func isSafeIdentName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') &&
			!(r >= '0' && r <= '9') && r != '-' && r != '_' && r != '.' {
			return false
		}
	}
	return true
}

// handleHealth 是整体健康探针：身份数、运行锁、心智活跃数。
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	infos, err := scanIdentities(s.cfg.Root)
	if infos == nil {
		infos = []IdentityInfo{}
	}
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	live := 0
	for _, i := range infos {
		if i.Live {
			live++
		}
	}
	writeJSON(w, 200, map[string]any{
		"version":    "0.1.0",
		"identities": len(infos),
		"live_minds": live,
		"checked_at": nowISO(),
	})
}

// nowISO 返回 UTC ISO8601——与 traj.TimeFormat 一致，viewer 类型期望。
func nowISO() string { return traj.NowString() }

// scanIdentities 扫描 root 下的身份目录列表。
func scanIdentities(root string) ([]IdentityInfo, error) {
	root = strings.TrimRight(root, string(os.PathSeparator))
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("scan: %w", err)
	}
	var out []IdentityInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if !isSafeIdentName(name) || isBadIdentSegment(name) {
			continue
		}
		dir := filepath.Join(root, name)
		id, err := identity.Load(name)
		if err != nil {
			continue
		}
		out = append(out, summarizeIdentity(id, dir))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// summarizeIdentity 收集 viewer IdentityInfo 全部字段。
func summarizeIdentity(id *identity.Identity, dir string) IdentityInfo {
	info := IdentityInfo{
		Name:        id.Name,
		Dir:         dir,
		PersonaPath: filepath.Join(dir, "persona.md"),
		Routes:      []string{},
	}
	if rt := id.Timeline.Path; rt != "" {
		info.RootTrajectory = id.Timeline.ID
		info.MindlogPath = rt
	}
	if info.MindlogPath != "" {
		if fi, err := os.Stat(info.MindlogPath); err == nil {
			info.LastModified = fi.ModTime().UTC().Format(traj.TimeFormat)
		}
		if steps, err := id.Timeline.Steps(); err == nil {
			info.StepCount = len(steps)
			if n := len(steps); n > 0 {
				last := steps[n-1]
				if c, ok := last.Field("content"); ok && c != "" {
					info.LastStepSummary = truncate(c, 30)
				} else {
					info.LastStepSummary = truncate(last.String(), 30)
				}
			}
		}
	}
	info.Live = isIdentityLive(id.Timeline.Dir)
	info.LiveBadge = "idle"
	if info.Live {
		info.LiveBadge = "live"
	} else if info.StepCount == 0 {
		info.LiveBadge = "never"
	}
	return info
}

// identitySummary 是单个身份概览。
func identitySummary(id *identity.Identity) map[string]any {
	return map[string]any{
		"name":            id.Name,
		"dir":             id.Dir,
		"root_trajectory": id.Timeline.ID,
		"mindlog_path":    id.Timeline.Path,
		"persona_path":    filepath.Join(id.Dir, "persona.md"),
	}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

var _ = context.Canceled
var _ = url.Values{}
var _ = json.Marshal
