package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"mindloop/internal/identity"
	"mindloop/internal/web"
)

func (c *CLI) newWebCmd() *cobra.Command {
	var portP int
	var hostP string
	var tokenP string
	var rootP string
	var viewerDirP string
	var noBuildP bool
	cmd := &cobra.Command{
		Use:   "web",
		Short: "启动仪表盘（后端 + 浏览器视图）",
		Long: `启动 mindloop 的仪表盘：纯 Go 后端读 disk 渲染身份/轨迹/
对话/记忆/健康，前端复用 headlong viewer（已随项目复制到 web/static/）。

默认绑 127.0.0.1:8080，仅本机可访问；要对外请显式 --host。
可选 bearer 鉴权：--token=XXX 或环境变量 MINDLOOP_WEB_TOKEN。`,
		Example: `  mindloop web
  mindloop web --port 9090
  mindloop web --host 0.0.0.0 --token $MINDLOOP_TOKEN
  MINDLOOP_HOME=~/mindloop-state mindloop web`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.runWeb(&webCfg{
				port:      portP,
				host:      hostP,
				token:     tokenP,
				root:      rootP,
				viewerDir: viewerDirP,
				noBuild:   noBuildP,
			})
		},
	}
	fs := cmd.Flags()
	fs.IntVar(&portP, "port", 8080, "监听端口")
	fs.StringVar(&hostP, "host", "127.0.0.1", "监听地址（0.0.0.0 = 对外可见）")
	fs.StringVar(&tokenP, "token", os.Getenv("MINDLOOP_WEB_TOKEN"), "可选 bearer token；非空时所有 /api/* 必须带 Authorization: Bearer <token>")
	fs.StringVar(&rootP, "root", "", "身份父目录；默认 MINDLOOP_HOME")
	fs.StringVar(&viewerDirP, "viewer-dir", "", "viewer 静态文件根；默认 <项目>/web/static")
	fs.BoolVar(&noBuildP, "no-build", false, "跳过 viewer 构建（开发模式：b/cd web/static && bun run dev）")
	return cmd
}

// webCfg 聚合 web 命令解析出来的运行参数。
type webCfg struct {
	port      int
	host      string
	token     string
	root      string
	viewerDir string
	noBuild   bool
}

func (c *CLI) runWeb(cfg *webCfg) error {
	root := cfg.root
	if root == "" {
		root = identity.Home()
	}
	viewerDir := cfg.viewerDir
	if viewerDir == "" {
		// <项目>/web/static 已作为 viewer 静态文件根；与可执行
		// 文件同级。优先用 MINDLOOP_VIEWER_DIR 显式覆盖。
		if env := os.Getenv("MINDLOOP_VIEWER_DIR"); env != "" {
			viewerDir = env
		} else {
			viewerDir = filepath.Join(projectRoot(), "web", "static")
		}
	}
	if !cfg.noBuild {
		if err := ensureViewerBuilt(viewerDir); err != nil {
			fmt.Fprintln(c.stderr, "⚠ viewer 构建/检查失败:", err)
			fmt.Fprintln(c.stderr, "  （首启会要求安装 bun 与运行构建；用 --no-build 跳过）")
		}
	}

	srv, err := web.New(web.Config{
		Root:      root,
		ViewerDir: viewerDir,
		Addr:      fmt.Sprintf("%s:%d", cfg.host, cfg.port),
		Token:     cfg.token,
	})
	if err != nil {
		return c.fail(err)
	}
	fmt.Fprintf(c.stderr, "mindloop 仪表盘：%s  → http://%s/\n", srv.Addr(), srv.Addr())
	fmt.Fprintf(c.stderr, "  身份根: %s\n", root)
	fmt.Fprintf(c.stderr, "  viewer: %s\n", viewerDir)
	if cfg.token != "" {
		fmt.Fprintf(c.stderr, "  鉴权:   Bearer token 已启用\n")
	}
	fmt.Fprintln(c.stderr, "  Ctrl+C 停机")
	ctx, cancel := context.WithCancel(c.ctx)
	defer cancel()
	return srv.Serve(ctx)
}

// projectRoot 尽力推断项目根：从可执行路径向上找。
func projectRoot() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	dir := filepath.Dir(exe)
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "."
}

// ensureViewerBuilt 简单探测 viewer 静态目录是否"准备好了"。
// 生产：viewer/dist 下应有 assets/index.js 等。本轮不强制构建——
// 跳过 build 链让 mindloop 保持零 npm/bun 依赖；用户用 --dev
// 或外部 npm run build 自行处理。探测到 404 时 SPA fallback 会给
// 出 hint，但仍能用于 API 测试。
func ensureViewerBuilt(viewerDir string) error {
	indexPath := filepath.Join(viewerDir, "index.html")
	if _, err := os.Stat(indexPath); err != nil {
		return fmt.Errorf("找不到 viewer index.html（%s）—— 跑一次 `cd web/static && bun install && bun run build` 或 --no-build 跳过", indexPath)
	}
	return nil
}

// ensureCancel 是被 c.ctx 启用的退场钩子——web.Serve 是阻塞调用，
// 确保 Ctrl+C 触发 srv.Shutdown（见 server.go 的 Serve 实现）。
var _ = time.Second
