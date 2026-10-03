package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/lomehong/mindloop/internal/system"
	"github.com/lomehong/mindloop/internal/traj"
)

// system 命令组是系统宿主的运维入口：system run 是唯一的常驻宿主
// （按 system.json 声明监督 web/心智/渠道桥全部能力子进程，崩溃
// 自愈、配置热加载）——"获得功能"不再需要逐条运行命令。
func (c *CLI) newSystemCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "system",
		Short: "系统宿主：一份配置托管全部能力（web/心智/渠道桥），崩溃自愈",
		Long: `系统宿主按 ` + "`<状态根>/system.json`" + ` 的声明监督全部能力子进程：
退出即指数退避重启（1s→60s）、配置变更 5 秒内热加载增停启、宿主
退出连带收割全部孩子（Windows Job Object）。

配置缺省按现状推导：web 开在本机回环 8080、全部身份开心智、有企微
凭据的身份开企微桥——零配置即用。能力增减改 system.json（或仪表盘
/system 页），不需要运行任何命令。

推荐以服务常驻：mindloop service install --host`,
	}
	cmd.AddCommand(
		c.newSystemRunCmd(),
		c.newSystemStatusCmd(),
	)
	return cmd
}

func (c *CLI) newSystemRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run",
		Short: "运行系统宿主（前台；常驻请配 service install --host）",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			home := traj.Home()
			exe, err := os.Executable()
			if err != nil {
				return c.fail(err)
			}
			cfg, err := system.Load(home)
			if err != nil {
				return c.fail(err)
			}
			logger := func(format string, args ...any) {
				fmt.Fprintf(c.stderr, "· "+format+"\n", args...)
			}
			// 缺失配置先落一份推导缺省（可读、可改——配置即数据，
			// 用户看得见才改得动）。
			if _, statErr := os.Stat(system.Path(home)); os.IsNotExist(statErr) {
				cfg = system.Defaults()
				_ = system.Save(home, cfg)
				logger("已生成缺省配置 %s", system.Path(home))
			}
			// 宿主单实例锁：两个宿主监督同一 home 会互抢孩子（冒烟
			// 实证）——第二个宿主拒绝启动。
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			lockPath := home + string(os.PathSeparator) + "system-host.lock"
			release, err := traj.AcquireDirLock(ctx, lockPath, 2*time.Second)
			if err != nil {
				return c.fail(fmt.Errorf("系统宿主已在运行（system status 查看；勿并行开第二个宿主）"))
			}
			defer release()
			sup := system.NewSupervisor(home, exe, cfg, logger)
			go func() {
				// 状态投影周期性刷新（子进程稳定运行时也要保鲜）。
				t := time.NewTicker(30 * time.Second)
				defer t.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-t.C:
						sup.Project()
					}
				}
			}()
			err = sup.Run(ctx)
			if err != nil && ctx.Err() != nil {
				fmt.Fprintln(c.stderr, "系统宿主停机。")
				return nil
			}
			return err
		},
	}
}

// newSystemStatusCmd 读状态投影（宿主不在跑时如实说明）。
func (c *CLI) newSystemStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "系统宿主状态（各能力子进程的运行/重启/最近退出）",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			home := traj.Home()
			data, err := os.ReadFile(system.StatusPath(home))
			if err != nil {
				fmt.Fprintln(c.stdout, "宿主没有在运行（无状态投影）——mindloop system run 或 service install --host 启动。")
				return nil
			}
			var st struct {
				Updated  string `json:"updated"`
				Children []struct {
					Name     string `json:"name"`
					Running  bool   `json:"running"`
					PID      int    `json:"pid"`
					Restarts int    `json:"restarts"`
					LastExit string `json:"last_exit"`
				} `json:"children"`
			}
			if json.Unmarshal(data, &st) != nil {
				return c.fail(fmt.Errorf("状态投影损坏（%s），宿主下一次投影会覆盖", system.StatusPath(home)))
			}
			fmt.Fprintf(c.stdout, "宿主状态（更新于 %s）\n", st.Updated)
			for _, ch := range st.Children {
				state := "运行中"
				if !ch.Running {
					state = "已停止"
				}
				line := fmt.Sprintf("  %-14s %s", ch.Name, state)
				if ch.PID != 0 {
					line += fmt.Sprintf("（pid %d）", ch.PID)
				}
				if ch.Restarts > 0 {
					line += fmt.Sprintf("，重启 %d 次", ch.Restarts)
				}
				if ch.LastExit != "" {
					line += fmt.Sprintf("，上次退出: %s", ch.LastExit)
				}
				fmt.Fprintln(c.stdout, line)
			}
			return nil
		},
	}
}
