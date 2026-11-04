// watchdog 命令：死睡外部告警（service.md §8 开放问题 2 的落地，
// headlong thinkers-silence 的对应物；Super-agent 论文第 6 节外部
// Watchdog 的 service 层形态）。设计要点：
//
//   - 外部检查者：独立于 mind 进程存活——由计划任务/cron 周期调用
//     （`schtasks /create /tn Mindloop-Watchdog /tr <wrap.cmd>
//     /sc minute /mo 30`），连接器与企微推送独立于 mind 生命周期，
//     天然满足「主系统死了告警还活着」；
//   - 判据是磁盘事实：轨迹 mtime（最后一步年龄）+ 运行锁属主探活
//     （复用 mind.RunLockDir + traj.LockOwnerAlive，与 web 活性判定
//     同源）。两类死睡：A 进程死（锁不活 + 超阈值无步骤）；B 卡死
//     （锁活 + 超阈值无步骤——watchdog 合成唤醒不落盘，轨迹停更
//     即铁证）；
//   - 告警去重（service.md 悬置的设计点）：状态文件
//     <home>/watchdog-state.json——进入静默告一次，持续静默按
//     --realert 周期重告，恢复清账并记一行恢复；
//   - 出口三路：stdout（计划任务日志）、<home>/watchdog-alerts.jsonl
//     （机器可读台账）、--wecom-to wecom:<uid> 时经 SendOnce 推企微
//     （凭据由操作员在包装脚本显式配置——watchdog 是操作员工具）。
//   - 退出码恒 0：告警不是任务失败，schtasks 不该因此标红。
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/lomehong/mindloop/internal/connector/wecom"
	"github.com/lomehong/mindloop/internal/identity"
	"github.com/lomehong/mindloop/internal/mind"
	"github.com/lomehong/mindloop/internal/traj"
)

func (c *CLI) newWatchdogCmd() *cobra.Command {
	var threshold, realert string
	var wecomTo string
	cmd := &cobra.Command{
		Use:   "watchdog",
		Short: "死睡外部告警：轨迹 N 小时无新步骤 → 企微/日志提醒",
		Long: `死睡外部告警（service.md §8，headlong thinkers-silence 对应物）。

独立于 mind 进程存活的外部检查者：按轨迹 mtime 判定最后一步年龄，
配合运行锁属主探活区分两类死睡——A 进程死（锁不活且超阈值）、
B 卡死（锁活但超阈值无步骤）。告警去重：进入静默告一次，持续
静默按 --realert 重告，恢复清账。

由计划任务周期调用（建议 30 分钟）：
  schtasks /create /tn Mindloop-Watchdog /tr "<wrap.cmd>" /sc minute /mo 30

企微推送需 --wecom-to wecom:<userid> 且环境有 WECOM_BOT_ID /
WECOM_BOT_SECRET（操作员在包装脚本显式配置）。退出码恒 0——
告警不是任务失败。`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			th, err := parseDurFlag(threshold, 6*time.Hour)
			if err != nil {
				return fmt.Errorf("--threshold: %w", err)
			}
			ra, err := parseDurFlag(realert, 24*time.Hour)
			if err != nil {
				return fmt.Errorf("--realert: %w", err)
			}
			return c.runWatchdog(watchdogCfg{
				threshold: th,
				realert:   ra,
				wecomTo:   wecomTo,
			})
		},
	}
	fs := cmd.Flags()
	fs.StringVar(&threshold, "threshold", "6h", "静默判定阈值（Go duration；超过即视为死睡）")
	fs.StringVar(&realert, "realert", "24h", "持续静默的重告周期")
	fs.StringVar(&wecomTo, "wecom-to", "", "企微告警收件人（wecom:<userid>；缺省只写日志与台账）")
	return cmd
}

func parseDurFlag(s string, def time.Duration) (time.Duration, error) {
	if strings.TrimSpace(s) == "" {
		return def, nil
	}
	return time.ParseDuration(s)
}

// watchdogCfg 聚合解析后的运行参数。
type watchdogCfg struct {
	threshold time.Duration
	realert   time.Duration
	wecomTo   string
}

// watchdogStateEntry 是单身份的告警去重账目。
type watchdogStateEntry struct {
	LastAlert time.Time `json:"last_alert"`
	Kind      string    `json:"kind"` // dead | wedged
	Since     time.Time `json:"since"`
}

// watchdogAlert 是台账行（watchdog-alerts.jsonl）。
type watchdogAlert struct {
	TS      string `json:"ts"`
	Kind    string `json:"kind"` // dead | wedged | recovered
	Ident   string `json:"identity"`
	AgeSec  int64  `json:"age_s"`
	Message string `json:"message"`
}

func (c *CLI) runWatchdog(cfg watchdogCfg) error {
	now := time.Now()
	idents, err := scanWatchdogIdentities()
	if err != nil {
		// 扫不出来是检查者自身的故障——记 stderr 但不失败退出。
		fmt.Fprintf(c.stderr, "watchdog: 扫描身份失败: %v\n", err)
		return nil
	}
	state := loadWatchdogState()
	changed := false

	var names []string
	for name := range idents {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		st := idents[name]
		age := now.Sub(st.lastStep)
		entry, had := state[name]

		recover := func() {
			if had {
				delete(state, name)
				changed = true
				c.emitWatchdogAlert(cfg, watchdogAlert{
					TS: traj.NowString(), Kind: "recovered", Ident: name,
					Message: fmt.Sprintf("%s 恢复活动（此前 %s 自 %s 起静默）", name, entry.Kind, entry.Since.Local().Format("01-02 15:04")),
				})
			}
		}

		if age <= cfg.threshold {
			recover()
			continue
		}
		kind := "dead"
		if st.lockAlive {
			kind = "wedged"
		}
		// 去重：新 episode（无账目）或跨过重告周期才告。
		should := !had || entry.Kind != kind || now.Sub(entry.LastAlert) >= cfg.realert
		if !should {
			continue
		}
		if !had {
			state[name] = watchdogStateEntry{Kind: kind, Since: now}
		}
		e := state[name]
		e.LastAlert = now
		e.Kind = kind
		state[name] = e
		changed = true

		var msg string
		if kind == "dead" {
			msg = fmt.Sprintf("死睡告警：%s 心智未运行，轨迹已 %.1f 小时无新步骤（自 %s）",
				name, age.Hours(), st.lastStep.Local().Format("01-02 15:04"))
		} else {
			msg = fmt.Sprintf("死睡告警：%s 心智在运行但已 %.1f 小时无新步骤（疑似卡死；自 %s）",
				name, age.Hours(), st.lastStep.Local().Format("01-02 15:04"))
		}
		c.emitWatchdogAlert(cfg, watchdogAlert{
			TS: traj.NowString(), Kind: kind, Ident: name,
			AgeSec: int64(age.Seconds()), Message: msg,
		})
	}

	if changed {
		if err := saveWatchdogState(state); err != nil {
			fmt.Fprintf(c.stderr, "watchdog: 状态写入失败: %v\n", err)
		}
	}
	return nil
}

// emitWatchdogAlert 三路出口：stdout（计划任务日志）+ 台账 JSONL +
// 可选企微推送（失败降级为 stderr——推送坏了不该影响检测本身）。
func (c *CLI) emitWatchdogAlert(cfg watchdogCfg, a watchdogAlert) {
	fmt.Fprintln(c.stdout, a.Message)
	if data, err := json.Marshal(a); err == nil {
		if f, err := os.OpenFile(filepath.Join(traj.Home(), "watchdog-alerts.jsonl"),
			os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
			_, _ = f.Write(append(data, '\n'))
			_ = f.Close()
		}
	}
	if cfg.wecomTo != "" {
		botID := os.Getenv("WECOM_BOT_ID")
		secret := os.Getenv("WECOM_BOT_SECRET")
		if err := wecom.SendOnce(context.Background(), wecom.SendOnceOptions{
			BotID: botID, Secret: secret,
		}, cfg.wecomTo, a.Message); err != nil {
			fmt.Fprintf(c.stderr, "watchdog: 企微推送失败（已降级为日志）: %v\n", err)
		}
	}
}

// watchdogIdent 是单身份的磁盘事实快照。
type watchdogIdent struct {
	lastStep  time.Time
	lockAlive bool
}

// scanWatchdogIdentities 走磁盘（identity.Load 的目录约定），不依赖
// 任何在跑进程——外部检查者的本分。
func scanWatchdogIdentities() (map[string]watchdogIdent, error) {
	root := identity.Home()
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]watchdogIdent{}, nil
		}
		return nil, err
	}
	out := map[string]watchdogIdent{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id, err := identity.Load(e.Name())
		if err != nil {
			continue // 坏身份跳过——检测器不因个别残缺停摆
		}
		st := watchdogIdent{}
		if fi, err := os.Stat(id.Timeline.Path); err == nil {
			st.lastStep = fi.ModTime()
		} else {
			// 轨迹文件不存在：从未活动过——按 epoch 记，走常规阈值判定。
			st.lastStep = time.Time{}
		}
		lock := mind.RunLockDir(id.Timeline.Dir)
		if _, err := os.Stat(lock); err == nil && traj.LockOwnerAlive(lock) {
			st.lockAlive = true
		}
		out[e.Name()] = st
	}
	return out, nil
}

func watchdogStatePath() string {
	return filepath.Join(traj.Home(), "watchdog-state.json")
}

func loadWatchdogState() map[string]watchdogStateEntry {
	out := map[string]watchdogStateEntry{}
	data, err := os.ReadFile(watchdogStatePath())
	if err != nil {
		return out
	}
	_ = json.Unmarshal(data, &out)
	return out
}

// saveWatchdogState 原子写（tmp+rename）——与仓库其他状态文件同款纪律。
func saveWatchdogState(state map[string]watchdogStateEntry) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(watchdogStatePath())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := watchdogStatePath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, watchdogStatePath())
}
