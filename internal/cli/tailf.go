package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"mindloop/internal/traj"
)

// cmdTailf 是 feeder 原语：跟踪一条轨迹，每个步骤写完即打印。
// 调度器的 tail feeder 就是这个循环再各加一个进程。
func (c *CLI) newTailfCmd() *cobra.Command {
	var interval time.Duration
	cmd := &cobra.Command{
		Use:   "tailf <traj>",
		Short: "实时跟踪轨迹（feeder 原语）",
		Args:  exactArgs(1, "用法: mindloop tailf <traj> [-i DURATION]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			t, err := traj.Load(args[0])
			if err != nil {
				return c.fail(err)
			}
			cursor := traj.NewCursor(t.Path)
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			fmt.Fprintf(c.stderr, "正在跟踪 %s（ctrl+c 停止）\n", t.Path)
			for {
				select {
				case <-c.ctx.Done():
					return nil
				case <-ticker.C:
					steps, err := cursor.ReadNew()
					if err != nil {
						return c.fail(err)
					}
					for _, s := range steps {
						printJSONL(c.stdout, s)
					}
				}
			}
		},
	}
	cmd.Flags().DurationVarP(&interval, "interval", "i", 200*time.Millisecond, "轮询间隔")
	return cmd
}
