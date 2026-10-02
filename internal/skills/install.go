package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// githubRe 是 "owner/repo" 形态——安装源经 https://github.com 克隆。
// 组件以字母数字开头结尾：排除前导连字符（git 参数注入的经典跳板）
// 与纯点号段，中间只允许字母数字与 . _ -。
var githubRe = regexp.MustCompile(
	`^[A-Za-z0-9](?:[A-Za-z0-9_.-]*[A-Za-z0-9])?/[A-Za-z0-9](?:[A-Za-z0-9_.-]*[A-Za-z0-9])?$`)

// sourceFileName 记录技能安装来源（source URL + commit SHA）。
// 点前缀确保它不与任何标准文件冲突，List/Parse 不受影响。
const sourceFileName = ".mindloop-source.json"

// Install 把安装源落到 dstDir（技能库根目录），返回安装的技能名。
// 源两种形态：
//   - 本地目录：目录含 SKILL.md 则整个目录是一个技能；否则扫描
//     直接子目录里的 SKILL.md（多技能仓库）。
//   - "owner/repo"：git clone --depth 1 到临时目录后按本地目录处理
//     （GitHub 是生态的默认分发渠道；git 是仓库本来就依赖的工具，
//     clone + 取 SHA 受 ctx 与 2 分钟超时双重约束）。
//
// 安装前先解析校验（不合格的技能一步都不落盘）；同名技能已存在
// 时报错——升级 = 先 remove 再 install，绝不静默覆盖。拷贝中途
// 失败会回滚本次已落盘的全部技能目录，不留半成品堵住重装。
func Install(ctx context.Context, dstDir, src string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	srcDir := src
	sourceURL := ""
	commit := ""
	if githubRe.MatchString(src) && !dirExists(src) {
		owner, repo, _ := strings.Cut(src, "/")
		clone, sha, err := gitClone(ctx, owner, repo)
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(clone)
		srcDir = clone
		sourceURL = "https://github.com/" + owner + "/" + repo
		commit = sha
	}
	fi, err := os.Stat(srcDir)
	if err != nil {
		return nil, fmt.Errorf("skills: 安装源不存在: %s", srcDir)
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("skills: 安装源必须是目录: %s", srcDir)
	}
	if sourceURL == "" {
		sourceURL = srcDir // 本地安装也留痕：记录绝对源路径
	}

	// 候选技能目录：源根含 SKILL.md → 单技能；否则扫子目录。
	candidates := []string{}
	if fileExists(filepath.Join(srcDir, SkillFile)) {
		candidates = append(candidates, srcDir)
	} else {
		entries, err := os.ReadDir(srcDir)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir() && fileExists(filepath.Join(srcDir, e.Name(), SkillFile)) {
				candidates = append(candidates, filepath.Join(srcDir, e.Name()))
			}
		}
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("skills: %s 里没有找到 %s（目录本身或其直接子目录）", srcDir, SkillFile)
	}

	// 先全部解析校验，再落盘。
	parsed := make([]Skill, 0, len(candidates))
	for _, c := range candidates {
		s, err := Parse(c)
		if err != nil {
			return nil, fmt.Errorf("skills: 安装被拒绝: %w", err)
		}
		parsed = append(parsed, s)
	}
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(parsed))
	installed := make([]string, 0, len(parsed)) // 已落盘目录，失败时回滚
	for _, s := range parsed {
		dst := filepath.Join(dstDir, s.Name)
		if dirExists(dst) {
			rollback(dstDir, installed)
			return nil, fmt.Errorf("skills: 技能 %q 已存在于 %s（先 remove 再安装）", s.Name, dst)
		}
		if err := copyTree(s.Dir, dst); err != nil {
			rollback(dstDir, installed)
			return nil, err
		}
		if err := writeSourceMeta(dst, sourceURL, commit); err != nil {
			rollback(dstDir, installed)
			return nil, err
		}
		names = append(names, s.Name)
		installed = append(installed, dst)
	}
	return names, nil
}

// rollback 删除本次安装已落盘的技能目录——半成品目录会以
// "已存在" 挡住重装，比不装更糟。
func rollback(dstDir string, installed []string) {
	for _, d := range installed {
		os.RemoveAll(d)
	}
}

// writeSourceMeta 把安装来源持久化到技能目录。失败视为安装失败
// （来源不可追溯的安装不如不装）。
func writeSourceMeta(dstDir, source, commit string) error {
	meta := struct {
		Source      string `json:"source"`
		Commit      string `json:"commit,omitempty"`
		InstalledAt string `json:"installed_at"`
	}{Source: source, Commit: commit, InstalledAt: time.Now().UTC().Format(time.RFC3339)}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dstDir, sourceFileName), append(data, '\n'), 0o644)
}

// Init 在 dir（…/skills/<name>）脚手架一个新技能：标准 frontmatter
// 模板 + 正文骨架。name 校验走同一套标准约束。
func Init(dir string) error {
	name := filepath.Base(dir)
	probe := Skill{Name: name, Description: "placeholder"}
	if err := probe.validate(dir); err != nil {
		return fmt.Errorf("skills: %w", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if fileExists(filepath.Join(dir, SkillFile)) {
		return fmt.Errorf("skills: %s 已存在", filepath.Join(dir, SkillFile))
	}
	tmpl := fmt.Sprintf(`---
name: %s
description: >-
  一句话说明这个技能做什么、什么时候该用它。这段描述会进入
  系统提示的技能索引——写清楚触发场景，模型据此决定是否读取全文。
---

# %s

在这里写技能正文：模型命中该技能时应遵循的步骤、约定与示例。
正文按需读取（渐进披露），可以尽量详细。
`, name, name)
	return os.WriteFile(filepath.Join(dir, SkillFile), []byte(tmpl), 0o644)
}

// Remove 从技能库里删除指定名字的技能，返回被删目录。身份级与
// 全局层同名时删高优先层（遮蔽者）。
func Remove(store Store, name string) (string, error) {
	s, err := store.Get(name)
	if err != nil {
		return "", err
	}
	if err := os.RemoveAll(s.Dir); err != nil {
		return "", err
	}
	return s.Dir, nil
}

// cleanSegment 只保留 GitHub 仓库段的安全字符（字母数字与 . _ -），
// 其余一律丢弃。它是白名单过滤器而非校验器：输出串的每个字符都
// 来自允许集合，外部输入经此重造后才允许写进 git 配置。
func cleanSegment(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '_', r == '-':
			return r
		default:
			return -1
		}
	}, s)
}

// installTimeout 封顶 git 克隆（含网络）时长——挂起的远端不能把
// install 命令无限拖住。
const installTimeout = 2 * time.Minute

// runGitStep 运行一步 git 子命令并等待结束：程序恒为字面量 "git"，
// 参数由调用点以字面量与内部临时目录拼装。ctx 取消（Ctrl+C/上层
// 超时）或 installTimeout 到点都会杀掉子进程——取消监听挂在对
// Wait 的 select 上，等价 CommandContext 的语义。
func runGitStep(ctx context.Context, timeout time.Duration, kill func(*exec.Cmd), args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	var buf strings.Builder
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	timer := time.AfterFunc(timeout, func() { kill(cmd) })
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var err error
	select {
	case err = <-done:
	case <-ctx.Done():
		kill(cmd)
		<-done
		err = ctx.Err()
	}
	timer.Stop()
	return []byte(buf.String()), err
}

// killProcess 是 runGitStep 的兜底击杀：先 Terminate（Job Object
// 整树），退化到进程级 Kill。
func killProcess(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}

// gitClone 把 owner/repo 的默认分支浅克隆到临时目录并返回 HEAD SHA。
//
// 等价于 `git clone --depth 1 <url>`，但分四步走：init、把远端
// URL 写进 .git/config、fetch origin HEAD、checkout FETCH_HEAD。
// 这样分步的原因是安全边界：owner/repo 来自外部输入，若直接进
// exec 参数（哪怕只是 git clone 的 URL 位），任何上游校验的疏漏
// 都会变成参数注入通道。改为经 cleanSegment 白名单重造后写进
// 配置文件内容——config 里的 url 只是数据，永远不会被当作选项
// 解析；而 exec 的程序恒为字面量 git，参数只有字面量与 MkdirTemp
// 产物，来源可证内部。
func gitClone(ctx context.Context, owner, repo string) (dir, commit string, err error) {
	clone, err := os.MkdirTemp("", "mindloop-skill-")
	if err != nil {
		return "", "", err
	}
	cctx, cancel := context.WithTimeout(ctx, installTimeout)
	defer cancel()
	step := func(args ...string) ([]byte, error) {
		return runGitStep(cctx, installTimeout, killProcess, args...)
	}

	if _, err := step("init", "-q", clone); err != nil {
		os.RemoveAll(clone)
		return "", "", fmt.Errorf("skills: git init 失败: %v", err)
	}
	url := "https://github.com/" + cleanSegment(owner) + "/" + cleanSegment(repo) + ".git"
	cfgPath := filepath.Join(clone, ".git", "config")
	base, err := os.ReadFile(cfgPath)
	if err != nil {
		os.RemoveAll(clone)
		return "", "", err
	}
	remote := "\n[remote \"origin\"]\n\turl = " + url +
		"\n\tfetch = +refs/heads/*:refs/remotes/origin/*\n"
	if err := os.WriteFile(cfgPath, append(base, []byte(remote)...), 0o644); err != nil {
		os.RemoveAll(clone)
		return "", "", err
	}
	if out, err := step("-C", clone, "fetch", "-q", "--depth", "1", "origin", "HEAD"); err != nil {
		os.RemoveAll(clone)
		return "", "", fmt.Errorf("skills: git fetch %s 失败: %s", url, strings.TrimSpace(string(out)))
	}
	if out, err := step("-C", clone, "checkout", "-q", "-f", "FETCH_HEAD"); err != nil {
		os.RemoveAll(clone)
		return "", "", fmt.Errorf("skills: git checkout 失败: %s", strings.TrimSpace(string(out)))
	}
	shaOut, err := step("-C", clone, "rev-parse", "HEAD")
	if err != nil {
		// SHA 只是溯源增强，拿不到不阻止安装（留空即可）。
		shaOut = nil
	}
	return clone, strings.TrimSpace(string(shaOut)), nil
}

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if fi.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !fi.Mode().IsRegular() {
			return nil // 符号链接等非常规条目不随技能分发
		}
		// 单文件拷贝提成独立函数：defer 随每次调用返回，而不是
		// 累积到整棵树遍历结束——大仓库会把 fd 打爆。
		return copyFile(path, target, fi.Mode().Perm())
	})
}

func copyFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
