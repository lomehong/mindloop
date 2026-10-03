package cli

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lomehong/mindloop/internal/connector/sensor"
	"github.com/lomehong/mindloop/internal/identity"
)

// sensors 命令组是感知系统的用户入口：sensors.json 是唯一事实（用户
// 可直接编辑，心智热加载），CLI 只做读写与呈现——观察由感官运行器
// 保证，CLI 不替它看世界。
func (c *CLI) newSensorsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sensors",
		Short: "感知系统：给心智接入观察目标（file/git/web），事件按显著性分级叫醒",
		Long: `感知系统配置 <身份目录>/sensors.json，由心智内的感官运行器驱动：
观察到的变化按显著性分级（s0 只沉淀 / s1 进报告素材 / s2 叫醒心智 /
s3 告警），新感官前 N 天是学习期（全部封顶 s1，防唤醒风暴——
MINDLOOP_SENSOR_LEARN_DAYS 调整，条目上 learning_days: -1 关闭）。

自愿审批的戒律不变：s2 唤醒消耗自发档预算（MINDLOOP_SPONTANEOUS_
TOKENS 建议设置，doctor 会提醒），感知只观察、动作仍过审批门。`,
	}
	cmd.AddCommand(
		c.newSensorsListCmd(),
		c.newSensorsAddCmd(),
		c.newSensorsRemoveCmd(),
	)
	return cmd
}

// loadSensorFile 读 sensors.json（不存在 = 空配置）；写回前整包校验。
func loadSensorFile(dir string) (*sensor.File, error) {
	f, err := sensor.Load(filepath.Join(dir, "sensors.json"))
	if err != nil {
		return nil, err
	}
	if f == nil {
		f = &sensor.File{Version: 1}
	}
	return f, nil
}

// saveSensorFile 校验后原子写回（锁外的 best-effort：配置文件是
// 用户可直接编辑的事实，写失败如实报错）。
func saveSensorFile(dir string, f *sensor.File) error {
	if f.Version == 0 {
		f.Version = 1
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if err := f.Validate(); err != nil {
		return err
	}
	path := filepath.Join(dir, "sensors.json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (c *CLI) newSensorsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <身份名>",
		Short: "列出已配置的感官",
		Args:  exactArgs(1, "用法: mindloop sensors list <身份名>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := c.loadIdentity(args[0])
			if err != nil {
				return err
			}
			f, err := loadSensorFile(id.Dir)
			if err != nil {
				return c.fail(err)
			}
			if len(f.Sensors) == 0 {
				fmt.Fprintln(c.stdout, "未配置感官。示例：")
				fmt.Fprintf(c.stdout, "  mindloop sensors add %s file D:\\work\\project\n", id.Name)
				fmt.Fprintf(c.stdout, "  mindloop sensors add %s web https://example.com/blog\n", id.Name)
				return nil
			}
			for _, cfg := range f.Sensors {
				state := "启用"
				if !cfg.IsEnabled() {
					state = "禁用"
				}
				target := cfg.Path
				if target == "" {
					target = cfg.URL
				}
				line := fmt.Sprintf("%-14s %-8s %-4s %s", cfg.ID, cfg.Type, state, target)
				if len(cfg.Salience.Rules) > 0 {
					line += fmt.Sprintf("（%d 条规则）", len(cfg.Salience.Rules))
				}
				fmt.Fprintln(c.stdout, line)
			}
			return nil
		},
	}
}

func (c *CLI) newSensorsAddCmd() *cobra.Command {
	var idP, keywordP, quietP, intervalP string
	var learnP int
	var salienceP string
	cmd := &cobra.Command{
		Use:   "add <身份名> <file|git|web> <目标路径或 URL>",
		Short: "接入一个观察目标（学习期自动开始：前 3 天只沉淀不叫醒）",
		Long: `示例：
  mindloop sensors add ada file D:\work\project --keyword mindloop --rule prod:s3:生产,事故
  mindloop sensors add ada web https://example.com --every 30m
  mindloop sensors add ada git D:\work\repo

--rule 规则形态：名称:档位:关键词[,关键词2]——词面命中即升档
（s1-s3），reason 字段记规则名，是校准与"别再报了"归因的依据。`,
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			typ, target := args[1], args[2]
			id, err := c.loadIdentity(args[0])
			if err != nil {
				return err
			}
			f, err := loadSensorFile(id.Dir)
			if err != nil {
				return c.fail(err)
			}
			cfg := sensor.SensorConfig{
				ID:           idP,
				Type:         typ,
				Path:         target,
				URL:          target,
				Interval:     intervalP,
				Quiet:        parseQuietFlag(quietP),
				Keywords:     splitNonEmpty(keywordP, ","),
				LearningDays: learnP,
			}
			if cfg.ID == "" {
				cfg.ID = typ + "-" + id.Name + "-" + fmt.Sprintf("%d", len(f.Sensors)+1)
			}
			if f.Get(cfg.ID) != nil {
				return c.fail(fmt.Errorf("感官 id %q 已存在（先 sensors remove 或换 --id）", cfg.ID))
			}
			for _, r := range splitNonEmpty(salienceP, ",") {
				parts := strings.SplitN(r, ":", 3)
				if len(parts) != 3 {
					return c.fail(fmt.Errorf("--rule 形态应为 名称:档位:关键词列表，得 %q", r))
				}
				cfg.Salience.Rules = append(cfg.Salience.Rules, sensor.SalienceRule{
					Name: parts[0], Salience: sensor.Salience(strings.ToLower(parts[1])),
					Keywords: splitNonEmpty(parts[2], ","),
				})
			}
			if typ == "webhook" {
				// 独立 per-sensor HMAC 密钥（耳）：与控制面 token 隔离。
				key := make([]byte, 32)
				if _, err := rand.Read(key); err != nil {
					return c.fail(err)
				}
				cfg.Secret = base64.RawStdEncoding.EncodeToString(key)
				fmt.Fprintf(c.stdout, "webhook 密钥（HMAC 签名用，只显示这一次）: %s\n", cfg.Secret)
			}
			f.Sensors = append(f.Sensors, cfg)
			if err := saveSensorFile(id.Dir, f); err != nil {
				return c.fail(err)
			}
			fmt.Fprintf(c.stdout, "已接入感官 %s（%s %s）——学习期 %d 天内只沉淀不叫醒%s。\n",
				cfg.ID, typ, target, cfg.LearnDays(), map[bool]string{true: "（缺省）", false: ""}[learnP == 0])
			c.warnSensorTakeEffect(id)
			return nil
		},
	}
	fs := cmd.Flags()
	fs.StringVar(&idP, "id", "", "感官 id（缺省自动生成）")
	fs.StringVar(&keywordP, "keyword", "", "议程关键词（逗号分隔：命中的变化至少 s1）")
	fs.StringVar(&salienceP, "rule", "", "显著性规则（名称:档位:关键词列表，逗号分隔多条）")
	fs.StringVar(&quietP, "quiet", "", "安静窗口 HH:MM-HH:MM（窗口内只沉淀）")
	fs.StringVar(&intervalP, "every", "", "轮询间隔（git/web，如 5m）")
	fs.IntVar(&learnP, "learning-days", 0, "学习期天数覆盖（负数关闭学习期）")
	return cmd
}

func (c *CLI) newSensorsRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <身份名> <id>",
		Short: "移除一个感官",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := c.loadIdentity(args[0])
			if err != nil {
				return err
			}
			f, err := loadSensorFile(id.Dir)
			if err != nil {
				return c.fail(err)
			}
			for i, cfg := range f.Sensors {
				if cfg.ID == args[1] {
					f.Sensors = append(f.Sensors[:i], f.Sensors[i+1:]...)
					if err := saveSensorFile(id.Dir, f); err != nil {
						return c.fail(err)
					}
					fmt.Fprintf(c.stdout, "已移除 %s（心智在跑则数秒内热加载停止观察）\n", args[1])
					return nil
				}
			}
			return c.fail(fmt.Errorf("感官 %q 不存在（sensors list 查看）", args[1]))
		},
	}
}

func parseQuietFlag(v string) *sensor.QuietWindow {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	parts := strings.SplitN(v, "-", 2)
	if len(parts) != 2 {
		return nil
	}
	return &sensor.QuietWindow{Start: strings.TrimSpace(parts[0]), End: strings.TrimSpace(parts[1])}
}

func splitNonEmpty(s, sep string) []string {
	var out []string
	for _, p := range strings.Split(s, sep) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// warnSensorTakeEffect 与 warnScheduleTakeEffect 同款提示。
func (c *CLI) warnSensorTakeEffect(id *identity.Identity) {
	if mindRunning(id.Timeline.Dir) {
		fmt.Fprintln(c.stdout, "心智在运行：感官将在数秒内热加载生效。")
		return
	}
	fmt.Fprintf(c.stderr, "⚠ 心智没有在运行——感官已保存，启动后生效（mindloop chat %s）\n", id.Name)
}
