package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSkill 在 dir 下落一个 SKILL.md。
func writeSkill(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, SkillFile), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestParsePlainFrontmatter(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data-analysis")
	writeSkill(t, dir, `---
name: data-analysis
description: Analyze datasets with pandas when the user asks for statistics.
license: MIT
allowed-tools:
  - Bash
  - Read
metadata:
  version: 1.0
  author: ada
---

# Data analysis

Use pandas. Always show the histogram.
`)
	s, err := Parse(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "data-analysis" || s.License != "MIT" {
		t.Fatalf("标量字段错位: %+v", s)
	}
	if len(s.AllowedTools) != 2 || s.AllowedTools[0] != "Bash" {
		t.Fatalf("allowed-tools 错位: %v", s.AllowedTools)
	}
	if s.Metadata["version"] != "1.0" || s.Metadata["author"] != "ada" {
		t.Fatalf("metadata 错位: %v", s.Metadata)
	}
	if !strings.Contains(s.Body, "# Data analysis") {
		t.Fatalf("正文丢失: %q", s.Body)
	}
}

func TestParseFoldedAndQuoted(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "code-review")
	writeSkill(t, dir, `---
name: code-review
description: >-
  Review diffs for correctness, security and style. Use when the user
  asks to review a PR or a patch.
---

Read the diff first.
`)
	s, err := Parse(dir)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(s.Description, "\n") || !strings.Contains(s.Description, "Review diffs for correctness") {
		t.Fatalf("folded 标量应折成单行: %q", s.Description)
	}
}

func TestParseRejects(t *testing.T) {
	cases := []struct {
		name    string
		dirName string
		fm      string
		wantErr string
	}{
		{"缺 name", "x", "---\ndescription: y\n---\nbody\n", "缺少必填字段 name"},
		{"缺 description", "x", "---\nname: x\n---\nbody\n", "缺少必填字段 description"},
		{"name 语法", "Bad_Name", "---\nname: Bad_Name\ndescription: y\n---\n", "不符合标准语法"},
		{"目录不一致", "wrong-dir", "---\nname: right-name\ndescription: y\n---\n", "目录名"},
		{"未闭合", "x", "---\nname: x\ndescription: y\nbody\n", "未闭合"},
		{"无 frontmatter", "x", "just markdown\n", "缺少 frontmatter"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), c.dirName)
			writeSkill(t, dir, c.fm)
			_, err := Parse(dir)
			if err == nil {
				t.Fatalf("应拒绝，却通过了")
			}
			if !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("错误应含 %q，得到 %v", c.wantErr, err)
			}
		})
	}
}

func TestListShadowsIdentityOverGlobal(t *testing.T) {
	root := t.TempDir()
	global := filepath.Join(root, "global")
	identity := filepath.Join(root, "identity")
	writeSkill(t, filepath.Join(global, "shared"), "---\nname: shared\ndescription: global 版本\n---\n")
	writeSkill(t, filepath.Join(global, "only-global"), "---\nname: only-global\ndescription: 只在全局\n---\n")
	writeSkill(t, filepath.Join(identity, "shared"), "---\nname: shared\ndescription: 身份级版本\n---\n")
	// 坏技能目录：跳过且不拖垮列表。
	writeSkill(t, filepath.Join(identity, "broken"), "---\nname: broken\n---\n")

	items, errs := Store{Dirs: []string{identity, global}}.List()
	if len(errs) != 1 {
		t.Fatalf("应有 1 个坏条目错误，得到 %v", errs)
	}
	if len(items) != 2 {
		t.Fatalf("应 2 个技能（shared 遮蔽 + only-global），得到 %v", items)
	}
	byName := map[string]Skill{}
	for _, it := range items {
		byName[it.Name] = it
	}
	if byName["shared"].Description != "身份级版本" || byName["shared"].Source != "identity" {
		t.Fatalf("身份级应遮蔽全局: %+v", byName["shared"])
	}
	if byName["only-global"].Source != "global" {
		t.Fatalf("only-global 应来自全局层: %+v", byName["only-global"])
	}
}

func TestPromptSection(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, filepath.Join(root, "demo"), "---\nname: demo\ndescription: 演示技能\n---\n正文")
	section := (Store{Dirs: []string{root}}).PromptSection()
	if !strings.Contains(section, "## Skills") || !strings.Contains(section, "demo — 演示技能") {
		t.Fatalf("索引段应含技能条目: %s", section)
	}
	if !strings.Contains(section, filepath.Join(root, "demo")) {
		t.Fatalf("索引段应含可 cat 的目录路径: %s", section)
	}
	empty := (Store{Dirs: []string{filepath.Join(root, "nothing")}}).PromptSection()
	if empty != "" {
		t.Fatalf("无技能时应返回空串，得到 %q", empty)
	}
}

func TestInstallFromLocalDir(t *testing.T) {
	root := t.TempDir()
	dst := filepath.Join(root, "skills")

	// 单技能目录。
	src := filepath.Join(root, "my-tool")
	writeSkill(t, src, "---\nname: my-tool\ndescription: 单技能目录\n---\n")
	names, err := Install(dst, src)
	if err != nil || len(names) != 1 || names[0] != "my-tool" {
		t.Fatalf("单技能安装: %v %v", names, err)
	}

	// 多技能仓库（根无 SKILL.md，子目录各一个；外加一个无 SKILL.md
	// 的普通目录——它不是技能，扫描时应被忽略）。
	repo := filepath.Join(root, "skill-pack")
	writeSkill(t, filepath.Join(repo, "alpha"), "---\nname: alpha\ndescription: A\n---\n")
	writeSkill(t, filepath.Join(repo, "beta"), "---\nname: beta\ndescription: B\n---\n")
	if err := os.MkdirAll(filepath.Join(repo, "not-a-skill"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "not-a-skill", "README.md"), []byte("just files\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	names, err = Install(dst, repo)
	if err != nil || len(names) != 2 {
		t.Fatalf("多技能安装: %v %v", names, err)
	}

	// 不合格技能整体拒绝、零落盘。
	bad := filepath.Join(root, "bad-pack")
	writeSkill(t, filepath.Join(bad, "good"), "---\nname: good\ndescription: ok\n---\n")
	writeSkill(t, filepath.Join(bad, "Bad_Name"), "---\nname: Bad_Name\ndescription: 非法\n---\n")
	if _, err := Install(dst, bad); err == nil {
		t.Fatal("含不合格技能的安装应整体拒绝")
	}
	if dirExists(filepath.Join(dst, "good")) {
		t.Fatal("拒绝时不得有部分落盘")
	}

	// 同名已存在报错。
	if _, err := Install(dst, src); err == nil {
		t.Fatal("同名已存在应报错")
	}

	// 装完即可被 Store 发现并解析。
	store := Store{Dirs: []string{dst}}
	items, _ := store.List()
	if len(items) != 3 {
		t.Fatalf("安装后应有 3 个技能，得到 %d", len(items))
	}
}

func TestInitThenParseRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "fresh-skill")
	if err := Init(dir); err != nil {
		t.Fatal(err)
	}
	s, err := Parse(dir)
	if err != nil {
		t.Fatalf("脚手架应通过标准校验: %v", err)
	}
	if s.Name != "fresh-skill" || s.Description == "" {
		t.Fatalf("脚手架字段缺失: %+v", s)
	}
	// 非法名字在 Init 入口就被拒。
	if err := Init(filepath.Join(t.TempDir(), "Bad_Name")); err == nil {
		t.Fatal("非法名字应被拒绝")
	}
}

func TestRemove(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, filepath.Join(root, "gone"), "---\nname: gone\ndescription: x\n---\n")
	store := Store{Dirs: []string{root}}
	removed, err := Remove(store, "gone")
	if err != nil || !strings.HasSuffix(filepath.ToSlash(removed), "/gone") {
		t.Fatalf("remove: %q %v", removed, err)
	}
	if _, err := store.Get("gone"); err == nil {
		t.Fatal("删除后应不存在")
	}
}
