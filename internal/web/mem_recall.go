// 记忆召回台账端点（scene 之外的观测面）：GET
// /api/identities/{name}/mem-recall?limit=N——读 usage/mem-recall.jsonl
// 尾部，记忆页据此展示召回统计（命中数分布/注入率/跳过原因）。
package web

import (
	"net/http"
	"strconv"

	"github.com/lomehong/mindloop/internal/identity"
	"github.com/lomehong/mindloop/internal/obs"
)

func (s *Server) handleMemRecall(w http.ResponseWriter, r *http.Request, id *identity.Identity) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "mem-recall 只接受 GET")
		return
	}
	limit := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	rows, err := obs.LoadRecall(id.Timeline.Dir, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "召回台账读取失败: "+err.Error())
		return
	}
	if rows == nil {
		rows = []obs.RecallRow{}
	}
	writeJSON(w, http.StatusOK, rows)
}
