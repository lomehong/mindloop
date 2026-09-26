// providers.go 是 providers.json 的读写与档位解析：LLM 提供商档案
// （连接与模型清单，非敏感）住在这里；密钥（敏感）仍住 .env，档案
// 用 api_key_env 显式引用或约定键 MINDLOOP_PROFILE_<ID>_API_KEY 关联。
// 两级布局与 .env 对称：<home>/providers.json 为底、<identity>/
// providers.json 覆盖（同名档案整体替换、tiers 逐档覆盖）。
//
// 档位解析的优先级是设计决定：.env 旧键（MINDLOOP_MODEL 等）优先
// 于 providers.json 绑定——"显式环境变量优先"哲学的延伸，旧配置
// 永不因新面存在而改变行为。
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Profile 是一个提供商档案（providers.json 的 profiles[] 条目）。
type Profile struct {
	ID        string   `json:"id"`
	Label     string   `json:"label,omitempty"`
	Provider  string   `json:"provider,omitempty"`
	BaseURL   string   `json:"base_url"`
	APIKeyEnv string   `json:"api_key_env,omitempty"`
	Models    []string `json:"models,omitempty"`
}

// TierBinding 是一个档位的模型绑定（providers.json 的 tiers 条目）。
type TierBinding struct {
	Profile string `json:"profile"`
	Model   string `json:"model"`
}

// Providers 是 providers.json 文档（两级合并后的有效视图）。
type Providers struct {
	Version  int                    `json:"version"`
	Profiles []Profile              `json:"profiles"`
	Tiers    map[string]TierBinding `json:"tiers"`
}

// TierChoice 是 ResolveTier 的解析结果。
type TierChoice struct {
	Tier    string
	Source  string // "env" | "providers.json"；"" = 未配置
	Model   string
	Profile Profile     // 仅 providers.json 路径有效
	Binding TierBinding // 仅 providers.json 路径有效
	APIKey  string      // 仅 providers.json 路径（env 路径由 llm.FromEnv* 自读）
}

// tierModelEnv 档位 → 旧 env 键（解析时 env 非空即赢）。
var tierModelEnv = map[string]string{
	"think":   "MINDLOOP_MODEL",
	"request": "MINDLOOP_REQUEST_MODEL",
	"summary": "MINDLOOP_SUMMARY_MODEL",
}

var profileIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

func validProvider(p string) bool {
	switch p {
	case "", "anthropic", "openai-compatible", "echo":
		return true
	}
	return false
}

// Profile 按 id 查档案。
func (p *Providers) Profile(id string) (Profile, bool) {
	for _, pr := range p.Profiles {
		if pr.ID == id {
			return pr, true
		}
	}
	return Profile{}, false
}

// LoadProviders 读取两级 providers.json 并合并。文件缺失不是错误；
// 非法档案（id 格式、重复 id、缺 base_url、未知 provider）报错并
// 带文件路径——配置错误应当被看见，降级由装配层决定。
func LoadProviders(homeDir, identityDir string) (*Providers, error) {
	merged := &Providers{Tiers: map[string]TierBinding{}}
	for _, dir := range []string{homeDir, identityDir} {
		if dir == "" {
			continue
		}
		level, err := loadProvidersFile(filepath.Join(dir, "providers.json"))
		if err != nil {
			return nil, err
		}
		mergeProviders(merged, level)
	}
	return merged, nil
}

func loadProvidersFile(path string) (*Providers, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Providers{}, nil
		}
		return nil, err
	}
	var p Providers
	if strings.TrimSpace(string(data)) != "" {
		if err := json.Unmarshal(data, &p); err != nil {
			return nil, fmt.Errorf("config: 解析 %s: %w", path, err)
		}
	}
	seen := map[string]bool{}
	for idx := range p.Profiles {
		pr := &p.Profiles[idx]
		if !profileIDRe.MatchString(pr.ID) {
			return nil, fmt.Errorf("config: %s 档案 %q 的 id 非法（小写字母/数字/_/-，首字符字母或数字）", path, pr.ID)
		}
		if seen[pr.ID] {
			return nil, fmt.Errorf("config: %s 档案 id %q 重复", path, pr.ID)
		}
		seen[pr.ID] = true
		pr.BaseURL = strings.TrimRight(strings.TrimSpace(pr.BaseURL), "/")
		if pr.BaseURL == "" {
			return nil, fmt.Errorf("config: %s 档案 %q 缺 base_url", path, pr.ID)
		}
		if !validProvider(pr.Provider) {
			return nil, fmt.Errorf("config: %s 档案 %q 的 provider %q 未知（anthropic|openai-compatible|echo）", path, pr.ID, pr.Provider)
		}
	}
	return &p, nil
}

// mergeProviders 把 src 合并进 dst：同名档案整体替换（保持 dst 的
// 位置），新档案按 src 顺序追加；tiers 逐档覆盖。
func mergeProviders(dst, src *Providers) {
	idx := map[string]int{}
	for i, pr := range dst.Profiles {
		idx[pr.ID] = i
	}
	for _, pr := range src.Profiles {
		if i, ok := idx[pr.ID]; ok {
			dst.Profiles[i] = pr
		} else {
			idx[pr.ID] = len(dst.Profiles)
			dst.Profiles = append(dst.Profiles, pr)
		}
	}
	for t, b := range src.Tiers {
		dst.Tiers[t] = b
	}
	if src.Version > dst.Version {
		dst.Version = src.Version
	}
}

// ResolveTier 解析一个档位的最终来源：
//   - env 键（tierModelEnv[tier]）非空 → Source="env"（连接走
//     llm.FromEnvModel 的既有推断与回退链）；
//   - 否则查 providers.json 绑定 → Source="providers.json"，附档案、
//     绑定与密钥解析结果；
//   - 两者皆无 → Source=""（未配置）。
//
// 绑定引用不存在的档案是坏配置，返回错误——解析层只陈述事实，
// 降级（回落主档等）由装配层决定。
func ResolveTier(p *Providers, tier string, getenv func(string) string) (TierChoice, error) {
	envKey, ok := tierModelEnv[tier]
	if !ok {
		return TierChoice{}, fmt.Errorf("config: 未知档位 %q（think|request|summary）", tier)
	}
	choice := TierChoice{Tier: tier}
	if v := strings.TrimSpace(getenv(envKey)); v != "" {
		choice.Source = "env"
		choice.Model = v
		return choice, nil
	}
	if p == nil {
		return choice, nil
	}
	b, ok := p.Tiers[tier]
	if !ok || b.Profile == "" {
		return choice, nil
	}
	pr, ok := p.Profile(b.Profile)
	if !ok {
		return TierChoice{}, fmt.Errorf("config: tiers.%s 引用的档案 %q 不存在", tier, b.Profile)
	}
	choice.Source = "providers.json"
	choice.Model = b.Model
	choice.Profile = pr
	choice.Binding = b
	choice.APIKey = resolveProfileKey(pr, getenv)
	return choice, nil
}

// KeyEnvName 返回档案密钥的查找键名：api_key_env 显式引用优先；
// 缺省约定 MINDLOOP_PROFILE_<ID>_API_KEY（id 大写、连字符转下划线）。
// 展示（配置页）、写入（密钥分流）与解析共用它，键名推导只有这一处。
func (pr Profile) KeyEnvName() string {
	if pr.APIKeyEnv != "" {
		return pr.APIKeyEnv
	}
	return "MINDLOOP_PROFILE_" + strings.ToUpper(strings.ReplaceAll(pr.ID, "-", "_")) + "_API_KEY"
}

// resolveProfileKey 解析档案密钥：按 KeyEnvName 查找，都没有 → 空
// （构造层负责报明确错误）。
func resolveProfileKey(pr Profile, getenv func(string) string) string {
	return strings.TrimSpace(getenv(pr.KeyEnvName()))
}

// ValidateProfiles 是文档内的档案字段校验（id 格式、重复、base_url、
// provider）——与档位引用无关，可独立用于继承场景（身份文档的档位可
// 引用全局档案，档案字段却必须自洽）。
func (p *Providers) ValidateProfiles() error {
	seen := map[string]bool{}
	for _, pr := range p.Profiles {
		if !profileIDRe.MatchString(pr.ID) {
			return fmt.Errorf("config: 档案 id %q 非法（小写字母/数字/_/-，首字符字母或数字）", pr.ID)
		}
		if seen[pr.ID] {
			return fmt.Errorf("config: 档案 id %q 重复", pr.ID)
		}
		seen[pr.ID] = true
		if strings.TrimSpace(pr.BaseURL) == "" {
			return fmt.Errorf("config: 档案 %q 缺 base_url", pr.ID)
		}
		if !validProvider(pr.Provider) {
			return fmt.Errorf("config: 档案 %q 的 provider %q 未知（anthropic|openai-compatible|echo）", pr.ID, pr.Provider)
		}
	}
	return nil
}

// ValidateTierRefs 校验档位绑定：键名已知、profile/model 非空，且引用
// 的档案能在 base ∪ p 的档案集合里解析。base 是继承底——写身份级文档
// 时传全局级（"引用仅全局存在的档案"是两级合并下的有效配置）；
// base=nil 则是文档自洽校验。
func (p *Providers) ValidateTierRefs(base *Providers) error {
	known := map[string]bool{}
	if base != nil {
		for _, pr := range base.Profiles {
			known[pr.ID] = true
		}
	}
	for _, pr := range p.Profiles {
		known[pr.ID] = true
	}
	for t, b := range p.Tiers {
		if _, ok := tierModelEnv[t]; !ok {
			return fmt.Errorf("config: 未知档位 %q（think|request|summary）", t)
		}
		if b.Profile == "" || b.Model == "" {
			return fmt.Errorf("config: tiers.%s 需要 profile 与 model", t)
		}
		if !known[b.Profile] {
			return fmt.Errorf("config: tiers.%s 引用的档案 %q 不存在", t, b.Profile)
		}
	}
	return nil
}

// ValidateForSave 是文档自洽的完整校验（ValidateProfiles +
// ValidateTierRefs(nil)）。读取路径不跑它——写时严格、读时宽容：
// 磁盘上的坏引用会在解析时报错陈述，而不是让配置页无法保存中间态。
func (p *Providers) ValidateForSave() error {
	if err := p.ValidateProfiles(); err != nil {
		return err
	}
	return p.ValidateTierRefs(nil)
}

// SaveProviders 原子写入身份级 providers.json（临时文件 + rename，
// 半次写入不会留下损坏文档）。保存端约定先过 ValidateForSave。
func SaveProviders(identityDir string, p *Providers) error {
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(identityDir, ".providers-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, filepath.Join(identityDir, "providers.json")); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}
