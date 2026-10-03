package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewImageRoundTrip(t *testing.T) {
	raw := []byte{0x89, 'P', 'N', 'G', 0x00, 0x01}
	img := NewImage("image/png", raw)
	if img.Data != base64.StdEncoding.EncodeToString(raw) {
		t.Fatalf("Data 编码不符: %q", img.Data)
	}
	dec, err := base64.StdEncoding.DecodeString(img.Data)
	if err != nil || string(dec) != string(raw) {
		t.Fatalf("round trip: %v %q", err, dec)
	}
}

func TestImageValidation(t *testing.T) {
	if err := (Image{MIMEType: "text/html", Data: "aGk="}).validate(); err == nil || !strings.Contains(err.Error(), "不支持的图片类型") {
		t.Fatalf("未知 MIME 应被拒: %v", err)
	}
	if err := (Image{MIMEType: "image/png", Data: "不是 base64!!"}).validate(); err == nil || !strings.Contains(err.Error(), "base64") {
		t.Fatalf("非法 base64 应被拒: %v", err)
	}
	if err := (Image{MIMEType: "image/png", Data: ""}).validate(); err == nil {
		t.Fatal("空图应被拒")
	}
	big := make([]byte, MaxImageBytes+1)
	if err := (Image{MIMEType: "image/png", Data: base64.StdEncoding.EncodeToString(big)}).validate(); err == nil || !strings.Contains(err.Error(), "上限") {
		t.Fatalf("超限应被拒: %v", err)
	}
	if err := (Image{MIMEType: "image/jpeg", Data: "aGk="}).validate(); err != nil {
		t.Fatalf("合法小图不应被拒: %v", err)
	}
}

func TestWireMessageTextOnlyUnchanged(t *testing.T) {
	// 无图消息的 wire 形状必须与历史行为一致：content 是纯字符串。
	wm, err := wireMessage(ProviderOpenAICompat, Message{Role: "user", Content: "你好"})
	if err != nil {
		t.Fatalf("wireMessage: %v", err)
	}
	if wm["content"] != "你好" {
		t.Fatalf("content 应保持字符串，得到 %T: %v", wm["content"], wm["content"])
	}
}

func TestWireMessageOpenAI(t *testing.T) {
	img := NewImage("image/png", []byte("pngbytes"))
	wm, err := wireMessage(ProviderOpenAICompat, Message{Role: "user", Content: "看屏幕", Images: []Image{img}})
	if err != nil {
		t.Fatalf("wireMessage: %v", err)
	}
	parts, ok := wm["content"].([]map[string]any)
	if !ok || len(parts) != 2 {
		t.Fatalf("content 应是两段内容块: %v", wm["content"])
	}
	if parts[0]["type"] != "text" || parts[0]["text"] != "看屏幕" {
		t.Fatalf("文本块不符: %v", parts[0])
	}
	if parts[1]["type"] != "image_url" {
		t.Fatalf("图片块类型不符: %v", parts[1])
	}
	u := parts[1]["image_url"].(map[string]any)["url"].(string)
	if !strings.HasPrefix(u, "data:image/png;base64,") || !strings.HasSuffix(u, base64.StdEncoding.EncodeToString([]byte("pngbytes"))) {
		t.Fatalf("data URL 不符: %q", u)
	}
}

func TestWireMessageAnthropic(t *testing.T) {
	img := NewImage("image/jpeg", []byte("jpgbytes"))
	wm, err := wireMessage(ProviderAnthropic, Message{Role: "user", Content: "看", Images: []Image{img}})
	if err != nil {
		t.Fatalf("wireMessage: %v", err)
	}
	parts := wm["content"].([]map[string]any)
	if len(parts) != 2 || parts[1]["type"] != "image" {
		t.Fatalf("anthropic 图片块不符: %v", parts)
	}
	src := parts[1]["source"].(map[string]any)
	if src["type"] != "base64" || src["media_type"] != "image/jpeg" || src["data"] != img.Data {
		t.Fatalf("source 块不符: %v", src)
	}
}

func TestWireMessageUnknownProvider(t *testing.T) {
	if _, err := wireMessage("nope", Message{Role: "user", Images: []Image{{MIMEType: "image/png", Data: "aGk="}}}); err == nil {
		t.Fatal("未知供应商带图应报错")
	}
}

func TestCompleteOpenAIWithImage(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"content":"看到了"}}]}`)
	}))
	defer srv.Close()

	c := &Client{Provider: ProviderOpenAICompat, BaseURL: srv.URL, APIKey: "k", Model: "glm-5", HTTP: srv.Client(), MaxRetries: 0}
	msg := Message{Role: "user", Content: "屏幕上有什么", Images: []Image{NewImage("image/png", []byte("screenpng"))}}
	out, err := c.Complete(context.Background(), "系统", []Message{msg})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if out != "看到了" {
		t.Fatalf("out = %q", out)
	}
	// 拆回请求体验证 wire 形状真的发出去了。
	var req struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal([]byte(gotBody), &req); err != nil {
		t.Fatalf("解析请求体: %v", err)
	}
	if !strings.Contains(gotBody, `"image_url"`) || !strings.Contains(gotBody, "data:image/png;base64,") {
		t.Fatalf("图片块缺失: %s", gotBody)
	}
	if !strings.Contains(string(req.Messages[0]), `"role":"system"`) {
		t.Fatalf("system 应仍是字符串 content: %s", req.Messages[0])
	}
}

func TestCompleteAnthropicWithImage(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		io.WriteString(w, `{"content":[{"type":"text","text":"看到光标"}]}`)
	}))
	defer srv.Close()

	c := &Client{Provider: ProviderAnthropic, BaseURL: srv.URL, APIKey: "k", Model: "claude-x", HTTP: srv.Client(), MaxRetries: 0}
	msg := Message{Role: "user", Content: "看屏幕", Images: []Image{NewImage("image/png", []byte("px"))}}
	if _, err := c.Complete(context.Background(), "", []Message{msg}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if !strings.Contains(gotBody, `"type":"image"`) || !strings.Contains(gotBody, `"media_type":"image/png"`) {
		t.Fatalf("anthropic source 块缺失: %s", gotBody)
	}
}

func TestCompleteBadImageNoRequest(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		io.WriteString(w, `{"choices":[{"message":{"content":"x"}}]}`)
	}))
	defer srv.Close()

	c := &Client{Provider: ProviderOpenAICompat, BaseURL: srv.URL, APIKey: "k", Model: "m", HTTP: srv.Client(), MaxRetries: 0}
	msg := Message{Role: "user", Content: "x", Images: []Image{{MIMEType: "image/png", Data: "@@@不是base64@@@"}}}
	if _, err := c.Complete(context.Background(), "", []Message{msg}); err == nil {
		t.Fatal("坏图应报错")
	}
	if calls != 0 {
		t.Fatalf("坏图的请求不应发出，实际发出 %d 次", calls)
	}
}

func TestEchoProviderWithImages(t *testing.T) {
	c := &Client{Provider: ProviderEcho, Model: "echo", MaxTokens: 100}
	msg := Message{Role: "user", Content: "看", Images: []Image{NewImage("image/png", []byte("x"))}}
	out, err := c.Complete(context.Background(), "", []Message{msg})
	if err != nil {
		t.Fatalf("echo 带图应照常回答: %v", err)
	}
	if out != echoResponse {
		t.Fatalf("out = %q", out)
	}
}

func TestCompleteStreamWithImage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		b, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(b, &body); err != nil {
			t.Errorf("解析请求体: %v", err)
		}
		msgs, _ := body["messages"].([]any)
		if len(msgs) == 0 {
			t.Errorf("没有消息")
		}
		if !strings.Contains(string(b), `"image_url"`) {
			t.Errorf("流式请求缺图片块: %s", b)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"看见\"}}]}\n\n")
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	c := &Client{Provider: ProviderOpenAICompat, BaseURL: srv.URL, APIKey: "k", Model: "m", HTTP: srv.Client(), MaxRetries: 0}
	msg := Message{Role: "user", Content: "看", Images: []Image{NewImage("image/png", []byte("z"))}}
	res, err := c.CompleteStream(context.Background(), Request{System: "s", Messages: []Message{msg}}, nil)
	if err != nil {
		t.Fatalf("CompleteStream: %v", err)
	}
	if res.Text != "看见" {
		t.Fatalf("res.Text = %q", res.Text)
	}
}

func TestCompleteStreamBadImageNoRequest(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		io.WriteString(w, `data: [DONE]`)
	}))
	defer srv.Close()

	c := &Client{Provider: ProviderAnthropic, BaseURL: srv.URL, APIKey: "k", Model: "m", HTTP: srv.Client(), MaxRetries: 0}
	msg := Message{Role: "user", Content: "x", Images: []Image{{MIMEType: "image/xyz", Data: "aGk="}}}
	if _, err := c.CompleteStream(context.Background(), Request{Messages: []Message{msg}}, nil); err == nil {
		t.Fatal("坏图应报错")
	}
	if calls != 0 {
		t.Fatalf("坏图的流式请求不应发出，实际 %d 次", calls)
	}
}
