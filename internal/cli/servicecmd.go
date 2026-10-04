package cli

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/user"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/lomehong/mindloop/internal/identity"
	"github.com/lomehong/mindloop/internal/service"
	"github.com/lomehong/mindloop/internal/traj"
)

// newServiceExecer 是任务计划程序执行器的装配点：生产路径在非
// Windows 上显式失败（schtasks 不存在，报错要可读）；测试替换
// 本钩子注入 fake，全部命令路径因此可跨平台验证。
var newServiceExecer = func() service.Execer {
	if runtime.GOOS != "windows" {
		return serviceUnavailableExecer{}
	}
	return service.Exec{}
}

// serviceUnavailableExecer 是非 Windows 平台的哨兵执行器。
type serviceUnavailableExecer struct{}

func (serviceUnavailableExecer) Run(string, ...string) (string, error) {
	return "", fmt.Errorf("service 命令目前仅支持 Windows（任务计划程序宿主）；Linux/macOS 的 systemd user units 是后续立项")
}

// serviceName 是 host 模式安全的身份显示名：host 无 *identity.Identity
//（serviceManager 对 "host" 返回 nil id），生命周期命令的提示行此前
// 直接解引用 id.Name 连环崩（2026-10-04 全系统测试 #9）。
func serviceName(id *identity.Identity) string {
	if id == nil {
		return "host"
	}
	return id.Name
}

// newServiceCmd 服务化命令组：注册与生命周期。任务设置与生命周期
// 语义的完整决策链在 docs/designs/service.md。
func (c *CLI) newServiceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "service",
		Short: "服务化：mind/connector/web 注册为登录自启的计划任务（Windows）",
		Long: `把三个常驻进程装配成 Windows 任务计划程序任务——headlong
deploy/*.service（每组件一个 systemd unit）的对应物，监督、崩溃
重启、登录自启全部委托 OS 宿主，本命令只负责注册与生命周期：

  install    注册 Mindloop-<身份>-{mind,connector,web} 三个任务：
             登录自启（延迟 15s 错峰）、崩溃 1 分钟退避自动重启、
             无时限、防双开。免管理员（以当前用户身份运行）。
  start      启用并立即拉起（复活被 stop 停掉的任务）。
  stop       刻意停止：mind 优雅停机（在途思考不腰斩），其余硬杀
             （都按崩溃窗口设计）；随后全部禁用——不会被重启机制
             复活，下次登录也不自启。
  restart    stop + start。
  status     任务状态投影（运行中/就绪/已禁用 + 上次结果）。
  uninstall  停跑并删除三个任务（幂等）。

组件日志（journald 的对应物）在 <MINDLOOP_HOME>/logs/<组件>.log。
任务绑定安装时的 exe 绝对路径：挪动或改名 mindloop.exe 后需重跑
install。真正的"登录前 boot-start"需要服务账户决策，暂不支持。`,
	}
	cmd.AddCommand(
		c.newServiceInstallCmd(),
		c.newServiceStartCmd(),
		c.newServiceStopCmd(),
		c.newServiceRestartCmd(),
		c.newServiceStatusCmd(),
		c.newServiceUninstallCmd(),
	)
	return cmd
}

// serviceManager 装配一个身份的 Manager：exe/user/home 在此刻固化
// 进任务定义（身份目录必须已存在，未注册的身份直接拒绝）。
func (c *CLI) serviceManager(identityName string) (*service.Manager, *identity.Identity, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, nil, c.fail(fmt.Errorf("无法定位自身可执行文件: %w", err))
	}
	u, err := user.Current()
	if err != nil {
		return nil, nil, c.fail(fmt.Errorf("无法获取当前用户: %w", err))
	}
	// 宿主模式（service start/stop/restart/status host）：没有单一
	// 身份可加载，Spec 占位 host（componentsFor 据此选系统宿主任务）。
	if identityName == "host" {
		return &service.Manager{
			Spec: service.Spec{
				Identity: "host",
				Exe:      exe,
				Home:     traj.Home(),
				User:     u.Username,
			},
			Exec: newServiceExecer(),
		}, nil, nil
	}
	id, err := c.loadIdentity(identityName)
	if err != nil {
		return nil, nil, err
	}
	return &service.Manager{
		Spec: service.Spec{
			Identity: id.Name,
			Exe:      exe,
			Home:     traj.Home(),
			User:     u.Username,
		},
		Exec: newServiceExecer(),
	}, id, nil
}

func (c *CLI) newServiceInstallCmd() *cobra.Command {
	var hostP bool
	cmd := &cobra.Command{
		Use:   "install <身份名|host>",
		Short: "注册计划任务（缺省按身份三组件；--host 装系统宿主单任务）",
		Long: `两种装机形态：

  mindloop service install ada          # 按身份：mind/connector/web 三个任务
  mindloop service install host --host  # 系统宿主：一个任务包装 system run

宿主模式按 system.json 监督全部能力子进程（崩溃自愈、配置热加载）
——此后能力增减改配置（仪表盘 /system 页或 system.json），不再重装。`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if hostP {
				return c.serviceInstallHost(args[0])
			}
			m, id, err := c.serviceManager(args[0])
			if err != nil {
				return c.fail(err)
			}
			// 预检只警告不阻止：web 构建产物缺失 → API-only 降级；
			// 渠道未配置 → connector 干净退出（RestartOnFailure 不触发）；
			// 端口被占用 → web 任务会按 1 分钟退避反复重试刷日志。
			if _, _, _, viewerOK := resolveViewerSource(""); !viewerOK {
				fmt.Fprintln(c.stderr, "⚠ 未找到 viewer 构建产物——web 任务将以 API-only 模式运行")
				fmt.Fprintln(c.stderr, "  先构建再重跑 install 可带界面: cd web/static && npm run build")
			}
			if env := c.connectorEnv(id); !env.ready() {
				fmt.Fprintln(c.stderr, "⚠ 未配置渠道（身份 .env 需要 WECOM_BOT_ID/WECOM_BOT_SECRET/WECOM_ALLOW）——connector 任务启动后无事可做")
			}
			warnIfPortBusy(c.stderr, "127.0.0.1:8080")
			if err := m.Install(); err != nil {
				return c.fail(err)
			}
			fmt.Fprintf(c.stdout, "已注册 %d 个计划任务（登录自启，身份 %s）：\n", len(service.Components), id.Name)
			for _, comp := range service.Components {
				fmt.Fprintf(c.stdout, "  %s → 日志 %s\n", service.TaskName(comp, id.Name), service.LogPath(m.Spec, comp))
			}
			fmt.Fprintf(c.stdout, "立即拉起: mindloop service start %s\n", id.Name)
			return nil
		},
	}
	cmd.Flags().BoolVar(&hostP, "host", false, "装机即系统宿主：单任务包装 system run（能力增减走配置）")
	return cmd
}

// serviceInstallHost 装宿主模式任务：Spec 的 Identity 占位为
// "host"（任务名 Mindloop-host-system），Home 是状态根——宿主按
// system.json 管全部身份，装机与会话身份解耦。
func (c *CLI) serviceInstallHost(name string) error {
	if name != "host" {
		return c.fail(fmt.Errorf("宿主模式的任务名占位是 host（mindloop service install host --host）"))
	}
	exe, err := os.Executable()
	if err != nil {
		return c.fail(fmt.Errorf("无法定位自身可执行文件: %w", err))
	}
	u, err := user.Current()
	if err != nil {
		return c.fail(fmt.Errorf("无法获取当前用户: %w", err))
	}
	m := &service.Manager{
		Spec: service.Spec{
			Identity: "host",
			Exe:      exe,
			Home:     traj.Home(),
			User:     u.Username,
		},
		Exec: newServiceExecer(),
	}
	// Identity=host 时 componentsFor() 只返回系统宿主组件——Install
	// 即单任务装机。
	if err := m.Install(); err != nil {
		return c.fail(err)
	}
	fmt.Fprintln(c.stdout, "已注册系统宿主任务（登录自启）：")
	fmt.Fprintf(c.stdout, "  %s → 日志 %s\n", service.TaskName(service.ComponentSystem, "host"), service.LogPath(m.Spec, service.ComponentSystem))
	fmt.Fprintln(c.stdout, "立即拉起: mindloop service start host")
	fmt.Fprintln(c.stdout, "此后能力增减改配置：仪表盘 /system 页或 <状态根>/system.json")
	return nil
}

func (c *CLI) newServiceStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start <身份名>",
		Short: "启用并立即拉起全部任务",
		Args:  exactArgs(1, "用法: mindloop service start <身份名>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			m, id, err := c.serviceManager(args[0])
			if err != nil {
				return c.fail(err)
			}
			if err := m.Start(); err != nil {
				return c.fail(err)
			}
			fmt.Fprintf(c.stdout, "已拉起身份 %s 的 %d 个任务；日志在 %s\n", serviceName(id), len(service.Components), service.LogDir(m.Spec))
			return nil
		},
	}
}

func (c *CLI) newServiceStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop <身份名>",
		Short: "刻意停止并禁用全部任务（不会被重启机制复活）",
		Args:  exactArgs(1, "用法: mindloop service stop <身份名>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			m, id, err := c.serviceManager(args[0])
			if err != nil {
				return c.fail(err)
			}
			if err := m.Stop(); err != nil {
				return c.fail(err)
			}
			fmt.Fprintf(c.stdout, "已停止并禁用 %d 个任务（刻意停止：重启机制与登录自启都不再拉起）\n", len(service.Components))
			fmt.Fprintf(c.stdout, "复活: mindloop service start %s\n", serviceName(id))
			return nil
		},
	}
}

func (c *CLI) newServiceRestartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restart <身份名>",
		Short: "stop + start",
		Args:  exactArgs(1, "用法: mindloop service restart <身份名>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			m, _, err := c.serviceManager(args[0])
			if err != nil {
				return c.fail(err)
			}
			if err := m.Stop(); err != nil {
				return c.fail(err)
			}
			if err := m.Start(); err != nil {
				return c.fail(err)
			}
			fmt.Fprintln(c.stdout, "已重启全部任务")
			return nil
		},
	}
}

func (c *CLI) newServiceStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status <身份名>",
		Short: "任务状态投影（状态、上次结果、心智进程探测）",
		Args:  exactArgs(1, "用法: mindloop service status <身份名>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			m, id, err := c.serviceManager(args[0])
			if err != nil {
				return c.fail(err)
			}
			statuses, err := m.Status()
			if err != nil {
				return c.fail(err)
			}
			mindAlive := id != nil && mindRunning(id.Timeline.Dir)
			fmt.Fprintf(c.stdout, "%-32s %-10s %-24s %s\n", "任务", "状态", "上次结果", "备注")
			for i, s := range statuses {
				state := serviceStateText(s)
				note := ""
				if i == 0 { // mind 行附运行锁探测——任务视角 ≠ 进程视角
					if mindAlive {
						note = "心智进程在运行"
					} else if s.Exists {
						note = "心智进程不在（看日志与上次结果）"
					}
				}
				fmt.Fprintf(c.stdout, "%-32s %-10s %-24s %s\n", s.Name, state, serviceLastResultText(s), note)
			}
			if !mindAlive {
				fmt.Fprintf(c.stdout, "\n提示: 拉起用 mindloop service start %s；组件日志在 %s\n", serviceName(id), service.LogDir(m.Spec))
			}
			return nil
		},
	}
}

// serviceStateText 翻译任务 State（PowerShell 英文枚举，值集稳定）。
func serviceStateText(s service.TaskStatus) string {
	if !s.Exists {
		return "未注册"
	}
	switch s.State {
	case "Running":
		return "运行中"
	case "Ready":
		return "就绪"
	case "Disabled":
		return "已禁用"
	case "":
		return "未知"
	default:
		return s.State
	}
}

// serviceLastResultText 翻译 LastTaskResult（调度器 HRESULT 的常见
// 值；其余原样展示）。
func serviceLastResultText(s service.TaskStatus) string {
	switch s.LastResult {
	case "":
		return "—"
	case "0":
		return "0（上次成功）"
	case "267009":
		return "267009（任务正在运行）"
	case "267011":
		return "267011（从未运行）"
	case "267014":
		return "267014（任务被终止）"
	default:
		return s.LastResult
	}
}

// warnIfPortBusy 探测 web 默认端口，被占用给出可读告警（真实
// 教训：端口冲突的失败任务会按 1 分钟退避反复重试）。
func warnIfPortBusy(w io.Writer, addr string) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Fprintf(w, "⚠ %s 已被占用——web 任务启动会失败并按 1 分钟退避反复重试；如另有实例在跑，可忽略或改用 --port\n", addr)
		return
	}
	_ = ln.Close()
}

func (c *CLI) newServiceUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall <身份名>",
		Short: "停跑并删除全部任务（幂等）",
		Args:  exactArgs(1, "用法: mindloop service uninstall <身份名>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			m, id, err := c.serviceManager(args[0])
			if err != nil {
				return c.fail(err)
			}
			if err := m.Uninstall(); err != nil {
				return c.fail(err)
			}
			fmt.Fprintf(c.stdout, "已删除 %s 的全部服务化任务（不存在的已跳过）\n", serviceName(id))
			return nil
		},
	}
}
