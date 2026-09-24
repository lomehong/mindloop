// Command mindloop 是 mindloop 的入口。这里只做三件事：接管
// Ctrl+C 为 context 取消、把命令行交给 internal/cli、透传退出码。
// 命令树基于 spf13/cobra，全部实现见 internal/cli。
package main

import (
	"context"
	"os"
	"os/signal"

	"mindloop/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(cli.Execute(ctx, os.Args[1:], os.Stdout, os.Stderr))
}
