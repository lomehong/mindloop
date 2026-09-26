package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"mindloop/internal/identity"
	"mindloop/internal/ids"
	"mindloop/internal/mind"
	"mindloop/internal/task"
)

// taskTestIdentity 在隔离 HOME 下建 ada 身份并返回——task 命令测试
// 的统一基底（同一条测试内多次 runCLI 共享它）。
func taskTestIdentity(t *testing.T) *identity.Identity {
	t.Helper()
	newTestHome(t)
	id, err := identity.Create(context.Background(), "ada")
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// parseTaskJSON 解析单任务 JSON 输出。
func parseTaskJSON(t *testing.T, out string) task.Task {
	t.Helper()
	var got task.Task
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("任务 JSON 无效: %v\n%s", err, out)
	}
	return got
}

// TestTaskSubmitShowListRoundtrip：提交返回完整任务 JSON；show 支持
// 前缀定位且与提交事实一致；list 投影同一事实。submit 时心智未运行
// 给出提示但不失败——停机提交是合法场景（重启后自动恢复）。
func TestTaskSubmitShowListRoundtrip(t *testing.T) {
	taskTestIdentity(t)
	code, out, errOut := runCLI(t, "task", "submit", "ada", "把结果写到 report.md", "--client-message-id", "cm-1", "--json")
	if code != 0 {
		t.Fatalf("submit exit=%d stderr=%s", code, errOut)
	}
	got := parseTaskJSON(t, out)
	if got.ID == "" || got.State != task.Queued || got.Attempt != 1 {
		t.Fatalf("提交结果 = %+v", got)
	}
	if got.ClientMessageID != "cm-1" || got.From != "operator" || got.Content != "把结果写到 report.md" || got.IdentityID != "ada" {
		t.Fatalf("提交字段 = %+v", got)
	}
	if !strings.Contains(errOut, "没有在运行") {
		t.Fatalf("离线提交应提示心智未运行: %q", errOut)
	}

	// 前缀解析：short id 也能定位同一任务。
	code, out, errOut = runCLI(t, "task", "show", "ada", ids.Short(got.ID, 8), "--json")
	if code != 0 {
		t.Fatalf("show exit=%d stderr=%s", code, errOut)
	}
	shown := parseTaskJSON(t, out)
	if shown.ID != got.ID || shown.State != task.Queued {
		t.Fatalf("show 与提交不一致: %+v", shown)
	}

	code, out, _ = runCLI(t, "task", "list", "ada", "--json")
	if code != 0 {
		t.Fatalf("list exit=%d", code)
	}
	var list []task.Task
	if err := json.Unmarshal([]byte(out), &list); err != nil || len(list) != 1 || list[0].ID != got.ID {
		t.Fatalf("list = %s (%v)", out, err)
	}

	// 默认（非 --json）show 输出人类可读摘要，含状态与完整 ID。
	code, out, _ = runCLI(t, "task", "show", "ada", got.ID)
	if code != 0 || !strings.Contains(out, got.ID) || !strings.Contains(out, "queued") {
		t.Fatalf("show 摘要 = %q exit=%d", out, code)
	}
}

// TestTaskSubmitIdempotentAndConflict：同键同载荷重发返回原任务
// （落盘后响应丢失的重试路径）；同键不同载荷是冲突；冲突不产生新任务。
func TestTaskSubmitIdempotentAndConflict(t *testing.T) {
	taskTestIdentity(t)
	_, out, _ := runCLI(t, "task", "submit", "ada", "内容A", "--client-message-id", "cm-dup", "--json")
	first := parseTaskJSON(t, out)

	code, out, errOut := runCLI(t, "task", "submit", "ada", "内容A", "--client-message-id", "cm-dup", "--json")
	if code != 0 {
		t.Fatalf("同键同载荷重发失败: exit=%d stderr=%s", code, errOut)
	}
	if second := parseTaskJSON(t, out); second.ID != first.ID {
		t.Fatalf("同键同载荷应返回原任务: %s vs %s", first.ID, second.ID)
	}

	code, _, errOut = runCLI(t, "task", "submit", "ada", "内容B", "--client-message-id", "cm-dup", "--json")
	if code != 1 || !strings.Contains(errOut, "幂等") {
		t.Fatalf("同键不同载荷应冲突: exit=%d stderr=%q", code, errOut)
	}

	_, out, _ = runCLI(t, "task", "list", "ada", "--json")
	var list []task.Task
	if err := json.Unmarshal([]byte(out), &list); err != nil || len(list) != 1 {
		t.Fatalf("冲突不得产生新任务: %s (%v)", out, err)
	}
}

// TestTaskSubmitFromSourceStep：--source-step 把既有消息转为任务，
// 内容从源消息派生、来源关联落盘（省略内容的位置参数）。
func TestTaskSubmitFromSourceStep(t *testing.T) {
	id := taskTestIdentity(t)
	if err := mind.PostMessage(id.Timeline, "operator", "ada", "cli", "帮我把日志轮转配置一下"); err != nil {
		t.Fatal(err)
	}
	last, err := id.Timeline.LastStep()
	if err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runCLI(t, "task", "submit", "ada", "--source-step", ids.Short(last.StepID, 8), "--client-message-id", "cm-src", "--json")
	if code != 0 {
		t.Fatalf("submit --source-step exit=%d stderr=%s", code, errOut)
	}
	got := parseTaskJSON(t, out)
	if got.Content != "帮我把日志轮转配置一下" || got.SourceStepID != last.StepID {
		t.Fatalf("来源派生 = %+v", got)
	}
}

// TestTaskCancelRetryLifecycle：queued 取消直接 canceled；retry 创建
// 新 attempt；同 --request-id 的取消可重发（幂等收据）且不改变结果。
func TestTaskCancelRetryLifecycle(t *testing.T) {
	taskTestIdentity(t)
	_, out, _ := runCLI(t, "task", "submit", "ada", "长任务", "--client-message-id", "cm-life", "--json")
	sub := parseTaskJSON(t, out)

	code, out, errOut := runCLI(t, "task", "cancel", "ada", sub.ID, "--json")
	if code != 0 {
		t.Fatalf("cancel exit=%d stderr=%s", code, errOut)
	}
	if got := parseTaskJSON(t, out); got.State != task.Canceled {
		t.Fatalf("queued 取消应直接 canceled: %+v", got)
	}

	code, out, errOut = runCLI(t, "task", "retry", "ada", sub.ID, "--json")
	if code != 0 {
		t.Fatalf("retry exit=%d stderr=%s", code, errOut)
	}
	if got := parseTaskJSON(t, out); got.State != task.Queued || got.Attempt != 2 {
		t.Fatalf("retry 应创建新 attempt: %+v", got)
	}

	code, out, _ = runCLI(t, "task", "cancel", "ada", sub.ID, "--request-id", "req-x", "--json")
	if code != 0 || parseTaskJSON(t, out).State != task.Canceled {
		t.Fatalf("二次取消失败: exit=%d out=%s", code, out)
	}
	// 幂等重发：同一 request-id 不报冲突，返回当前任务。
	code, out, errOut = runCLI(t, "task", "cancel", "ada", sub.ID, "--request-id", "req-x", "--json")
	if code != 0 {
		t.Fatalf("幂等取消重发失败: exit=%d stderr=%s", code, errOut)
	}
	if got := parseTaskJSON(t, out); got.State != task.Canceled {
		t.Fatalf("幂等重发改变了状态: %+v", got)
	}
}

// TestTaskRetryRejectsNonTerminal：queued 任务不能 retry——重试只属于
// 终态（failed/canceled/interrupted/budget_exceeded），拒绝要明确。
func TestTaskRetryRejectsNonTerminal(t *testing.T) {
	taskTestIdentity(t)
	_, out, _ := runCLI(t, "task", "submit", "ada", "还在排队", "--client-message-id", "cm-q", "--json")
	sub := parseTaskJSON(t, out)
	code, _, errOut := runCLI(t, "task", "retry", "ada", sub.ID)
	if code != 1 || !strings.Contains(errOut, "不允许") {
		t.Fatalf("queued retry 应拒绝: exit=%d stderr=%q", code, errOut)
	}
}

// TestTaskWaitReachesTerminalState：wait 轮询到外部进展（别处取消）
// 后输出终态快照并以 0 结束。
func TestTaskWaitReachesTerminalState(t *testing.T) {
	id := taskTestIdentity(t)
	_, out, _ := runCLI(t, "task", "submit", "ada", "等待中的任务", "--client-message-id", "cm-wait", "--json")
	sub := parseTaskJSON(t, out)
	store := task.New(id.Timeline, id.Name)
	go func() {
		// 固定 request-id：重试安全（幂等收据），不受锁竞争影响。
		for i := 0; i < 20; i++ {
			time.Sleep(200 * time.Millisecond)
			if _, err := store.Cancel(context.Background(), sub.ID, 1, "tester", "req-wait"); err == nil {
				return
			}
		}
	}()

	code, out, errOut := runCLI(t, "task", "wait", "ada", sub.ID, "--timeout", "10s", "--json")
	if code != 0 {
		t.Fatalf("wait exit=%d stderr=%s", code, errOut)
	}
	if got := parseTaskJSON(t, out); got.State != task.Canceled {
		t.Fatalf("wait 终态 = %+v", got)
	}
}

// TestTaskWaitTimeoutSnapshot：超时不当作成功——输出当前状态快照
// （JSON）并以 1 结束，任务可能仍在别处推进。
func TestTaskWaitTimeoutSnapshot(t *testing.T) {
	taskTestIdentity(t)
	_, out, _ := runCLI(t, "task", "submit", "ada", "永不完成", "--client-message-id", "cm-to", "--json")
	sub := parseTaskJSON(t, out)
	code, out, errOut := runCLI(t, "task", "wait", "ada", sub.ID, "--timeout", "600ms", "--json")
	if code != 1 || !strings.Contains(errOut, "超时") {
		t.Fatalf("wait 超时: exit=%d stderr=%q", code, errOut)
	}
	if got := parseTaskJSON(t, out); got.State != task.Queued {
		t.Fatalf("超时快照 = %+v", got)
	}
}

// TestTaskShowMissingPrefix：未命中的前缀是明确拒绝，不是空结果。
func TestTaskShowMissingPrefix(t *testing.T) {
	taskTestIdentity(t)
	code, _, errOut := runCLI(t, "task", "show", "ada", "ffffffff")
	if code != 1 || !strings.Contains(errOut, "不存在") {
		t.Fatalf("未命中前缀: exit=%d stderr=%q", code, errOut)
	}
}

// TestTaskSubmitRequiresContent：既无内容又无 --source-step 是用法
// 错误（退出码 2），不落任何事实。
func TestTaskSubmitRequiresContent(t *testing.T) {
	taskTestIdentity(t)
	code, _, errOut := runCLI(t, "task", "submit", "ada")
	if code != 2 || !strings.Contains(errOut, "用法") {
		t.Fatalf("空提交应为用法错误: exit=%d stderr=%q", code, errOut)
	}
}
