package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"mindloop/internal/identity"
	"mindloop/internal/llm"
	"mindloop/internal/traj"
)

// initStdin 可注入点：测试换 bufio.NewReader(strings.NewReader(...))，
// 生产是标准输入。经 bufio 统一为读行原语。
var initStdin = bufio.NewReader(os.Stdin)

// initCfg 聚合 init 命令的参数。
type initCfg struct {
	demo     bool
	yes      bool
	model    string
	apiKey   string
	baseURL  string
	identity string
}

func (c *CLI) newInitCmd() *cobra.Command {
	var cfg initCfg
	cmd := &cobra.Command{
		Use:   "init",
		Short: "初始化：配置模型 + 创建身份，两分钟跑到 ada 的第一句话",
		Long: `初始化向导：把模型配置写进 <MINDLOOP_HOME>/.env（原位合并，
保留既有内容），创建演示身份 ada，并实测模型连通性。

三条路：
  --demo                       echo 演示模型，零 key 离线可跑；
  --model X --api-key k        真实模型（按模型名自动识别 provider，
                               glm-* 落智谱、claude-* 落 anthropic、
                               其余 openai-compatible）；
  都不带                       交互式向导（检测到已有配置时默认保留）。

探通之后：mindloop chat ada 开始对话，mindloop web 打开仪表盘。`,
		Example: `  mindloop init
  mindloop init --demo
  mindloop init --model glm-5 --api-key sk-xxx --yes
  mindloop init --model gpt-4.1-mini --api-key sk-xxx --base-url https://api.openai.com/v1`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.runInit(&cfg)
		},
	}
	fs := cmd.Flags()
	fs.BoolVar(&cfg.demo, "demo", false, "echo 演示模型（零 key、离线可跑）")
	fs.BoolVar(&cfg.yes, "yes", false, "非交互：全部按参数与缺省执行，不提问")
	fs.StringVar(&cfg.model, "model", "", "模型名（provider 按模型名推断）")
	fs.StringVar(&cfg.apiKey, "api-key", "", "API key（也可走 MINDLOOP_API_KEY 环境变量）")
	fs.StringVar(&cfg.baseURL, "base-url", "", "自定义端点（可选；默认按 provider 推断）")
	fs.StringVar(&cfg.identity, "identity", "ada", "要创建/复用的身份名")
	return cmd
}

func (c *CLI) runInit(cfg *initCfg) error {
	home := traj.Home()
	envPath := filepath.Join(home, ".env")

	// 现状盘点：已有配置才谈得上"保留"。
	existingModel := os.Getenv("MINDLOOP_MODEL")
	if existingModel != "" {
		fmt.Fprintf(c.stdout, "状态根: %s（已配置模型 %s）\n", home, existingModel)
	} else {
		fmt.Fprintf(c.stdout, "状态根: %s（尚未配置模型）\n", home)
	}

	// ── 1) 模型配置 ────────────────────────────────────────────
	patches := map[string]string{}
	probeModel := existingModel
	probeKey := firstEnv("MINDLOOP_API_KEY", "ANTHROPIC_API_KEY", "OPENAI_API_KEY")
	probeURL := ""
	switch {
	case cfg.demo:
		patches["MINDLOOP_MODEL"] = "echo"
		probeModel, probeKey = "echo", ""
		fmt.Fprintln(c.stdout, "模式: echo 演示（零 key，模型回声你的输入）")
	case cfg.model != "":
		patches["MINDLOOP_MODEL"] = cfg.model
		probeModel = cfg.model
		if cfg.apiKey != "" {
			patches["MINDLOOP_API_KEY"] = cfg.apiKey
			probeKey = cfg.apiKey
		}
		if cfg.baseURL != "" {
			patches["MINDLOOP_BASE_URL"] = cfg.baseURL
			probeURL = cfg.baseURL
		}
	default:
		choice := c.initPromptChoice(existingModel != "")
		switch choice {
		case 1:
			patches["MINDLOOP_MODEL"] = "echo"
			probeModel, probeKey = "echo", ""
			fmt.Fprintln(c.stdout, "模式: echo 演示（零 key，模型回声你的输入）")
		case 2:
			p, ok := c.initPromptModel(probeKey)
			if !ok {
				return c.fail(fmt.Errorf("未提供模型配置；可随时重跑 mindloop init 或用 --model/--api-key 直配"))
			}
			patches = p
			probeModel = patches["MINDLOOP_MODEL"]
			probeKey = patches["MINDLOOP_API_KEY"]
			probeURL = patches["MINDLOOP_BASE_URL"]
		default:
			fmt.Fprintln(c.stdout, "保留现有配置，跳过模型设置")
		}
	}

	// ── 2) 原位合并写入 <home>/.env ───────────────────────────
	if len(patches) > 0 {
		fmt.Fprintf(c.stdout, "将写入 %s:\n", envPath)
		for _, k := range sortedKeys(patches) {
			v := patches[k]
			if k == "MINDLOOP_API_KEY" {
				v = maskKey(v)
			}
			fmt.Fprintf(c.stdout, "  %s=%s\n", k, v)
		}
		if !cfg.yes && !c.initConfirm("确认写入?") {
			fmt.Fprintln(c.stdout, "已取消，未做任何修改")
			return nil
		}
		if err := writeEnvPatch(envPath, patches); err != nil {
			return c.fail(err)
		}
		fmt.Fprintln(c.stdout, "已写入 ✓")
	}

	// ── 3) 连通性探测 ─────────────────────────────────────────
	if probeModel != "" {
		fmt.Fprintf(c.stdout, "探测模型 %s ……", probeModel)
		ok, latency, detail := probeLLM(probeModel, probeKey, probeURL)
		if ok {
			fmt.Fprintf(c.stdout, " 通（%dms）\n", latency)
		} else {
			fmt.Fprintf(c.stdout, " 不通: %s\n", detail)
			fmt.Fprintln(c.stdout, "  配置已保留；请检查 key/网络后重跑 mindloop init 验证。")
		}
	}

	// ── 4) 身份 ───────────────────────────────────────────────
	name := cfg.identity
	if _, err := identity.Load(name); err == nil {
		fmt.Fprintf(c.stdout, "身份 %s 已存在，直接复用\n", name)
	} else {
		if _, err := identity.Create(c.ctx, name); err != nil {
			return c.fail(err)
		}
		fmt.Fprintf(c.stdout, "身份 %s 已创建 ✓\n", name)
	}

	// ── 5) 下一步 ─────────────────────────────────────────────
	fmt.Fprintf(c.stdout, `
就绪。接下来：

  mindloop chat %s        # 开始对话（ada 的第一句话就在这里）
  mindloop web            # 仪表盘（15 页面：时间线/记忆/技能/用量…）
  mindloop init --demo    # 换 echo 演示模式（零 key）
`, name)
	return nil
}

// initPromptChoice 问"怎么配模型"：1=echo 演示，2=真实模型，3=保留。
// hasExisting 决定空输入的缺省——已有配置的人多半是误跑，保留。
func (c *CLI) initPromptChoice(hasExisting bool) int {
	def := "1"
	hint := "直接回车 = echo 演示"
	if hasExisting {
		def = "3"
		hint = "直接回车 = 保留现有配置"
	}
	fmt.Fprintf(c.stdout, `
模型配置：
  1) echo 演示——零 key、离线可跑，先体验再说
  2) 配置真实模型（glm / claude / openai-compatible…）
  3) 跳过——保持现有配置
选择 [%s/2/3]（%s）: `, def, hint)
	ans := strings.ToLower(strings.TrimSpace(c.initReadLine()))
	switch ans {
	case "", "1":
		if hasExisting && ans == "" {
			return 3
		}
		return 1
	case "2":
		return 2
	default:
		return 3
	}
}

// initPromptModel 收集真实模型三元组。key 回车 = 沿用环境里已有的
// key（MINDLOOP_API_KEY/ANTHROPIC/OPENAI）。返回的 patches 只含用户
// 实际提供的键；ok=false 表示放弃了。
func (c *CLI) initPromptModel(envKey string) (patches map[string]string, ok bool) {
	patches = map[string]string{}
	fmt.Fprint(c.stdout, "模型名（如 glm-5 / claude-sonnet-4-5 / gpt-4.1-mini）: ")
	model := strings.TrimSpace(c.initReadLine())
	if model == "" {
		fmt.Fprintln(c.stdout, "模型名不能为空")
		return nil, false
	}
	patches["MINDLOOP_MODEL"] = model

	hint := ""
	if envKey != "" {
		hint = "（回车 = 沿用环境已有 key）"
	}
	fmt.Fprintf(c.stdout, "API key %s: ", hint)
	key := strings.TrimSpace(c.initReadLine())
	if key != "" {
		patches["MINDLOOP_API_KEY"] = key
	}

	fmt.Fprint(c.stdout, "自定义端点（可选，回车 = 按 provider 默认）: ")
	if u := strings.TrimSpace(c.initReadLine()); u != "" {
		patches["MINDLOOP_BASE_URL"] = u
	}
	return patches, true
}

// initReadLine 读一行 stdin；EOF/读失败返回空串（向导各问句都对
// 空串有安全缺省，脚本化调用不会卡死）。
func (c *CLI) initReadLine() string {
	if initStdin == nil {
		return ""
	}
	line, err := initStdin.ReadString('\n')
	if err != nil && line == "" {
		return ""
	}
	return line
}

// initConfirm 问 Y/n：空与 Y 算确认；EOF 也算确认（非交互容错）。
func (c *CLI) initConfirm(question string) bool {
	fmt.Fprintf(c.stdout, "%s [Y/n] ", question)
	ans := strings.ToLower(strings.TrimSpace(c.initReadLine()))
	return ans == "" || ans == "y" || ans == "yes"
}

// probeLLM 实测一次最小补全。与 web 的 /api/llm-health/probe 同款
// 问法；显式 Spec 而非 FromEnv——init 刚写完 .env，进程环境还是旧的。
func probeLLM(model, key, baseURL string) (bool, int, string) {
	client, err := llm.New(llm.Spec{Model: model, APIKey: key, BaseURL: baseURL})
	if err != nil {
		return false, 0, err.Error()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	start := time.Now()
	_, callErr := client.Complete(ctx, "Reply with the single word: pong",
		[]llm.Message{{Role: "user", Content: "ping"}})
	latency := int(time.Since(start).Milliseconds())
	if callErr != nil {
		return false, latency, callErr.Error()
	}
	return true, latency, ""
}

// writeEnvPatch 把 patches 原位合并进 .env：已有键在原行位置更新
// （保留行序与注释），新键追加文件尾；权限 0600。与 web 包
// channels_page 的身份 .env 合并同构——控制面收口（roadmap §2.2）
// 时两处并成一个包。
func writeEnvPatch(path string, patches map[string]string) error {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if len(data) == 0 {
		// 全新文件：开头留一行出身说明（合并算法会原样保留注释）。
		data = []byte("# mindloop init 生成——完整模板见仓库 .env.example\n")
	}
	lines := strings.Split(string(data), "\n")
	seen := map[string]bool{}
	for i, ln := range lines {
		trimmed := strings.TrimSpace(ln)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if k, _, ok := strings.Cut(trimmed, "="); ok {
			k = strings.TrimSpace(k)
			if _, hit := patches[k]; hit {
				lines[i] = k + "=" + patches[k]
				seen[k] = true
			}
		}
	}
	var extra []string
	for _, k := range sortedKeys(patches) {
		if !seen[k] {
			extra = append(extra, k+"="+patches[k])
		}
	}
	if len(extra) > 0 {
		if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
			lines = append(lines, "")
		}
		lines = append(lines, extra...)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o600)
}

// sortedKeys 让写入与展示的键序稳定（map 迭代无序）。
func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// maskKey 是 key 的展示形态：只留首尾，中间不给。
func maskKey(k string) string {
	if k == "" {
		return "(未设置)"
	}
	if len(k) <= 12 {
		return k[:2] + "…"
	}
	return k[:6] + "…" + k[len(k)-4:]
}

// firstEnv 返回第一个非空的环境变量值。
func firstEnv(names ...string) string {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}
	return ""
}
