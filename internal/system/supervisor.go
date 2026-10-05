// supervisor.go — 宿主的监督循环：按 Config 派生期望子进程集
// （self-exec 既有命令），退出即指数退避重启，system.json 变更
// 热加载按 Diff 增停启，状态投影原子写 system-status.json。
//
// 收割语义：全部子进程登记进 Windows Job Object（KILL_ON_JOB_CLOSE
// job_windows.go）——宿主被杀时孩子连带收割，孤儿不会占住运行锁
// 饿死重启后的同名人。非 Windows 无作业对象，靠 stop 命令与运行锁
// 兜底（设计 §6 非目标：跨平台作业控制另立项）。
package system

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lomehong/mindloop/internal/traj"
)

const (
	reloadEvery = 5 * time.Second  // 配置热加载检查周期
	backoffCap  = 60 * time.Second // 重启退避上限
	stableAfter = 5 * time.Minute  // 稳定运行多久清零连败
)

// childProc 是一个被监督的子进程。
type childProc struct {
	name     ChildName
	cmd      *exec.Cmd
	logFile  *os.File
	started  time.Time
	fails    int       // 连败计数（退避用）
	lastOK   time.Time // 稳定计时起点
	stopping bool      // 计划内停止（不重启）
	exited   bool      // Wait 落定（ProcessState 只在 Wait 后可用——退出判据用它）
	fp       string    // 启动参数指纹（Args 派生）——热加载检测参数变更换参重启
}

// ChildStatus 是状态投影里一个孩子的条目。
type ChildStatus struct {
	Name     string `json:"name"`
	Running  bool   `json:"running"`
	PID      int    `json:"pid,omitempty"`
	Restarts int    `json:"restarts"`
	LastExit string `json:"last_exit,omitempty"`
	Since    string `json:"since,omitempty"`
	Stopping bool   `json:"stopping,omitempty"`
}

// Supervisor 是宿主。
type Supervisor struct {
	home    string
	exePath string // self 绝对路径（解析+校验一次，Cmd 结构体装配）

	mu       sync.Mutex
	cfg      *Config
	children map[ChildName]*childProc
	restarts map[ChildName]int
	lastExit map[ChildName]string
	// fails 是按名字的连败计数——存活于孩子生命周期之外。挂在
	// childProc 上会在摘除时丢失、新孩子又从 0 起，退避恒 1s
	//（2026-10-04 全系统测试 #6：205 连崩全部 1s，指数承诺失效）。
	fails   map[ChildName]int
	job     jobObject
	stopped bool                                    // 停机：禁止一切重启路径（含已排定的退避定时器）
	spawn   func(name ChildName) (string, []string) // 子进程命令注入点（测试用）；nil = self-exec
	logger  func(format string, args ...any)
}

// NewSupervisor 构造宿主。cfg 为 nil 时走 Defaults 推导（零配置即
// 用）。exe 是当前可执行文件路径（os.Executable），此处统一解析
// 校验（与 mcp 客户端同一收口形状：绝对路径、存在、非目录）。
func NewSupervisor(home, exe string, cfg *Config, logger func(format string, args ...any)) *Supervisor {
	resolved, err := filepath.Abs(exe)
	if err == nil {
		resolved = filepath.Clean(resolved)
		if fi, statErr := os.Stat(resolved); statErr != nil || fi.IsDir() {
			err = fmt.Errorf("可执行文件不可用: %s", resolved)
		}
	}
	if err != nil && logger != nil {
		logger("system: %v", err)
	}
	if cfg == nil || cfg.IsEmpty() {
		cfg = Defaults() // 零配置即用（冒烟实证：空配置不推导 = 监督 0 个孩子）
	}
	return &Supervisor{
		home:     home,
		exePath:  resolved,
		cfg:      cfg,
		children: map[ChildName]*childProc{},
		restarts: map[ChildName]int{},
		lastExit: map[ChildName]string{},
		fails:    map[ChildName]int{},
		logger:   logger,
	}
}

// Run 运行宿主直到 ctx 取消：初始派生 → 热加载循环 → 退出收割。
func (s *Supervisor) Run(ctx context.Context) error {
	if err := s.job.init(); err != nil && s.logger != nil {
		s.logger("system: 作业对象初始化失败（收割退化为 stop 命令）: %v", err)
	}
	s.mu.Lock()
	s.applyDesiredLocked()
	started := len(s.children)
	s.mu.Unlock()
	if s.logger != nil {
		s.logger("system: 宿主启动，监督 %d 个子进程", started)
	}

	tick := time.NewTicker(reloadEvery)
	defer tick.Stop()
	monitor := time.NewTicker(500 * time.Millisecond)
	defer monitor.Stop()
	for {
		select {
		case <-ctx.Done():
			s.stopAll()
			s.job.close()
			s.project()
			return ctx.Err()
		case <-tick.C:
			s.reload()
		case <-monitor.C:
			s.reap()
		}
	}
}

// start 派生一个孩子（调用方须持锁）。输出重定向到
// <home>/logs/<名>.log（追加）——孩子自己的结构化产物（轨迹/台账）
// 不经过宿主。命令装配走 Cmd 结构体（程序位 = 已校验的 self 绝对
// 路径，参数列表来自配置纯函数，无 shell；s.spawn 为测试注入点）。
func (s *Supervisor) start(name ChildName) {
	path, args := s.exePath, Args(name, s.cfg)
	if s.spawn != nil {
		path, args = s.spawn(name)
	}
	if path == "" || len(args) == 0 {
		return
	}
	// 日志目录显式创建：service install 路径会建，宿主直启此前不
	// 建——OpenFile 失败被静默吞掉，子进程输出整体丢失且无任何
	// 痕迹（2026-10-04 全系统测试候选#7）。
	if err := os.MkdirAll(filepath.Dir(s.logPath(name)), 0o755); err != nil && s.logger != nil {
		s.logger("system: 日志目录创建失败（子进程输出将丢失）: %v", err)
	}
	logFile, err := os.OpenFile(s.logPath(name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		logFile = nil
		if s.logger != nil {
			s.logger("system: %s 日志文件打开失败（子进程输出将丢失）: %v", name, err)
		}
	}
	cmd := &exec.Cmd{Path: path, Args: append([]string{path}, args...)}
	if logFile != nil {
		cmd.Stdout, cmd.Stderr = logFile, logFile
	}
	cmd.Dir = s.home // 相对路径配置（file 感官 "." 等）以状态根为工作目录
	if err := cmd.Start(); err != nil {
		if logFile != nil {
			logFile.Close()
		}
		s.lastExit[name] = "启动失败: " + err.Error()
		if s.logger != nil {
			s.logger("system: %s 启动失败: %v", name, err)
		}
		return
	}
	s.job.assign(cmd.Process)
	c := &childProc{name: name, cmd: cmd, logFile: logFile, started: time.Now(), lastOK: time.Now(), fp: s.fingerprint(name)}
	s.children[name] = c
	// 退出的唯一落定点：ProcessState 只在 Wait 后可用——没有这个
	// goroutine，reap 会把死孩子永远视作运行中（崩溃自愈与停后再
	// 启双双静默失效，冒烟实测复现）。
	go func() {
		_ = cmd.Wait()
		s.mu.Lock()
		c.exited = true
		s.mu.Unlock()
	}()
	if s.logger != nil {
		s.logger("system: %s 已启动（pid %d，第 %d 次派生）", name, cmd.Process.Pid, s.restarts[name])
	}
}

// reap 巡检退出的孩子（exited 由 start 的 Wait goroutine 落定）：计划
// 内停止的摘除；意外退出记因并按退避重启（1s 起指数翻倍封顶 60s；
// 稳定运行 ≥ stableAfter 清零连败）。
func (s *Supervisor) reap() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for name, c := range s.children {
		if !c.exited {
			if now.Sub(c.lastOK) >= stableAfter {
				s.fails[name] = 0 // 稳定运行清零：退避从 1s 重新起步
				c.lastOK = now
			}
			continue
		}
		exit := ""
		if c.cmd.ProcessState != nil {
			exit = c.cmd.ProcessState.String()
		}
		s.lastExit[name] = exit
		stopping := c.stopping
		s.removeChildLocked(name)
		s.projectLocked()
		if stopping {
			if s.logger != nil {
				s.logger("system: %s 已按计划停止", name)
			}
			continue
		}
		// 连败计数在 Supervisor 上（跨孩子存活）：摘除-重启不丢，
		// 指数退避才真正翻倍（1s→2s→…→60s 封顶）。
		s.fails[name]++
		s.restarts[name]++
		delay := backoffDelay(s.fails[name])
		if s.logger != nil {
			s.logger("system: %s 退出（%s），%v 后第 %d 次重启", name, exit, delay, s.restarts[name])
		}
		time.AfterFunc(delay, func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			if s.stopped {
				return // 宿主已停机：幽灵重启防线
			}
			if _, ok := s.children[name]; ok {
				return
			}
			if !s.cfg.Desired()[name] {
				return // 热加载已决定不要它
			}
			s.start(name)
		})
	}
}

// removeChildLocked 摘除子进程条目（reap 与 applyDesiredLocked 共用）。
func (s *Supervisor) removeChildLocked(name ChildName) {
	if c, ok := s.children[name]; ok {
		if c.logFile != nil {
			c.logFile.Close()
		}
		delete(s.children, name)
	}
}

// reload 热加载 system.json：文件缺失 = 沿用现行（不因误删全停）；
// 坏配置拒绝并保留现行（fail-closed）；合法配置按 Diff 增停启。
func (s *Supervisor) reload() {
	c, err := Load(s.home)
	if err != nil {
		if s.logger != nil {
			s.logger("system: 配置热加载拒绝（保留现行）: %v", err)
		}
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(c.Identities) == 0 && !c.Web.Enabled {
		return // 零值 = 文件不存在：沿用现行（含启动时推导的缺省）
	}
	s.cfg = c
	// 参数变更收敛：名字集不变但参数变了（如 web 端口 8095→8096）
	// 的孩子，Diff 看不见——按指纹检测，标记计划内停止换参重启
	//（reap 摘除后下一轮 applyDesiredLocked 以新参数拉起；失败计数
	// 不涨——计划内换参不是连败）。
	desired := c.Desired()
	for name, ch := range s.children {
		if ch.exited || !desired[name] {
			continue
		}
		if fp := s.fingerprint(name); fp != ch.fp {
			ch.stopping = true
			_ = ch.cmd.Process.Kill()
			if s.logger != nil {
				s.logger("system: %s 参数变更，重启换参", name)
			}
		}
	}
	plan := c.Diff(s.runningLocked())
	for name := range plan.Stop {
		if p, ok := s.children[name]; ok {
			p.stopping = true
			_ = p.cmd.Process.Kill()
		}
	}
	s.applyDesiredLocked()
	if s.logger != nil && (len(plan.Start) > 0 || len(plan.Stop) > 0) {
		s.logger("system: 配置热加载（+%d/-%d）", len(plan.Start), len(plan.Stop))
	}
}

// fingerprint 是配置派生的孩子参数指纹：热加载对比用——名字集
// 相同而参数不同的变更靠它收敛（Diff 只看名字集）。
func (s *Supervisor) fingerprint(name ChildName) string {
	return strings.Join(Args(name, s.cfg), "\x00")
}

func (s *Supervisor) runningLocked() map[ChildName]bool {
	out := map[ChildName]bool{}
	for name, c := range s.children {
		if !c.exited {
			out[name] = true
		}
	}
	return out
}

func (s *Supervisor) applyDesiredLocked() {
	for name := range s.cfg.Desired() {
		if c, ok := s.children[name]; ok {
			if !c.exited {
				continue
			}
			s.removeChildLocked(name) // 已退出未及收割：允许立刻重新拉起
		}
		s.start(name)
	}
	s.projectLocked()
}

// stopAll 计划内停止全部孩子（宿主停机；作业对象兜底收割）。
func (s *Supervisor) stopAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopped = true
	for _, c := range s.children {
		c.stopping = true
		_ = c.cmd.Process.Kill()
	}
	// 有界等待孩子退出并同步收割（关日志句柄、摘出映射）：宿主
	// 返回后不留持句柄的垂死孩子——否则 Windows 上日志文件被继承
	// 句柄锁住，停机后的目录清理/轮转全部撞锁（2026-10-04 测试实
	// 证：退避拉长后清理窗口撞上）。退出判定 c.exited 由 start 的
	// Wait goroutine 落定（其持锁窗口极短，这里解锁轮询）。
	deadline := time.Now().Add(3 * time.Second)
	for _, c := range s.children {
		for !c.exited && time.Now().Before(deadline) {
			s.mu.Unlock()
			time.Sleep(20 * time.Millisecond)
			s.mu.Lock()
		}
		if c.exited {
			s.removeChildLocked(c.name)
		}
	}
}

// Status 是状态投影快照（CLI status 与 /api/system 共用）。
func (s *Supervisor) Status() []ChildStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.statusLocked()
}

func (s *Supervisor) statusLocked() []ChildStatus {
	out := make([]ChildStatus, 0, len(s.children))
	for name, c := range s.children {
		out = append(out, ChildStatus{
			Name: string(name), Running: !c.exited,
			PID: pidOf(c), Since: c.started.Format(traj.TimeFormat),
			Restarts: s.restarts[name], LastExit: s.lastExit[name], Stopping: c.stopping,
		})
	}
	return out
}

// Project 立即刷新一次状态投影（CLI 宿主的保鲜循环调用）。
func (s *Supervisor) Project() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.projectLocked()
}

// project 是内部便捷名。
func (s *Supervisor) project() {
	s.Project()
}

// projectLocked 写投影（调用方须持锁）。
func (s *Supervisor) projectLocked() {
	st := struct {
		Updated  string        `json:"updated"`
		Children []ChildStatus `json:"children"`
	}{Updated: traj.NowString(), Children: s.statusLocked()}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return
	}
	path := StatusPath(s.home)
	tmp := path + ".tmp"
	if os.WriteFile(tmp, data, 0o644) == nil {
		_ = os.Rename(tmp, path)
	}
}

func (s *Supervisor) logPath(name ChildName) string {
	safe := strings.NewReplacer(":", "-", "\\", "-", "/", "-").Replace(string(name))
	return filepath.Join(s.home, "logs", safe+".log")
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

// backoffDelay 折算第 fails 次连败后的重启延迟：1s 起指数翻倍、
// backoffCap(60s) 封顶。纯函数——退避曲线此前只有集成测试断言
// "重启 ≥2 次"，曲线本身（翻倍节奏与封顶）无人验证。
func backoffDelay(fails int) time.Duration {
	if fails < 1 {
		fails = 1
	}
	delay := time.Duration(1<<uint(min64(int64(fails-1), 6))) * time.Second
	if delay > backoffCap {
		return backoffCap
	}
	return delay
}

func pidOf(c *childProc) int {
	if c == nil || c.cmd == nil || c.cmd.Process == nil {
		return 0
	}
	return c.cmd.Process.Pid
}
