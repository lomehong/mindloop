package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lomehong/mindloop/internal/identity"
	"github.com/lomehong/mindloop/internal/llm"
)

// errNoTaskAssistLLM 测试哨兵：身份未配置任何 LLM。
var errNoTaskAssistLLM = errors.New("no llm configured")

// withTaskAssistFake 把起草客户端替换为 echo 占位（本地回显，零外呼），
// 并恢复原状。echo 供应商会把完整提示词+草稿原样回显——足够验证接线
// 与回填，不需要真模型。
func withTaskAssistFake(t *testing.T) {
	t.Helper()
	prev := taskAssistClientFor
	taskAssistClientFor = func(*identity.Identity) (*llm.Client, error) {
		return llm.New(llm.Spec{Model: "echo"})
	}
	t.Cleanup(func() { taskAssistClientFor = prev })
}

// withTaskAssistBroken 把起草客户端替换为恒失败——验证 409 如实降级。
func withTaskAssistBroken(t *testing.T) {
	t.Helper()
	prev := taskAssistClientFor
	taskAssistClientFor = func(*identity.Identity) (*llm.Client, error) {
		return nil, errNoTaskAssistLLM
	}
	t.Cleanup(func() { taskAssistClientFor = prev })
}

func postTaskAssist(t *testing.T, ts *httptest.Server, body string) (*http.Response, map[string]any) {
	t.Helper()
	resp, err := http.Post(ts.URL+"/api/identities/ada/task-assist", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("响应应为 JSON: %v", err)
	}
	return resp, out
}

func TestTaskAssistEndpoint(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	id, err := identity.Create(context.Background(), "ada")
	if err != nil {
		t.Fatal(err)
	}
	_ = id
	withTaskAssistFake(t)
	ts, _ := newTestServer(t, identity.Home(), "")

	resp, out := postTaskAssist(t, ts, `{"draft":"整理下载文件夹"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	task, _ := out["task"].(string)
	if strings.TrimSpace(task) == "" {
		t.Fatalf("task 应为非空正文（echo 占位也有固定话术），得到: %q", task)
	}

	// 空草稿 400。
	resp, _ = postTaskAssist(t, ts, `{"draft":"  "}`)
	if resp.StatusCode != 400 {
		t.Fatalf("空草稿 status = %d，应为 400", resp.StatusCode)
	}
	// 非法体 400。
	resp, err = http.Post(ts.URL+"/api/identities/ada/task-assist", "application/json", strings.NewReader("not-json"))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 400 {
		t.Fatalf("非法体 status = %d，应为 400", resp.StatusCode)
	}
	// 未知子路径 404。
	resp, err = http.Get(ts.URL + "/api/identities/ada/task-assist/extra")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 404 {
		t.Fatalf("未知子路径 status = %d，应为 404", resp.StatusCode)
	}
	// GET 405。
	resp, err = http.Get(ts.URL + "/api/identities/ada/task-assist")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 405 {
		t.Fatalf("GET status = %d，应为 405", resp.StatusCode)
	}
}

func TestTaskAssistUnconfiguredLLM(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	id, err := identity.Create(context.Background(), "ada")
	if err != nil {
		t.Fatal(err)
	}
	_ = id
	withTaskAssistBroken(t)
	ts, _ := newTestServer(t, identity.Home(), "")

	resp, out := postTaskAssist(t, ts, `{"draft":"整理下载文件夹"}`)
	if resp.StatusCode != 409 {
		t.Fatalf("未配 LLM status = %d，应为 409", resp.StatusCode)
	}
	if msg := fmt.Sprint(out["detail"]); !strings.Contains(msg, "未配置") {
		t.Fatalf("错误应说明 LLM 未配置: %v", out)
	}
}
