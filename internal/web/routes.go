package web

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
)

// routes 装载所有 API 端点 + 静态 viewer 文件 + catch-all。
//
// 端点形态对齐 headlong viewer 的 lib/api.ts——viewer 是从 headlong
// 复制来的，它的 fetch 路径就是本包的 API 契约。已实现：config /
// identities / mindlog(+search) / step / runs command / chat /
// memories / thinkers / health。其余端点（push/openrouter/usage/…）
// 会在被 viewer 请求时得到 404——前端对应功能区显示空态。
func (s *Server) routes() {
	// /api/* —— API 端点
	s.mux.HandleFunc("/api/config", s.withAuth(s.handleConfig))
	s.mux.HandleFunc("/api/identities", s.withAuth(s.handleIdentities))
	s.mux.HandleFunc("/api/identities/", s.withAuth(s.routeIdentity)) // /api/identities/{name}/...
	s.mux.HandleFunc("/api/health", s.withAuth(s.handleHealth))

	// /assets/* 与 /favicon.ico —— viewer 构建产物
	if s.cfg.ViewerDir != "" {
		s.mux.Handle("/assets/", http.StripPrefix("/assets/",
			http.FileServer(http.Dir(filepath.Join(s.cfg.ViewerDir, "assets")))))
		s.mux.HandleFunc("/favicon.ico", s.favicon)
	}

	// SPA catch-all：把未知路径回退到 index.html，由 react-router 客户端路由兜底。
	if s.cfg.ViewerDir != "" {
		indexPath := filepath.Join(s.cfg.ViewerDir, "index.html")
		s.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				http.NotFound(w, r)
				return
			}
			http.ServeFile(w, r, indexPath)
		})
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

// favicon 直接回 204（viewer 默认 favicon 不影响功能；占位避免
// 浏览器重复请求 404）。
func (s *Server) favicon(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

// writeJSON / writeError 是回复类型的小把柄，被 handlers.go 共用。
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"detail": map[string]any{"message": msg, "code": code}})
}
