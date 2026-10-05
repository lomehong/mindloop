package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/lomehong/mindloop/internal/childenv"
	"github.com/lomehong/mindloop/internal/connector/wecom"
	"github.com/lomehong/mindloop/internal/identity"
)

// connector 命令组是外部渠道桥的用户入口：bridge 是独立的轨迹读写
// 进程（不与心智共进程、不需要运行锁），凭据走身份 .env 的既有
// 机制，白名单缺失拒绝启动（fail-closed 红线）。
func (c *CLI) newConnectorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "connector",
		Short: "外部渠道桥：把企微等渠道接入身份对话流",
		Long: `外部渠道桥（bridge）：独立进程 tail 根轨迹，把身份的回复与
主动汇报投递到渠道，把渠道消息写入对话流。

路由即数据：一条消息归哪个渠道，只由轨迹上的 to 字段表达
（如 to=wecom:zhangsan）。凭据与白名单在身份 .env：

  WECOM_BOT_ID=ww1234567890
  WECOM_BOT_SECRET=xxx          # 长连接专用 Secret
  WECOM_ALLOW=zhangsan,lisi     # 白名单 userid，缺失拒绝启动

红线：无白名单不启动；白名单外消息不落轨迹；凭据永不下传沙箱。`,
	}
	cmd.AddCommand(
		c.newConnectorRunCmd(),
		c.newConnectorRunAllCmd(),
		c.newConnectorListCmd(),
	)
	return cmd
}

// connectorEnv 是渠道配置快照（botID/secret/allow 三元组）。
type connectorEnv struct {
	botID, secret string
	allow         []string
}

func (e connectorEnv) ready() bool {
	return e.botID != "" && e.secret != "" && len(e.allow) > 0
}

func connectorEnvEqual(a, b connectorEnv) bool {
	if a.botID != b.botID || a.secret != b.secret || len(a.allow) != len(b.allow) {
		return false
	}
	for i := range a.allow {
		if a.allow[i] != b.allow[i] {
			return false
		}
	}
	return true
}

// connectorEnv 读取渠道配置快照。热加载语义：三个渠道键**直接读
// 身份 .env 文件**（文件是渠道配置的真相源——仪表盘保存写的也是
// 它；config.LoadEnv 的"不覆盖进程环境"语义会让启动后的文件变更
// 永远不可见，实测踩过）。文件缺键才回落进程环境。
func (c *CLI) connectorEnv(id *identity.Identity) connectorEnv {
	fileKeys := parseEnvFileKeys(filepath.Join(id.Dir, ".env"))
	pick := func(key string) string {
		if v, ok := fileKeys[key]; ok {
			return v
		}
		return os.Getenv(key)
	}
	return connectorEnv{
		botID:  pick("WECOM_BOT_ID"),
		secret: pick("WECOM_BOT_SECRET"),
		allow:  childenv.List(pick("WECOM_ALLOW")),
	}
}

// parseEnvFileKeys 读 dotenv 子集（键=值行；注释与空行跳过，成对
// 引号剥离）。读失败返回空 map——文件缺失等同未配置。
func parseEnvFileKeys(path string) map[string]string {
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string]string{}
	}
	out := map[string]string{}
	for _, ln := range strings.Split(string(data), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		k, v, ok := strings.Cut(ln, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
			v = v[1 : len(v)-1]
		}
		if k != "" {
			out[k] = v
		}
	}
	return out
}

// connectorStateDir 是 bridge 的状态目录（出站泵游标）。
func connectorStateDir(id *identity.Identity) string {
	return filepath.Join(id.Dir, "connectors")
}

// wecomS0Downgrade 折算感知降档谓词（perception.md §5 Phase 2）：
// WECOM_S0_KEYWORDS（逗号分隔）——内容命中任一关键词的入站只沉淀
// 不叫醒（群通知、广播类）。未配置返回 nil = 全部照旧走 message。
func wecomS0Downgrade() func(from, content string) bool {
	kws := childenv.List(os.Getenv("WECOM_S0_KEYWORDS"))
	if len(kws) == 0 {
		return nil
	}
	for i := range kws {
		kws[i] = strings.ToLower(strings.TrimSpace(kws[i]))
	}
	return func(from, content string) bool {
		lc := strings.ToLower(content)
		for _, k := range kws {
			if k != "" && strings.Contains(lc, k) {
				return true
			}
		}
		return false
	}
}

// runWecom 运行企微 bridge，外层是配置热加载监督：每 5s 重读身份
// .env 的渠道键，白名单/凭据变更即取消当前桥并用新配置重建（旧
// 连接被单连接互踢语义自然让位）——改白名单不再需要手工重启。
// ErrKicked 终止（同 BotID 竞争，重启即让位，不与另一实例争抢）；
// 其余断线由 bridge 内部重连消化，不冒泡。
func (c *CLI) runWecom(id *identity.Identity) error {
	const reloadEvery = 5 * time.Second
	tick := time.NewTicker(reloadEvery)
	defer tick.Stop()
	env := c.connectorEnv(id)
	if !env.ready() {
		return c.fail(fmt.Errorf("企微 bridge 配置不完整（.env 需要 WECOM_BOT_ID / WECOM_BOT_SECRET / WECOM_ALLOW）"))
	}
	start := func(env connectorEnv) (*wecom.Bridge, error) {
		return wecom.New(wecom.Options{
			Timeline:  id.Timeline,
			Self:      id.Name,
			BotID:     env.botID,
			Secret:    env.secret,
			Allow:     env.allow,
			StateDir:  connectorStateDir(id),
			Downgrade: wecomS0Downgrade(),
			Logger: func(format string, args ...any) {
				fmt.Fprintf(c.stderr, "wecom: "+format+"\n", args...)
			},
		})
	}
	for {
		bridge, err := start(env)
		if err != nil {
			return c.fail(err)
		}
		fmt.Fprintf(c.stdout, "企微 bridge 已启动（身份 %s，白名单 %d 人；Ctrl+C 退出）\n", id.Name, len(env.allow))
		runCtx, cancel := context.WithCancel(c.ctx)
		done := make(chan error, 1)
		go func(b *wecom.Bridge) { done <- b.Run(runCtx) }(bridge)
		// 等待三选一：停机 / 桥自行退出 / 配置变更触发重建。
	waitLoop:
		for {
			select {
			case <-c.ctx.Done():
				cancel()
				return nil
			case err := <-done:
				cancel()
				if errors.Is(err, wecom.ErrKicked) {
					return nil // 让位：同 BotID 的新实例接管
				}
				if err != nil && c.ctx.Err() == nil {
					return c.fail(err) // ErrAuthFailed 等不可恢复错误
				}
				break waitLoop // 干净退出（内部断线已消化仍退出）：重建
			case <-tick.C:
				next := c.connectorEnv(id)
				if connectorEnvEqual(env, next) {
					continue
				}
				env = next
				if !env.ready() {
					fmt.Fprintln(c.stdout, "渠道配置已失效（缺 BotID/Secret/白名单），bridge 停止")
					cancel()
					<-done
					return c.fail(fmt.Errorf("配置不完整，bridge 停止（修正 .env 后重新启动）"))
				}
				cancel()
				<-done // 旧桥经互踢让位退出
				fmt.Fprintf(c.stdout, "渠道配置变更，重建企微 bridge（白名单 %d 人）\n", len(env.allow))
				break waitLoop
			}
		}
	}
}

func (c *CLI) newConnectorRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run <身份名> <渠道>",
		Short: "运行一个渠道桥（当前支持 wecom）",
		Args:  exactArgs(2, "用法: mindloop connector run <身份名> wecom"),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := c.loadIdentity(args[0])
			if err != nil {
				return c.fail(err)
			}
			switch strings.ToLower(args[1]) {
			case "wecom":
				return c.runWecom(id)
			default:
				return usageErr(fmt.Sprintf("未知渠道 %q（当前支持：wecom）", args[1]))
			}
		},
	}
	return cmd
}

// newConnectorRunAllCmd 拉起全部已配置渠道。渠道清单就是本文件里
// 的 runs 映射——加渠道时在这里登记（Phase 2 名单：微信客服/钉钉/
// 飞书，均未实施）。
func (c *CLI) newConnectorRunAllCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run-all <身份名>",
		Short: "运行全部已配置的渠道桥",
		Args:  exactArgs(1, "用法: mindloop connector run-all <身份名>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := c.loadIdentity(args[0])
			if err != nil {
				return c.fail(err)
			}
			env := c.connectorEnv(id)
			configured := env.ready()
			if !configured {
				fmt.Fprintln(c.stderr, "⚠ 没有已配置的渠道（.env 需要 WECOM_BOT_ID/WECOM_BOT_SECRET/WECOM_ALLOW）——无事可做")
				return nil
			}
			return c.runWecom(id)
		},
	}
	return cmd
}

func (c *CLI) newConnectorListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list <身份名>",
		Short: "显示渠道桥的配置与状态",
		Args:  exactArgs(1, "用法: mindloop connector list <身份名>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := c.loadIdentity(args[0])
			if err != nil {
				return c.fail(err)
			}
			env := c.connectorEnv(id)
			state := func(set bool) string {
				if set {
					return "已配置"
				}
				return "缺失"
			}
			fmt.Fprintf(c.stdout, "wecom:\n")
			fmt.Fprintf(c.stdout, "  WECOM_BOT_ID    %s\n", state(env.botID != ""))
			fmt.Fprintf(c.stdout, "  WECOM_BOT_SECRET %s\n", state(env.secret != ""))
			fmt.Fprintf(c.stdout, "  WECOM_ALLOW     %s（%v）\n", state(len(env.allow) > 0), env.allow)
			cursor := filepath.Join(connectorStateDir(id), "wecom-outbound.cursor")
			if fi, err := os.Stat(cursor); err == nil {
				fmt.Fprintf(c.stdout, "  出站游标        存在（%d 字节，上次运行 %s）\n", fi.Size(), fi.ModTime().Format("2006-01-02 15:04"))
			} else {
				fmt.Fprintln(c.stdout, "  出站游标        尚不存在（首启从 EOF 起步）")
			}
			return nil
		},
	}
	return cmd
}
