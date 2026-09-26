package llm

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestListModelsOpenAICompat：openai-compatible 目录——GET {base}/models，
// Bearer 鉴权，data[].id 按目录顺序返回。
func TestListModelsOpenAICompat(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		io.WriteString(w, `{"object":"list","data":[{"id":"glm-5"},{"id":"glm-4.5-air"},{"id":"glm-4-flash"}]}`)
	}))
	defer srv.Close()

	c := &Client{Provider: ProviderOpenAICompat, BaseURL: srv.URL + "/v1", APIKey: "sk-test", HTTP: srv.Client()}
	ids, err := c.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if gotPath != "/v1/models" {
		t.Fatalf("路径 = %q，应为 /v1/models", gotPath)
	}
	if gotAuth != "Bearer sk-test" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	want := []string{"glm-5", "glm-4.5-air", "glm-4-flash"}
	if len(ids) != len(want) {
		t.Fatalf("ids = %v", ids)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("ids[%d] = %q，应为 %q（保持目录顺序）", i, ids[i], want[i])
		}
	}
}

// TestListModelsNoKeyStillTries：无 key 也发请求（本地 Ollama/vLLM
// 无鉴权可用；401 也更快暴露）——不带 Authorization 头。
func TestListModelsNoKeyStillTries(t *testing.T) {
	var hit bool
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		gotAuth = r.Header.Get("Authorization")
		io.WriteString(w, `{"data":[{"id":"llama3"}]}`)
	}))
	defer srv.Close()
	c := &Client{Provider: ProviderOpenAICompat, BaseURL: srv.URL, HTTP: srv.Client()}
	ids, err := c.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if !hit || len(ids) != 1 || ids[0] != "llama3" {
		t.Fatalf("hit=%v ids=%v", hit, ids)
	}
	if gotAuth != "" {
		t.Fatalf("无 key 不应带 Authorization，实际 %q", gotAuth)
	}
}

// TestListModelsAnthropic：anthropic 目录——GET {base}/v1/models，
// x-api-key + anthropic-version 头。
func TestListModelsAnthropic(t *testing.T) {
	var gotPath, gotKey, gotVersion string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		io.WriteString(w, `{"data":[{"type":"model","id":"claude-sonnet-4-5","display_name":"Claude Sonnet 4.5"},{"type":"model","id":"claude-haiku-4-5"}],"has_more":false}`)
	}))
	defer srv.Close()
	c := &Client{Provider: ProviderAnthropic, BaseURL: srv.URL, APIKey: "sk-ant", HTTP: srv.Client()}
	ids, err := c.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if gotPath != "/v1/models" {
		t.Fatalf("路径 = %q，应为 /v1/models", gotPath)
	}
	if gotKey != "sk-ant" || gotVersion != "2023-06-01" {
		t.Fatalf("headers: key=%q version=%q", gotKey, gotVersion)
	}
	if len(ids) != 2 || ids[0] != "claude-sonnet-4-5" || ids[1] != "claude-haiku-4-5" {
		t.Fatalf("ids = %v", ids)
	}
}

// TestListModelsEcho：echo 目录是固定单例，不发网络请求。
func TestListModelsEcho(t *testing.T) {
	c := &Client{Provider: ProviderEcho} // HTTP 故意留 nil：发请求会 panic
	ids, err := c.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(ids) != 1 || ids[0] != "echo" {
		t.Fatalf("ids = %v", ids)
	}
}

// TestListModelsErrors：HTTP 错误带状态码；200 坏 JSON 报解析错误。
func TestListModelsErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":{"message":"bad key"}}`)
	}))
	defer srv.Close()
	c := &Client{Provider: ProviderOpenAICompat, BaseURL: srv.URL, APIKey: "k", HTTP: srv.Client()}
	_, err := c.ListModels(context.Background())
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("401 应报错含状态码: %v", err)
	}

	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `not json`)
	}))
	defer srv2.Close()
	c2 := &Client{Provider: ProviderOpenAICompat, BaseURL: srv2.URL, APIKey: "k", HTTP: srv2.Client()}
	if _, err := c2.ListModels(context.Background()); err == nil || !strings.Contains(err.Error(), "解析") {
		t.Fatalf("坏 JSON 应报解析错误: %v", err)
	}
}

// TestListModelsEmptyCatalog：空目录是合法状态（200 + data:[]），
// 不是错误——UI 显示"目录为空"即可。
func TestListModelsEmptyCatalog(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"object":"list","data":[]}`)
	}))
	defer srv.Close()
	c := &Client{Provider: ProviderOpenAICompat, BaseURL: srv.URL, HTTP: srv.Client()}
	ids, err := c.ListModels(context.Background())
	if err != nil {
		t.Fatalf("空目录不应报错: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("ids = %v", ids)
	}
}
