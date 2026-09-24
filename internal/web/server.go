// Package web 提供 mindloop 的仪表盘后端：纯 Go、net/http 标准库
// ServeMux、绑 127.0.0.1、读 disk 提供身份/轨迹/记忆/对话/健康
// 视图、与 headlong viewer 静态文件共存。
//
// 设计原则（来自 headlong-web 的 observations，本仓库不假装原创）：
//   - filesystem-as-database：后端不缓存轨迹，每次请求从 disk 读；
//     心智运行时的写入是新鲜的，监控无需轮询数据库。
//   - 路径白名单：身份名走 resolve，不接受 .. 路径穿越；身份
//     目录范围限于用户根（默认 ~/.mindloop/identities）。
//   - API 形态对齐 headlong viewer：viewer 复用了 headlong 的类型
//     约定，本包必须产出兼容字段（见 handlers_test.go）。
package web

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Config 描述仪表盘启动参数。Root 是身份目录本身（里面直接是
// 各个身份子目录；默认 identity.Home()，即 ~/.mindloop/identities
// ——扫描与默认值必须同一语义，否则列表恒空）；ViewerDir 是
// viewer 构建产物的静态文件根；Addr 是监听地址，默认
// 127.0.0.1:8080；Token 非空时所有 /api/* 请求必须带
// Authorization: Bearer <token>。
type Config struct {
	Root      string
	ViewerDir string
	Addr      string
	Token     string
}

// Server 是仪表盘 HTTP 服务。
type Server struct {
	cfg Config
	mux *http.ServeMux
	srv *http.Server
}

// New 创建仪表盘服务并装载所有路由。安全默认值：
//   - Root 未配置则取 identity.Home() 的父目录（identities/ 的父）
//   - ViewerDir 空时 SPA 路由不返回 HTML（前后端不匹配）
//   - Token 非空时 bearer 鉴权强制启用
//   - 绑定非回环地址（如 0.0.0.0）必须显式配 Token——无鉴权的
//     写端点（启停心智、导入归档、killall）暴露到局域网等价于
//     把机器交给同网段的任何人
//   - /api/* 经 sameOrigin 守卫：跨源请求与回环部署下的非回环
//     Host 一律 403（防 CSRF 与 DNS rebinding）
func New(cfg Config) (*Server, error) {
	if cfg.Root == "" {
		return nil, errors.New("web: Root is required")
	}
	if cfg.Addr == "" {
		cfg.Addr = "127.0.0.1:8080"
	}
	if !isLoopbackAddr(cfg.Addr) && cfg.Token == "" {
		return nil, errors.New("web: 绑定非回环地址（" + cfg.Addr + "）必须设置 Token（--token 或 MINDLOOP_WEB_TOKEN）")
	}
	s := &Server{cfg: cfg, mux: http.NewServeMux()}
	s.routes()
	s.srv = &http.Server{
		Addr:              cfg.Addr,
		Handler:           s,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	return s, nil
}

// Serve 启动服务，阻塞直到 ctx 取消或 server 失败。
func (s *Server) Serve(ctx context.Context) error {
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.srv.Shutdown(shutdown)
	}()
	if err := s.srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("web: %w", err)
	}
	return nil
}

// ServeHTTP 实现 http.Handler；/api/* 先过同源守卫再进 mux。
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "跨源请求被拒绝（Origin/Host 校验失败）")
		return
	}
	s.mux.ServeHTTP(w, r)
}

// sameOrigin 防两类浏览器驱动的攻击：
//   - CSRF：恶意网页用 <img src> / no-cors fetch 打我们的写端点。
//     这类请求若带 Origin 头，其主机必与 Host 不同——拦下。
//   - DNS rebinding：攻击者把域名解析到 127.0.0.1 诱导浏览器访问。
//     顶层导航请求没有 Origin，但 Host 是攻击者域名——回环部署下
//     Host 必须是回环名，拦下。
// 非回环部署（局域网可达）无法用 Host 判别合法来源，由 New() 强制
// 的 Token 兜底。静态资源与 SPA 不校验（读路径无副作用）。
func (s *Server) sameOrigin(r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, "/api/") {
		return true
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		if err != nil || u.Host == "" || u.Host != r.Host {
			return false
		}
	}
	if !isLoopbackAddr(s.cfg.Addr) {
		return true
	}
	return isLoopbackHost(r.Host)
}

// hostOnly 剥掉 Host 头里的端口（兼容 IPv6 字面量）。
func hostOnly(host string) string {
	if strings.HasPrefix(host, "[") {
		if i := strings.Index(host, "]"); i >= 0 {
			return host[1:i]
		}
		return host
	}
	if i := strings.LastIndex(host, ":"); i >= 0 {
		return host[:i]
	}
	return host
}

func isLoopbackHost(host string) bool {
	switch strings.ToLower(hostOnly(host)) {
	case "127.0.0.1", "localhost", "::1":
		return true
	}
	return false
}

// isLoopbackAddr 报告监听地址是否只回环可达。":8080" 这类省略主机的
// 形式绑定全部接口——按非回环处理（宁可多要一次 Token）。
func isLoopbackAddr(addr string) bool {
	h, _, err := net.SplitHostPort(addr)
	if err != nil {
		h = addr
	}
	if h == "" {
		return false
	}
	return isLoopbackHost(h)
}

// Addr 暴露监听地址（测试与日志使用）。
func (s *Server) Addr() string { return s.cfg.Addr }

// Handler 暴露 mux（嵌入到 net/http/httptest 用）。
func (s *Server) Handler() http.Handler { return s.mux }
