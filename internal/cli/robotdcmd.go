package cli

import (
	"time"

	"github.com/spf13/cobra"

	"github.com/lomehong/mindloop/internal/robotd"
)

// newRobotdCmd 把"身"的 MCP server 挂进主二进制：与独立 cmd/robotd
// 共用同一实现与旗标面。单二进制部署时心智经 mcp.json 调它；独立
// 部署用 cmd/robotd（设计红线：动作面与心智进程隔离——两种入口
// 都以独立进程运行）。
func (c *CLI) newRobotdCmd() *cobra.Command {
	allow := &robotd.WindowAllow{}
	var minIdle time.Duration
	cmd := &cobra.Command{
		Use:   "robotd",
		Short: "屏幕的看与触：\"身\"的 stdio MCP 服务器（观察模式缺省，动作需 --window-allow）",
		Long: `在 stdin/stdout 上运行标准 MCP 服务器，暴露截屏、窗口、光标
（观察类）与聚焦、点击、输入（动作类）。

安全链：
  - 不给 --window-allow = 观察模式，动作一律拒绝；
  - 操作员空闲守卫：键鼠输入后 %v 内动作拒绝（不与操作员抢键盘）；
  - 动作的前台窗口必须命中白名单（窗口级 tripwire 谓词）；
  - 密码/凭据画面的前台窗口在任何模式下都拒绝（截屏也不给）；
  - 每个动作强制回读：动作后自动截屏作为证据返回，验证失败即报错。

接入 mcp.json（心智经沙箱调用）：
  mindloop mcp add robotd -- <mindloop.exe 绝对路径> robotd --window-allow 记事本

接入 Claude Desktop / Cursor：command 指到 mindloop（或独立 robotd
二进制），args 同上。`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return robotd.Run(c.ctx, robotd.Options{
				ServerName:  "robotd",
				Version:     Version,
				WindowAllow: allow.Values(),
				MinIdle:     minIdle,
			})
		},
	}
	cmd.Flags().Var(allow, "window-allow", "窗口标题白名单子串（可重复；不给则观察模式）")
	cmd.Flags().DurationVar(&minIdle, "min-idle", robotd.DefaultMinIdle, "操作员空闲阈值（键鼠输入后 N 秒内动作拒绝；0 关闭守卫）")
	return cmd
}
