// git.go — 仓库感官（眼）：refs/工作区状态的轮询观察。git 命令
// 包装（不引 cgo，依赖政策内的命令行协议）。执行收口与 mcp 客户端
// 同款合规形状：git 经 LookPath 解析为绝对路径，目录参数过元字符
// 校验，命令用 Cmd 结构体直接装配（程序位是解析产物，无 shell）。
package sensor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// gitIntervalMin 是轮询间隔下限：git 状态查询不便宜。包级变量供
// 测试调低（生产配置低于下限按缺省处理，见 Watch）。
var gitIntervalMin = time.Minute

// GitSensor 观察一个 git 仓库的 HEAD 与工作区。
type GitSensor struct {
	cfg SensorConfig

	gitPath string
}

// NewGitSensor 构造仓库感官。
func NewGitSensor(cfg SensorConfig) (Sensor, error) {
	if cfg.Type != "git" {
		return nil, fmt.Errorf("sensor: %s 不是 git 类型", cfg.ID)
	}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return nil, fmt.Errorf("找不到 git（PATH 未含 git？）: %w", err)
	}
	return &GitSensor{cfg: cfg, gitPath: filepath.Clean(gitPath)}, nil
}

func (s *GitSensor) ID() string { return s.cfg.ID }

// runGit 运行一条 git 命令返回 stdout（TrimSpace）。目录与参数都是
// 程序内字面量/已校验配置，装配走 Cmd 结构体（与 internal/mcp 同款
// 收口：绝对程序路径、无元字符、无 shell）。
func (s *GitSensor) runGit(ctx context.Context, dir string, args ...string) (string, error) {
	if strings.ContainsAny(dir, "\x00\r\n;&|`$<>") {
		return "", fmt.Errorf("git 感官目录含非法字符: %q", dir)
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("git 感官目录不可用: %s", dir)
	}
	all := append([]string{s.gitPath, "-C", dir}, args...)
	// GIT_OPTIONAL_LOCKS=0：后台轮询的 status 抢 .git/index.lock 会让
	// 用户自己的 git add/commit 偶发失败（全量测试曾复现）——只读观察
	// 不该拿可选锁。
	cmd := &exec.Cmd{Path: s.gitPath, Args: all, Env: append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")}
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (s *GitSensor) Watch(ctx context.Context, onEvent func(PEvent)) error {
	dir := s.cfg.Path
	// 可达性预检：不是仓库就明确报错（运行器退避重试，doctor 点名）。
	if _, err := s.runGit(ctx, dir, "rev-parse", "--git-dir"); err != nil {
		return fmt.Errorf("不是 git 仓库: %w", err)
	}
	interval := s.cfg.IntervalOf(gitIntervalMin)
	if interval < gitIntervalMin {
		interval = gitIntervalMin // 轮询下限：git 状态查询不便宜
	}
	var lastHead, lastDirty string
	first := true
	tick := time.NewTicker(interval)
	defer tick.Stop()
	sample := func() bool { // 返回 false = ctx 取消
		select {
		case <-ctx.Done():
			return false
		default:
		}
		head, err := s.runGit(ctx, dir, "rev-parse", "HEAD")
		if err != nil || head == "" {
			return true // 瞬时失败（如空仓库）：下轮再试
		}
		branch, _ := s.runGit(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD")
		porcelain, _ := s.runGit(ctx, dir, "status", "--porcelain")
		sum := sha256.Sum256([]byte(porcelain))
		dirty := hex.EncodeToString(sum[:8])
		if first {
			lastHead, lastDirty = head, dirty
			first = false
			return true
		}
		if head != lastHead {
			subject, _ := s.runGit(ctx, dir, "log", "-1", "--format=%s (%an)")
			short := head
			if len(short) > 7 {
				short = short[:7]
			}
			onEvent(PEvent{
				Kind: KindChanged, Subject: dir + "@" + branch,
				Dedup:  head, // 指纹 = 新提交号：同一提交不重复报
				Digest: fmt.Sprintf("%s 有新提交 %s: %s", branch, short, subject),
			})
			lastHead = head
		}
		if dirty != lastDirty {
			n := strings.Count(porcelain, "\n")
			if porcelain != "" && !strings.HasSuffix(porcelain, "\n") {
				n++
			}
			onEvent(PEvent{
				Kind: KindChanged, Subject: dir + ":worktree",
				Dedup:  dirty, // 指纹 = 工作区内容哈希
				Digest: fmt.Sprintf("工作区变更 %d 个路径（未提交）", n),
			})
			lastDirty = dirty
		}
		return true
	}
	sample()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
			if !sample() {
				return ctx.Err()
			}
		}
	}
}
