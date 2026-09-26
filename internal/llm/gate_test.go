package llm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// TestGateBlocksRequest：Gate 拒绝时请求根本不发出，错误原样返回
// 给调用方——熔断与每日预算用它阻止新请求。
func TestGateBlocksRequest(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
	}))
	defer srv.Close()
	gateErr := errors.New("准入被拒")
	c := &Client{Provider: ProviderOpenAICompat, BaseURL: srv.URL, APIKey: "k", Model: "m",
		HTTP: srv.Client(), MaxRetries: 3, MaxTokens: 100,
		Gate: func(context.Context) error { return gateErr }}

	_, err := c.Complete(context.Background(), "", []Message{{Role: "user", Content: "hi"}})
	if !errors.Is(err, gateErr) {
		t.Fatalf("err = %v，应原样返回 Gate 错误", err)
	}
	if got := hits.Load(); got != 0 {
		t.Fatalf("Gate 拒绝后不应发出请求: hits=%d", got)
	}
}

// TestGateBlocksStream：流式路径同样受 Gate 约束。
func TestGateBlocksStream(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
	}))
	defer srv.Close()
	gateErr := errors.New("准入被拒")
	c := &Client{Provider: ProviderOpenAICompat, BaseURL: srv.URL, APIKey: "k", Model: "m",
		HTTP: srv.Client(), MaxRetries: 3, MaxTokens: 100,
		Gate: func(context.Context) error { return gateErr }}

	_, err := c.CompleteStream(context.Background(), Request{System: "s", Messages: []Message{{Role: "user", Content: "hi"}}}, nil)
	if !errors.Is(err, gateErr) {
		t.Fatalf("err = %v，应原样返回 Gate 错误", err)
	}
	if got := hits.Load(); got != 0 {
		t.Fatalf("Gate 拒绝后不应发出请求: hits=%d", got)
	}
}

// TestGateCheckedPerAttempt：Gate 在每次尝试（含重试）前被检查——
// 重试不能绕开熔断。
func TestGateCheckedPerAttempt(t *testing.T) {
	var checks atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := &Client{Provider: ProviderOpenAICompat, BaseURL: srv.URL, APIKey: "k", Model: "m",
		HTTP: srv.Client(), MaxRetries: 1, MaxTokens: 100,
		Gate: func(context.Context) error { checks.Add(1); return nil }}

	_, _ = c.Complete(context.Background(), "", []Message{{Role: "user", Content: "hi"}})
	if got := checks.Load(); got != 2 {
		t.Fatalf("Gate 检查次数 = %d，应为 2（首试 + 1 次重试）", got)
	}
}
