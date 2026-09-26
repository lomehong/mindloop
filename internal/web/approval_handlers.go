package web

// /api/identities/{name}/approvals 执行授权控制面——等待批准的脚本
// 在 Web 上的唯一入口（与 CLI approve 共用 policy 文件控制面）：
//
//	GET  /approvals                 列出待批（正文、归属、风险）
//	POST /approvals/{hash}/approve  批准
//	POST /approvals/{hash}/deny     拒绝
//
// {hash} 可用唯一前缀（至少 4 位十六进制）。批准/拒绝只写决定文件，
// 等待方（policy.Gate 的轮询）消费后脚本才继续或终止——HTTP 层不
// 碰执行，也不推断状态。

import (
	"errors"
	"net/http"
	"strings"

	"mindloop/internal/identity"
	"mindloop/internal/policy"
)

// handleApprovals 按路径段分流审批控制面；方法路由与任务面同风格
// （写端点只认 POST——GET 触发决定是整类跨站问题的根）。
func (s *Server) handleApprovals(w http.ResponseWriter, r *http.Request, id *identity.Identity, rest []string) {
	dir := policy.Dir(id.Timeline.Dir)
	switch {
	case len(rest) == 0:
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		s.approvalList(w, dir)
	case len(rest) == 2 && (rest[1] == "approve" || rest[1] == "deny"):
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		full, err := policy.ResolveHash(dir, rest[0])
		if err != nil {
			writeApprovalError(w, err)
			return
		}
		if err := policy.Decide(dir, full, rest[1] == "approve"); err != nil {
			writeApprovalError(w, err)
			return
		}
		writeJSON(w, 200, map[string]string{"hash": full, "decision": rest[1]})
	default:
		writeError(w, 404, "未知子路径: approvals/"+strings.Join(rest, "/"))
	}
}

// approvalList 读控制面待批请求；空列表给 [] 而不是 null——前端
// 直接迭代。
func (s *Server) approvalList(w http.ResponseWriter, dir string) {
	pending, err := policy.ListPending(dir)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if pending == nil {
		pending = []policy.PendingRequest{}
	}
	writeJSON(w, 200, pending)
}

// writeApprovalError 把控制面错误映射为 HTTP 状态：非法哈希 400、
// 未命中 404、前缀歧义 409——决定必须有对象，猜测不是便利。
func writeApprovalError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, policy.ErrBadHash):
		writeError(w, 400, err.Error())
	case errors.Is(err, policy.ErrNoMatch):
		writeError(w, 404, err.Error())
	case errors.Is(err, policy.ErrAmbiguous):
		writeError(w, 409, err.Error())
	default:
		writeError(w, 500, err.Error())
	}
}
