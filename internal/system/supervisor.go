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
	job      jobObject
	logger   func(format string, args ...any)
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
// 路径，参数列表来自配置纯函数，无 shell）。
func (s *Supervisor) start(name ChildName) {
	args := Args(name, s.cfg)
	if len(args) == 0 || s.exePath == "" {
		return
	}
	logFile, err := os.OpenFile(s.logPath(name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		logFile = nil
	}
	cmd := &exec.Cmd{Path: s.exePath, Args: append([]string{s.exePath}, args...)}
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
	s.children[name] = &childProc{name: name, cmd: cmd, logFile: logFile, started: time.Now(), lastOK: time.Now()}
	if s.logger != nil {
		s.logger("system: %s 已启动（pid %d，第 %d 次派生）", name, cmd.Process.Pid, s.restarts[name])
	}
}

// reap 巡检退出的孩子：计划内停止的摘除；意外退出记因并按退避重启
// （1s 起指数翻倍封顶 60s；稳定运行 ≥ stableAfter 清零连败）。
func (s *Supervisor) reap() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for name, c := range s.children {
		if c.cmd.ProcessState == nil {
			if now.Sub(c.lastOK) >= stableAfter {
				c.fails = 0
				c.lastOK = now
			}
			continue
		}
		exit := c.cmd.ProcessState.String()
		s.lastExit[name] = exit
		stopping := c.stopping
		if c.logFile != nil {
			c.logFile.Close()
		}
		delete(s.children, name)
		s.projectLocked()
		if stopping {
			if s.logger != nil {
				s.logger("system: %s 已按计划停止", name)
			}
			continue
		}
		c.fails++
		s.restarts[name]++
		delay := time.Duration(1<<uint(min64(int64(c.fails), 6))) * time.Second
		if delay > backoffCap {
			delay = backoffCap
		}
		if s.logger != nil {
			s.logger("system: %s 退出（%s），%v 后第 %d 次重启", name, exit, delay, s.restarts[name])
		}
		time.AfterFunc(delay, func() {
			s.mu.Lock()
			defer s.mu.Unlock()
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

func (s *Supervisor) runningLocked() map[ChildName]bool {
	out := map[ChildName]bool{}
	for name, c := range s.children {
		if c.cmd.ProcessState == nil {
			out[name] = true
		}
	}
	return out
}

func (s *Supervisor) applyDesiredLocked() {
	for name := range s.cfg.Desired() {
		if _, ok := s.children[name]; ok {
			continue
		}
		s.start(name)
	}
	s.projectLocked()
}

// stopAll 计划内停止全部孩子（宿主停机；作业对象兜底收割）。
func (s *Supervisor) stopAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.children {
		c.stopping = true
		_ = c.cmd.Process.Kill()
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
			Name: string(name), Running: c.cmd.ProcessState == nil,
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

func pidOf(c *childProc) int {
	if c == nil || c.cmd == nil || c.cmd.Process == nil {
		return 0
	}
	return c.cmd.Process.Pid
}
