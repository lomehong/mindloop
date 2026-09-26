// llm_providers.go 是多提供商配置的 web 面：providers 有效视图/
// 保存、模型目录探测、档位解析视图。核心契约是"配置页显示 = CLI
// 实际使用"：解析走 config.LoadProviders/ResolveTier 同一解析器，
// 探测走 llm.ListModels 同一列表接口；密钥只在身份 .env（PUT 的
// 明文 api_key 分流写入），providers.json 与响应体永不出现密钥。
package web

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mindloop/internal/config"
	"mindloop/internal/identity"
	"mindloop/internal/llm"
	"mindloop/internal/traj"
)

// routeLLM 分发 /api/identities/{id}/llm/* 子树：
//
//	GET  .../llm/providers          → 两级合并后的有效视图
//	PUT  .../llm/providers          → 保存身份级文档（api_key 分流 .env）
//	GET  .../llm/models?profile=&fresh= → 拉取模型目录（TTL 缓存）
//	GET  .../llm/config             → 三档位解析结果（与 CLI 同一解析器）
func (s *Server) routeLLM(w http.ResponseWriter, r *http.Request, id *identity.Identity, rest []string) {
	if len(rest) == 0 {
		writeError(w, 404, "缺少 llm 子路径（providers|models|config）")
		return
	}
	switch rest[0] {
	case "providers":
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, 200, s.llmProvidersResponse(id))
		case http.MethodPut:
			s.handleLlmProvidersPut(w, r, id)
		default:
			w.Header().Set("Allow", "GET, PUT")
			writeError(w, http.StatusMethodNotAllowed, "llm/providers 只接受 GET/PUT")
		}
	case "models":
		s.handleLlmModels(w, r, id)
	case "config":
		s.handleLlmConfig(w, r, id)
	default:
		writeError(w, 404, "未知子路径: llm/"+strings.Join(rest, "/"))
	}
}

// identityEnvVars 读身份 .env 的原始键值。仅供服务端解析密钥与
// 配置用，绝不回显——回显走脱敏路径（handleIdentityEnv）。
func identityEnvVars(idDir string) map[string]string {
	data, err := os.ReadFile(filepath.Join(idDir, ".env"))
	if err != nil {
		return map[string]string{}
	}
	return parseEnvRaw(string(data))
}

// parseEnvRaw 解析 dotenv 子集（同 config.LoadEnv 规则：BOM 清理、
// 注释、成对引号），返回原始值。与 parseEnvRedacted 同一行解析。
func parseEnvRaw(content string) map[string]string {
	out := map[string]string{}
	first := true
	for _, ln := range strings.Split(content, "\n") {
		ln = strings.TrimSpace(ln)
		if first {
			ln = strings.TrimPrefix(ln, "\ufeff")
			first = false
		}
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		k, v, ok := strings.Cut(ln, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
			v = v[1 : len(v)-1]
		}
		if k != "" {
			out[k] = v
		}
	}
	return out
}

// envLookup 合成 CLI 的配置查找语义：显式进程环境（含启动时已
// 加载的全局 .env）> 身份 .env 文件。逐请求构造，不写回进程——
// web 是多身份常驻进程，身份 .env 不能灌进全局环境。
func envLookup(identityVars map[string]string) func(string) string {
	return func(k string) string {
		if v, ok := os.LookupEnv(k); ok {
			return v
		}
		return identityVars[k]
	}
}

// profileView 组装单个档案的展示视图（绝不包含密钥值——只有
// 键名与命中状态）。origin 标记档案在磁盘上的归属层级："identity"
// （身份级存在，含覆盖全局的同名档案）或 "global"（仅全局级）——
// 两级合并下删除全局档案会回潮，配置页需要据此禁用删除。
func profileView(pr config.Profile, getenv func(string) string, origin string) map[string]any {
	name := pr.KeyEnvName()
	has, source := false, ""
	if strings.TrimSpace(getenv(name)) != "" {
		has = true
		if strings.TrimSpace(os.Getenv(name)) != "" {
			source = "env"
		} else {
			source = "identity"
		}
	}
	models := pr.Models
	if models == nil {
		models = []string{}
	}
	return map[string]any{
		"id":           pr.ID,
		"label":        pr.Label,
		"provider":     pr.Provider,
		"base_url":     pr.BaseURL,
		"api_key_env":  pr.APIKeyEnv,
		"models":       models,
		"key_env_name": name,
		"has_key":      has,
		"key_source":   source,
		"origin":       origin,
	}
}

// llmProvidersResponse 构造 providers 有效视图：两级合并结果 +
// 每档案密钥命中状态。坏文档不报错误码——配置页需要看到错误并
// 用 PUT 覆盖修复。
func (s *Server) llmProvidersResponse(id *identity.Identity) map[string]any {
	home := traj.Home()
	out := map[string]any{
		"identity":       map[string]string{"id": id.Name, "name": id.Name},
		"version":        1,
		"profiles":       []any{},
		"tiers":          map[string]any{},
		"identity_tiers": map[string]any{},
		"paths": map[string]string{
			"global":   filepath.Join(home, "providers.json"),
			"identity": filepath.Join(id.Dir, "providers.json"),
		},
	}
	merged, err := config.LoadProviders(home, id.Dir)
	if err != nil {
		out["error"] = err.Error()
		return out
	}
	getenv := envLookup(identityEnvVars(id.Dir))
	// 身份级档案 id 集：区分覆盖/新增（identity）与纯全局（global）；
	// identity_tiers 是身份文档的原始档位——客户端写面的基线（最小
	// 写入：合并视图里的全局档位不回写进身份文档）。
	identityProfiles := map[string]bool{}
	identityTiers := map[string]any{}
	if data, err := os.ReadFile(filepath.Join(id.Dir, "providers.json")); err == nil {
		var doc config.Providers
		if json.Unmarshal(data, &doc) == nil {
			for _, pr := range doc.Profiles {
				identityProfiles[pr.ID] = true
			}
			for t, b := range doc.Tiers {
				identityTiers[t] = map[string]any{"profile": b.Profile, "model": b.Model}
			}
		}
	}
	profiles := []any{}
	for _, pr := range merged.Profiles {
		origin := "global"
		if identityProfiles[pr.ID] {
			origin = "identity"
		}
		profiles = append(profiles, profileView(pr, getenv, origin))
	}
	tiers := map[string]any{}
	for t, b := range merged.Tiers {
		tiers[t] = map[string]any{"profile": b.Profile, "model": b.Model}
	}
	if merged.Version > 0 {
		out["version"] = merged.Version
	}
	out["profiles"] = profiles
	out["tiers"] = tiers
	out["identity_tiers"] = identityTiers
	return out
}

// llmProviderPutDoc 是 PUT 的请求体形态：在 Providers 文档基础上
// 每个档案多一个明文 api_key 字段（仅请求体存在，分流写入身份
// .env 后即消失）。
type llmProviderPutDoc struct {
	Version  int                           `json:"version"`
	Profiles []llmProviderPutProfile       `json:"profiles"`
	Tiers    map[string]config.TierBinding `json:"tiers"`
}

type llmProviderPutProfile struct {
	ID        string   `json:"id"`
	Label     string   `json:"label"`
	Provider  string   `json:"provider"`
	BaseURL   string   `json:"base_url"`
	APIKeyEnv string   `json:"api_key_env"`
	Models    []string `json:"models"`
	APIKey    string   `json:"api_key"`
}

// handleLlmProvidersPut 保存身份级 providers.json：
//   - 写时严格（ValidateProfiles + ValidateTierRefs(全局) + api_key_env/
//     api_key 字符检查）；
//   - 明文 api_key 分流写入身份 .env（显式 api_key_env 或约定键）；
//   - 被本次保存移除的档案，其密钥键从身份 .env 清理。
func (s *Server) handleLlmProvidersPut(w http.ResponseWriter, r *http.Request, id *identity.Identity) {
	var req llmProviderPutDoc
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, 400, "请求体必须是 providers 文档 JSON")
		return
	}
	version := req.Version
	if version <= 0 {
		version = 1
	}
	doc := &config.Providers{Version: version, Tiers: req.Tiers}
	if doc.Tiers == nil {
		doc.Tiers = map[string]config.TierBinding{}
	}
	envSets := []envKV{}
	for i := range req.Profiles {
		p := &req.Profiles[i]
		p.ID = strings.TrimSpace(p.ID)
		p.BaseURL = strings.TrimSpace(p.BaseURL)
		if p.APIKeyEnv != "" && !isEnvKey(p.APIKeyEnv) {
			writeError(w, 400, "档案 "+p.ID+" 的 api_key_env 非法（仅字母/数字/下划线，不以数字开头）")
			return
		}
		// 换行能注入任意 .env 行（再塞一个 MINDLOOP_MODEL 之类）——
		// 密钥值只走单行。
		if strings.ContainsAny(p.APIKey, "\n\r") {
			writeError(w, 400, "档案 "+p.ID+" 的 api_key 不能包含换行")
			return
		}
		pr := config.Profile{
			ID: p.ID, Label: p.Label, Provider: p.Provider,
			BaseURL: p.BaseURL, APIKeyEnv: p.APIKeyEnv, Models: p.Models,
		}
		if p.APIKey != "" {
			envSets = append(envSets, envKV{Key: pr.KeyEnvName(), Value: p.APIKey})
		}
		doc.Profiles = append(doc.Profiles, pr)
	}
	if err := doc.ValidateProfiles(); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	// 档位引用以"全局 ∪ 本档"为域：引用仅全局存在的档案是有效配置
	// （两级合并下可解析）。全局文档坏/缺失 → 空底，身份面仍可保存。
	base, baseErr := config.LoadProviders(traj.Home(), "")
	if baseErr != nil || base == nil {
		base = &config.Providers{}
	}
	if err := doc.ValidateTierRefs(base); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	deletes := removedProfileKeys(id.Dir, doc)
	// 密钥先落地、文档后写入：任何一步失败都可用同一请求重试自愈。
	if err := writeIdentityEnvKeys(filepath.Join(id.Dir, ".env"), envSets, deletes); err != nil {
		writeError(w, 500, "写入身份 .env 失败: "+err.Error())
		return
	}
	if err := config.SaveProviders(id.Dir, doc); err != nil {
		writeError(w, 500, "保存 providers.json 失败: "+err.Error())
		return
	}
	writeJSON(w, 200, s.llmProvidersResponse(id))
}

// removedProfileKeys 计算被本次保存移除的档案所引用的密钥键
// （约定键或显式 api_key_env）。旧文档坏/不存在 → 跳过清理。
func removedProfileKeys(idDir string, next *config.Providers) []string {
	data, err := os.ReadFile(filepath.Join(idDir, "providers.json"))
	if err != nil {
		return nil
	}
	var old config.Providers
	if err := json.Unmarshal(data, &old); err != nil {
		return nil
	}
	kept := map[string]bool{}
	for _, pr := range next.Profiles {
		kept[pr.ID] = true
	}
	var out []string
	for _, pr := range old.Profiles {
		if !kept[pr.ID] {
			out = append(out, pr.KeyEnvName())
		}
	}
	return out
}

// envKV 是一个待写入 .env 的键值（保序）。
type envKV struct {
	Key   string
	Value string
}

// writeIdentityEnvKeys 对身份 .env 做一次"读-改-写"：sets 覆盖/
// 追加，deletes 移除，其余行与注释原样保留。临时文件 + 原子改名；
// envPutMu 与单键 env 端点共用同一把锁，避免同进程交错。
func writeIdentityEnvKeys(envPath string, sets []envKV, deletes []string) error {
	if len(sets) == 0 && len(deletes) == 0 {
		return nil
	}
	envPutMu.Lock()
	defer envPutMu.Unlock()
	data, err := os.ReadFile(envPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	del := map[string]bool{}
	for _, k := range deletes {
		if k != "" {
			del[k] = true
		}
	}
	done := map[string]bool{}
	var kept []string
	for _, ln := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		k, _, ok := strings.Cut(ln, "=")
		key := strings.TrimSpace(k)
		if ok && del[key] {
			continue
		}
		if ok {
			replaced := false
			for _, kv := range sets {
				if kv.Key == key && !done[kv.Key] {
					kept = append(kept, kv.Key+"="+kv.Value)
					done[kv.Key] = true
					replaced = true
					break
				}
			}
			if replaced {
				continue
			}
		}
		kept = append(kept, ln)
	}
	for _, kv := range sets {
		if done[kv.Key] {
			continue
		}
		if n := len(kept); n > 0 && kept[n-1] == "" {
			kept = kept[:n-1]
		}
		kept = append(kept, kv.Key+"="+kv.Value)
		done[kv.Key] = true
	}
	out := strings.Join(kept, "\n")
	if out != "" && !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	tmp := envPath + ".tmp"
	if err := os.WriteFile(tmp, []byte(out), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, envPath); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// llmModelsTTL 是模型目录的进程内缓存时长：拉取后的窗口内重复
// 查询不打网络；错误不缓存（修好配置即可重试）。
const llmModelsTTL = 5 * time.Minute

// probeModelsClient 构造探测用客户端：ListModels 无对话构造的高
// 门槛（无 key 也试——本地网关常常无鉴权）。档案 provider 为空时
// 按 openai-compatible 处理：探测语境没有模型名可推断，兼容端点
// 是唯一合理默认。
func probeModelsClient(pr config.Profile, apiKey string) *llm.Client {
	provider := pr.Provider
	if provider == "" {
		provider = llm.ProviderOpenAICompat
	}
	return &llm.Client{
		Provider: provider,
		Model:    "probe",
		APIKey:   apiKey,
		BaseURL:  pr.BaseURL,
		HTTP:     &http.Client{Timeout: 20 * time.Second},
	}
}

// handleLlmModels 拉取模型目录：profile 指定档案；缺省用主档
// （think）解析结果，走不了 providers.json 就回落 env 路径（与
// CLI 回落纪律一致）。?fresh=1 绕过缓存（配置页"重新拉取"按钮）。
func (s *Server) handleLlmModels(w http.ResponseWriter, r *http.Request, id *identity.Identity) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	profileID := strings.TrimSpace(r.URL.Query().Get("profile"))
	getenv := envLookup(identityEnvVars(id.Dir))
	merged, err := config.LoadProviders(traj.Home(), id.Dir)
	if err != nil {
		writeJSON(w, 200, map[string]any{"profile": profileID, "models": []any{}, "source": "", "error": err.Error()})
		return
	}

	var client *llm.Client
	if profileID != "" {
		pr, ok := merged.Profile(profileID)
		if !ok {
			writeError(w, 404, "档案不存在: "+profileID)
			return
		}
		client = probeModelsClient(pr, strings.TrimSpace(getenv(pr.KeyEnvName())))
	} else {
		choice, err := config.ResolveTier(merged, "think", getenv)
		if err != nil {
			writeJSON(w, 200, map[string]any{"profile": "", "models": []any{}, "source": "", "error": err.Error()})
			return
		}
		if choice.Source == "providers.json" {
			profileID = choice.Profile.ID
			client = probeModelsClient(choice.Profile, choice.APIKey)
		} else {
			c, err := llm.FromEnvLookup(getenv)
			if err != nil {
				writeJSON(w, 200, map[string]any{"profile": "", "models": []any{}, "source": "", "error": err.Error()})
				return
			}
			client = c
		}
	}

	cacheKey := id.Dir + "\x00" + profileID + "\x00" + client.Provider + "\x00" + client.BaseURL
	if r.URL.Query().Get("fresh") != "1" {
		if models, ok := s.llmModelsCached(cacheKey); ok {
			writeJSON(w, 200, map[string]any{"profile": profileID, "models": models, "source": "cache"})
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	models, err := client.ListModels(ctx)
	if err != nil {
		writeJSON(w, 200, map[string]any{"profile": profileID, "models": []any{}, "source": "live", "error": err.Error()})
		return
	}
	s.llmModelsStore(cacheKey, models)
	writeJSON(w, 200, map[string]any{"profile": profileID, "models": models, "source": "live"})
}

func (s *Server) llmModelsCached(key string) ([]string, bool) {
	s.llmModelsMu.Lock()
	defer s.llmModelsMu.Unlock()
	e, ok := s.llmModelsCache[key]
	if !ok || time.Since(e.at) > llmModelsTTL {
		return nil, false
	}
	return e.models, true
}

func (s *Server) llmModelsStore(key string, models []string) {
	s.llmModelsMu.Lock()
	defer s.llmModelsMu.Unlock()
	s.llmModelsCache[key] = llmModelsEntry{models: models, at: time.Now()}
}

// llmModelsEntry 是一次成功的目录探测（错误不缓存）。
type llmModelsEntry struct {
	models []string
	at     time.Time
}

// handleLlmConfig 返回三档位的有效解析（与 CLI 同一解析器）：
// {tiers: {think:{source,model,profile,error}, request:…, summary:…}}。
// env 旧键优先、providers.json 绑定次之、坏引用装 error 字段。
func (s *Server) handleLlmConfig(w http.ResponseWriter, r *http.Request, id *identity.Identity) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	merged, err := config.LoadProviders(traj.Home(), id.Dir)
	tiers := map[string]any{}
	for _, t := range []string{"think", "request", "summary"} {
		row := map[string]any{"source": "", "model": "", "profile": ""}
		if err != nil {
			row["error"] = err.Error()
			tiers[t] = row
			continue
		}
		getenv := envLookup(identityEnvVars(id.Dir))
		choice, rerr := config.ResolveTier(merged, t, getenv)
		if rerr != nil {
			row["error"] = rerr.Error()
		} else {
			row["source"] = choice.Source
			row["model"] = choice.Model
			if choice.Source == "providers.json" {
				row["profile"] = choice.Profile.ID
			}
		}
		tiers[t] = row
	}
	out := map[string]any{"tiers": tiers}
	if err != nil {
		out["error"] = err.Error()
	}
	writeJSON(w, 200, out)
}
