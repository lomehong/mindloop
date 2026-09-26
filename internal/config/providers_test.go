package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeProviders 在 dir 下落一份 providers.json。
func writeProviders(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "providers.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// envMap 把 map 包成 getenv。
func envMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

const providersGlobalDoc = `{
  "version": 1,
  "profiles": [
    {"id": "zhipu", "label": "智谱", "base_url": "https://open.bigmodel.cn/api/paas/v4", "models": ["glm-5"]},
    {"id": "openrouter", "base_url": "https://openrouter.ai/api/v1", "models": ["openai/gpt-oss-120b"]}
  ],
  "tiers": {
    "request": {"profile": "openrouter", "model": "openai/gpt-oss-120b"}
  }
}`

const providersIdentityDoc = `{
  "profiles": [
    {"id": "zhipu", "base_url": "https://api.z.ai/api/paas/v4", "models": ["glm-4.5-air"]},
    {"id": "anthropic", "base_url": "https://api.anthropic.com", "provider": "anthropic", "models": ["claude-sonnet-4-5"]}
  ],
  "tiers": {
    "think": {"profile": "zhipu", "model": "glm-4.5-air"},
    "request": {"profile": "anthropic", "model": "claude-haiku-4-5"}
  }
}`

// TestLoadProvidersMissingFilesAreFine：两级文件都缺失不是错误——
// 未配置 providers.json 的身份维持旧行为（按模型名推断）。
func TestLoadProvidersMissingFilesAreFine(t *testing.T) {
	p, err := LoadProviders(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatalf("缺失文件不应报错: %v", err)
	}
	if len(p.Profiles) != 0 || len(p.Tiers) != 0 {
		t.Fatalf("缺失文件应得到空结构: %+v", p)
	}
}

// TestLoadProvidersMergesLevels：全局为底、身份覆盖——同名档案整体
// 替换且保持原位置，身份新增档案追加在后；tiers 逐档覆盖。
func TestLoadProvidersMergesLevels(t *testing.T) {
	home, idDir := t.TempDir(), t.TempDir()
	writeProviders(t, home, providersGlobalDoc)
	writeProviders(t, idDir, providersIdentityDoc)

	p, err := LoadProviders(home, idDir)
	if err != nil {
		t.Fatalf("LoadProviders: %v", err)
	}
	if len(p.Profiles) != 3 {
		t.Fatalf("合并后档案数 = %d，应为 3: %+v", len(p.Profiles), p.Profiles)
	}
	// 顺序：全局 zhipu（被身份覆盖值）→ 全局 openrouter → 身份新增 anthropic。
	if p.Profiles[0].ID != "zhipu" || p.Profiles[0].BaseURL != "https://api.z.ai/api/paas/v4" {
		t.Fatalf("同名档案应整体替换: %+v", p.Profiles[0])
	}
	if p.Profiles[1].ID != "openrouter" {
		t.Fatalf("全局档案位置应保持: %+v", p.Profiles)
	}
	if p.Profiles[2].ID != "anthropic" {
		t.Fatalf("身份新增档案应追加: %+v", p.Profiles)
	}
	// tiers：think 来自身份，request 身份覆盖全局。
	if b := p.Tiers["think"]; b.Profile != "zhipu" || b.Model != "glm-4.5-air" {
		t.Fatalf("think 绑定 = %+v", b)
	}
	if b := p.Tiers["request"]; b.Profile != "anthropic" || b.Model != "claude-haiku-4-5" {
		t.Fatalf("request 绑定应被身份覆盖 = %+v", b)
	}
}

// TestLoadProvidersValidatesProfiles：档案字段校验——非法 id、
// 缺 base_url、未知 provider 都要报错且带定位信息。
func TestLoadProvidersValidatesProfiles(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		want string
	}{
		{"非法 id 大写", `{"profiles":[{"id":"Zhipu","base_url":"https://x"}]}`, "档案"},
		{"非法 id 空", `{"profiles":[{"id":"","base_url":"https://x"}]}`, "档案"},
		{"缺 base_url", `{"profiles":[{"id":"zhipu"}]}`, "base_url"},
		{"未知 provider", `{"profiles":[{"id":"zhipu","base_url":"https://x","provider":"openai"}]}`, "provider"},
		{"同文件重复 id", `{"profiles":[{"id":"zhipu","base_url":"https://x"},{"id":"zhipu","base_url":"https://y"}]}`, "重复"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			writeProviders(t, home, tc.doc)
			_, err := LoadProviders(home, "")
			if err == nil {
				t.Fatalf("坏档案应报错: %s", tc.doc)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("错误应含 %q: %v", tc.want, err)
			}
		})
	}
}

// TestLoadProvidersBadJSON：坏 JSON 报错并带文件路径。
func TestLoadProvidersBadJSON(t *testing.T) {
	home := t.TempDir()
	writeProviders(t, home, "{ not json")
	_, err := LoadProviders(home, "")
	if err == nil || !strings.Contains(err.Error(), "providers.json") {
		t.Fatalf("坏 JSON 应报错且带路径: %v", err)
	}
}

// TestResolveTierPrefersEnv：档位解析优先级——env 旧键（显式
// 环境变量 > 身份 .env > 全局 .env 的合成值）优先于 providers.json
// 绑定；两者皆无 = 未配置。
func TestResolveTierPrefersEnv(t *testing.T) {
	home, idDir := t.TempDir(), t.TempDir()
	writeProviders(t, home, providersGlobalDoc)
	writeProviders(t, idDir, providersIdentityDoc)
	p, err := LoadProviders(home, idDir)
	if err != nil {
		t.Fatal(err)
	}

	// env 有 MINDLOOP_MODEL → env 赢（即便 JSON 有 think 绑定）。
	choice, err := ResolveTier(p, "think", envMap(map[string]string{"MINDLOOP_MODEL": "glm-5"}))
	if err != nil {
		t.Fatalf("ResolveTier: %v", err)
	}
	if choice.Source != "env" || choice.Model != "glm-5" {
		t.Fatalf("env 优先解析 = %+v", choice)
	}
	// summary 无 env 键、JSON 无绑定 → 未配置。
	choice, err = ResolveTier(p, "summary", envMap(nil))
	if err != nil {
		t.Fatalf("ResolveTier: %v", err)
	}
	if choice.Source != "" {
		t.Fatalf("未配置档位 Source 应为空: %+v", choice)
	}
	// summary 无 env 键，JSON 无绑定 → 同上；think 无 env → JSON 绑定。
	choice, err = ResolveTier(p, "think", envMap(nil))
	if err != nil {
		t.Fatalf("ResolveTier: %v", err)
	}
	if choice.Source != "providers.json" || choice.Model != "glm-4.5-air" {
		t.Fatalf("JSON 绑定解析 = %+v", choice)
	}
	if choice.Profile.ID != "zhipu" || choice.Binding.Profile != "zhipu" {
		t.Fatalf("档案与绑定应随 choice 返回: %+v", choice)
	}
}

// TestResolveTierKeyResolution：档案密钥解析——api_key_env 显式
// 引用优先；缺省约定 MINDLOOP_PROFILE_<ID>_API_KEY（id 大写化）；
// 都没有 → 空（构造层负责报明确错误）。
func TestResolveTierKeyResolution(t *testing.T) {
	home := t.TempDir()
	writeProviders(t, home, `{
	  "profiles": [
	    {"id": "zhipu", "base_url": "https://x", "api_key_env": "OPENROUTER_API_KEY"},
	    {"id": "plain", "base_url": "https://y"}
	  ],
	  "tiers": {
	    "think": {"profile": "zhipu", "model": "m1"},
	    "request": {"profile": "plain", "model": "m2"}
	  }
	}`)
	p, err := LoadProviders(home, "")
	if err != nil {
		t.Fatal(err)
	}
	env := envMap(map[string]string{
		"OPENROUTER_API_KEY":             "sk-ref",
		"MINDLOOP_PROFILE_PLAIN_API_KEY": "sk-conv",
	})
	choice, err := ResolveTier(p, "think", env)
	if err != nil {
		t.Fatal(err)
	}
	if choice.APIKey != "sk-ref" {
		t.Fatalf("api_key_env 引用未生效: %+v", choice)
	}
	choice, err = ResolveTier(p, "request", env)
	if err != nil {
		t.Fatal(err)
	}
	if choice.APIKey != "sk-conv" {
		t.Fatalf("约定键未生效: %+v", choice)
	}
	// 都没有 → 空。
	choice, err = ResolveTier(p, "request", envMap(nil))
	if err != nil {
		t.Fatal(err)
	}
	if choice.APIKey != "" {
		t.Fatalf("无密钥来源时应为空: %+v", choice)
	}
}

// TestResolveTierBrokenReference：tiers 引用不存在的档案要报错
// 且带档案名——降级由装配层决定，解析层只陈述事实。
func TestResolveTierBrokenReference(t *testing.T) {
	home := t.TempDir()
	writeProviders(t, home, `{
	  "profiles": [{"id": "zhipu", "base_url": "https://x"}],
	  "tiers": {"think": {"profile": "ghost", "model": "m"}}
	}`)
	p, err := LoadProviders(home, "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = ResolveTier(p, "think", envMap(nil))
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("坏引用应报错且带档案名: %v", err)
	}
}

// TestValidateForSave：保存校验（web PUT 用）——重复 id、空
// profile/model 的 tier、引用不存在的档案都拒绝。
func TestValidateForSave(t *testing.T) {
	cases := []struct {
		name string
		p    Providers
		want string
	}{
		{"tier 引用不存在", Providers{
			Profiles: []Profile{{ID: "zhipu", BaseURL: "https://x"}},
			Tiers:    map[string]TierBinding{"think": {Profile: "ghost", Model: "m"}},
		}, "ghost"},
		{"tier 缺 model", Providers{
			Profiles: []Profile{{ID: "zhipu", BaseURL: "https://x"}},
			Tiers:    map[string]TierBinding{"think": {Profile: "zhipu"}},
		}, "model"},
		{"未知档位", Providers{
			Profiles: []Profile{{ID: "zhipu", BaseURL: "https://x"}},
			Tiers:    map[string]TierBinding{"nope": {Profile: "zhipu", Model: "m"}},
		}, "档位"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.p.ValidateForSave()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("应报错含 %q: %v", tc.want, err)
			}
		})
	}
	// 合法文档通过。
	ok := Providers{
		Version:  1,
		Profiles: []Profile{{ID: "zhipu", BaseURL: "https://x"}},
		Tiers:    map[string]TierBinding{"think": {Profile: "zhipu", Model: "m"}},
	}
	if err := ok.ValidateForSave(); err != nil {
		t.Fatalf("合法文档不应报错: %v", err)
	}
}

// TestValidateTierRefsScopedToBase：档位引用的存在性以"base ∪ 本档"
// 为域——写身份级文档时 base 是全局级，引用仅全局存在的档案是合法
// 的；自洽校验（base=nil，ValidateForSave 的语义）拒绝之。
func TestValidateTierRefsScopedToBase(t *testing.T) {
	base := &Providers{Profiles: []Profile{{ID: "shared", BaseURL: "https://s/v1"}}}
	doc := &Providers{
		Profiles: []Profile{{ID: "local", BaseURL: "https://l/v1"}},
		Tiers:    map[string]TierBinding{"think": {Profile: "shared", Model: "m"}},
	}
	if err := doc.ValidateProfiles(); err != nil {
		t.Fatalf("档案字段自洽应通过: %v", err)
	}
	if err := doc.ValidateTierRefs(base); err != nil {
		t.Fatalf("引用 base 档案应通过: %v", err)
	}
	if err := doc.ValidateTierRefs(nil); err == nil || !strings.Contains(err.Error(), "shared") {
		t.Fatalf("无 base 时引用外部档案应被拒绝: %v", err)
	}
	if err := doc.ValidateForSave(); err == nil {
		t.Fatal("ValidateForSave 保持文档自洽语义（base=nil），应拒绝仅 base 存在的引用")
	}
	// 档案字段错误在 ValidateProfiles 处拦下（tier 校验不越权）。
	bad := &Providers{Profiles: []Profile{{ID: "BAD ID", BaseURL: "https://x"}}}
	if err := bad.ValidateProfiles(); err == nil {
		t.Fatal("非法 id 应被 ValidateProfiles 拒绝")
	}
}

// TestSaveProvidersRoundTrip：保存后读回一致（原子写路径），
// 且只写身份级文件。
func TestSaveProvidersRoundTrip(t *testing.T) {
	home, idDir := t.TempDir(), t.TempDir()
	in := Providers{
		Version:  1,
		Profiles: []Profile{{ID: "zhipu", Label: "智谱", BaseURL: "https://x", Models: []string{"m1", "m2"}}},
		Tiers:    map[string]TierBinding{"think": {Profile: "zhipu", Model: "m1"}},
	}
	if err := SaveProviders(idDir, &in); err != nil {
		t.Fatalf("SaveProviders: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "providers.json")); !os.IsNotExist(err) {
		t.Fatal("不应写入全局文件")
	}
	back, err := LoadProviders(home, idDir)
	if err != nil {
		t.Fatalf("回读: %v", err)
	}
	if len(back.Profiles) != 1 || back.Profiles[0].Label != "智谱" ||
		len(back.Profiles[0].Models) != 2 || back.Tiers["think"].Model != "m1" {
		t.Fatalf("回读不一致: %+v", back)
	}
}
