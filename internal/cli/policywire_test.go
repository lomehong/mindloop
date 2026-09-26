package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"mindloop/internal/policy"
	"mindloop/internal/runner"
	"mindloop/internal/task"
	"mindloop/internal/traj"
)

// fenceScript 把脚本包进 bash 代码块——run 循环从模型回复里提取的
// 就是这种形态。
func fenceScript(code string) string { return "```bash\n" + code + "\n```" }

// mockLLMServer 是 OpenAI 兼容的最小回放：每条请求返回预置回复并
// 计数调用次数——"等待审批不继续调用模型"的验收需要一个可数的模型面。
func mockLLMServer(t *testing.T, reply string) (*httptest.Server, *int32) {
	t.Helper()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		b, _ := json.Marshal(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"role": "assistant", "content": reply}},
			},
		})
		_, _ = w.Write(b)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

// useMockLLM 把进程环境指向 mock 服务：run 命令经 llm.FromEnv 构造
// openai-compatible 客户端。
func useMockLLM(t *testing.T, baseURL string) {
	t.Helper()
	t.Setenv("MINDLOOP_PROVIDER", "openai-compatible")
	t.Setenv("MINDLOOP_BASE_URL", baseURL)
	t.Setenv("MINDLOOP_API_KEY", "test-key")
	t.Setenv("MINDLOOP_MODEL", "mock-1")
}

// TestTaskApprovalHookSyncsAwaitingApproval：审批等待的进入/离开必须
// 同步到任务状态机（running ↔ awaiting_approval），无任务归属的执行
// （run 命令场景）不触碰任务状态。
func TestTaskApprovalHookSyncsAwaitingApproval(t *testing.T) {
	id := taskTestIdentity(t)
	ctx := context.Background()
	store := task.New(id.Timeline, "ada")
	sub, err := store.Submit(ctx, task.Submission{From: "user", ClientMessageID: "cm-1", Content: "做个动作"})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	claimed, err := store.Claim(ctx, "run-1")
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if claimed.ID != sub.ID || claimed.State != task.Running {
		t.Fatalf("Claim 结果异常: %+v", claimed)
	}

	hook := taskApprovalHook(id.Timeline, "ada")
	ex := runner.Execution{Script: "echo hi", WorkDir: "wd", RunID: "run-1", TaskID: sub.ID, Attempt: claimed.Attempt}

	hook(ex, true)
	got, err := store.Get(ctx, sub.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != task.AwaitingApproval {
		t.Fatalf("进入等待后状态 = %s，应为 awaiting_approval", got.State)
	}

	hook(ex, false)
	if got, _ = store.Get(ctx, sub.ID); got.State != task.Running {
		t.Fatalf("离开等待后状态 = %s，应为 running", got.State)
	}

	hook(runner.Execution{Script: "x", WorkDir: "wd", RunID: "run-1"}, true)
	if got, _ = store.Get(ctx, sub.ID); got.State != task.Running {
		t.Fatalf("无任务归属的 hook 不应改状态: %s", got.State)
	}
}

// TestTaskApprovalHookRespectsCancellation：等待期间用户取消——离开
// 等待的回调不得把 canceling 改回 running（转换表拒绝，hook 吞错）。
func TestTaskApprovalHookRespectsCancellation(t *testing.T) {
	id := taskTestIdentity(t)
	ctx := context.Background()
	store := task.New(id.Timeline, "ada")
	sub, err := store.Submit(ctx, task.Submission{From: "user", ClientMessageID: "cm-1", Content: "动作"})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	claimed, err := store.Claim(ctx, "run-1")
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}

	hook := taskApprovalHook(id.Timeline, "ada")
	ex := runner.Execution{Script: "echo hi", WorkDir: "wd", RunID: "run-1", TaskID: sub.ID, Attempt: claimed.Attempt}
	hook(ex, true)
	if _, err := store.Cancel(ctx, sub.ID, claimed.Attempt, "user", "req-1"); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	hook(ex, false)
	got, err := store.Get(ctx, sub.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != task.Canceling {
		t.Fatalf("取消中的任务不应被离开等待改回 running: %s", got.State)
	}
}

// TestRunDenyNeverExecutes：deny 策略下脚本零执行——模型仍被调用
// 一次给出脚本，随后整条运行以拒绝失败，不再继续调用模型。
func TestRunDenyNeverExecutes(t *testing.T) {
	newTestHome(t)
	workdir := t.TempDir()
	marker := filepath.Join(workdir, "marker.txt")
	tl, err := traj.Create(context.Background(), "policy-deny")
	if err != nil {
		t.Fatal(err)
	}
	srv, calls := mockLLMServer(t, fenceScript("echo pwned > marker.txt\nFINAL=\"不该执行\""))
	useMockLLM(t, srv.URL)
	t.Setenv("MINDLOOP_EXEC_POLICY", "deny")

	code, out, errOut := runCLI(t, "run", tl.ID, "写一个文件", "--workdir", workdir)
	if code != 1 {
		t.Fatalf("deny 下 exit = %d（应为 1）stdout=%s stderr=%s", code, out, errOut)
	}
	if !strings.Contains(errOut, "脚本执行被拒绝") {
		t.Fatalf("应报告拒绝原因: %q", errOut)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("deny 下脚本不得执行（marker 存在）")
	}
	if got := atomic.LoadInt32(calls); got != 1 {
		t.Fatalf("拒绝后不得再调用模型: calls=%d", got)
	}
}

// TestRunAskApprovalWaitsThenExecutes：ask 策略下脚本执行前展示正文
// 与工作目录并等待；批准后同一脚本才真正执行。
func TestRunAskApprovalWaitsThenExecutes(t *testing.T) {
	newTestHome(t)
	workdir := t.TempDir()
	marker := filepath.Join(workdir, "marker.txt")
	tl, err := traj.Create(context.Background(), "policy-ask")
	if err != nil {
		t.Fatal(err)
	}
	srv, _ := mockLLMServer(t, fenceScript("echo ran > marker.txt\nFINAL=\"批准后完成\""))
	useMockLLM(t, srv.URL)
	t.Setenv("MINDLOOP_EXEC_POLICY", "ask")

	// 在独立 goroutine 里跑整条 run：主 goroutine 扮演"另一个终端"
	// 的批准者。这里不用 runCLI，避免测试辅助在非测试 goroutine 的
	// 使用面。
	type runResult struct {
		code        int
		out, errOut string
	}
	done := make(chan runResult, 1)
	go func() {
		var out, errBuf bytes.Buffer
		code := Execute(context.Background(), []string{"run", tl.ID, "写文件", "--workdir", workdir}, &out, &errBuf)
		done <- runResult{code: code, out: out.String(), errOut: errBuf.String()}
	}()

	dir := policy.Dir(tl.Dir)
	var p policy.PendingRequest
	deadline := time.Now().Add(30 * time.Second)
	for {
		pending, _ := policy.ListPending(dir)
		if len(pending) > 0 {
			p = pending[0]
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("等待待批请求出现超时")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(p.Script, "marker.txt") {
		t.Fatalf("待批正文应含脚本: %q", p.Script)
	}
	if p.WorkDir != workdir {
		t.Fatalf("待批工作目录 = %q，应为 %q", p.WorkDir, workdir)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("批准前脚本不得执行（marker 存在）")
	}

	if err := policy.Decide(dir, p.Hash, true); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	select {
	case res := <-done:
		if res.code != 0 {
			t.Fatalf("批准后 exit = %d stderr=%s", res.code, res.errOut)
		}
		if !strings.Contains(res.out, "批准后完成") {
			t.Fatalf("stdout 应含终局: %q", res.out)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("批准后运行未结束")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("批准后脚本应已执行: %v", err)
	}
}

// TestRunHelpPolicyBoundary：帮助必须让用户看清授权开关与边界——
// 授权是"启动授权与工作目录约定"，不是操作系统访问隔离。
func TestRunHelpPolicyBoundary(t *testing.T) {
	newTestHome(t)
	for _, args := range [][]string{{"run", "--help"}, {"approve", "--help"}} {
		code, out, errOut := runCLI(t, args...)
		if code != 0 {
			t.Fatalf("%v exit = %d stderr=%s", args, code, errOut)
		}
		for _, want := range []string{"MINDLOOP_EXEC_POLICY", "不是操作系统访问隔离"} {
			if !strings.Contains(out, want) {
				t.Fatalf("%v 帮助应含 %q:\n%s", args, want, out)
			}
		}
	}
}
