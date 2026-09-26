// guard.go 是模型请求的准入守卫：熔断（连续失败冷却 + 单次探测）
// 与身份每日 token 预算。守卫读健康标记与用量台账（观测面的既有
// 产物），装配层把它挂到 llm.Client.Gate 上——请求路径每次尝试前
// 调用，拒绝时返回 llm 的准入哨兵错误，请求不会发出。
//
// 熔断判定基于 UsageRecorder 已维护的连续错误计数；只有模型调用
// 收尾会写健康标记，普通工具的非零退出不经过这里，天然不计作
// 供应商故障。冷却结束后只放行一次探测：探测成功（计数清零）即
// 恢复；失败则更新失败时刻，重新冷却。
package obs

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"mindloop/internal/llm"
	"mindloop/internal/traj"
)

const (
	// CircuitThreshold 是触发熔断的连续失败次数。
	CircuitThreshold = 3
	// circuitCooldown 是熔断后的冷却时长。
	circuitCooldown = 5 * time.Minute
)

// Guard 是准入守卫。一个身份一份，装配时挂到该身份的各个客户端
// （思考档/请求档/摘要档）上。
type Guard struct {
	dir   string
	daily int // 每日 token 预算上限；<=0 未设置即不启用
	logf  func(format string, args ...any)

	mu      sync.Mutex // 冷却与探测状态
	probing bool       // 冷却后的一次探测已放行、结果尚未落盘
	probeAt time.Time  // 探测放行时刻（与健康标记的 LastCheck 比较）

	dailyMu sync.Mutex // 当日用量增量聚合
	day     string     // 已统计的 UTC 日期
	counted int        // 已计入的当日 token 合计
	offset  int64      // 台账已消费的字节偏移
}

// NewGuard 从环境读每日预算构造守卫：MINDLOOP_DAILY_TOKENS，未设
// 或非法即不启用（0）。
func NewGuard(dir string, logf func(format string, args ...any)) *Guard {
	daily := 0
	if v := strings.TrimSpace(os.Getenv("MINDLOOP_DAILY_TOKENS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			daily = n
		}
	}
	return newGuard(dir, daily, logf)
}

func newGuard(dir string, daily int, logf func(format string, args ...any)) *Guard {
	return &Guard{dir: dir, daily: daily, logf: logf}
}

// Attach 把守卫挂到客户端上；返回同一指针便于装配链式书写。
func (g *Guard) Attach(c *llm.Client) *llm.Client {
	c.Gate = g.Allow
	return c
}

// Allow 是请求准入检查：每日预算 → 熔断。拒绝时返回 llm 的哨兵
// 错误（ErrDailyBudget / ErrCircuitOpen），调用方可 errors.Is 识别。
func (g *Guard) Allow(_ context.Context) error {
	if err := g.allowDaily(); err != nil {
		return err
	}
	return g.allowCircuit()
}

// allowDaily 检查当日 token 累计是否达到预算上限。观测面故障
// （台账读不动）按放行处理——守卫不能反过来变成新的故障面。
func (g *Guard) allowDaily() error {
	if g.daily <= 0 {
		return nil
	}
	used, err := g.dailyTokens()
	if err != nil {
		if g.logf != nil {
			g.logf("obs: 统计当日用量失败（按放行处理）: %v", err)
		}
		return nil
	}
	if used >= g.daily {
		return fmt.Errorf("%w（今日 %d/%d tokens，UTC 日界重置）", llm.ErrDailyBudget, used, g.daily)
	}
	return nil
}

// dailyTokens 增量聚合当日 token：只消费以换行结尾的完整行，残行
// 留给下次；文件被替换或截断（大小小于已消费偏移）时从头重扫；
// UTC 日期变化重置为零起点。失败行（error 非空）不计入——与用量
// 页的聚合同一口径。
func (g *Guard) dailyTokens() (int, error) {
	g.dailyMu.Lock()
	defer g.dailyMu.Unlock()
	today := time.Now().UTC().Format("2006-01-02")
	if today != g.day {
		g.day, g.counted, g.offset = today, 0, 0
	}
	f, err := os.Open(filepath.Join(g.dir, "usage", "llm-usage.jsonl"))
	if err != nil {
		return g.counted, nil // 台账不存在 = 零用量
	}
	defer f.Close()
	if fi, err := f.Stat(); err == nil && fi.Size() < g.offset {
		g.counted, g.offset = 0, 0
	}
	if _, err := f.Seek(g.offset, io.SeekStart); err != nil {
		return g.counted, err
	}
	reader := bufio.NewReaderSize(f, 64*1024)
	for {
		line, rerr := reader.ReadString('\n')
		if rerr != nil {
			break // EOF 或半行：未以换行结尾的残行不消费
		}
		g.offset += int64(len(line))
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var rec struct {
			TS               string `json:"ts"`
			PromptTokens     int    `json:"prompt_tokens"`
			CompletionTokens int    `json:"completion_tokens"`
			Error            string `json:"error"`
		}
		if json.Unmarshal([]byte(line), &rec) != nil || len(rec.TS) < 10 {
			continue
		}
		if rec.TS[:10] != today || rec.Error != "" {
			continue
		}
		g.counted += rec.PromptTokens + rec.CompletionTokens
	}
	return g.counted, nil
}

// allowCircuit 检查熔断状态。探测是否结束以健康标记的 LastCheck
// 是否晚于放行时刻判定——UsageRecorder 每次调用收尾都会更新它。
func (g *Guard) allowCircuit() error {
	h := LoadHealth(filepath.Join(g.dir, "llm-health.json"))
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.probing {
		if lc, err := time.Parse(traj.TimeFormat, h.LastCheck); err == nil && lc.After(g.probeAt) {
			g.probing = false // 探测结果已落盘
		}
	}
	if h.ConsecutiveErrors < CircuitThreshold {
		g.probing = false
		return nil
	}
	if at, perr := time.Parse(traj.TimeFormat, h.LastErrorAt); perr == nil {
		if until := at.Add(circuitCooldown); time.Now().UTC().Before(until) {
			return fmt.Errorf("%w：连续 %d 次失败，%s 前拒绝新请求（mindloop llm resume 可显式恢复）",
				llm.ErrCircuitOpen, h.ConsecutiveErrors, until.Format("15:04:05Z"))
		}
	}
	if g.probing {
		return fmt.Errorf("%w：探测请求进行中", llm.ErrCircuitOpen)
	}
	g.probing = true
	g.probeAt = time.Now().UTC()
	return nil
}

// ClearHealth 显式恢复：清零连续错误计数，返回清除前的健康标记
// （供命令报告恢复前状态）。写盘走锁 + 临时文件 + 原子改名，与
// UsageRecorder 同一纪律。
func ClearHealth(dir string) (Health, error) {
	path := filepath.Join(dir, "llm-health.json")
	release, err := traj.AcquireDirLock(context.Background(), path+".lock", 5*time.Second)
	if err != nil {
		return Health{}, fmt.Errorf("obs: 健康标记加锁失败: %w", err)
	}
	defer release()
	old := LoadHealth(path)
	h := Health{LastOK: old.LastOK, LastCheck: traj.NowString()}
	data, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return old, err
	}
	tmp := fmt.Sprintf("%s.%d.tmp", path, os.Getpid())
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return old, err
	}
	if err := os.Rename(tmp, path); err != nil {
		return old, err
	}
	return old, nil
}

// AdmissionStatus 是准入状态的只读快照，供 CLI 与仪表盘展示：每日
// 预算上限与当日消耗、熔断的连续失败计数与冷却截止。
type AdmissionStatus struct {
	DailyLimit        int    `json:"daily_limit"`             // 0 = 未设置
	UsedToday         int    `json:"used_today"`              // 今日（UTC 日界）已消耗 token；失败行不计
	ConsecutiveErrors int    `json:"consecutive_errors"`      // 连续失败计数（健康标记）
	CircuitThreshold  int    `json:"circuit_threshold"`       // 触发熔断的阈值
	CoolingUntil      string `json:"cooling_until,omitempty"` // 冷却截止；空 = 未在冷却
}

// LoadAdmission 读一个身份的准入状态快照：预算上限来自环境
// （MINDLOOP_DAILY_TOKENS），当日消耗来自台账聚合，熔断来自健康
// 标记。只读、无副作用——不写文件，也不占用探测名额：冷却已过但
// 计数未清零时快照不报冷却（下一次调用会被放行为探测）。
func LoadAdmission(dir string) AdmissionStatus {
	g := NewGuard(dir, nil)
	used, _ := g.dailyTokens() // 读不动台账时退回已计到的数（守卫本身按放行处理）
	h := LoadHealth(filepath.Join(dir, "llm-health.json"))
	st := AdmissionStatus{
		DailyLimit:        g.daily,
		UsedToday:         used,
		ConsecutiveErrors: h.ConsecutiveErrors,
		CircuitThreshold:  CircuitThreshold,
	}
	if h.ConsecutiveErrors >= CircuitThreshold && h.LastErrorAt != "" {
		if at, perr := time.Parse(traj.TimeFormat, h.LastErrorAt); perr == nil {
			if until := at.Add(circuitCooldown); time.Now().UTC().Before(until) {
				st.CoolingUntil = until.Format(traj.TimeFormat)
			}
		}
	}
	return st
}
