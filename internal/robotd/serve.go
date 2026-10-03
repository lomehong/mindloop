// robotd 的进程入口：flag 解析 + stdio serve。主二进制
// （mindloop robotd）与独立二进制（cmd/robotd）共用。
package robotd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/lomehong/mindloop/internal/mcp"
)

// Options 是 serve 的装配参数（flag 层注入）。
type Options struct {
	ServerName   string
	Version      string
	WindowAllow  []string
	IdentityHint string // 审计行里标注服务的身份（可空）
}

// Run 解析选项并阻塞运行 stdio MCP 服务器；Ctrl-C / SIGTERM 优雅退出。
func Run(ctx context.Context, opt Options) error {
	if len(opt.WindowAllow) == 0 {
		fmt.Fprintln(os.Stderr, "[robotd] 观察模式启动（截屏/窗口/光标可用，动作拒绝）——授权动作加 --window-allow <标题子串>（可重复）")
	} else {
		fmt.Fprintf(os.Stderr, "[robotd] 动作已授权，窗口白名单: %v\n", opt.WindowAllow)
	}
	if opt.IdentityHint != "" {
		fmt.Fprintf(os.Stderr, "[robotd] 身份标注: %s\n", opt.IdentityHint)
	}
	srv := NewServer(opt.WindowAllow, nil)
	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return mcp.ServeStdio(ctx, opt.ServerName, opt.Version, srv.Tools())
}
