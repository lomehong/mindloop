package llm

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// TestCallQuotaCountsRetries：重试计入调用预算——服务器始终 500、
// 配额小于 MaxRetries+1 时，实际请求次数恰好等于配额，返回
// ErrBudgetExceeded 而不是"重试 N 次后仍失败"。
func TestCallQuotaCountsRetries(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := &Client{Provider: ProviderOpenAICompat, BaseURL: srv.URL, APIKey: "k", Model: "m",
		HTTP: srv.Client(), MaxRetries: 3, MaxTokens: 100}

	ctx := WithCallQuota(context.Background(), NewCallQuota(2))
	_, err := c.Complete(ctx, "", []Message{{Role: "user", Content: "hi"}})
	if !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("err = %v，应为 ErrBudgetExceeded", err)
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("请求次数 = %d，应恰好为配额 2（重试计入预算）", got)
	}
}

// TestCallQuotaWithinBudgetSucceeds：预算内的调用不受影响。
func TestCallQuotaWithinBudgetSucceeds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"回答"}}]}`)
	}))
	defer srv.Close()
	c := &Client{Provider: ProviderOpenAICompat, BaseURL: srv.URL, APIKey: "k", Model: "m",
		HTTP: srv.Client(), MaxRetries: 0, MaxTokens: 100}

	ctx := WithCallQuota(context.Background(), NewCallQuota(1))
	out, err := c.Complete(ctx, "", []Message{{Role: "user", Content: "hi"}})
	if err != nil || out != "回答" {
		t.Fatalf("预算内应正常完成: out=%q err=%v", out, err)
	}
}

// TestCallQuotaZeroBlocksStream：配额耗尽时流式路径在发出任何请求
// 之前就被拒绝——预算阻止的是新请求。
func TestCallQuotaZeroBlocksStream(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
	}))
	defer srv.Close()
	c := &Client{Provider: ProviderOpenAICompat, BaseURL: srv.URL, APIKey: "k", Model: "m",
		HTTP: srv.Client(), MaxRetries: 3, MaxTokens: 100}

	ctx := WithCallQuota(context.Background(), NewCallQuota(0))
	_, err := c.CompleteStream(ctx, Request{System: "s", Messages: []Message{{Role: "user", Content: "hi"}}}, nil)
	if !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("err = %v，应为 ErrBudgetExceeded", err)
	}
	if got := hits.Load(); got != 0 {
		t.Fatalf("预算耗尽后不应发出请求: hits=%d", got)
	}
}
