package web

// system_api.go — 统一配置面的后端（docs/designs/system.md §3）：
//   GET/PUT /api/system            能力总开关（system.json 的读/写；
//                                  宿主 5 秒内热加载增停启）
//   GET  /api/system/status        宿主状态投影（孩子运行/重启/退出）
// 感官管理在 identities 路由下（sensors_api.go）。
//
// 红线沿用控制面既有约定：写端点只认 PUT/POST（方法路由强制），
// withAuth + sameOrigin 全套生效——这是配置面，不是匿名开关。

import (
	"encoding/json"
	"net/http"
	"os"

	"github.com/lomehong/mindloop/internal/system"
	"github.com/lomehong/mindloop/internal/traj"
)

// readFileIfExists 读文件（不存在返回 err——调用方区分兜底）。
func readFileIfExists(path string) ([]byte, error) {
	return os.ReadFile(path)
}

func (s *Server) registerSystemRoutes() {
	s.mux.HandleFunc("GET /api/system", s.withAuth(s.handleSystemGet))
	s.mux.HandleFunc("PUT /api/system", s.withAuth(s.handleSystemPut))
	s.mux.HandleFunc("GET /api/system/status", s.withAuth(s.handleSystemStatus))
}

// handleSystemGet 读 system.json + 状态投影（一份响应给全配置面）。
func (s *Server) handleSystemGet(w http.ResponseWriter, _ *http.Request) {
	cfg, err := system.Load(traj.Home())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "system: "+err.Error())
		return
	}
	if len(cfg.Identities) == 0 && !cfg.Web.Enabled {
		cfg = system.Defaults() // 零配置 = 展示推导缺省（用户看到即改得动）
	}
	var status json.RawMessage
	if data, err := readFileIfExists(system.StatusPath(traj.Home())); err == nil {
		status = data
	} else {
		status = json.RawMessage(`{"children":[]}`)
	}
	writeJSON(w, 200, map[string]any{"config": cfg, "status": status})
}

// handleSystemPut 写 system.json（校验前置，宿主热加载兑现）。
func (s *Server) handleSystemPut(w http.ResponseWriter, r *http.Request) {
	var cfg system.Config
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		writeError(w, http.StatusBadRequest, "system: 配置不是合法 JSON")
		return
	}
	if err := system.Save(traj.Home(), &cfg); err != nil {
		writeError(w, http.StatusBadRequest, "system: "+err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"saved": true, "note": "宿主将在 5 秒内热加载（未运行宿主则下次 system run 生效）"})
}

// handleSystemStatus 只回状态投影。
func (s *Server) handleSystemStatus(w http.ResponseWriter, _ *http.Request) {
	data, err := readFileIfExists(system.StatusPath(traj.Home()))
	if err != nil {
		writeJSON(w, 200, map[string]any{"running": false})
		return
	}
	writeJSON(w, 200, json.RawMessage(data))
}
