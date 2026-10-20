// Command mindloop 是 mindloop 的入口。这里只做三件事：接管
// Ctrl+C 为 context 取消、把命令行交给 internal/cli、透传退出码。
// 命令树基于 spf13/cobra，全部实现见 internal/cli。
//
// 青鸟（Nuwa 采集 SDK v0.2）挂载五挂点中的两类：
//   - panic：捕获 → 同步上报 → 原样重抛（采集不改变失败语义）；
//   - 非 0 退出（Ctrl+C 的 130 除外）：Error 上报 + flush 后退出——
//     启动失败/运行失败不是 panic，但同样值得进自愈流水线。
//
// 服务端地址用 QINGNIAO_ENDPOINT / QINGNIAO_TOKEN（兼容 NUWA_* 旧键）。
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/lomehong/qingniao-go"

	"github.com/lomehong/mindloop/internal/cli"
)

func main() {
	q := qingniao.NewFromEnv(qingniao.Meta{
		Repo:    "github.com/lomehong/mindloop",
		Service: "mindloop",
	})
	defer q.Close(context.Background())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	defer q.Capture(func() {
		code := cli.Execute(ctx, os.Args[1:], os.Stdout, os.Stderr)
		if code != 0 && code != 130 {
			// 失败自检语义：非 0 退出（用户中断除外）。Error 进异步
			// 队列，Close flush 后按原退出码退出（保留进程语义）。
			q.Error("mindloop exited with error", fmt.Errorf("exit code %d", code))
			q.Close(context.Background())
		}
		os.Exit(code)
	})
}
