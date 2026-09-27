package web

// /api/identities/{name}/schedule 日程面——web 只做"读写同一份
// schedule.json + 手动触发一次"：条目事实与执行语义全部由
// schedule 包提供（与 CLI 同一套解析/状态投影/幂等键），HTTP 层
// 只做方法路由、参数校验与 JSON 呈现。
//
//	GET    /schedule              条目列表（解析结果 + 状态投影）
//	POST   /schedule/{id}/toggle  启用/禁用（{enabled} 写回文件）
//	POST   /schedule/{id}/run     手动触发（exec 同步执行、task 按今天提交）
//	DELETE /schedule/{id}         删除条目

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"mindloop/internal/identity"
	"mindloop/internal/schedule"
	"mindloop/internal/task"
)

// scheduleRunTimeout 钳制 web 手动执行 exec 条目的等待时长。条目
// 超时可能远大于 HTTP 写超时（30s），挂起请求只会让浏览器先断线；
// 宁可给一个"被终止"的明确结果（与 CLI 的完整等待语义分档——
// 到期节拍仍由心智心跳按条目配置接管）。
const scheduleRunTimeout = 25 * time.Second

// scheduleEntryView 是条目的 JSON 形态（与 CLI schedule list --json
// 同构）：解析结果 + 状态投影合并（两个消费面同一份数据）。
type scheduleEntryView struct {
	ID           string `json:"id"`
	Enabled      bool   `json:"enabled"`
	Kind         string `json:"kind"`
	Trigger      string `json:"trigger"`
	Action       string `json:"action"`
	NextRun      string `json:"next_run,omitempty"`
	LastRun      string `json:"last_run,omitempty"`
	LastExitCode int    `json:"last_exit_code,omitempty"`
	LastError    string `json:"last_error,omitempty"`
	LastNote     string `json:"last_note,omitempty"`
}

// scheduleView 是日程页响应：条目 + 解析警告。坏条目跳过但可见
// （warnings）；文件级损坏单独成 parse_error——页面显示"文件
// 坏了、去修"，而不是整个请求 500。
type scheduleView struct {
	Identity    map[string]string   `json:"identity"`
	File        string              `json:"file"`
	Entries     []scheduleEntryView `json:"entries"`
	Warnings    []string            `json:"warnings"`
	ParseError  string              `json:"parse_error,omitempty"`
	MindRunning bool                `json:"mind_running"`
}

// routeSchedule 分流日程子路径（写端点全部 POST/DELETE——方法
// 模式在子树内由 requireMethod 强制）。
func (s *Server) routeSchedule(w http.ResponseWriter, r *http.Request, id *identity.Identity, rest []string) {
	switch {
	case len(rest) == 0:
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		s.handleScheduleList(w, r, id)
	case len(rest) == 1:
		if !requireMethod(w, r, http.MethodDelete) {
			return
		}
		s.handleScheduleRemove(w, r, id, rest[0])
	case len(rest) == 2 && rest[1] == "toggle":
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		s.handleScheduleToggle(w, r, id, rest[0])
	case len(rest) == 2 && rest[1] == "run":
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		s.handleScheduleRun(w, r, id, rest[0])
	default:
		writeError(w, 404, "未知子路径: schedule/"+strings.Join(rest, "/"))
	}
}

// handleScheduleList GET /api/identities/{id}/schedule。
func (s *Server) handleScheduleList(w http.ResponseWriter, _ *http.Request, id *identity.Identity) {
	schedulePath, _, statePath := schedule.Paths(id.Dir, id.Timeline.Dir)
	out := scheduleView{
		Identity:    map[string]string{"id": id.Name, "name": id.Name},
		File:        schedulePath,
		Entries:     []scheduleEntryView{},
		Warnings:    []string{},
		MindRunning: isIdentityLive(id.Timeline.Dir),
	}
	parsed, warns, err := schedule.Load(schedulePath)
	if err != nil {
		// 文件级损坏：列表降级为错误横幅（含修复指引），页面仍可打开。
		out.ParseError = err.Error()
		writeJSON(w, 200, out)
		return
	}
	out.Warnings = append(out.Warnings, warns...)
	state := schedule.LoadState(statePath)
	now := time.Now()
	for _, p := range parsed {
		st := state.Items[p.Item.ID]
		out.Entries = append(out.Entries, scheduleEntryView{
			ID: p.Item.ID, Enabled: p.Enabled, Kind: string(p.Kind),
			Trigger: schedule.TriggerText(p), Action: schedule.ActionText(p),
			NextRun: schedule.NextText(p, st, now),
			LastRun: st.LastRun, LastExitCode: st.LastExitCode,
			LastError: st.LastError, LastNote: st.LastNote,
		})
	}
	writeJSON(w, 200, out)
}

// handleScheduleToggle POST /api/identities/{id}/schedule/{eid}/toggle
// {enabled}——只翻转 enabled 字段：true 置缺省（字段移除），false
// 写显式禁用。改前先整体读文件（损坏拒绝，避免覆盖用户数据）。
func (s *Server) handleScheduleToggle(w http.ResponseWriter, r *http.Request, id *identity.Identity, entryID string) {
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, 400, "请求体必须是 {enabled} JSON")
		return
	}
	if req.Enabled == nil {
		writeError(w, 400, "缺少 enabled 字段")
		return
	}
	schedulePath, _, _ := schedule.Paths(id.Dir, id.Timeline.Dir)
	file, err := schedule.LoadFile(schedulePath)
	if err != nil {
		writeError(w, 409, err.Error())
		return
	}
	found := false
	for i := range file.Items {
		if file.Items[i].ID == entryID {
			if *req.Enabled {
				file.Items[i].Enabled = nil // 缺省 = 启用（字段移除）
			} else {
				disabled := false
				file.Items[i].Enabled = &disabled
			}
			found = true
			break
		}
	}
	if !found {
		writeError(w, 404, "没有条目 "+entryID)
		return
	}
	if err := schedule.SaveFile(schedulePath, file); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"ok": true, "id": entryID, "enabled": *req.Enabled,
		"mind_running": isIdentityLive(id.Timeline.Dir),
	})
}

// handleScheduleRemove DELETE /api/identities/{id}/schedule/{eid}。
func (s *Server) handleScheduleRemove(w http.ResponseWriter, _ *http.Request, id *identity.Identity, entryID string) {
	schedulePath, _, _ := schedule.Paths(id.Dir, id.Timeline.Dir)
	file, err := schedule.LoadFile(schedulePath)
	if err != nil {
		writeError(w, 409, err.Error())
		return
	}
	kept := file.Items[:0]
	removed := false
	for _, it := range file.Items {
		if it.ID == entryID {
			removed = true
			continue
		}
		kept = append(kept, it)
	}
	if !removed {
		writeError(w, 404, "没有条目 "+entryID)
		return
	}
	file.Items = kept
	if err := schedule.SaveFile(schedulePath, file); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"ok": true, "removed": entryID,
		"mind_running": isIdentityLive(id.Timeline.Dir),
	})
}

// handleScheduleRun POST /api/identities/{id}/schedule/{eid}/run——
// 手动触发一次，不改变调度节拍（与 CLI schedule run 同一语义）：
//   - exec：同步在沙箱执行（等待钳 25s），结果原样返回；
//   - task：按到点触发相同的幂等键提交——同一天不会因手动+自动
//     产生两个任务（已提交过则返回既有任务）。
func (s *Server) handleScheduleRun(w http.ResponseWriter, r *http.Request, id *identity.Identity, entryID string) {
	schedulePath, _, _ := schedule.Paths(id.Dir, id.Timeline.Dir)
	parsed, warns, err := schedule.Load(schedulePath)
	if err != nil {
		writeError(w, 409, err.Error())
		return
	}
	var found *schedule.Parsed
	for i := range parsed {
		if parsed[i].Item.ID == entryID {
			found = &parsed[i]
			break
		}
	}
	if found == nil {
		// 文件里有但解析被跳过：条目本身是坏的，回具体原因让用户去修。
		if file, ferr := schedule.LoadFile(schedulePath); ferr == nil {
			for _, it := range file.Items {
				if it.ID == entryID {
					writeError(w, 400, "条目无效："+firstScheduleWarning(warns, entryID))
					return
				}
			}
		}
		writeError(w, 404, "没有条目 "+entryID)
		return
	}

	rt := scheduleRuntimeFor(id)
	if found.Kind == schedule.KindExec {
		ctx, cancel := context.WithTimeout(r.Context(), scheduleRunTimeout)
		defer cancel()
		out := rt.ExecuteNow(ctx, *found)
		res := map[string]any{
			"ok": out.Err == nil, "id": entryID, "kind": string(found.Kind),
			"exit_code": out.ExitCode, "duration_ms": out.Duration.Milliseconds(),
		}
		if out.Err != nil {
			res["error"] = out.Err.Error()
			res["detail"] = out.Detail
		}
		writeJSON(w, 200, res)
		return
	}
	now := time.Now()
	note, err := rt.SubmitNow(r.Context(), *found, now)
	if err != nil {
		writeError(w, 500, "任务提交失败: "+err.Error())
		return
	}
	// 键与 SubmitNow 内部一致：由计划触发时刻导出（PlannedFor）——
	// at 条目手动+自动同键，同一天不双发；直接用 now 会漂移。
	writeJSON(w, 200, map[string]any{
		"ok": true, "id": entryID, "kind": string(found.Kind),
		"note": note, "task_key": schedule.DateKey(entryID, schedule.PlannedFor(*found, now)),
	})
}

// firstScheduleWarning 从解析警告里取该条目的原因（Parse 的警告
// 前缀是 "条目 <id>: "）；没有匹配时给通用指引。
func firstScheduleWarning(warns []string, entryID string) string {
	prefix := "条目 " + entryID + ": "
	for _, wn := range warns {
		if strings.HasPrefix(wn, prefix) {
			return strings.TrimPrefix(wn, prefix)
		}
	}
	return "查看日程页警告，修复后重试"
}

// scheduleRuntimeFor 按装配语义构造日程运行时（与 CLI 同一路径、
// 同一任务提交通道）——web 手动触发直接使用；日志走文件，不注入
// 终端。
func scheduleRuntimeFor(id *identity.Identity) *schedule.Runtime {
	schedulePath, logPath, statePath := schedule.Paths(id.Dir, id.Timeline.Dir)
	return schedule.New(schedule.Options{
		Path:      schedulePath,
		Dir:       id.Dir,
		LogPath:   logPath,
		StatePath: statePath,
		Env: []string{
			"MINDLOOP_IDENTITY_DIR=" + id.Dir,
			"SKILLS_DIR=" + filepath.Join(id.Dir, "skills"),
		},
		Submit: scheduleSubmitter(id),
	})
}

// scheduleSubmitter 返回日程任务提交通道（与 CLI 装配同语义）：
// From=schedule、幂等键 = sched-<id>-<date>；同键不同载荷
// （ErrConflict：当天键已被用于不同内容）视为"当天已有任务"的
// 良性跳过。
func scheduleSubmitter(id *identity.Identity) func(ctx context.Context, content, clientMessageID string) (string, error) {
	store := task.New(id.Timeline, id.Name)
	return func(ctx context.Context, content, clientMessageID string) (string, error) {
		item, err := store.Submit(ctx, task.Submission{
			From:            schedule.SourceName,
			ClientMessageID: clientMessageID,
			Content:         content,
		})
		if err != nil {
			if errors.Is(err, task.ErrConflict) {
				return "当天已有任务", nil
			}
			return "", err
		}
		return "任务 " + item.ID, nil
	}
}
