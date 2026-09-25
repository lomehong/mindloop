package skills

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// githubRe 是 "owner/repo" 形态——安装源经 https://github.com 克隆。
var githubRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// Install 把安装源落到 dstDir（技能库根目录），返回安装的技能名。
// 源两种形态：
//   - 本地目录：目录含 SKILL.md 则整个目录是一个技能；否则扫描
//     直接子目录里的 SKILL.md（多技能仓库）。
//   - "owner/repo"：git clone --depth 1 到临时目录后按本地目录处理
//     （GitHub 是生态的默认分发渠道；git 是仓库本来就依赖的工具）。
//
// 安装前先解析校验（不合格的技能一步都不落盘）；同名技能已存在
// 时报错——升级 = 先 remove 再 install，绝不静默覆盖。
func Install(dstDir, src string) ([]string, error) {
	srcDir := src
	if githubRe.MatchString(src) && !dirExists(src) {
		clone, err := gitClone(src)
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(clone)
		srcDir = clone
	}
	fi, err := os.Stat(srcDir)
	if err != nil {
		return nil, fmt.Errorf("skills: 安装源不存在: %s", srcDir)
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("skills: 安装源必须是目录: %s", srcDir)
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
	for _, s := range parsed {
		dst := filepath.Join(dstDir, s.Name)
		if dirExists(dst) {
			return nil, fmt.Errorf("skills: 技能 %q 已存在于 %s（先 remove 再安装）", s.Name, dst)
		}
		if err := copyTree(s.Dir, dst); err != nil {
			return nil, err
		}
		names = append(names, s.Name)
	}
	return names, nil
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

func gitClone(ownerRepo string) (string, error) {
	clone, err := os.MkdirTemp("", "mindloop-skill-")
	if err != nil {
		return "", err
	}
	url := "https://github.com/" + ownerRepo + ".git"
	cmd := exec.Command("git", "clone", "--depth", "1", url, clone)
	out, err := cmd.CombinedOutput()
	if err != nil {
		os.RemoveAll(clone)
		return "", fmt.Errorf("skills: git clone %s 失败: %s", url, strings.TrimSpace(string(out)))
	}
	return clone, nil
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
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fi.Mode().Perm())
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, in)
		return err
	})
}
