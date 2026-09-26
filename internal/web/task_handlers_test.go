package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"mindloop/internal/identity"
	"mindloop/internal/mind"
	"mindloop/internal/traj"
)

// newTaskTestRig 建隔离状态根 + ada 身份 + 测试服务器——web 任务面
// 测试的统一基底。
func newTaskTestRig(t *testing.T) (*httptest.Server, *Server, *identity.Identity) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	home := identity.Home()
	id, err := identity.Create(context.Background(), "ada")
	if err != nil {
		t.Fatal(err)
	}
	ts, s := newTestServer(t, home, "")
	return ts, s, id
}

// TestTaskAPISubmitListShowIdempotent：POST 提交返回任务 JSON；同
// client_message_id 重发返回原任务（响应丢失的安全重发）；列表与
// 详情投影同一事实。
func TestTaskAPISubmitListShowIdempotent(t *testing.T) {
	ts, _, _ := newTaskTestRig(t)
	body := map[string]string{"content": "把报告写到 report.md", "from_name": "you", "client_message_id": "cm-web-1"}

	resp, out := postJSON(t, ts, "/api/identities/ada/tasks", body)
	if resp.StatusCode != 200 {
		t.Fatalf("submit = %d %v", resp.StatusCode, out)
	}
	taskID, _ := out["task_id"].(string)
	if taskID == "" || out["status"] != "queued" || out["attempt"] != float64(1) {
		t.Fatalf("提交契约错位: %v", out)
	}
	if out["client_message_id"] != "cm-web-1" || out["content"] != "把报告写到 report.md" {
		t.Fatalf("提交字段错位: %v", out)
	}

	resp2, out2 := postJSON(t, ts, "/api/identities/ada/tasks", body)
	if resp2.StatusCode != 200 || out2["task_id"] != taskID {
		t.Fatalf("同键同载荷应返回原任务: %d %v", resp2.StatusCode, out2)
	}

	list, err := http.Get(ts.URL + "/api/identities/ada/tasks")
	if err != nil {
		t.Fatal(err)
	}
	defer list.Body.Close()
	var items []map[string]any
	if err := json.NewDecoder(list.Body).Decode(&items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0]["task_id"] != taskID {
		t.Fatalf("列表 = %v", items)
	}

	show, err := http.Get(ts.URL + "/api/identities/ada/tasks/" + taskID)
	if err != nil {
		t.Fatal(err)
	}
	defer show.Body.Close()
	var one map[string]any
	if err := json.NewDecoder(show.Body).Decode(&one); err != nil {
		t.Fatal(err)
	}
	if show.StatusCode != 200 || one["status"] != "queued" {
		t.Fatalf("详情 = %d %v", show.StatusCode, one)
	}
}

// TestTaskAPICancelRetry：取消 queued 直接 canceled；retry 创建新
// attempt；对非终态任务 retry 是 409 而不是静默成功。
func TestTaskAPICancelRetry(t *testing.T) {
	ts, _, _ := newTaskTestRig(t)
	_, out := postJSON(t, ts, "/api/identities/ada/tasks",
		map[string]string{"content": "长任务", "client_message_id": "cm-cr"})
	taskID, _ := out["task_id"].(string)

	resp, canceled := postJSON(t, ts, "/api/identities/ada/tasks/"+taskID+"/cancel", map[string]any{})
	if resp.StatusCode != 200 || canceled["status"] != "canceled" {
		t.Fatalf("cancel = %d %v", resp.StatusCode, canceled)
	}

	resp, retried := postJSON(t, ts, "/api/identities/ada/tasks/"+taskID+"/retry", map[string]any{})
	if resp.StatusCode != 200 || retried["status"] != "queued" || retried["attempt"] != float64(2) {
		t.Fatalf("retry = %d %v", resp.StatusCode, retried)
	}

	resp, _ = postJSON(t, ts, "/api/identities/ada/tasks/"+taskID+"/retry", map[string]any{})
	if resp.StatusCode != 409 {
		t.Fatalf("非终态 retry 应 409，得到 %d", resp.StatusCode)
	}
}

// TestTaskAPIErrors：未知任务 404；空内容 400；未知子路径 404。
func TestTaskAPIErrors(t *testing.T) {
	ts, _, _ := newTaskTestRig(t)

	if r, err := http.Get(ts.URL + "/api/identities/ada/tasks/ffff"); err != nil {
		t.Fatal(err)
	} else {
		r.Body.Close()
		if r.StatusCode != 404 {
			t.Fatalf("未知任务应 404，得到 %d", r.StatusCode)
		}
	}

	resp, _ := postJSON(t, ts, "/api/identities/ada/tasks", map[string]string{"content": "  "})
	if resp.StatusCode != 400 {
		t.Fatalf("空内容应 400，得到 %d", resp.StatusCode)
	}

	if r, err := http.Get(ts.URL + "/api/identities/ada/tasks/x/y/z"); err != nil {
		t.Fatal(err)
	} else {
		r.Body.Close()
		if r.StatusCode != 404 {
			t.Fatalf("未知子路径应 404，得到 %d", r.StatusCode)
		}
	}
}

// TestChatSendStepIDAndIdempotency：发送返回 step_id 与
// client_message_id（前端按 ID 对账）；同键同载荷重发返回原步骤且
// 不重复落盘；同键不同载荷是 409（不能静默吞掉客户端的不一致）。
func TestChatSendStepIDAndIdempotency(t *testing.T) {
	ts, _, id := newTaskTestRig(t)
	body := map[string]string{"content": "你好", "from_name": "you", "client_message_id": "cm-chat-1"}

	resp, out := postJSON(t, ts, "/api/identities/ada/chat", body)
	if resp.StatusCode != 200 {
		t.Fatalf("send = %d %v", resp.StatusCode, out)
	}
	stepID, _ := out["step_id"].(string)
	if stepID == "" || out["client_message_id"] != "cm-chat-1" {
		t.Fatalf("发送响应缺对账字段: %v", out)
	}

	resp2, out2 := postJSON(t, ts, "/api/identities/ada/chat", body)
	if resp2.StatusCode != 200 || out2["step_id"] != stepID {
		t.Fatalf("同键同载荷应返回原步骤: %d %v", resp2.StatusCode, out2)
	}
	steps, err := id.Timeline.Steps()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, s := range steps {
		if c, _ := s.Field("content"); c == "你好" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("重发不得重复落盘（落盘 %d 条）", n)
	}

	resp3, _ := postJSON(t, ts, "/api/identities/ada/chat",
		map[string]string{"content": "改了内容", "from_name": "you", "client_message_id": "cm-chat-1"})
	if resp3.StatusCode != 409 {
		t.Fatalf("同键不同载荷应 409，得到 %d", resp3.StatusCode)
	}
}

// TestChatOutcomesFromReceipts：outcomes 从回复与状态收据投影——
// replied / no-reply / failed；收据是元事实，不混进 messages。
func TestChatOutcomesFromReceipts(t *testing.T) {
	ts, _, id := newTaskTestRig(t)
	ctx := context.Background()
	for _, c := range []string{"问题一", "问题二", "问题三"} {
		if err := mind.PostMessage(id.Timeline, "you", "ada", "chat", c); err != nil {
			t.Fatal(err)
		}
	}
	steps, err := id.Timeline.Steps()
	if err != nil {
		t.Fatal(err)
	}
	m := steps[len(steps)-3:]
	appendMsg := func(fields map[string]string) {
		s := traj.NewStep("message")
		for k, v := range fields {
			s.Fields[k] = v
		}
		if err := id.Timeline.Append(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	appendMsg(map[string]string{"from": "ada", "to": "you", "content": "答一", "reply_to": m[0].StepID})
	appendMsg(map[string]string{"from": "ada", "to": "you", "content": "元事实不应进对话", "source": "reply-status", "state": "no-reply", "reply_to": m[1].StepID})
	appendMsg(map[string]string{"from": "ada", "to": "you", "content": "元事实不应进对话", "source": "reply-status", "state": "reply-failed", "reply_to": m[2].StepID})

	get, err := http.Get(ts.URL + "/api/identities/ada/chat")
	if err != nil {
		t.Fatal(err)
	}
	defer get.Body.Close()
	var log struct {
		Messages []map[string]any  `json:"messages"`
		Outcomes map[string]string `json:"outcomes"`
	}
	if err := json.NewDecoder(get.Body).Decode(&log); err != nil {
		t.Fatal(err)
	}
	if log.Outcomes[m[0].StepID] != "replied" || log.Outcomes[m[1].StepID] != "no-reply" || log.Outcomes[m[2].StepID] != "failed" {
		t.Fatalf("outcomes = %v", log.Outcomes)
	}
	if len(log.Messages) != 4 {
		t.Fatalf("收据不应进消息流（4 条入站+回复，得到 %d 条）: %v", len(log.Messages), log.Messages)
	}
	for _, msg := range log.Messages {
		if msg["content"] == "元事实不应进对话" {
			t.Fatalf("状态收据混入对话流: %v", msg)
		}
	}
}

// TestRepliesStreamStepAttribution：step 事件携带 task_id/run_id/
// attempt 归因字段（SSE 消费者据此分辨"本任务的进度"与身份其他
// 活动）；无归因字段的步骤不带这些键。
func TestRepliesStreamStepAttribution(t *testing.T) {
	ts, s, id := newTaskTestRig(t)
	s.replyPollEvery = 50 * time.Millisecond
	s.replyPingEvery = 200 * time.Millisecond
	r := openStream(t, ts.URL+"/api/identities/ada/replies/stream")

	appendStepLine(t, id, `{"type":"task-event","step_id":"ev-1","ts":"2026-02-01T00:00:00.000Z","task_id":"task-1","run_id":"run-1","attempt":2,"status":"running"}`)
	appendStepLine(t, id, `{"type":"action","step_id":"act-1","ts":"2026-02-01T00:00:01.000Z","content":"无归因"}`)

	var seen []map[string]any
	for len(seen) < 2 {
		f := readFrame(t, r)
		if f.event != "step" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(f.data), &m); err != nil {
			t.Fatalf("step data 不是 JSON: %v (%q)", err, f.data)
		}
		seen = append(seen, m)
	}
	ev := seen[0]
	if ev["task_id"] != "task-1" || ev["run_id"] != "run-1" || ev["attempt"] != float64(2) {
		t.Fatalf("归因字段错位: %v", ev)
	}
	if _, ok := seen[1]["task_id"]; ok {
		t.Fatalf("无归因步骤不应带 task_id: %v", seen[1])
	}
	if _, ok := seen[1]["run_id"]; ok {
		t.Fatalf("无归因步骤不应带 run_id: %v", seen[1])
	}
}
