package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lomehong/mindloop/internal/identity"
	"github.com/lomehong/mindloop/internal/mcp"
	"github.com/lomehong/mindloop/internal/obs"
	"github.com/lomehong/mindloop/internal/skills"
	"github.com/lomehong/mindloop/internal/traj"
)

// doctorCheck 是一项体检结果。
type doctorCheck struct {
	name   string
	status string // "ok" | "warn" | "fail"
	detail string
}

func (c *doctorCheck) pass(detail string) { c.status, c.detail = "ok", detail }
func (c *doctorCheck) warn(detail string) { c.status, c.detail = "warn", detail }
func (c *doctorCheck) fail(detail string) { c.status, c.detail = "fail", detail }

func (c *doctorCheck) symbol() string {
	switch c.status {
	case "ok":
		return "✓"
	case "warn":
		return "⚠"
	default:
		return "✗"
	}
}

func (c *CLI) newDoctorCmd() *cobra.Command {
	var identityP string
	var noProbeP bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "体检：状态根/模型连通/viewer/身份/技能/MCP 一键自查",
		Long: `对安装与运行的关键环节逐项体检，✓✗ 表输出。退出码 0 =
全部可用（含警告）；1 = 有失败项。

检查项：状态根可写、模型配置与连通（实测一次最小补全，--no-probe
跳过）、viewer 构建产物命中哪一级来源、身份目录、技能库、MCP
服务器配置（stdio 服务器查命令可达性，远程 URL 跳过）、执行策略
开关现状。`,
		Example: `  mindloop doctor
  mindloop doctor --identity ada
  mindloop doctor --no-probe`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.runDoctor(identityP, noProbeP)
		},
	}
	fs := cmd.Flags()
	fs.StringVar(&identityP, "identity", "", "针对单个身份做深度检查（技能/MCP/身份项）")
	fs.BoolVar(&noProbeP, "no-probe", false, "跳过模型连通实测（只查配置形态）")
	return cmd
}

func (c *CLI) runDoctor(identityName string, noProbe bool) error {
	checks := []*doctorCheck{
		c.doctorHome(),
		c.doctorModel(noProbe),
		c.doctorViewer(),
		c.doctorIdentities(identityName),
		c.doctorSkills(identityName),
		c.doctorMCP(identityName),
		c.doctorBudget(identityName),
		doctorPolicy(),
	}

	failed := 0
	for _, ck := range checks {
		fmt.Fprintf(c.stdout, " %s  %-12s %s\n", ck.symbol(), ck.name, ck.detail)
		if ck.status == "fail" {
			failed++
		}
	}
	if failed > 0 {
		fmt.Fprintf(c.stdout, "\n%d 项失败；按提示修复后重跑 mindloop doctor。\n", failed)
		return exitError{code: 1}
	}
	fmt.Fprintf(c.stdout, "\n全部可用。\n")
	return nil
}

// doctorHome 状态根存在且可写——一切数据的地基。写入探测走
// os.Root：路径活动被限制在状态根之内。
func (c *CLI) doctorHome() *doctorCheck {
	ck := &doctorCheck{name: "状态根"}
	home := traj.Home()
	if fi, err := os.Stat(home); err != nil || !fi.IsDir() {
		ck.fail(fmt.Sprintf("%s 不存在——先跑 mindloop init", home))
		return ck
	}
	root, err := os.OpenRoot(home)
	if err != nil {
		ck.fail(fmt.Sprintf("%s 打不开: %v", home, err))
		return ck
	}
	defer root.Close()
	f, err := root.Create(".doctor-probe")
	if err != nil {
		ck.fail(fmt.Sprintf("%s 不可写: %v", home, err))
		return ck
	}
	f.Close()
	_ = root.Remove(".doctor-probe")
	ck.pass(home)
	return ck
}

// doctorModel 模型配置形态 + 可选连通实测。
func (c *CLI) doctorModel(noProbe bool) *doctorCheck {
	ck := &doctorCheck{name: "模型"}
	model := os.Getenv("MINDLOOP_MODEL")
	if model == "" {
		ck.fail("MINDLOOP_MODEL 未配置——跑 mindloop init 或编辑 .env")
		return ck
	}
	key := firstEnv("MINDLOOP_API_KEY", "ANTHROPIC_API_KEY", "OPENAI_API_KEY")
	keyNote := "无 key"
	if key != "" {
		keyNote = "key " + maskKey(key)
	}
	if noProbe {
		ck.pass(fmt.Sprintf("%s（%s；未实测）", model, keyNote))
		return ck
	}
	ok, latency, detail := probeLLM(model, key, "")
	if ok {
		ck.pass(fmt.Sprintf("%s（%s，实测 %dms）", model, keyNote, latency))
	} else {
		ck.fail(fmt.Sprintf("%s 实测不通: %s", model, detail))
	}
	return ck
}

// doctorViewer viewer 构建产物命中哪一级来源——"页面开不出来"的
// 第一直觉排查点。
func (c *CLI) doctorViewer() *doctorCheck {
	ck := &doctorCheck{name: "viewer"}
	dir, fsys, tier, ok := resolveViewerSource("")
	if !ok {
		ck.warn("未找到构建产物（API-only）——cd web/static && npm run build，或用 release 构建（内嵌）")
		return ck
	}
	label := viewerSourceLabel(dir, fsys)
	tierNote := map[string]string{
		tierFlag:     "旗标指定",
		tierEnv:      "环境变量指定",
		tierEmbedded: "内嵌（release 构建）",
		tierAuto:     "磁盘自动探测",
	}[tier]
	ck.pass(fmt.Sprintf("%s（%s）", label, tierNote))
	return ck
}

// doctorIdentities 身份清单；--identity 时深度确认可加载。
func (c *CLI) doctorIdentities(name string) *doctorCheck {
	ck := &doctorCheck{name: "身份"}
	list, err := identity.List()
	if err != nil {
		ck.fail(err.Error())
		return ck
	}
	if len(list) == 0 {
		ck.warn("无身份——跑 mindloop init 创建演示身份")
		return ck
	}
	if name == "" {
		ck.pass(fmt.Sprintf("%d 个: %s", len(list), strings.Join(list, "、")))
		return ck
	}
	for _, have := range list {
		if have == name {
			if _, err := identity.Load(name); err != nil {
				ck.fail(fmt.Sprintf("%s 加载失败: %v", name, err))
				return ck
			}
			ck.pass(fmt.Sprintf("%s（可加载）", name))
			return ck
		}
	}
	ck.fail(fmt.Sprintf("身份 %s 不存在（现有: %s）", name, strings.Join(list, "、")))
	return ck
}

// doctorSkills 技能库体检：清点条目，坏 SKILL.md 单独点名。
func (c *CLI) doctorSkills(identityName string) *doctorCheck {
	ck := &doctorCheck{name: "技能"}
	dirs := []string{filepath.Join(traj.Home(), "skills")}
	scope := "全局"
	if identityName != "" {
		dirs = append(dirs, filepath.Join(identity.Home(), identityName, "skills"))
		scope = "全局+身份"
	}
	items, errs := skills.Store{Dirs: dirs}.List()
	detail := fmt.Sprintf("%s %d 个技能", scope, len(items))
	if len(errs) > 0 {
		ck.warn(detail + fmt.Sprintf("；%d 条损坏（skills list 查看详情）", len(errs)))
		return ck
	}
	if len(items) == 0 {
		ck.pass(detail + "（skills init 可脚手架）")
		return ck
	}
	ck.pass(detail)
	return ck
}

// doctorMCP mcp.json 体检：配置可解析 + stdio 服务器命令可达。
// 远程 URL 服务器不探测（可达性属运行期，留给 mcp tools 实测）。
func (c *CLI) doctorMCP(identityName string) *doctorCheck {
	ck := &doctorCheck{name: "MCP"}
	paths := []string{filepath.Join(traj.Home(), "mcp.json")}
	if identityName != "" {
		paths = append(paths, filepath.Join(identity.Home(), identityName, "mcp.json"))
	}
	cfg, err := mcp.LoadConfig(paths...)
	if err != nil {
		ck.fail("mcp.json 解析失败: " + err.Error())
		return ck
	}
	names := cfg.Names()
	if len(names) == 0 {
		ck.pass("未配置（mcp add 可接入标准服务器）")
		return ck
	}
	var broken []string
	var remote int
	for _, name := range names {
		sc := cfg.MCPServers[name]
		switch {
		case sc.URL != "":
			remote++
		case sc.Command != "":
			if _, err := exec.LookPath(sc.Command); err != nil {
				broken = append(broken, fmt.Sprintf("%s（%s 不可达）", name, sc.Command))
			}
		default:
			broken = append(broken, name+"（缺 command/url）")
		}
	}
	detail := fmt.Sprintf("%d 个服务器", len(names))
	if remote > 0 {
		detail += fmt.Sprintf("（%d 个远程跳过探测）", remote)
	}
	if len(broken) > 0 {
		ck.fail(detail + "；问题: " + strings.Join(broken, "、"))
		return ck
	}
	ck.pass(detail)
	return ck
}

// doctorBudget 预算与熔断现状。记账按身份走——--identity 深查该
// 身份的消耗与剩余；未指定时报告环境上限的设置情况。
func (c *CLI) doctorBudget(identityName string) *doctorCheck {
	ck := &doctorCheck{name: "预算"}
	if identityName == "" {
		ck.pass("按身份记账——加 --identity <名字> 查看消耗与剩余")
		return ck
	}
	id, err := identity.Load(identityName)
	if err != nil {
		ck.warn("身份加载失败，预算状态未知: " + err.Error())
		return ck
	}
	ad := obs.LoadAdmission(id.Dir)
	detail := "未设置每日上限（MINDLOOP_DAILY_TOKENS / MINDLOOP_SPONTANEOUS_TOKENS）"
	if ad.DailyLimit > 0 || ad.SelfLimit > 0 {
		detail = fmt.Sprintf("今日 %d", ad.UsedToday)
		if ad.DailyLimit > 0 {
			detail += fmt.Sprintf("/%d", ad.DailyLimit)
		}
		if ad.SelfLimit > 0 {
			detail += fmt.Sprintf("（自发 %d/%d）", ad.SelfUsedToday, ad.SelfLimit)
		}
		detail += " tokens"
	}
	if ad.CoolingUntil != "" {
		ck.warn(detail + "；⚠ 熔断冷却中")
		return ck
	}
	switch {
	case ad.DailyLimit > 0 && ad.UsedToday >= ad.DailyLimit:
		ck.fail(detail + "；总预算已用完（UTC 日界重置，或调高上限）")
	case ad.SelfLimit > 0 && ad.SelfUsedToday >= ad.SelfLimit:
		ck.warn(detail + "；自发档预算已用完（watchdog/定时唤醒暂停）")
	default:
		ck.pass(detail)
	}
	return ck
}

// doctorPolicy 执行策略开关现状——支持对话时先看这个。
func doctorPolicy() *doctorCheck {
	ck := &doctorCheck{name: "执行策略"}
	mode := os.Getenv("MINDLOOP_EXEC_POLICY")
	if mode == "" {
		mode = "ask（缺省）"
	}
	detail := mode
	if os.Getenv("MINDLOOP_EXEC_POLICY_AUTO") == "0" {
		detail += "；只读自动放行已关闭"
	}
	if os.Getenv("MINDLOOP_EXEC_TRIPWIRE") == "0" {
		detail += "；不可逆操作守卫已关闭"
	}
	ck.pass(detail)
	return ck
}
