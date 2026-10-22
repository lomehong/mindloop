// 检测升级三端点（internal/update 的 web 门面）：
//
//	GET  /api/update/latest   最近一次检测读数（后台定时 + 前端轮询）
//	POST /api/update/check    立即检测（手动「检查更新」按钮）
//	POST /api/update/install  执行升级（操作员显式点击后才到这里）
//
// 功能关闭（Config.Update 为 nil）时端点 503 如实说明——这是
// git 源码部署的合理形态（升级=git pull）。升级动作是操作员特权：
// 检测自动、安装必须显式点击（本文件的 install 端点是唯一入口）。
package web

import (
	"net/http"

	"github.com/lomehong/mindloop/internal/update"
)

func (s *Server) updateDisabled(w http.ResponseWriter) {
	writeError(w, http.StatusServiceUnavailable, "检测升级未启用（源码部署请 git pull 后重装）")
}

// handleUpdateLatest 返回最近一次检测读数；从未检测过时就地触发
// 一次（首次打开页面也能立刻拿到读数）。
func (s *Server) handleUpdateLatest(w http.ResponseWriter, r *http.Request) {
	if s.updateChecker == nil {
		s.updateDisabled(w)
		return
	}
	info := s.updateChecker.Snapshot()
	if info.CheckedAt.IsZero() {
		info = s.updateChecker.CheckNow(r.Context())
	}
	writeJSON(w, 200, info)
}

// handleUpdateCheck 立即检测（绕过缓存周期）。
func (s *Server) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	if s.updateChecker == nil {
		s.updateDisabled(w)
		return
	}
	writeJSON(w, 200, s.updateChecker.CheckNow(r.Context()))
}

// handleUpdateInstall 执行升级。检测读数缺席或上次失败时先就地检测
// 一次——操作员点了「升级」不该被"请先检查"挡回去。
func (s *Server) handleUpdateInstall(w http.ResponseWriter, r *http.Request) {
	if s.updateChecker == nil {
		s.updateDisabled(w)
		return
	}
	info := s.updateChecker.Snapshot()
	if info.CheckedAt.IsZero() || info.Error != "" {
		info = s.updateChecker.CheckNow(r.Context())
	}
	if info.Error != "" {
		writeError(w, http.StatusBadGateway, "检测失败："+info.Error)
		return
	}
	if !info.HasUpdate {
		writeJSON(w, 200, map[string]any{
			"mode": "none", "detail": "已是最新版本（" + info.Current + "）",
		})
		return
	}
	rel, err := s.updateChecker.LatestRelease(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	env := update.CurrentEnvironment(s.updateChecker.Version())
	result, err := update.Install(r.Context(), nil, rel, env)
	if err != nil {
		// 升级是操作员特权动作，成败都向操作员如实呈现。
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"mode":     result.Mode,
		"replaced": result.Replaced,
		"detail":   result.Detail,
		"from":     env.Version,
		"to":       info.LatestTag,
	})
}
