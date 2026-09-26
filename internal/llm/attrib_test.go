// 归因与记账测试：ctx 元数据传播（WithAttrib/AttribFrom）、
// usage_known 语义（未知不伪装零成本）、重试计数、时延，以及共享
// client 并发调用不串线——共享 lastUsage 是真实事故，这里逐字段钉死。
package llm

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestWithAttribMergesNonEmptyFields(t *testing.T) {
	ctx := WithAttrib(context.Background(), Attrib{
		Thinker: "monolith", Phase: "wake", Wake: "scheduled spontaneity",
	})
	ctx = WithAttrib(ctx, Attrib{Task: "task-1", Run: "run-1", Attempt: 2})

	got := AttribFrom(ctx)
	if got.Task != "task-1" || got.Run != "run-1" || got.Attempt != 2 {
		t.Fatalf("内层字段未合并: %+v", got)
	}
	if got.Thinker != "monolith" || got.Phase != "wake" || got.Wake != "scheduled spontaneity" {
		t.Fatalf("外层字段被空值误覆盖: %+v", got)
	}

	// 内层同名非空字段覆盖外层（recap 在唤醒链路里挂自己的阶段）。
	ctx = WithAttrib(ctx, Attrib{Thinker: "recap", Phase: "recap"})
	got = AttribFrom(ctx)
	if got.Thinker != "recap" || got.Phase != "recap" {
		t.Fatalf("内层应覆盖同名归因: %+v", got)
	}
	if got.Task != "task-1" || got.Wake != "scheduled spontaneity" {
		t.Fatalf("覆盖不应擦掉其他字段: %+v", got)
	}

	if got := AttribFrom(context.Background()); got != (Attrib{}) {
		t.Fatalf("无归因的 ctx 应为零值: %+v", got)
	}
}

func TestCompleteRecordsAttribution(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(20 * time.Millisecond)
		io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":7,"completion_tokens":3}}`)
	}))
	defer srv.Close()

	var got Usage
	c := &Client{Provider: ProviderOpenAICompat, BaseURL: srv.URL, APIKey: "k", Model: "m",
		HTTP: srv.Client(), MaxRetries: 0,
		OnDone: func(u Usage, err error) { got = u }}
	ctx := WithAttrib(context.Background(), Attrib{
		Task: "task-1", Run: "run-1", Attempt: 2,
		Thinker: "monolith", Wake: "scheduled spontaneity", Phase: "task",
	})
	if _, err := c.Complete(ctx, "sys", nil); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got.Task != "task-1" || got.Run != "run-1" || got.Attempt != 2 ||
		got.Thinker != "monolith" || got.Wake != "scheduled spontaneity" || got.Phase != "task" {
		t.Fatalf("归因不完整: %+v", got)
	}
	if !got.Known || got.PromptTokens != 7 || got.CompletionTokens != 3 {
		t.Fatalf("用量: %+v", got)
	}
	if got.Retries != 0 {
		t.Fatalf("首次成功不应计重试，Retries = %d", got.Retries)
	}
	if got.LatencyMS < 10 {
		t.Fatalf("时延未记录（服务端延迟 20ms）: %d", got.LatencyMS)
	}
}

func TestCompleteUsageUnknown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer srv.Close()

	var got Usage
	c := &Client{Provider: ProviderOpenAICompat, BaseURL: srv.URL, APIKey: "k", Model: "m",
		HTTP: srv.Client(), MaxRetries: 0,
		OnDone: func(u Usage, err error) { got = u }}
	if _, err := c.Complete(context.Background(), "", nil); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got.Known {
		t.Fatalf("响应无 usage 对象应为未知: %+v", got)
	}
	if got.PromptTokens != 0 || got.CompletionTokens != 0 {
		t.Fatalf("未知用量不应伪造 token 数: %+v", got)
	}
}

func TestParseUsageKnowsBothDialects(t *testing.T) {
	u := parseUsage([]byte(`{"usage":{"input_tokens":11,"output_tokens":22}}`))
	if !u.Known || u.PromptTokens != 11 || u.CompletionTokens != 22 {
		t.Fatalf("anthropic 方言: %+v", u)
	}
	u = parseUsage([]byte(`{"usage":{"prompt_tokens":3,"completion_tokens":4}}`))
	if !u.Known || u.PromptTokens != 3 || u.CompletionTokens != 4 {
		t.Fatalf("openai 方言: %+v", u)
	}
	if u := parseUsage([]byte(`{"choices":[]}`)); u.Known {
		t.Fatalf("无 usage 对象应为未知: %+v", u)
	}
}

func TestCompleteRetriesCounted(t *testing.T) {
	for _, tc := range []struct {
		name      string
		failures  int
		maxRetry  int
		wantRetry int
		wantErr   bool
	}{
		{name: "retry-once-then-success", failures: 1, maxRetry: 2, wantRetry: 1},
		{name: "exhausted", failures: 9, maxRetry: 2, wantRetry: 2, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls <= tc.failures {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
			}))
			defer srv.Close()

			var got Usage
			c := &Client{Provider: ProviderOpenAICompat, BaseURL: srv.URL, APIKey: "k", Model: "m",
				HTTP: srv.Client(), MaxRetries: tc.maxRetry, Backoff: time.Millisecond,
				OnDone: func(u Usage, err error) { got = u }}
			ctx := WithAttrib(context.Background(), Attrib{Task: "t-retry", Phase: "task"})
			_, err := c.Complete(ctx, "", nil)
			if tc.wantErr != (err != nil) {
				t.Fatalf("err = %v（wantErr=%v）", err, tc.wantErr)
			}
			if got.Retries != tc.wantRetry {
				t.Fatalf("Retries = %d，应为 %d", got.Retries, tc.wantRetry)
			}
			if got.Task != "t-retry" || got.Phase != "task" {
				t.Fatalf("失败调用也要带归因: %+v", got)
			}
		})
	}
}

func TestCompleteStreamRecordsAttribution(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"你好\"}}]}\n\n")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":2}}\n\n")
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	var got Usage
	c := &Client{Provider: ProviderOpenAICompat, BaseURL: srv.URL, APIKey: "k", Model: "m",
		HTTP: srv.Client(), MaxRetries: 0,
		OnDone: func(u Usage, err error) { got = u }}
	ctx := WithAttrib(context.Background(), Attrib{Task: "t2", Thinker: "responder", Phase: "chat"})
	res, err := c.CompleteStream(ctx, Request{Messages: []Message{{Role: "user", Content: "hi"}}}, nil)
	if err != nil {
		t.Fatalf("CompleteStream: %v", err)
	}
	if res.Text != "你好" {
		t.Fatalf("text = %q", res.Text)
	}
	if got.Task != "t2" || got.Thinker != "responder" || got.Phase != "chat" {
		t.Fatalf("流式归因: %+v", got)
	}
	if !got.Known || got.PromptTokens != 5 || got.CompletionTokens != 2 {
		t.Fatalf("流式用量: %+v", got)
	}
}

func TestCompleteStreamUnknownUsage(t *testing.T) {
	var got Usage
	c := &Client{Provider: ProviderEcho, Model: "echo",
		OnDone: func(u Usage, err error) { got = u }}
	if _, err := c.CompleteStream(context.Background(), Request{}, nil); err != nil {
		t.Fatalf("CompleteStream: %v", err)
	}
	if got.Known {
		t.Fatalf("echo 无供应商 usage，应保持未知: %+v", got)
	}
}

func TestCompleteStreamRetriesCounted(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n")
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	var got Usage
	c := &Client{Provider: ProviderOpenAICompat, BaseURL: srv.URL, APIKey: "k", Model: "m",
		HTTP: srv.Client(), MaxRetries: 2, Backoff: time.Millisecond,
		OnDone: func(u Usage, err error) { got = u }}
	ctx := WithAttrib(context.Background(), Attrib{Task: "t3", Phase: "wake"})
	if _, err := c.CompleteStream(ctx, Request{}, nil); err != nil {
		t.Fatalf("CompleteStream: %v", err)
	}
	if got.Retries != 1 {
		t.Fatalf("重试 1 次后成功，Retries = %d", got.Retries)
	}
	if got.Task != "t3" || got.Phase != "wake" {
		t.Fatalf("归因: %+v", got)
	}
}

func TestConcurrentCallsAttributionNoCrosstalk(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), `"stream":true`) {
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")
			io.WriteString(w, "data: [DONE]\n\n")
			return
		}
		io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
	}))
	defer srv.Close()

	const n = 8
	type entry struct{ task, phase string }
	var mu sync.Mutex
	seen := make([]entry, 0, n)
	c := &Client{Provider: ProviderOpenAICompat, BaseURL: srv.URL, APIKey: "k", Model: "m",
		HTTP: srv.Client(), MaxRetries: 0,
		OnDone: func(u Usage, err error) {
			mu.Lock()
			seen = append(seen, entry{u.Task, u.Phase})
			mu.Unlock()
		}}
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx := WithAttrib(context.Background(), Attrib{
				Task:  fmt.Sprintf("task-%d", i),
				Phase: fmt.Sprintf("phase-%d", i),
			})
			var err error
			if i%2 == 0 {
				_, err = c.Complete(ctx, "", nil)
			} else {
				_, err = c.CompleteStream(ctx, Request{}, nil)
			}
			if err != nil {
				t.Errorf("调用 %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	if len(seen) != n {
		t.Fatalf("OnDone 次数 = %d，应为 %d", len(seen), n)
	}
	for _, e := range seen {
		idx := strings.TrimPrefix(e.task, "task-")
		if e.phase != "phase-"+idx {
			t.Fatalf("归因串线: %s 配到 %s", e.task, e.phase)
		}
	}
}
