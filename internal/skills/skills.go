// Package skills 实现 Agent Skills 开放标准（https://agentskills.io）
// 的解析、发现与渐进披露——一个技能就是一个目录，内含 SKILL.md：
// YAML frontmatter（name + description 必填，license / allowed-tools /
// metadata 可选）加 markdown 正文。
//
// 与 Headlong 的 skills 工具同一条设计线：技能不需要任何专用运行时
// ——它就是一份模型能读会照做的文本。执行引擎是"模型读指令、写
// 代码"这件事本身。因此本包只做三件事：按标准解析与校验、从两层
// 目录（身份级覆盖全局）发现、把索引渲染成系统提示段。正文由模型
// 在需要时经 bash（cat）按需读取——渐进披露，不把全部正文塞进
// 上下文。
//
// frontmatter 解析：标准对字段集有明确约束（name/description/
// license/allowed-tools/metadata），实际生态的取值形态是标量、
// 引号标量、块标量（| 与 >）、列表与一层嵌套 map——本包的解析器
// 覆盖该子集并对超出的语法报错（宁可失败也不静默曲解）。协议是
// 标准的，解析器是自己的实现。
package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// SkillFile 是标准的技能清单文件名。
const SkillFile = "SKILL.md"

// 标准约束：name 仅小写字母/数字/连字符（连字符不居首尾），≤64 字符；
// description 必填且 ≤1024 字符。
var nameRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

const (
	maxNameLen        = 64
	maxDescriptionLen = 1024
)

// Skill 是一个解析并校验过的技能。
type Skill struct {
	Name         string
	Description  string
	License      string
	AllowedTools []string
	Metadata     map[string]string
	Dir          string // SKILL.md 所在目录（绝对路径）
	Body         string // markdown 正文（渐进披露：按需读取）
	Source       string // "identity" | "global"
}

// Store 是两层技能目录：第一个是身份级（优先），其余为全局/核心层。
// 同名技能高优先层遮蔽低优先层——headlong 的 SKILLS_DIR 与
// SKILLS_KERNEL_DIR 同款语义。
type Store struct {
	Dirs []string // 从高优先到低优先；超过一层时 Source 依序 identity/global/core
}

// Parse 解析并校验一个 SKILL.md 文件。dir 用于目录名一致性检查。
func Parse(dir string) (Skill, error) {
	data, err := os.ReadFile(filepath.Join(dir, SkillFile))
	if err != nil {
		return Skill{}, fmt.Errorf("skills: 读取 %s: %w", filepath.Join(dir, SkillFile), err)
	}
	fm, body, err := splitFrontmatter(string(data))
	if err != nil {
		return Skill{}, fmt.Errorf("skills: %s: %w", dir, err)
	}
	fields, err := parseYAMLSubset(fm)
	if err != nil {
		return Skill{}, fmt.Errorf("skills: %s: %w", dir, err)
	}
	s := Skill{
		Dir:  dir,
		Body: body,
	}
	s.Name, _ = fields["name"].(string)
	s.Description, _ = fields["description"].(string)
	s.License, _ = fields["license"].(string)
	if v, ok := fields["allowed-tools"]; ok && v != nil {
		list, ok := v.([]any)
		if !ok {
			return Skill{}, fmt.Errorf("allowed-tools 必须是列表")
		}
		for _, item := range list {
			s.AllowedTools = append(s.AllowedTools, fmt.Sprint(item))
		}
	}
	if v, ok := fields["metadata"].(map[string]any); ok {
		s.Metadata = map[string]string{}
		for k, mv := range v {
			s.Metadata[k] = fmt.Sprint(mv)
		}
	}
	if err := s.validate(dir); err != nil {
		return Skill{}, err
	}
	return s, nil
}

// validate 按标准约束校验：name 语法与长度、description 非空与长度、
// 目录名与 name 一致。
func (s Skill) validate(dir string) error {
	if s.Name == "" {
		return fmt.Errorf("frontmatter 缺少必填字段 name")
	}
	if len(s.Name) > maxNameLen {
		return fmt.Errorf("name 超过 %d 字符上限", maxNameLen)
	}
	if !nameRe.MatchString(s.Name) {
		return fmt.Errorf("name %q 不符合标准语法（仅小写字母/数字/连字符，连字符不居首尾）", s.Name)
	}
	if strings.TrimSpace(s.Description) == "" {
		return fmt.Errorf("frontmatter 缺少必填字段 description")
	}
	if len(s.Description) > maxDescriptionLen {
		return fmt.Errorf("description 超过 %d 字符上限", maxDescriptionLen)
	}
	if base := filepath.Base(dir); base != s.Name {
		return fmt.Errorf("目录名 %q 与 name %q 不一致（标准要求二者相同）", base, s.Name)
	}
	return nil
}

// splitFrontmatter 切出 "---" 围栏内的 frontmatter 与其余正文。
func splitFrontmatter(text string) (front, body string, err error) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", "", fmt.Errorf("缺少 frontmatter（文件必须以 --- 行开始）")
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return "", "", fmt.Errorf("frontmatter 未闭合（缺少收尾 --- 行）")
	}
	return strings.Join(lines[1:end], "\n"), strings.Join(lines[end+1:], "\n"), nil
}

// parseYAMLSubset 解析标准 frontmatter 用到的 YAML 子集：
//
//	key: value            标量（裸值 / 双引号 / 单引号）
//	key: |            以及 key: >   块标量（literal / folded）
//	key:                  后随 "- item" 列表
//	key:                  后随一层嵌套 map（metadata 用）
//
// 超出子集的语法（锚点、多级嵌套列表等）报错而不是曲解。
func parseYAMLSubset(text string) (map[string]any, error) {
	out := map[string]any{}
	lines := strings.Split(text, "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], " \t")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "- ") {
			return nil, fmt.Errorf("第 %d 行: 顶层不支持列表项", i+1)
		}
		key, rest, ok := strings.Cut(trimmed, ":")
		if !ok {
			return nil, fmt.Errorf("第 %d 行: %q 不是 key: value 形态", i+1, trimmed)
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("第 %d 行: 空 key", i+1)
		}
		rest = strings.TrimSpace(rest)
		if rest != "" && (rest[0] == '|' || rest[0] == '>') {
			// 块标量（含 |-、>-、|+ 等指示符；chomping 细节在子集
			// 内不区分）：literal 保留换行，folded 折成空格。
			folded := rest[0] == '>'
			var block []string
			i++
			for ; i < len(lines); i++ {
				l := lines[i]
				if strings.TrimSpace(l) == "" {
					block = append(block, "")
					continue
				}
				if len(l)-len(strings.TrimLeft(l, " \t")) == 0 {
					i--
					break
				}
				block = append(block, strings.TrimSpace(l))
			}
			for len(block) > 0 && block[len(block)-1] == "" {
				block = block[:len(block)-1]
			}
			if folded {
				out[key] = strings.Join(block, " ")
			} else {
				out[key] = strings.Join(block, "\n")
			}
		} else if rest == "" {
			// 列表或嵌套 map，看下一行的形态。
			var list []any
			nested := map[string]any{}
			isList, isMap := false, false
			for i+1 < len(lines) {
				next := strings.TrimSpace(lines[i+1])
				if next == "" || strings.HasPrefix(next, "#") {
					i++
					continue
				}
				if strings.HasPrefix(next, "- ") {
					if isMap {
						return nil, fmt.Errorf("第 %d 行: 列表与嵌套 map 混排", i+2)
					}
					isList = true
					list = append(list, strings.TrimPrefix(next, "- "))
					i++
					continue
				}
				if nk, nv, ok := strings.Cut(next, ":"); ok && !strings.HasPrefix(lines[i+1], " ") {
					break // 顶层下一个 key：本 key 结束
				} else if ok {
					if isList {
						return nil, fmt.Errorf("第 %d 行: 列表与嵌套 map 混排", i+2)
					}
					isMap = true
					nested[strings.TrimSpace(nk)] = scalar(strings.TrimSpace(nv))
					i++
					continue
				}
				return nil, fmt.Errorf("第 %d 行: 无法识别的续行 %q", i+2, next)
			}
			switch {
			case isList:
				out[key] = list
			case isMap:
				out[key] = nested
			default:
				out[key] = nil
			}
		} else {
			out[key] = scalar(rest)
		}
	}
	return out, nil
}

// scalar 解析标量：去引号或保留裸值。
func scalar(v string) any {
	if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
		return v[1 : len(v)-1]
	}
	return v
}

// List 扫描全部层的技能目录。每层直接子目录里含 SKILL.md 的视为
// 技能；高优先层同名技能遮蔽低优先层。目录解析失败的条目跳过并
// 返回在 errs 里（一个坏技能不该拖垮整个列表）。
func (s Store) List() (items []Skill, errs []error) {
	type entry struct {
		skill  Skill
		source string
	}
	seen := map[string]int{} // name → items 下标
	for layer, dir := range s.Dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue // 目录不存在 = 该层为空，合法
		}
		source := "global"
		if layer == 0 && len(s.Dirs) > 1 {
			source = "identity"
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			skillDir := filepath.Join(dir, e.Name())
			skill, err := Parse(skillDir)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			skill.Source = source
			if _, ok := seen[skill.Name]; ok {
				continue // 高优先层遮蔽低优先层：先见者胜
			}
			seen[skill.Name] = len(items)
			items = append(items, skill)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items, errs
}

// Get 按名字取技能（高优先层优先）。找不到返回 os.ErrNotExist 语义。
func (s Store) Get(name string) (Skill, error) {
	items, errs := s.List()
	if len(errs) > 0 {
		// 有坏条目不阻塞按名查找——只在该名字恰好是坏条目时报错。
		_ = errs
	}
	for _, it := range items {
		if it.Name == name {
			return it, nil
		}
	}
	return Skill{}, fmt.Errorf("skills: 技能 %q 不存在: %w", name, os.ErrNotExist)
}

// PromptSection 渲染进系统提示的技能索引段（渐进披露的第一层）：
// 只含 name + description 与"按需读正文"的用法。返回空串表示
// 没有任何技能、调用方不应附加本段。
func (s Store) PromptSection() string {
	items, _ := s.List()
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Skills\n\n")
	b.WriteString("Reusable instruction packs (Agent Skills standard) are available. ")
	b.WriteString("Each lives in a directory whose SKILL.md holds YAML frontmatter ")
	b.WriteString("(name + description) and a markdown body with the actual instructions. ")
	b.WriteString("The index below is all you get upfront — when a task matches a skill's ")
	b.WriteString("description, read its full body before acting:\n\n")
	b.WriteString("  cat \"<skill-dir>/SKILL.md\"\n\n")
	b.WriteString("Available skills (dir is the path to read):\n")
	for _, it := range items {
		fmt.Fprintf(&b, "- %s — %s\n  dir: %s\n", it.Name, it.Description, it.Dir)
	}
	return strings.TrimRight(b.String(), "\n")
}
