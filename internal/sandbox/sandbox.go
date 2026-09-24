// Package sandbox 在受控环境里执行生成的 bash 脚本——Headlong 的
// shellm 执行段 + Docker 看门狗的 Windows 原生对应物。
//
// 三条防线，全部在调用方返回前落地：
//   - Job Object 整树管辖：杀树不需要遍历 /proc，也不与 reparent
//     竞态；句柄关闭时 KILL_ON_JOB_CLOSE 兜底收割。
//   - 三类超时：总时长、空闲（无新输出）、输出过大——各有原因
//     标记，上层据此生成"为什么被杀"的可执行诊断。
//   - FINAL 副作用协议：完成信号是脚本写下的哨兵文件（内容来自
//     环境变量 FINAL），不是对模型响应文本的解析——对输出格式
//     漂移天然免疫。
package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// 默认防线参数。
const (
	DefaultTimeout        = 10 * time.Minute
	DefaultIdleTimeout    = 30 * time.Second
	DefaultMaxOutputBytes = 1 << 20 // 1MB，每管道
)

// ErrNoBash：找不到 bash。整条链路以 bash 为执行语言（Git Bash /
// MSYS2 均可）。
var ErrNoBash = errors.New("sandbox: 找不到 bash（请安装 Git Bash 并确保其在 PATH 中）")

// Request 描述一次受控执行。零值字段取默认防线参数。
type Request struct {
	// Script 是 bash 脚本正文（不含 FINAL 协议尾，由本包注入）。
	Script string
	// Dir 是工作目录；脚本与产物都落在这里。必填。
	Dir string
	// Env 追加到进程环境（NAME=VALUE）。
	Env []string
	// FinalPath 是 FINAL 哨兵文件路径；脚本里 set FINAL=... 即可
	// 生效。空串表示本次不检查 FINAL。
	FinalPath string
	// Timeout 是总时长上限（默认 10 分钟）。
	Timeout time.Duration
	// IdleTimeout 是无新输出的上限（默认 30 秒）。
	IdleTimeout time.Duration
	// MaxOutputBytes 是每管道的保留上限（默认 1MB；超出即杀）。
	MaxOutputBytes int64
	// MemLimitBytes 是单进程内存上限（字节；0 = 不限）。
	MemLimitBytes uint64
}

// KillReason 集合。
const (
	KillIdle     = "idle"
	KillOutput   = "output"
	KillTimeout  = "timeout"
	KillCanceled = "canceled"
)

// Result 是一次执行的结果。
type Result struct {
	ExitCode      int
	Stdout        string
	Stderr        string
	KillReason    string // "" 表示正常退出
	FinalSet      bool
	Duration      time.Duration
	StdoutDropped int64
	StderrDropped int64
}

// BashPath 返回 bash 的绝对路径或 ErrNoBash。
//
// 真实使用场景（Headlong 自带 bash 的开发机，部署到任意 Windows
// 用户的 PowerShell 环境）需要 fallback：PATH 里找不到时，扫描
// Git Bash / Git for Windows / MSYS2 / WSL 的常见安装位置，避免
// “明明装了 Git Bash 却报找不到”。
func BashPath() (string, error) {
	if p, err := exec.LookPath("bash"); err == nil {
		return p, nil
	}
	for _, p := range bashFallbackPaths() {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", ErrNoBash
}

// bashFallbackPaths 列出常见安装位置——按优先级排列（Git for Windows
// 64 位、Win32、便携版、MSYS2、SDKMAN、WSL）。
func bashFallbackPaths() []string {
	candidates := []string{
		`C:\Program Files\Git\bin\bash.exe`,
		`C:\Program Files\Git\usr\bin\bash.exe`,
		`C:\Program Files (x86)\Git\bin\bash.exe`,
		`C:\Program Files (x86)\Git\usr\bin\bash.exe`,
		`C:\Git\bin\bash.exe`,
		`C:\msys64\usr\bin\bash.exe`,
		`C:\Program Files\Git-sdk\usr\bin\bash.exe`,
	}
	for _, ev := range []string{"ProgramFiles", "ProgramFiles(x86)", "LOCALAPPDATA", "USERPROFILE"} {
		dir := os.Getenv(ev)
		if dir == "" {
			continue
		}
		candidates = append(candidates,
			filepath.Join(dir, "Git", "bin", "bash.exe"),
			filepath.Join(dir, "Git", "usr", "bin", "bash.exe"),
		)
	}
	return candidates
}

// capture 是并发安全的带上限输出缓冲：内容截断保留、字节照常
// 计数、记录最后写入时刻供空闲判定。Write 永不阻塞、永不报错
// ——输出管道的背压不该干扰被监控的进程。
type capture struct {
	mu      sync.Mutex
	buf     bytes.Buffer
	bytes   int64
	max     int64
	dropped int64
	last    time.Time
}

func (c *capture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.bytes += int64(len(p))
	c.last = time.Now()
	if c.max <= 0 || c.buf.Len() < int(c.max) {
		room := int(c.max) - c.buf.Len()
		if c.max <= 0 || room > len(p) {
			room = len(p)
		}
		if room > 0 {
			c.buf.Write(p[:room])
		}
	}
	if c.max > 0 && c.bytes > c.max {
		c.dropped = c.bytes - c.max
	}
	return len(p), nil
}

func (c *capture) stats() (bytes int64, last time.Time, dropped int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bytes, c.last, c.dropped
}

func (c *capture) snapshotResult() (string, int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String(), c.dropped
}

// newCapture 以当前时刻为空闲基线：刚启动的进程还没有输出，
// 但不能因此立刻被判空闲。
func newCapture(max int64) *capture {
	return &capture{max: max, last: time.Now()}
}

// Run 执行一次脚本并返回结果。ctx 取消等价于"立即杀"。
func Run(ctx context.Context, req Request) (Result, error) {
	if req.Dir == "" {
		return Result{}, errors.New("sandbox: 缺少工作目录")
	}
	if fi, err := os.Stat(req.Dir); err != nil || !fi.IsDir() {
		return Result{}, fmt.Errorf("sandbox: 工作目录不可用 %s: %w", req.Dir, err)
	}
	bash, err := BashPath()
	if err != nil {
		return Result{}, err
	}
	if req.Timeout <= 0 {
		req.Timeout = DefaultTimeout
	}
	if req.IdleTimeout <= 0 {
		req.IdleTimeout = DefaultIdleTimeout
	}
	if req.MaxOutputBytes <= 0 {
		req.MaxOutputBytes = DefaultMaxOutputBytes
	}

	// FINAL 协议尾：set -e 之下脚本失败时不会执行——失败不产生
	// FINAL，这是有意的语义。
	script := "set -e\n" + req.Script + "\n"
	if req.FinalPath != "" {
		script += "\nif [ -n \"${FINAL+x}\" ]; then printf '%s' \"$FINAL\" > \"$FINAL_PATH\"; fi\n"
	}
	scriptFile, err := os.CreateTemp(req.Dir, ".mindloop-script-*.sh")
	if err != nil {
		return Result{}, fmt.Errorf("sandbox: 写脚本: %w", err)
	}
	scriptPath := scriptFile.Name()
	defer os.Remove(scriptPath)
	if _, err := scriptFile.WriteString(script); err != nil {
		scriptFile.Close()
		return Result{}, fmt.Errorf("sandbox: 写脚本: %w", err)
	}
	if err := scriptFile.Close(); err != nil {
		return Result{}, fmt.Errorf("sandbox: 写脚本: %w", err)
	}

	cmd := exec.Command(bash, scriptPath)
	cmd.Dir = req.Dir
	cmd.Env = append(os.Environ(), req.Env...)
	if req.FinalPath != "" {
		cmd.Env = append(cmd.Env, "FINAL_PATH="+req.FinalPath)
	}

	stdout := newCapture(req.MaxOutputBytes)
	stderr := newCapture(req.MaxOutputBytes)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	res := Result{}
	start := time.Now()
	if err := cmd.Start(); err != nil {
		return res, fmt.Errorf("sandbox: 启动 bash: %w", err)
	}

	// Job Object：尽力而为。分配失败不阻止执行——进程级 Kill 仍
	// 生效，只是失去整树管辖（degraded 模式）。
	j, jobErr := newJob(req.MemLimitBytes)
	if jobErr == nil {
		defer j.close()
		if err := j.assignPID(cmd.Process.Pid); err != nil {
			j.close()
			j = nil
		}
	}

	killReason := ""
	var killOnce sync.Once
	kill := func(reason string) {
		killOnce.Do(func() {
			killReason = reason
			j.terminate()
			_ = cmd.Process.Kill()
		})
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	total := time.NewTimer(req.Timeout)
	defer total.Stop()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()

	var killedAt time.Time
	waiting := true
	for waiting {
		select {
		case err := <-done:
			waiting = false
			if err != nil {
				var exitErr *exec.ExitError
				if errors.As(err, &exitErr) {
					res.ExitCode = exitErr.ExitCode()
				} else {
					return res, fmt.Errorf("sandbox: 等待进程: %w", err)
				}
			}
		case <-ctx.Done():
			kill(KillCanceled)
		case <-total.C:
			kill(KillTimeout)
		case <-tick.C:
			if killReason != "" {
				// 杀不掉的极端进程：等 5 秒后放弃等待，交由
				// KILL_ON_JOB_CLOSE 在句柄关闭时兜底收割。
				if time.Since(killedAt) > 5*time.Second {
					waiting = false
				}
				continue
			}
			// 输出过大：任一管道超出保留上限即杀。
			_, _, oDropped := stdout.stats()
			_, _, eDropped := stderr.stats()
			if oDropped > 0 || eDropped > 0 {
				kill(KillOutput)
				killedAt = time.Now()
				continue
			}
			// 空闲判定：两个管道在 IdleTimeout 内都无新字节。
			_, oLast, _ := stdout.stats()
			_, eLast, _ := stderr.stats()
			last := oLast
			if eLast.After(last) {
				last = eLast
			}
			if time.Since(last) >= req.IdleTimeout {
				kill(KillIdle)
				killedAt = time.Now()
			}
		}
		if killReason != "" && killedAt.IsZero() {
			killedAt = time.Now()
		}
	}

	res.Stdout, res.StdoutDropped = stdout.snapshotResult()
	res.Stderr, res.StderrDropped = stderr.snapshotResult()
	res.KillReason = killReason
	res.Duration = time.Since(start)
	if req.FinalPath != "" {
		if _, err := os.Stat(req.FinalPath); err == nil {
			res.FinalSet = true
		}
	}
	return res, nil
}
