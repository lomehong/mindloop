package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"

	"mindloop/internal/identity"
	"mindloop/internal/traj"
)

// IdentityInfo 是 viewer 首页 Identity 契约的 Go 形态——字段名与
// web/static/app/lib/types.ts 的 Identity 接口一一对齐。home.tsx
// 直接读取 dispatcher/group/thinkers_* 渲染表格，缺一个字段就崩
// 进错误边界（首版发布的真实事故：缺 group 导致首页 "Oops!"）。
// steps_in_flight/mindlog_path/persona_path/live_badge 已随契约
// 漂移清理删除（恒 0 的调度器内存态 / 服务器绝对路径 / 恒空死字段，
// 前后端同步删除）。
type IdentityInfo struct {
	// Dir 与 TLDir 仅服务端内部使用（killall 的 live 探测与停机
	// 投递需要轨迹目录层级），不进 JSON 契约。
	Dir            string         `json:"-"`
	TLDir          string         `json:"-"`
	ID             string         `json:"id"`
	Name           string         `json:"name"`
	PathRel        string         `json:"path_rel"`
	Created        *string        `json:"created"`
	RootTrajectory *string        `json:"root_trajectory"`
	Group          string         `json:"group"`
	Live           bool           `json:"live"`
	LastActivityTS *string        `json:"last_activity_ts"`
	StepCount      int            `json:"step_count"`
	Dispatcher     dispatcherInfo `json:"dispatcher"`
	ThinkersTotal  int            `json:"thinkers_total"`
	ThinkersActive int            `json:"thinkers_active"`
}

type dispatcherInfo struct {
	Running bool `json:"running"`
	PID     *int `json:"pid"`
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

// handleIdentities 返回身份列表——viewer 首页契约是 **裸数组**，
// 每项带 Identity 接口的全部字段（group/dispatcher/thinkers_* 等，
// home.tsx 渲染表格列时直接读取，缺了就崩进错误边界）。
func (s *Server) handleIdentities(w http.ResponseWriter, _ *http.Request) {
	infos, err := scanIdentities(s.cfg.Root)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if infos == nil {
		infos = []IdentityInfo{}
	}
	writeJSON(w, 200, infos)
}

// handleIdentityCreate 新建身份——POST /api/identities {name}。
// 返回 {id, name}（viewer 首页建身份表单契约）。
func (s *Server) handleIdentityCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, 400, "请求体必须是 {name} JSON")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if !isSafeIdentName(req.Name) || isBadIdentSegment(req.Name) {
		writeError(w, 400, "身份名含非法字符（仅允许字母/数字/-/_/.，且不含穿越段）")
		return
	}
	id, err := identity.Create(r.Context(), req.Name)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"id": id.Name, "name": id.Name})
}

// routeIdentity 把 /api/identities/{name}/{sub,...} 路由到具体 handler。
// 注意 sub 是单段（splitIdentityPath 的第二段）；recap/refresh 这类
// 两段子路径靠 rest[0] 再分流——写成 case "recap/refresh" 永远不可达。
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
		writeJSON(w, 200, identitySummary(s.cfg.Root, id))
	case "activity":
		s.handleActivity(w, r, id, rest)
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
		if r.Method == http.MethodPost {
			s.handleChatSend(w, r, id)
			return
		}
		s.handleChat(w, r, id)
	case "memories":
		s.handleMemories(w, r, id, rest)
	case "replies":
		s.routeReplies(w, r, id, rest)
	case "thinkers":
		s.routeThinkers(w, r, id, rest)
	case "thinker-sync":
		if r.Method == http.MethodPost {
			s.handleThinkerSyncPull(w, r, id)
			return
		}
		s.handleThinkerSync(w, r, id)
	case "dispatch":
		s.handleDispatchLog(w, r, id, rest)
	case "health":
		s.handleLlmHealth(w, r, id, rest)
	case "recap":
		if len(rest) > 0 && rest[0] == "refresh" {
			if !requireMethod(w, r, http.MethodPost) {
				return
			}
			s.handleRecapRefresh(w, r, id)
			return
		}
		s.handleRecap(w, r, id, rest)
	case "usage":
		if len(rest) > 0 && rest[0] == "refresh" {
			if !requireMethod(w, r, http.MethodPost) {
				return
			}
			s.handleUsageRefresh(w, r, id)
			return
		}
		s.handleUsage(w, r, id, rest)
	case "export":
		s.handleIdentityExport(w, r, id)
	case "export-jobs":
		switch r.Method {
		case http.MethodPost:
			s.handleExportJobCreate(w, r, id)
		case http.MethodGet:
			s.handleExportJobsList(w, r, id)
		default:
			w.Header().Set("Allow", "GET, POST")
			writeError(w, http.StatusMethodNotAllowed, "export-jobs 只接受 GET/POST")
		}
	case "env":
		switch r.Method {
		case http.MethodPut:
			s.handleEnvPut(w, r, id)
		case http.MethodDelete:
			key := ""
			if len(rest) > 0 {
				key = rest[0]
			}
			s.handleEnvDelete(w, r, id, key)
		default:
			s.handleIdentityEnv(w, r, id, rest)
		}
	case "skills":
		switch r.Method {
		case http.MethodPost:
			s.handleSkillsInstall(w, r, id)
		case http.MethodDelete:
			name := ""
			if len(rest) > 0 {
				name = rest[0]
			}
			s.handleSkillsDelete(w, r, id, name)
		default:
			s.handleSkillsList(w, r, id)
		}
	case "tree":
		s.handleTree(w, r, id, rest)
	case "logs":
		if len(rest) > 0 {
			s.handleLogTail(w, r, id, rest)
			return
		}
		s.handleLogs(w, r, id, rest)
	case "traj":
		s.handleSubTrajectory(w, r, id, rest)
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
		id, err := identity.Load(name)
		if err != nil {
			continue
		}
		out = append(out, summarizeIdentity(id, root))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// summarizeIdentity 收集 viewer Identity 契约的全部字段。
func summarizeIdentity(id *identity.Identity, root string) IdentityInfo {
	dir := id.Dir
	info := IdentityInfo{
		Dir:     dir,
		TLDir:   id.Timeline.Dir,
		ID:      id.Name,
		Name:    id.Name,
		PathRel: relTrajDir(root, dir),
		Group:   "local", // 单根部署：全部身份归入 local 组
	}
	info.RootTrajectory = &id.Timeline.ID
	if fi, err := os.Stat(id.Timeline.Path); err == nil {
		t := fi.ModTime().UTC().Format(traj.TimeFormat)
		info.LastActivityTS = &t
	}
	// thinker 统计：从轨迹的 launched_by 字段提炼（去重计数）。
	thinkers := map[string]bool{}
	if steps, err := id.Timeline.Steps(); err == nil {
		info.StepCount = len(steps)
		for _, s := range steps {
			if by, ok := s.Field("launched_by"); ok && by != "" && !thinkers[by] {
				thinkers[by] = true
			}
		}
	}
	info.ThinkersTotal = len(thinkers)
	// 运行中的 thinker 数需要调度器内存态，仪表盘侧不可知——
	// 活跃数以 live 近似：心智在跑则认为其 thinker 全部活跃。
	if info.Live = isIdentityLive(dir); info.Live {
		info.ThinkersActive = len(thinkers)
	}
	info.Dispatcher = dispatcherInfo{Running: info.Live}
	return info
}

// identitySummary 是单个身份概览。mindlog_path/persona_path 已随
// 契约漂移清理删除（前端零引用，服务器绝对路径不外泄）。
func identitySummary(root string, id *identity.Identity) map[string]any {
	return map[string]any{
		"id":              id.Name,
		"name":            id.Name,
		"path_rel":        relTrajDir(root, id.Dir),
		"root_trajectory": id.Timeline.ID,
	}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
