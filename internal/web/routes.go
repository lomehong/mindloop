package web

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"mindloop/internal/identity"
)

// routes 装载所有 API 端点 + 静态 viewer 文件 + catch-all。
//
// 写端点用 Go 1.22 方法模式注册（"POST /api/…"）：方法不匹配时
// mux 自动回 405（带 Allow 头）——从根上杜绝"GET 触发写操作"
// 被 <img src> 一类跨站请求驱动的整类问题。读端点不标方法（浏览器
// 导航、下载链接都走 GET，标了反而拒掉合法变体）。
//
// 端点形态对齐 headlong viewer 的 lib/api.ts。
func (s *Server) routes() {
	// /api/* —— 根级 API
	s.mux.HandleFunc("GET /api/config", s.withAuth(s.handleConfig))
	s.mux.HandleFunc("GET /api/identities", s.withAuth(s.handleIdentities))
	s.mux.HandleFunc("POST /api/identities", s.withAuth(s.handleIdentityCreate))
	s.mux.HandleFunc("/api/identities/", s.withAuth(s.routeIdentity)) // /api/identities/{name}/...
	s.mux.HandleFunc("GET /api/health", s.withAuth(s.handleHealth))
	// 导入是字面路径（比 /api/identities/ 子树更具体，mux 择优）。
	s.mux.HandleFunc("GET /api/export", s.withAuth(s.handleExport))
	s.mux.HandleFunc("POST /api/identities/import", s.withAuth(s.handleImport))

	// 单身份导出任务的全局端点（config 页的导出标签）。
	s.mux.HandleFunc("GET /api/export-jobs/{jobID}", s.withAuth(s.handleExportJobGet))
	s.mux.HandleFunc("DELETE /api/export-jobs/{jobID}", s.withAuth(s.handleExportJobDelete))
	s.mux.HandleFunc("GET /api/export-jobs/{jobID}/download", s.withAuth(s.handleExportJobDownload))

	// 全局端点
	s.mux.HandleFunc("GET /api/llm-health", s.withAuth(s.handleLlmHealthGlobal))
	s.mux.HandleFunc("POST /api/llm-health/probe", s.withAuth(s.handleLlmHealthProbe))
	s.mux.HandleFunc("GET /api/openrouter/models", s.withAuth(s.handleOpenRouterModels))
	s.mux.HandleFunc("POST /api/killall", s.withAuth(s.handleKillall))
	s.mux.HandleFunc("POST /api/update", s.withAuth(s.handleSelfUpdate))
	s.mux.HandleFunc("GET /api/push/key", s.withAuth(s.handleEmpty404))
	s.mux.HandleFunc("POST /api/push/subscriptions", s.withAuth(s.handleEmpty404))
	s.mux.HandleFunc("POST /api/push/unsubscribe", s.withAuth(s.handleEmpty404))

	// /assets/* 与 /favicon.ico —— viewer 构建产物
	if s.cfg.ViewerDir != "" {
		// 资产文件名带内容哈希，可永久缓存。
		assets := http.StripPrefix("/assets/",
			http.FileServer(http.Dir(s.cfg.ViewerDir+"/assets")))
		s.mux.Handle("/assets/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			assets.ServeHTTP(w, r)
		}))
		s.mux.HandleFunc("/favicon.ico", s.favicon)
		s.mux.HandleFunc("/fonts/", func(w http.ResponseWriter, r *http.Request) {
			http.StripPrefix("/fonts/", http.FileServer(http.Dir(s.cfg.ViewerDir+"/fonts"))).ServeHTTP(w, r)
		})
		s.mux.Handle("/icons/", http.StripPrefix("/icons/",
			http.FileServer(http.Dir(s.cfg.ViewerDir+"/icons"))))
		// PWA 辅助文件必须直出真实内容——落到 SPA catch-all 会拿到
		// index.html，service worker 注册静默失败。
		s.mux.HandleFunc("/sw.js", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFile(w, r, s.cfg.ViewerDir+"/sw.js")
		})
		s.mux.HandleFunc("/manifest.webmanifest", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFile(w, r, s.cfg.ViewerDir+"/manifest.webmanifest")
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
			// index.html 是部署的指针：永远重新校验，否则浏览器
			// 启发式缓存会让旧构建在新部署后继续存活。
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFile(w, r, indexPath)
		})
	}
}

// requireMethod 在子路径手工路由里强制 HTTP 方法（子树是通配注册，
// 方法细化要到 handler 内部才能做）。方法不符写 405 + Allow。
func requireMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method == method {
		return true
	}
	w.Header().Set("Allow", method)
	writeError(w, http.StatusMethodNotAllowed, "该方法只接受 "+method)
	return false
}

// routeThinkers 调度 viewers thinkers/* 多层路径（写端点全部只收
// POST——thinkers/start 曾因不分方法被 GET 触发子进程拉起）：
//
//	/api/identities/{n}/thinkers                       → handleThinkers（读）
//	/api/identities/{n}/thinkers/start                 → 全部启动（POST）
//	/api/identities/{n}/thinkers/stop                  → 全部停止（POST）
//	/api/identities/{n}/thinkers/{name}/enable|disable → 切换某 thinker（POST）
//	/api/identities/{n}/thinkers/{name}/step           → 给某 thinker 一次手动唤醒（POST）
func (s *Server) routeThinkers(w http.ResponseWriter, r *http.Request, id *identity.Identity, rest []string) {
	if len(rest) == 0 {
		s.handleThinkers(w, r, id, nil)
		return
	}
	switch rest[0] {
	case "start":
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		s.handleThinkersAll(w, r, id, "start")
	case "stop":
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		s.handleThinkersAll(w, r, id, "stop")
	default:
		// rest[0]=thinker name，rest[1]=enable|disable|step
		if len(rest) >= 2 && rest[1] == "step" {
			if !requireMethod(w, r, http.MethodPost) {
				return
			}
			if !validThinkerPathParam(w, rest[0]) {
				return
			}
			s.handleThinkerStep(w, r, id, rest[0])
			return
		}
		if len(rest) >= 2 && (rest[1] == "enable" || rest[1] == "disable") {
			if !requireMethod(w, r, http.MethodPost) {
				return
			}
			if !validThinkerPathParam(w, rest[0]) {
				return
			}
			s.handleThinkerToggle(w, r, id, rest[0], rest[1] == "enable")
			return
		}
		writeError(w, 404, "未知子路径: thinkers/"+strings.Join(rest, "/"))
	}
}

// validThinkerPathParam 校验 URL 段里的 thinker 名并直接写 400。
// mind.SignalWake 用它拼 wake.<name> 文件名，Windows 下 \ 是路径
// 分隔符而 mux 的 cleanPath 不消化它——必须在这里白名单拦下。
func validThinkerPathParam(w http.ResponseWriter, name string) bool {
	if name == "" || strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		writeError(w, 400, "非法 thinker 名: "+name)
		return false
	}
	return true
}

// withAuth 是 Token 鉴权中间件。Token 为空则放行（默认本机安全，
// 且 New() 强制非回环绑定必须配 Token）；非空则所有 /api/* 必须带
// Authorization: Bearer <token>。比较用 constant-time（先比长度）：
// Token 是局域网暴露场景的唯一防线，不能给时序侧信道留缝。
func (s *Server) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.Token == "" {
			next(w, r)
			return
		}
		const prefix = "Bearer "
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, prefix) ||
			len(auth)-len(prefix) != len(s.cfg.Token) ||
			subtle.ConstantTimeCompare([]byte(auth[len(prefix):]), []byte(s.cfg.Token)) != 1 {
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
