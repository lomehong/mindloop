package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	viewerDir, viewerOK := resolveViewerDir(cfg.viewerDir)
	if cfg.viewerDir == "" && !viewerOK {
		// 未显式指定也找不到构建产物：API 仍可用（curl/集成），
		// 但不开浏览器——开一个 404 页面毫无意义。
		fmt.Fprintln(c.stderr, "⚠ 未找到 viewer 构建产物（web/static/build/client/index.html）")
		fmt.Fprintln(c.stderr, "  构建方法: cd web/static && bun install && bun run build")
		fmt.Fprintln(c.stderr, "  或用 --viewer-dir 指向已构建目录。API 仍将正常服务。")
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
	fmt.Fprintf(c.stderr, "  viewer: %s（就绪=%v）\n", viewerDir, viewerOK)
	if cfg.token != "" {
		fmt.Fprintf(c.stderr, "  鉴权:   Bearer token 已启用\n")
	}
	fmt.Fprintln(c.stderr, "  Ctrl+C 停机")

	// 自动打开浏览器（headlong `ada dash` 的同款体验），但仅在
	// viewer 就绪时——开一个 404 页毫无意义。等一小会确保端口
	// 已绑定；失败静默——用户可手动开。
	url := fmt.Sprintf("http://%s/", srv.Addr())
	if viewerOK {
		go func() {
			time.Sleep(600 * time.Millisecond)
			_ = openBrowser(url)
		}()
	}

	ctx, cancel := context.WithCancel(c.ctx)
	defer cancel()
	return srv.Serve(ctx)
}

// resolveViewerDir 按 旗标 > MINDLOOP_VIEWER_DIR > 从 CWD 与 exe
// 位置向上探测 的顺序解析 viewer 构建产物目录。第二个返回值表示
// 是否真的找到了 index.html。
func resolveViewerDir(flagValue string) (string, bool) {
	if flagValue != "" {
		ok := viewerReady(flagValue)
		return flagValue, ok
	}
	if env := os.Getenv("MINDLOOP_VIEWER_DIR"); env != "" {
		return env, viewerReady(env)
	}
	// 候选根（按优先级）：
	//  1. 编译期源码路径——go install 装到 go/bin 后，从任何目录
	//     运行都能找到它出生的项目（跨盘也有效；换机器则自然失效）。
	//  2. CWD 及其向上 4 级——项目目录里运行。
	//  3. exe 所在目录。
	var roots []string
	if _, sourceFile, _, ok := runtime.Caller(0); ok {
		roots = append(roots, filepath.Dir(filepath.Dir(filepath.Dir(sourceFile))))
	}
	if cwd, err := os.Getwd(); err == nil {
		roots = append(roots, cwd)
		d := cwd
		for i := 0; i < 4; i++ {
			parent := filepath.Dir(d)
			if parent == d {
				break
			}
			d = parent
			roots = append(roots, d)
		}
	}
	if exe, err := os.Executable(); err == nil {
		roots = append(roots, filepath.Dir(exe))
	}
	rel := filepath.Join("web", "static", "build", "client")
	for _, r := range roots {
		candidate := filepath.Join(r, rel)
		if viewerReady(candidate) {
			return candidate, true
		}
		// 兼容"直接指向源码目录"的旧用法：源码根没有 index.html，
		// 不算就绪。
	}
	return "", false
}

// viewerReady 报告目录里有没有构建产物入口 index.html。
func viewerReady(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "index.html"))
	return err == nil
}

// openBrowser 用系统默认浏览器打开 URL。Windows 走 rundll32（无
// 外部依赖）；其他平台退化为 xdg-open/open，失败静默。
func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}

// ensureCancel 是被 c.ctx 启用的退场钩子——web.Serve 是阻塞调用，
// 确保 Ctrl+C 触发 srv.Shutdown（见 server.go 的 Serve 实现）。
var _ = time.Second
