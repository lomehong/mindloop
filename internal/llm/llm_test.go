package llm

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCompleteOpenAI(t *testing.T) {
	var gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"回答内容"}}]}`)
	}))
	defer srv.Close()

	c := &Client{Provider: ProviderOpenAICompat, BaseURL: srv.URL, APIKey: "sk-test", Model: "glm-5", HTTP: srv.Client(), MaxRetries: 0}
	out, err := c.Complete(context.Background(), "系统提示", []Message{{Role: "user", Content: "你好"}})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if out != "回答内容" {
		t.Fatalf("content = %q", out)
	}
	if gotAuth != "Bearer sk-test" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if !strings.Contains(gotBody, `"role":"system"`) || !strings.Contains(gotBody, "系统提示") {
		t.Fatalf("system 消息缺失: %s", gotBody)
	}
}

func TestCompleteOpenAIErrorIn200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"error":{"message":"上游超载"}}`)
	}))
	defer srv.Close()
	c := &Client{Provider: ProviderOpenAICompat, BaseURL: srv.URL, APIKey: "k", Model: "m", HTTP: srv.Client(), MaxRetries: 0}
	if _, err := c.Complete(context.Background(), "", nil); err == nil || !strings.Contains(err.Error(), "上游超载") {
		t.Fatalf("200 内嵌错误应被拆包发现，得到: %v", err)
	}
}

func TestCompleteAnthropic(t *testing.T) {
	var gotKey, gotVersion string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		if !strings.HasSuffix(r.URL.Path, "/v1/messages") {
			t.Errorf("路径 = %q", r.URL.Path)
		}
		io.WriteString(w, `{"content":[{"type":"thinking","text":"思路"},{"type":"text","text":"最终回答"}]}`)
	}))
	defer srv.Close()

	c := &Client{Provider: ProviderAnthropic, BaseURL: srv.URL, APIKey: "sk-ant", Model: "claude-x", HTTP: srv.Client(), MaxRetries: 0}
	out, err := c.Complete(context.Background(), "系统", []Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if out != "最终回答" {
		t.Fatalf("content = %q（thinking 块不应混入）", out)
	}
	if gotKey != "sk-ant" || gotVersion != "2023-06-01" {
		t.Fatalf("headers: key=%q version=%q", gotKey, gotVersion)
	}
}

func TestRetryOn500ThenSuccess(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			io.WriteString(w, `{"error":"boom"}`)
			return
		}
		io.WriteString(w, `{"choices":[{"message":{"content":"第三次成功"}}]}`)
	}))
	defer srv.Close()

	c := &Client{Provider: ProviderOpenAICompat, BaseURL: srv.URL, APIKey: "k", Model: "m", HTTP: srv.Client(), MaxRetries: 3, Backoff: 0}
	out, err := c.Complete(context.Background(), "", []Message{{Role: "user", Content: "x"}})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if out != "第三次成功" || calls.Load() != 3 {
		t.Fatalf("out=%q calls=%d", out, calls.Load())
	}
}

func TestNoRetryOnDeterministic4xx(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `bad key`)
	}))
	defer srv.Close()
	c := &Client{Provider: ProviderOpenAICompat, BaseURL: srv.URL, APIKey: "k", Model: "m", HTTP: srv.Client(), MaxRetries: 3, Backoff: 0}
	if _, err := c.Complete(context.Background(), "", nil); err == nil {
		t.Fatal("401 应报错")
	}
	if calls.Load() != 1 {
		t.Fatalf("确定性 4xx 不应重试，实际调用 %d 次", calls.Load())
	}
}

func TestFromEnvProviderDetection(t *testing.T) {
	t.Setenv("MINDLOOP_PROVIDER", "")
	t.Setenv("MINDLOOP_MODEL", "claude-sonnet-5")
	t.Setenv("MINDLOOP_API_KEY", "k")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	c, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if c.Provider != ProviderAnthropic {
		t.Fatalf("provider = %q，应为 anthropic（claude 前缀推断）", c.Provider)
	}
	if c.BaseURL != "https://api.anthropic.com" {
		t.Fatalf("默认 BaseURL = %q", c.BaseURL)
	}

	t.Setenv("MINDLOOP_MODEL", "glm-5")
	c, err = FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if c.Provider != ProviderOpenAICompat {
		t.Fatalf("provider = %q，应为 openai-compatible", c.Provider)
	}
}

func TestFromEnvModelEcho(t *testing.T) {
	t.Setenv("MINDLOOP_PROVIDER", "")
	t.Setenv("MINDLOOP_MODEL", "echo")
	t.Setenv("MINDLOOP_API_KEY", "")
	c, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if c.Provider != ProviderEcho {
		t.Fatalf("MINDLOOP_MODEL=echo 应推断为 echo 供应商，得到 %q", c.Provider)
	}
}
