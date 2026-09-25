package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// streamTestClient 构造指向 httptest 服务器的客户端：退避压到
// 毫秒级，重试 2 次，让重试语义的用例秒级完成。
func streamTestClient(t *testing.T, ts *httptest.Server, provider string) *Client {
	t.Helper()
	return &Client{
		Provider:   provider,
		BaseURL:    ts.URL,
		APIKey:     "test-key",
		Model:      "test-model",
		MaxTokens:  1024,
		HTTP:       &http.Client{},
		MaxRetries: 2,
		Backoff:    time.Millisecond,
	}
}

// sseWrite 写一行 SSE 事件并立即 flush——逐 write 控制分片，
// 用来构造"一条 JSON 行被切成多个 TCP 段"的场景。
func sseWrite(t *testing.T, w http.ResponseWriter, parts ...string) {
	t.Helper()
	flusher, ok := w.(http.Flusher)
	if !ok {
		t.Fatal("ResponseWriter 不支持 Flush")
	}
	for _, p := range parts {
		if _, err := io.WriteString(w, p); err != nil {
			t.Errorf("写 SSE: %v", err)
		}
		flusher.Flush()
	}
}

func TestCompleteStreamOpenAI(t *testing.T) {
	var hits int32
	bodyCh := make(chan string, 4)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		data, _ := io.ReadAll(r.Body)
		select {
		case bodyCh <- string(data):
		default:
		}
		w.Header().Set("Content-Type", "text/event-stream")
		sseWrite(t, w, `data: {"model":"glm-5","choices":[{"delta":{"content":"你"}}]}`+"\n\n")
		sseWrite(t, w, `data: {"choices":[{"delta":{"content":"好，世界"}}]}`+"\n\n")
		sseWrite(t, w, `data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":7}}`+"\n\n")
		sseWrite(t, w, "data: [DONE]\n\n")
	}))
	defer ts.Close()

	var deltas []string
	res, err := streamTestClient(t, ts, ProviderOpenAICompat).
		CompleteStream(context.Background(), Request{System: "sys", Messages: []Message{{Role: "user", Content: "hi"}}},
			func(d string) { deltas = append(deltas, d) })
	if err != nil {
		t.Fatal(err)
	}
	if len(deltas) != 2 || deltas[0] != "你" || deltas[1] != "好，世界" {
		t.Fatalf("增量序列不符: %q", deltas)
	}
	if res.Text != "你好，世界" {
		t.Fatalf("完整文本应为增量拼接: %q", res.Text)
	}
	if res.Usage.PromptTokens != 11 || res.Usage.CompletionTokens != 7 {
		t.Fatalf("末 chunk usage 未采集: %+v", res.Usage)
	}
	if res.FinishReason != "stop" || res.Model != "glm-5" {
		t.Fatalf("finish/model 不符: %s %s", res.FinishReason, res.Model)
	}
	if n := atomic.LoadInt32(&hits); n != 1 {
		t.Fatalf("成功路径不应重试，hits=%d", n)
	}
	close(bodyCh)
	body := <-bodyCh
	var sent struct {
		Stream    bool   `json:"stream"`
		MaxTokens int    `json:"max_tokens"`
		Model     string `json:"model"`
	}
	if err := json.Unmarshal([]byte(body), &sent); err != nil {
		t.Fatalf("请求体解析: %v", err)
	}
	if !sent.Stream || sent.Model != "test-model" || sent.MaxTokens != 1024 {
		t.Fatalf("请求体缺 stream/max_tokens/model: %s", body)
	}
}

// UTF-8 多字节跨 chunk 切割：一条 data 行被切成多段 TCP 写，
// "你好" 的 6 个字节跨在两段里——scanner 重组后必须是完整字符，
// 不允许出现 U+FFFD。
func TestCompleteStreamOpenAIUTF8Split(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// 一条 data 行分三段写：JSON 字符串值在中段断开，多字节
		// 字符与前后的行片段各拼一半——scanner 必须无损重组。
		head := "data: {\"choices\":[{\"delta\":{\"content\":\""
		sseWrite(t, w, head)
		sseWrite(t, w, "你好")
		sseWrite(t, w, "世界\"}}]}\n\n")
		sseWrite(t, w, "data: [DONE]\n\n")
	}))
	defer ts.Close()

	var got strings.Builder
	res, err := streamTestClient(t, ts, ProviderOpenAICompat).
		CompleteStream(context.Background(), Request{}, func(d string) { got.WriteString(d) })
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "你好世界" || got.String() != "你好世界" {
		t.Fatalf("多字节切割后文本损坏: res=%q deltas=%q", res.Text, got.String())
	}
	if strings.ContainsRune(res.Text, '\uFFFD') {
		t.Fatalf("出现替换符，多字节被切断: %q", res.Text)
	}
}

// 首 delta 前的瞬时错误按现行纪律重试：第一次 500，第二次成功。
func TestCompleteStreamRetryBeforeFirstDelta(t *testing.T) {
	var hits int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			http.Error(w, "overloaded", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		sseWrite(t, w, `data: {"choices":[{"delta":{"content":"ok"}}]}`+"\n\n")
		sseWrite(t, w, "data: [DONE]\n\n")
	}))
	defer ts.Close()

	res, err := streamTestClient(t, ts, ProviderOpenAICompat).
		CompleteStream(context.Background(), Request{}, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "ok" {
		t.Fatalf("重试后文本不符: %q", res.Text)
	}
	if n := atomic.LoadInt32(&hits); n != 2 {
		t.Fatalf("应恰好重试一次，hits=%d", n)
	}
}

// 首 delta 前重试耗尽：错误如实上报。
func TestCompleteStreamRetryExhaustedBeforeFirstDelta(t *testing.T) {
	var hits int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	defer ts.Close()

	_, err := streamTestClient(t, ts, ProviderOpenAICompat).
		CompleteStream(context.Background(), Request{}, func(string) {})
	if err == nil || !strings.Contains(err.Error(), "重试 2 次后仍失败") {
		t.Fatalf("应报告重试耗尽: %v", err)
	}
	if n := atomic.LoadInt32(&hits); n != 3 {
		t.Fatalf("MaxRetries=2 应共 3 次尝试，hits=%d", n)
	}
}

// [DONE] 缺失：已有增量发出后流被正常关闭——内容可能被截断，
// 必须报错且错误注明中断点在首增量之后（不可重试）。
func TestCompleteStreamDoneMissing(t *testing.T) {
	var hits int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		sseWrite(t, w, `data: {"choices":[{"delta":{"content":"半截"}}]}`+"\n\n")
		// 不发 [DONE]，直接返回。
	}))
	defer ts.Close()

	var sawDelta bool
	_, err := streamTestClient(t, ts, ProviderOpenAICompat).
		CompleteStream(context.Background(), Request{}, func(string) { sawDelta = true })
	if err == nil || !sawDelta {
		t.Fatalf("[DONE] 缺失必须报错: err=%v sawDelta=%v", err, sawDelta)
	}
	if !strings.Contains(err.Error(), "首增量之后") {
		t.Fatalf("错误应注明中断于首增量之后: %v", err)
	}
	if n := atomic.LoadInt32(&hits); n != 1 {
		t.Fatalf("首增量之后的错误不可重试，hits=%d", n)
	}
}

// 断流：增量发出后连接被服务器硬切（非优雅关闭）——同样立即失败。
func TestCompleteStreamConnectionDrop(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		sseWrite(t, w, `data: {"choices":[{"delta":{"content":"前半"}}]}`+"\n\n")
		panic(http.ErrAbortHandler) // 硬切连接
	}))
	defer ts.Close()

	_, err := streamTestClient(t, ts, ProviderOpenAICompat).
		CompleteStream(context.Background(), Request{}, func(string) {})
	if err == nil || !strings.Contains(err.Error(), "首增量之后") {
		t.Fatalf("断流应报首增量之后错误: %v", err)
	}
}

// 首 delta 之前的断流可重试：第一次连接在任何增量前被硬切，
// 第二次成功。
func TestCompleteStreamRetryOnPreDeltaDrop(t *testing.T) {
	var hits int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if atomic.AddInt32(&hits, 1) == 1 {
			w.(http.Flusher).Flush()
			panic(http.ErrAbortHandler) // 增量前硬切
		}
		sseWrite(t, w, `data: {"choices":[{"delta":{"content":"第二笑了"}}]}`+"\n\n")
		sseWrite(t, w, "data: [DONE]\n\n")
	}))
	defer ts.Close()

	res, err := streamTestClient(t, ts, ProviderOpenAICompat).
		CompleteStream(context.Background(), Request{}, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "第二笑了" {
		t.Fatalf("重试后文本不符: %q", res.Text)
	}
	if n := atomic.LoadInt32(&hits); n != 2 {
		t.Fatalf("首增量前的断流应恰好重试一次，hits=%d", n)
	}
}

// GLM「错误装进 HTTP 200」在流式路径同样拆包：上游错误对象不重试。
func TestCompleteStreamOpenAIUpstreamErrorIn200(t *testing.T) {
	var hits int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		sseWrite(t, w, `data: {"error":{"message":"额度不足"}}`+"\n\n")
		sseWrite(t, w, "data: [DONE]\n\n")
	}))
	defer ts.Close()

	_, err := streamTestClient(t, ts, ProviderOpenAICompat).
		CompleteStream(context.Background(), Request{}, func(string) {})
	if err == nil || !strings.Contains(err.Error(), "上游错误: 额度不足") {
		t.Fatalf("200 拆包应识别上游错误: %v", err)
	}
	if n := atomic.LoadInt32(&hits); n != 1 {
		t.Fatalf("上游错误不可重试，hits=%d", n)
	}
}

// GLM 流式「只有思考没有答案」诊断：reasoning 增量 + finish=length。
func TestCompleteStreamOpenAIReasoningNoContent(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		sseWrite(t, w, `data: {"choices":[{"delta":{"reasoning_content":"让我想想…"}}]}`+"\n\n")
		sseWrite(t, w, `data: {"choices":[{"delta":{},"finish_reason":"length"}]}`+"\n\n")
		sseWrite(t, w, "data: [DONE]\n\n")
	}))
	defer ts.Close()

	_, err := streamTestClient(t, ts, ProviderOpenAICompat).
		CompleteStream(context.Background(), Request{}, func(string) {})
	if err == nil || !strings.Contains(err.Error(), "思考耗尽了输出预算") {
		t.Fatalf("思考耗尽诊断应在流式路径同样生效: %v", err)
	}
}

func TestCompleteStreamAnthropic(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		sseWrite(t, w, "event: message_start\n",
			`data: {"type":"message_start","message":{"usage":{"input_tokens":12}}}`+"\n\n")
		sseWrite(t, w, "event: content_block_delta\n",
			`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"早"}}`+"\n\n")
		sseWrite(t, w, `data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"安"}}`+"\n\n")
		sseWrite(t, w, "event: message_delta\n",
			`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`+"\n\n")
		sseWrite(t, w, "event: message_stop\n", `data: {"type":"message_stop"}`+"\n\n")
	}))
	defer ts.Close()

	var deltas []string
	res, err := streamTestClient(t, ts, ProviderAnthropic).
		CompleteStream(context.Background(), Request{}, func(d string) { deltas = append(deltas, d) })
	if err != nil {
		t.Fatal(err)
	}
	if len(deltas) != 2 || res.Text != "早安" {
		t.Fatalf("anthropic 增量/拼接不符: %q %q", deltas, res.Text)
	}
	if res.Usage.PromptTokens != 12 || res.Usage.CompletionTokens != 5 {
		t.Fatalf("usage 采集不符: %+v", res.Usage)
	}
	if res.FinishReason != "end_turn" {
		t.Fatalf("stop_reason 不符: %s", res.FinishReason)
	}
}

func TestCompleteStreamAnthropicErrorEvent(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		sseWrite(t, w, `data: {"type":"error","error":{"message":"overloaded_error"}}`+"\n\n")
	}))
	defer ts.Close()

	_, err := streamTestClient(t, ts, ProviderAnthropic).
		CompleteStream(context.Background(), Request{}, func(string) {})
	if err == nil || !strings.Contains(err.Error(), "上游错误: overloaded_error") {
		t.Fatalf("error 事件应拆出上游错误: %v", err)
	}
}

// echo 流式端到端：无网络，分段回调，拼接等于完整产出；OnDone
// 照常收尾（观测面口径与 Complete 一致）。
func TestCompleteStreamEcho(t *testing.T) {
	var onDoneCalls int32
	var onDoneUsage Usage
	c := &Client{Provider: ProviderEcho, Model: "echo", OnDone: func(u Usage, err error) {
		atomic.AddInt32(&onDoneCalls, 1)
		onDoneUsage = u
	}}
	var deltas []string
	res, err := c.CompleteStream(context.Background(), Request{}, func(d string) { deltas = append(deltas, d) })
	if err != nil {
		t.Fatal(err)
	}
	if len(deltas) < 2 {
		t.Fatalf("echo 应合成多段增量，得到 %d 段", len(deltas))
	}
	if res.Text != echoResponse || strings.Join(deltas, "") != echoResponse {
		t.Fatalf("拼接应等于完整产出: res=%q", res.Text)
	}
	if n := atomic.LoadInt32(&onDoneCalls); n != 1 {
		t.Fatalf("OnDone 应恰被调用一次，得到 %d", n)
	}
	_ = onDoneUsage
}
