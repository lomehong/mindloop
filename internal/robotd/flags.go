// 命令行装配：旗标定义与进程入口的胶水。放包内而不是两个 main
// 里重复——mindloop robotd 子命令与 cmd/robotd 独立二进制的旗标
// 面必须逐字一致。
package robotd

import (
	"context"
	"flag"
	"fmt"
	"os"
)

// WindowAllow 是 --window-allow 的可重复 flag.Value：stdlib flag 与
// cobra/pflag 都接受（Type() 是 pflag 的额外要求，stdlib 不在乎）。
type WindowAllow struct {
	values []string
}

func (w *WindowAllow) String() string { return fmt.Sprint(w.values) }

func (w *WindowAllow) Type() string { return "stringSlice" }

func (w *WindowAllow) Set(s string) error {
	if s == "" {
		return fmt.Errorf("--window-allow 不能为空")
	}
	w.values = append(w.values, s)
	return nil
}

// Values 返回已收集的白名单（nil = 观察模式）。
func (w *WindowAllow) Values() []string { return w.values }

// AddFlags 把 robotd 的旗标注册到 stdlib flag set（独立二进制用；
// cobra 子命令直接用 WindowAccept 的 Var 形态）。
func AddFlags(fs *flag.FlagSet) func() Options {
	var allow WindowAllow
	fs.Var(&allow, "window-allow", "窗口标题白名单子串（可重复；不给则观察模式——只许看不许动）")
	return func() Options { return Options{WindowAllow: allow.Values()} }
}

// RunStandalone 是独立二进制（cmd/robotd）的入口：解析命令行、
// 运行 serve，失败带退出码。
func RunStandalone(ctx context.Context, serverName, version string) {
	opt := AddFlags(flag.CommandLine)()
	flag.Parse()
	if err := Run(ctx, Options{ServerName: serverName, Version: version, WindowAllow: opt.WindowAllow}); err != nil {
		fmt.Fprintln(os.Stderr, "robotd:", err)
		os.Exit(1)
	}
}
