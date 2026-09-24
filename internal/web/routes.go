package web

import (
	"fmt"
	"net/http"
	"strings"

	"mindloop/internal/identity"
)

// routes 装载所有 API 端点 + 静态 viewer 文件 + catch-all。
//
// 端点形态对齐 headlong viewer 的 lib/api.ts；v0.1 实现所有读取
// 与控制端点，外部服务（OpenRouter、push）返回空实现——viewer
// 那里走空态分支。
func (s *Server) routes() {
	// /api/* —— 根级 API
	s.mux.HandleFunc("/api/config", s.withAuth(s.handleConfig))
	s.mux.HandleFunc("/api/identities", s.withAuth(s.handleIdentities))
	s.mux.HandleFunc("/api/identities/", s.withAuth(s.routeIdentity)) // /api/identities/{name}/...
	s.mux.HandleFunc("/api/health", s.withAuth(s.handleHealth))
	// 导入是字面路径（比 /api/identities/ 子树更具体，mux 择优）。
	s.mux.HandleFunc("/api/export", s.withAuth(s.handleExport))
	s.mux.HandleFunc("/api/identities/import", s.withAuth(s.handleImport))

	// 全局端点
	s.mux.HandleFunc("/api/llm-health", s.withAuth(s.handleLlmHealthGlobal))
	s.mux.HandleFunc("/api/llm-health/probe", s.withAuth(s.handleLlmHealthProbe))
	s.mux.HandleFunc("/api/openrouter/models", s.withAuth(s.handleOpenRouterModels))
	s.mux.HandleFunc("/api/killall", s.withAuth(s.handleKillall))
	s.mux.HandleFunc("/api/update", s.withAuth(s.handleSelfUpdate))
	s.mux.HandleFunc("/api/push/key", s.withAuth(s.handleEmpty404))
	s.mux.HandleFunc("/api/push/subscriptions", s.withAuth(s.handleEmpty404))
	s.mux.HandleFunc("/api/push/unsubscribe", s.withAuth(s.handleEmpty404))

	// /assets/* 与 /favicon.ico —— viewer 构建产物
	if s.cfg.ViewerDir != "" {
		s.mux.Handle("/assets/", http.StripPrefix("/assets/",
			http.FileServer(http.Dir(s.cfg.ViewerDir+"/assets"))))
		s.mux.HandleFunc("/favicon.ico", s.favicon)
		s.mux.HandleFunc("/fonts/", func(w http.ResponseWriter, r *http.Request) {
			http.StripPrefix("/fonts/", http.FileServer(http.Dir(s.cfg.ViewerDir+"/fonts"))).ServeHTTP(w, r)
		})
	}

	// SPA catch-all
	if s.cfg.ViewerDir != "" {
		indexPath := s.cfg.ViewerDir + "/index.html"
		s.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				http.NotFound(w, r)
				return
			}
			http.ServeFile(w, r, indexPath)
		})
	}
}

// routeThinkers 调度 viewers thinkers/* 多层路径：
//
//	/api/identities/{n}/thinkers                       → handleThinkers
//	/api/identities/{n}/thinkers/start                 → 全部启动
//	/api/identities/{n}/thinkers/stop                  → 全部停止
//	/api/identities/{n}/thinkers/{name}/enable|disable → 切换某 thinker
//	/api/identities/{n}/thinkers/{name}/step           → 给某 thinker 一次手动唤醒
func (s *Server) routeThinkers(w http.ResponseWriter, r *http.Request, id *identity.Identity, rest []string) {
	if len(rest) == 0 {
		s.handleThinkers(w, r, id, nil)
		return
	}
	switch rest[0] {
	case "start":
		s.handleThinkersAll(w, r, id, []string{"start"})
	case "stop":
		s.handleThinkersAll(w, r, id, []string{"stop"})
	default:
		// rest[0]=thinker name，rest[1]=enable|disable|step
		if len(rest) >= 2 && rest[1] == "step" {
			s.handleThinkerStep(w, r, id, rest[:1])
			return
		}
		if len(rest) >= 2 && (rest[1] == "enable" || rest[1] == "disable") {
			s.handleThinkerToggle(w, r, id, rest[:2])
			return
		}
		writeError(w, 404, fmt.Sprintf("未知子路径: thinkers/%s", strings.Join(rest, "/")))
	}
}

// withAuth 是 Token 鉴权中间件。Token 为空则放行（默认本机安全）；
// 非空则所有 /api/* 必须带 Authorization: Bearer <token>。
func (s *Server) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.Token == "" {
			next(w, r)
			return
		}
		const prefix = "Bearer "
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, prefix) || auth[len(prefix):] != s.cfg.Token {
			writeError(w, http.StatusUnauthorized, "未认证（需要 Authorization: Bearer <token>）")
			return
		}
		next(w, r)
	}
}

// handleEmpty404 用于未实现的推送等端点——viewer 拿到 404 走空态分支。
func (s *Server) handleEmpty404(w http.ResponseWriter, _ *http.Request) {
	writeError(w, 404, "该功能未启用")
}

// favicon 直接回 204（避免浏览器重复请求 404）。
func (s *Server) favicon(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}
