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
	"net/http"
	"time"
)

// Config 描述仪表盘启动参数。Root 是身份根的父目录（默认
// MINDLOOP_HOME，等价于 ~/.mindloop）；ViewerDir 是 viewer 构建
// 产物的静态文件根；Addr 是监听地址，默认 127.0.0.1:8080；Token
// 非空时所有 /api/* 请求必须带 Authorization: Bearer <token>。
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
func New(cfg Config) (*Server, error) {
	if cfg.Root == "" {
		return nil, errors.New("web: Root is required")
	}
	if cfg.Addr == "" {
		cfg.Addr = "127.0.0.1:8080"
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

// ServeHTTP 实现 http.Handler；与 net/http 标准兼容。
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// Addr 暴露监听地址（测试与日志使用）。
func (s *Server) Addr() string { return s.cfg.Addr }

// Handler 暴露 mux（嵌入到 net/http/httptest 用）。
func (s *Server) Handler() http.Handler { return s.mux }
