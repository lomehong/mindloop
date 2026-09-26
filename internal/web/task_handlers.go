package web

// /api/identities/{name}/tasks 任务面——显式委托的 Web 入口：
//
//	GET  /tasks                    任务列表（日志序，新的在后）
//	POST /tasks                    提交 {content, from_name?,
//	                               client_message_id?, source_step_id?}
//	GET  /tasks/{taskID}           单任务详情（完整 id；未知 404）
//	POST /tasks/{taskID}/cancel    {attempt?, request_id?}
//	POST /tasks/{taskID}/retry     {attempt?, request_id?}
//
// 任务事实全部来自根轨迹投影（task.Store）；HTTP 层只做参数校验、
// 错误映射与 JSON 呈现——状态推断权在 task 包的状态机。幂等语义与
// CLI 一致：提交同键同载荷返回原任务；cancel/retry 的 request_id
// 重发返回原收据；对非终态任务的 retry 是 409 而不是静默成功。

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"mindloop/internal/identity"
	"mindloop/internal/ids"
	"mindloop/internal/task"
)

// maxTaskIDLen 限制路径段长度防超长键拖垮投影；控制字符在名称层
// 不可见，直接拒绝。
const maxTaskIDLen = 256

func validTaskID(s string) bool {
	if s == "" || len(s) > maxTaskIDLen {
		return false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// handleTasks 按路径段分流任务面；方法路由与现行读端点风格一致。
func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request, id *identity.Identity, rest []string) {
	store := task.New(id.Timeline, id.Name)
	switch {
	case len(rest) == 0:
		switch r.Method {
		case http.MethodGet:
			s.taskList(w, r, store)
		case http.MethodPost:
			s.taskSubmit(w, r, store)
		default:
			w.Header().Set("Allow", "GET, POST")
			writeError(w, http.StatusMethodNotAllowed, "tasks 只接受 GET/POST")
		}
	case len(rest) == 1 && validTaskID(rest[0]):
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		s.taskShow(w, r, store, rest[0])
	case len(rest) == 2 && validTaskID(rest[0]) && (rest[1] == "cancel" || rest[1] == "retry"):
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		s.taskCommand(w, r, store, rest[0], rest[1])
	default:
		writeError(w, 404, "未知子路径: tasks/"+strings.Join(rest, "/"))
	}
}

func (s *Server) taskList(w http.ResponseWriter, r *http.Request, store *task.Store) {
	items, err := store.List(r.Context())
	if err != nil {
		writeTaskError(w, err)
		return
	}
	writeJSON(w, 200, items)
}

func (s *Server) taskShow(w http.ResponseWriter, r *http.Request, store *task.Store, taskID string) {
	item, err := store.Get(r.Context(), taskID)
	if err != nil {
		writeTaskError(w, err)
		return
	}
	writeJSON(w, 200, item)
}

// taskSubmit 提交显式委托：client_message_id 缺省时服务端兜底生成
// UUID——未带幂等键的提交也获得可重放的标识，响应丢失后可安全重发。
func (s *Server) taskSubmit(w http.ResponseWriter, r *http.Request, store *task.Store) {
	var req struct {
		Content         string `json:"content"`
		FromName        string `json:"from_name"`
		ClientMessageID string `json:"client_message_id"`
		SourceStepID    string `json:"source_step_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, 400, "请求体必须是 {content, from_name?, client_message_id?} JSON")
		return
	}
	if strings.TrimSpace(req.Content) == "" {
		writeError(w, 400, "任务内容不能为空")
		return
	}
	from := strings.TrimSpace(req.FromName)
	if from == "" {
		from = "operator"
	}
	cid := strings.TrimSpace(req.ClientMessageID)
	if cid == "" {
		cid = ids.NewUUID()
	}
	item, err := store.Submit(r.Context(), task.Submission{
		From:            from,
		ClientMessageID: cid,
		Content:         req.Content,
		SourceStepID:    strings.TrimSpace(req.SourceStepID),
	})
	if err != nil {
		writeTaskError(w, err)
		return
	}
	writeJSON(w, 200, item)
}

// taskCommand 执行 cancel/retry。body 容忍空/null/{}：控制命令的
// 参数全部可选。基准 attempt 默认“当前代次”（先读后写，锁内校验
// 兜底防 TOCTOU——状态已变则 ErrTransition 拒绝，不误作用于新代次）；
// request_id 缺省时服务端生成，重发同键返回原收据。
func (s *Server) taskCommand(w http.ResponseWriter, r *http.Request, store *task.Store, taskID, op string) {
	var req struct {
		Attempt   int    `json:"attempt"`
		RequestID string `json:"request_id"`
	}
	if data, _ := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20)); len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, &req); err != nil {
			writeError(w, 400, "请求体必须是 {attempt?, request_id?} JSON")
			return
		}
	}
	item, err := store.Get(r.Context(), taskID)
	if err != nil {
		writeTaskError(w, err)
		return
	}
	base := item.Attempt
	if req.Attempt > 0 {
		base = req.Attempt
	}
	rid := strings.TrimSpace(req.RequestID)
	if rid == "" {
		rid = ids.NewUUID()
	}
	var out task.Task
	if op == "cancel" {
		out, err = store.Cancel(r.Context(), taskID, base, "operator", rid)
	} else {
		out, err = store.Retry(r.Context(), taskID, base, "operator", rid)
	}
	if err != nil {
		writeTaskError(w, err)
		return
	}
	writeJSON(w, 200, out)
}

// writeTaskError 把任务域错误映射为 HTTP 状态：不存在 404、幂等/
// 状态冲突 409、参数无效 400、损坏与内部错误 500——绝不把冲突
// 伪装成成功。
func writeTaskError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, task.ErrNotFound):
		writeError(w, 404, "任务不存在")
	case errors.Is(err, task.ErrConflict):
		writeError(w, 409, "幂等键冲突：同一键已用于不同请求")
	case errors.Is(err, task.ErrTransition):
		writeError(w, 409, "任务当前状态不允许此操作")
	case errors.Is(err, task.ErrInvalid):
		writeError(w, 400, err.Error())
	case errors.Is(err, task.ErrBusy):
		writeError(w, 409, err.Error())
	default:
		writeError(w, 500, err.Error())
	}
}
