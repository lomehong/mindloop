package web

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"mindloop/internal/identity"
)

// providersHome 建一个隔离的 MINDLOOP_HOME 并创建身份 ada。
func providersHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("MINDLOOP_HOME", home)
	if _, err := identity.Create(context.Background(), "ada"); err != nil {
		t.Fatalf("identity.Create: %v", err)
	}
	return home
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}

func writeJSONFile(t *testing.T, path string, doc any) {
	t.Helper()
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	writeFile(t, path, string(data))
}

// doJSON 发请求并返回状态码、解码后的对象与原始 body 文本。
func doJSON(t *testing.T, method, url string, body any) (int, map[string]any, string) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		rd = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, url, rd)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	out := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return resp.StatusCode, out, string(raw)
}

// profileByID 从响应里取 profiles 数组中指定 id 的条目。
func profileByID(t *testing.T, resp map[string]any, id string) map[string]any {
	t.Helper()
	arr, ok := resp["profiles"].([]any)
	if !ok {
		t.Fatalf("profiles 不是数组: %v", resp["profiles"])
	}
	for _, p := range arr {
		m, ok := p.(map[string]any)
		if !ok {
			continue
		}
		if m["id"] == id {
			return m
		}
	}
	t.Fatalf("profiles 中未找到 %q: %v", id, arr)
	return nil
}

// TestLlmProvidersViewMergesTwoLevels：GET 返回两级合并后的有效视图，
// 密钥状态（has_key/key_source/key_env_name）按"进程环境 > 身份 .env"
// 解析，且响应永不包含密钥字面量。
func TestLlmProvidersViewMergesTwoLevels(t *testing.T) {
	home := providersHome(t)
	writeJSONFile(t, filepath.Join(home, "providers.json"), map[string]any{
		"version": 1,
		"profiles": []map[string]any{
			{"id": "glm", "label": "全局 GLM", "provider": "openai-compatible", "base_url": "https://global.example/v1"},
			{"id": "local", "label": "本地", "provider": "openai-compatible", "base_url": "http://127.0.0.1:9/v1", "api_key_env": "LOCAL_KEY"},
		},
		"tiers": map[string]any{"think": map[string]any{"profile": "glm", "model": "glm-4.6"}},
	})
	writeJSONFile(t, filepath.Join(home, "identities", "ada", "providers.json"), map[string]any{
		"profiles": []map[string]any{
			{"id": "glm", "label": "身份 GLM", "provider": "openai-compatible", "base_url": "https://identity.example/v1"},
		},
		"tiers": map[string]any{"request": map[string]any{"profile": "local", "model": "qwen3"}},
	})
	writeFile(t, filepath.Join(home, "identities", "ada", ".env"), "LOCAL_KEY=sk-local\n")

	ts, _ := newTestServer(t, identity.Home(), "")
	status, resp, raw := doJSON(t, "GET", ts.URL+"/api/identities/ada/llm/providers", nil)
	if status != 200 {
		t.Fatalf("status = %d, body = %s", status, raw)
	}

	glm := profileByID(t, resp, "glm")
	if glm["label"] != "身份 GLM" {
		t.Fatalf("同名档案应被身份级替换，label = %v", glm["label"])
	}
	if glm["origin"] != "identity" {
		t.Fatalf("glm.origin = %v，期望 identity（身份级存在）", glm["origin"])
	}
	local := profileByID(t, resp, "local")
	if local["label"] != "本地" {
		t.Fatalf("新档案应来自全局级，label = %v", local["label"])
	}
	if local["origin"] != "global" {
		t.Fatalf("local.origin = %v，期望 global（仅全局级存在）", local["origin"])
	}
	if local["has_key"] != true {
		t.Fatalf("local.has_key = %v（身份 .env 里的 LOCAL_KEY 应命中）", local["has_key"])
	}
	if local["key_source"] != "identity" {
		t.Fatalf("local.key_source = %v，期望 identity", local["key_source"])
	}
	if local["key_env_name"] != "LOCAL_KEY" {
		t.Fatalf("local.key_env_name = %v", local["key_env_name"])
	}
	if glm["has_key"] != false {
		t.Fatalf("glm.has_key = %v，期望 false", glm["has_key"])
	}
	if glm["key_env_name"] != "MINDLOOP_PROFILE_GLM_API_KEY" {
		t.Fatalf("glm.key_env_name = %v（应为约定键）", glm["key_env_name"])
	}

	tiers, ok := resp["tiers"].(map[string]any)
	if !ok {
		t.Fatalf("tiers 缺失: %v", resp)
	}
	if len(tiers) != 2 {
		t.Fatalf("tiers 应有 think+request 两档，得到 %v", tiers)
	}
	if _, ok := tiers["think"]; !ok {
		t.Fatal("tiers.think 缺失")
	}
	if _, ok := tiers["request"]; !ok {
		t.Fatal("tiers.request 缺失")
	}
	// identity_tiers 只暴露身份级原始档位（request），不回写合并结果。
	it, ok := resp["identity_tiers"].(map[string]any)
	if !ok {
		t.Fatalf("identity_tiers 缺失: %v", resp)
	}
	if len(it) != 1 {
		t.Fatalf("identity_tiers 应只含身份级原始档位（request），得到 %v", it)
	}
	if _, ok := it["request"]; !ok {
		t.Fatalf("identity_tiers.request 缺失: %v", it)
	}
	if strings.Contains(raw, "sk-local") {
		t.Fatalf("响应包含密钥字面量: %s", raw)
	}

	// 进程环境命中 → key_source=env 且优先于身份 .env。
	t.Setenv("MINDLOOP_PROFILE_GLM_API_KEY", "sk-proc")
	_, resp2, raw2 := doJSON(t, "GET", ts.URL+"/api/identities/ada/llm/providers", nil)
	glm2 := profileByID(t, resp2, "glm")
	if glm2["has_key"] != true || glm2["key_source"] != "env" {
		t.Fatalf("进程环境密钥应命中: has_key=%v source=%v", glm2["has_key"], glm2["key_source"])
	}
	if strings.Contains(raw2, "sk-proc") {
		t.Fatalf("响应包含密钥字面量: %s", raw2)
	}
}

// TestLlmProvidersPutDivertsKeyAndNeverWritesIt：PUT 的明文 api_key
// 分流写入身份 .env（约定键），providers.json 与响应体永不出现密钥。
func TestLlmProvidersPutDivertsKeyAndNeverWritesIt(t *testing.T) {
	home := providersHome(t)
	ts, _ := newTestServer(t, identity.Home(), "")

	body := map[string]any{
		"version": 1,
		"profiles": []map[string]any{{
			"id": "glm", "label": "智谱", "provider": "openai-compatible",
			"base_url": "https://open.bigmodel.cn/api/paas/v4",
			"api_key":  "sk-secret-123",
			"models":   []string{"glm-4.6"},
		}},
		"tiers": map[string]any{"think": map[string]any{"profile": "glm", "model": "glm-4.6"}},
	}
	status, resp, raw := doJSON(t, "PUT", ts.URL+"/api/identities/ada/llm/providers", body)
	if status != 200 {
		t.Fatalf("status = %d, body = %s", status, raw)
	}
	if strings.Contains(raw, "sk-secret-123") {
		t.Fatalf("PUT 响应包含密钥字面量: %s", raw)
	}

	idDir := filepath.Join(home, "identities", "ada")
	jsonData, err := os.ReadFile(filepath.Join(idDir, "providers.json"))
	if err != nil {
		t.Fatalf("providers.json 未落盘: %v", err)
	}
	if strings.Contains(string(jsonData), "sk-secret-123") {
		t.Fatalf("providers.json 包含密钥字面量: %s", jsonData)
	}
	if strings.Contains(string(jsonData), `"api_key":`) {
		t.Fatalf("providers.json 不应有 api_key 字段: %s", jsonData)
	}
	envData, err := os.ReadFile(filepath.Join(idDir, ".env"))
	if err != nil {
		t.Fatalf(".env 未写入: %v", err)
	}
	if !strings.Contains(string(envData), "MINDLOOP_PROFILE_GLM_API_KEY=sk-secret-123") {
		t.Fatalf(".env 应含约定键密钥: %s", envData)
	}

	glm := profileByID(t, resp, "glm")
	if glm["has_key"] != true || glm["key_source"] != "identity" {
		t.Fatalf("保存后视图应显示身份密钥: has_key=%v source=%v", glm["has_key"], glm["key_source"])
	}
	models, ok := glm["models"].([]any)
	if !ok || len(models) != 1 || models[0] != "glm-4.6" {
		t.Fatalf("models 应原样回显: %v", glm["models"])
	}
}

// TestLlmProvidersPutTierRefsGlobalProfile：身份级档位可以引用仅全局
// 存在的档案（两级合并下是有效配置，保存校验以"全局 ∪ 身份"为域）；
// 视图的 identity_tiers 暴露身份级原始档位——合并来的档位不回写。
func TestLlmProvidersPutTierRefsGlobalProfile(t *testing.T) {
	home := providersHome(t)
	writeJSONFile(t, filepath.Join(home, "providers.json"), map[string]any{
		"version": 1,
		"profiles": []map[string]any{
			{"id": "shared", "label": "全局共享", "base_url": "https://shared.example/v1"},
		},
		"tiers": map[string]any{"summary": map[string]any{"profile": "shared", "model": "m-sum"}},
	})
	ts, _ := newTestServer(t, identity.Home(), "")

	body := map[string]any{
		"profiles": []map[string]any{{"id": "local", "base_url": "http://127.0.0.1:9/v1"}},
		"tiers":    map[string]any{"think": map[string]any{"profile": "shared", "model": "glm-4.6"}},
	}
	status, resp, raw := doJSON(t, "PUT", ts.URL+"/api/identities/ada/llm/providers", body)
	if status != 200 {
		t.Fatalf("status = %d，引用全局档案应可保存（body = %s）", status, raw)
	}
	it, ok := resp["identity_tiers"].(map[string]any)
	if !ok {
		t.Fatalf("identity_tiers 缺失: %v", resp)
	}
	if len(it) != 1 {
		t.Fatalf("identity_tiers 应只含身份级原始档位（think），得到 %v", it)
	}
	if _, ok := it["think"]; !ok {
		t.Fatalf("identity_tiers.think 缺失: %v", it)
	}
	tiers, _ := resp["tiers"].(map[string]any)
	if len(tiers) != 2 {
		t.Fatalf("合并 tiers 应含 think（身份覆盖）+ summary（全局）: %v", tiers)
	}

	// 落盘文档只保存身份级档位——合并视图不回写。
	data, err := os.ReadFile(filepath.Join(identity.Home(), "ada", "providers.json"))
	if err != nil {
		t.Fatalf("providers.json 未落盘: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("解析落盘文档: %v", err)
	}
	savedTiers, _ := doc["tiers"].(map[string]any)
	if len(savedTiers) != 1 {
		t.Fatalf("落盘 tiers 应只含身份级 think: %v", savedTiers)
	}
	if _, ok := savedTiers["summary"]; ok {
		t.Fatalf("全局 summary 档位不应被固化进身份文档: %s", data)
	}
}

// TestLlmProvidersPutValidates：保存端严格校验（读时宽容、写时严格）。
func TestLlmProvidersPutValidates(t *testing.T) {
	cases := []struct {
		name string
		body map[string]any
	}{
		{"重复 id", map[string]any{"profiles": []map[string]any{
			{"id": "glm", "base_url": "https://a/v1"},
			{"id": "glm", "base_url": "https://b/v1"},
		}}},
		{"缺 base_url", map[string]any{"profiles": []map[string]any{
			{"id": "glm", "provider": "openai-compatible"},
		}}},
		{"tier 引用不存在档案", map[string]any{
			"profiles": []map[string]any{{"id": "glm", "base_url": "https://a/v1"}},
			"tiers":    map[string]any{"think": map[string]any{"profile": "ghost", "model": "x"}},
		}},
		{"非法 api_key_env", map[string]any{"profiles": []map[string]any{
			{"id": "glm", "base_url": "https://a/v1", "api_key_env": "1BAD KEY"},
		}}},
		{"api_key 含换行", map[string]any{"profiles": []map[string]any{
			{"id": "glm", "base_url": "https://a/v1", "api_key": "sk-a\nINJECTED=1"},
		}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			providersHome(t)
			ts, _ := newTestServer(t, identity.Home(), "")
			status, resp, raw := doJSON(t, "PUT", ts.URL+"/api/identities/ada/llm/providers", tc.body)
			if status != 400 {
				t.Fatalf("status = %d，期望 400（body = %s）", status, raw)
			}
			if resp["detail"] == nil {
				t.Fatalf("400 响应应带 detail: %s", raw)
			}
			if _, err := os.Stat(filepath.Join(identity.Home(), "ada", "providers.json")); !os.IsNotExist(err) {
				t.Fatalf("校验失败不应落盘: %v", err)
			}
		})
	}
}

// TestLlmProvidersPutCleansRemovedProfileKey：删除档案时顺带清理
// 身份 .env 里它引用的密钥键；文件其余内容不动。
func TestLlmProvidersPutCleansRemovedProfileKey(t *testing.T) {
	home := providersHome(t)
	idDir := filepath.Join(home, "identities", "ada")
	writeFile(t, filepath.Join(idDir, ".env"), "MINDLOOP_PROFILE_GLM_API_KEY=sk-old\nKEEP_ME=1\n")
	writeJSONFile(t, filepath.Join(idDir, "providers.json"), map[string]any{
		"profiles": []map[string]any{
			{"id": "glm", "base_url": "https://a/v1"},
			{"id": "mini", "base_url": "https://b/v1"},
		},
	})

	ts, _ := newTestServer(t, identity.Home(), "")
	body := map[string]any{"profiles": []map[string]any{{"id": "mini", "base_url": "https://b/v1"}}}
	status, _, raw := doJSON(t, "PUT", ts.URL+"/api/identities/ada/llm/providers", body)
	if status != 200 {
		t.Fatalf("status = %d, body = %s", status, raw)
	}
	envData, err := os.ReadFile(filepath.Join(idDir, ".env"))
	if err != nil {
		t.Fatalf(".env 读取失败: %v", err)
	}
	if strings.Contains(string(envData), "MINDLOOP_PROFILE_GLM_API_KEY") {
		t.Fatalf("被删档案的密钥键应被清理: %s", envData)
	}
	if !strings.Contains(string(envData), "KEEP_ME=1") {
		t.Fatalf("无关变量不应被动: %s", envData)
	}
}

// TestLlmModelsProbeAndCache：实拉目录（带档案密钥鉴权头）→ 二次
// 命中缓存 → 错误不缓存（修好即可重试）。
func TestLlmModelsProbeAndCache(t *testing.T) {
	home := providersHome(t)

	var hits int32
	var gotAuth atomic.Value
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		gotAuth.Store(r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"id":"glm-4.6"},{"id":"glm-4.5"}]}`)
	}))
	defer mock.Close()

	writeJSONFile(t, filepath.Join(home, "providers.json"), map[string]any{
		"profiles": []map[string]any{{"id": "glm", "provider": "openai-compatible", "base_url": mock.URL, "api_key_env": "GLM_KEY"}},
		"tiers":    map[string]any{"think": map[string]any{"profile": "glm", "model": "glm-4.6"}},
	})
	writeFile(t, filepath.Join(home, "identities", "ada", ".env"), "GLM_KEY=sk-id-1\n")

	ts, _ := newTestServer(t, identity.Home(), "")
	status, resp, raw := doJSON(t, "GET", ts.URL+"/api/identities/ada/llm/models?profile=glm", nil)
	if status != 200 {
		t.Fatalf("status = %d, body = %s", status, raw)
	}
	models, _ := resp["models"].([]any)
	if len(models) != 2 || models[0] != "glm-4.6" {
		t.Fatalf("models = %v", resp["models"])
	}
	if resp["source"] != "live" {
		t.Fatalf("source = %v，期望 live", resp["source"])
	}
	if atomic.LoadInt32(&hits) != 1 {
		t.Fatalf("hits = %d，期望 1", hits)
	}
	if v, _ := gotAuth.Load().(string); v != "Bearer sk-id-1" {
		t.Fatalf("mock 收到鉴权头 = %q，期望档案密钥", v)
	}

	// 二次：缓存命中，不再打网络。
	_, resp2, raw2 := doJSON(t, "GET", ts.URL+"/api/identities/ada/llm/models?profile=glm", nil)
	if resp2["source"] != "cache" {
		t.Fatalf("第二次 source = %v，期望 cache（body = %s）", resp2["source"], raw2)
	}
	if atomic.LoadInt32(&hits) != 1 {
		t.Fatalf("缓存命中不应发请求，hits = %d", hits)
	}

	// 未带 profile → 主档（think 绑定）解析。
	_, resp3, raw3 := doJSON(t, "GET", ts.URL+"/api/identities/ada/llm/models", nil)
	if resp3["profile"] != "glm" {
		t.Fatalf("缺省 profile 应为主档 glm: %v（body = %s）", resp3["profile"], raw3)
	}

	// 未知档案 → 404。
	statusBad, _, _ := doJSON(t, "GET", ts.URL+"/api/identities/ada/llm/models?profile=ghost", nil)
	if statusBad != 404 {
		t.Fatalf("未知档案 status = %d，期望 404", statusBad)
	}
}

// TestLlmModelsErrorsAsFields：探测失败装 error 字段（200 非错误码）；
// 错误不缓存——修好后再拉立即成功。
func TestLlmModelsErrorsAsFields(t *testing.T) {
	home := providersHome(t)
	var fail atomic.Bool
	fail.Store(true)
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			w.WriteHeader(500)
			_, _ = io.WriteString(w, "boom")
			return
		}
		_, _ = io.WriteString(w, `{"data":[{"id":"m1"}]}`)
	}))
	defer mock.Close()

	writeJSONFile(t, filepath.Join(home, "providers.json"), map[string]any{
		"profiles": []map[string]any{{"id": "glm", "provider": "openai-compatible", "base_url": mock.URL}},
	})
	ts, _ := newTestServer(t, identity.Home(), "")

	status, resp, raw := doJSON(t, "GET", ts.URL+"/api/identities/ada/llm/models?profile=glm", nil)
	if status != 200 {
		t.Fatalf("探测失败也应是 200（错误装字段），得到 %d: %s", status, raw)
	}
	if errStr, _ := resp["error"].(string); errStr == "" {
		t.Fatalf("应带 error 字段: %s", raw)
	}

	// 修好后再拉 → 立即成功（错误没有被缓存）。
	fail.Store(false)
	_, resp2, raw2 := doJSON(t, "GET", ts.URL+"/api/identities/ada/llm/models?profile=glm", nil)
	models, _ := resp2["models"].([]any)
	if len(models) != 1 || models[0] != "m1" {
		t.Fatalf("修好后应立即可拉: %v（body = %s）", resp2["models"], raw2)
	}
}

// TestLlmModelsEnvFallback：无档案配置时缺省探测回落 env 路径
// （echo 固定单例），与 CLI 的回落纪律一致。
func TestLlmModelsEnvFallback(t *testing.T) {
	providersHome(t)
	t.Setenv("MINDLOOP_MODEL", "echo")
	ts, _ := newTestServer(t, identity.Home(), "")

	status, resp, raw := doJSON(t, "GET", ts.URL+"/api/identities/ada/llm/models", nil)
	if status != 200 {
		t.Fatalf("status = %d, body = %s", status, raw)
	}
	models, _ := resp["models"].([]any)
	if len(models) != 1 || models[0] != "echo" {
		t.Fatalf("env 回落应得到 echo: %v（body = %s）", resp["models"], raw)
	}
}

// TestLlmConfigResolvesLikeCli：GET /llm/config 与 CLI 同一解析器
// （config.ResolveTier）：env 旧键优先、providers.json 绑定次之，
// 坏引用报错装字段而不是 500。
func TestLlmConfigResolvesLikeCli(t *testing.T) {
	home := providersHome(t)
	writeJSONFile(t, filepath.Join(home, "providers.json"), map[string]any{
		"profiles": []map[string]any{{"id": "glm", "provider": "openai-compatible", "base_url": "https://a/v1"}},
		"tiers":    map[string]any{"think": map[string]any{"profile": "glm", "model": "glm-4.6"}},
	})
	t.Setenv("MINDLOOP_REQUEST_MODEL", "echo")
	ts, _ := newTestServer(t, identity.Home(), "")

	status, resp, raw := doJSON(t, "GET", ts.URL+"/api/identities/ada/llm/config", nil)
	if status != 200 {
		t.Fatalf("status = %d, body = %s", status, raw)
	}
	tiers, ok := resp["tiers"].(map[string]any)
	if !ok {
		t.Fatalf("tiers 缺失: %s", raw)
	}
	think, _ := tiers["think"].(map[string]any)
	if think["source"] != "providers.json" || think["model"] != "glm-4.6" || think["profile"] != "glm" {
		t.Fatalf("think = %v", think)
	}
	request, _ := tiers["request"].(map[string]any)
	if request["source"] != "env" || request["model"] != "echo" {
		t.Fatalf("request = %v（env 旧键应优先）", request)
	}
	summary, _ := tiers["summary"].(map[string]any)
	if summary["source"] != "" {
		t.Fatalf("summary = %v（未配置应为空源）", summary)
	}

	// 坏引用（身份级覆盖 summary）→ error 装字段，不影响其他档。
	writeJSONFile(t, filepath.Join(home, "identities", "ada", "providers.json"), map[string]any{
		"tiers": map[string]any{"summary": map[string]any{"profile": "ghost", "model": "x"}},
	})
	_, resp2, raw2 := doJSON(t, "GET", ts.URL+"/api/identities/ada/llm/config", nil)
	tiers2, _ := resp2["tiers"].(map[string]any)
	summary2, _ := tiers2["summary"].(map[string]any)
	if errStr, _ := summary2["error"].(string); errStr == "" {
		t.Fatalf("坏引用应装 error 字段: %v（body = %s）", summary2, raw2)
	}
	think2, _ := tiers2["think"].(map[string]any)
	if think2["source"] != "providers.json" {
		t.Fatalf("坏引用不应影响其他档: %v", think2)
	}
}

// TestLlmProvidersBadDocumentShowsErrorAndAllowsPutFix：磁盘上的坏
// 文档在读取端报告为 error 字段（配置页可见）；PUT 合法文档可直接
// 覆盖修复（清理步骤跳过坏旧文档）。
func TestLlmProvidersBadDocumentShowsErrorAndAllowsPutFix(t *testing.T) {
	home := providersHome(t)
	writeFile(t, filepath.Join(home, "identities", "ada", "providers.json"), "{bad json")
	ts, _ := newTestServer(t, identity.Home(), "")

	status, resp, raw := doJSON(t, "GET", ts.URL+"/api/identities/ada/llm/providers", nil)
	if status != 200 {
		t.Fatalf("坏文档读取 status = %d，期望 200+error 字段", status)
	}
	if errStr, _ := resp["error"].(string); errStr == "" {
		t.Fatalf("坏文档应装 error 字段: %s", raw)
	}

	body := map[string]any{
		"profiles": []map[string]any{{"id": "glm", "provider": "openai-compatible", "base_url": "https://a/v1"}},
	}
	statusPut, respPut, rawPut := doJSON(t, "PUT", ts.URL+"/api/identities/ada/llm/providers", body)
	if statusPut != 200 {
		t.Fatalf("PUT 覆盖修复 status = %d, body = %s", statusPut, rawPut)
	}
	if errStr, _ := respPut["error"].(string); errStr != "" {
		t.Fatalf("修复后不应再有 error: %s", rawPut)
	}
	profileByID(t, respPut, "glm")

	_, respGet, _ := doJSON(t, "GET", ts.URL+"/api/identities/ada/llm/providers", nil)
	if errStr, _ := respGet["error"].(string); errStr != "" {
		t.Fatalf("修复后 GET 应无 error: %v", respGet["error"])
	}
}
