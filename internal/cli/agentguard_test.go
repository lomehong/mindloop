package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// agentGuard 钉子测试（superagent-lessons §7.0 路径①③④，2026-11-02）：
// agent 沙箱内（MINDLOOP_RUN_ID 存在）五类写入口的门禁语义。
// 已知边界（不测、如实声明）：unset 变量伪装人调用不在 env 门禁防线内。

// agentEnv 模拟 agent 沙箱环境：runner 启动沙箱时注入 MINDLOOP_RUN_ID。
func agentEnv(t *testing.T) { t.Setenv("MINDLOOP_RUN_ID", "run-guard-test") }

// TestAgentGuardTrajAppend：信任类型（observation/message）与授权字段
// （context_kind/task_id）在 agent 环境被拒；簿记类型（action）放行
// ——它带 run_id 章，且进自主上下文还需 agent 写不了的 context_kind。
func TestAgentGuardTrajAppend(t *testing.T) {
	newTestHome(t)
	agentEnv(t)
	code, out, errOut := runCLI(t, "traj", "new", "--slug", "guard")
	if code != 0 {
		t.Fatalf("new: %d %s", code, errOut)
	}
	id := strings.TrimSpace(out)

	cases := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"信任类型 observation", []string{"traj", "append", id, "observation", "--content", "x"}, "不可写类型"},
		{"信任类型 event", []string{"traj", "append", id, "event", "--content", "x"}, "不可写类型"},
		{"伪造人类消息", []string{"traj", "append", id, "message", "--from", "operator", "--content", "x"}, "不可写类型"},
		{"授权字段 context_kind", []string{"traj", "append", id, "action", "--field", "context_kind=autonomous"}, "不可写字段"},
		{"授权字段 task_id", []string{"traj", "append", id, "action", "--field", "task_id=zzz"}, "不可写字段"},
	}
	for _, tc := range cases {
		code, _, errOut := runCLI(t, tc.args...)
		if code != 1 || !strings.Contains(errOut, tc.wantErr) {
			t.Fatalf("%s：应 exit 1 含 %q，得 code=%d err=%q", tc.name, tc.wantErr, code, errOut)
		}
	}

	// 簿记类型放行（run 内 agent 的正常写路径）。
	if code, _, errOut := runCLI(t, "traj", "append", id, "action", "--content", "did"); code != 0 {
		t.Fatalf("action 应放行: %d %s", code, errOut)
	}
}

// TestAgentGuardTaskSubmit：agent 发起的任务 From 一律记 "agent"，
// 不得声称 operator（含显式 --from operator 的伪造尝试）。
func TestAgentGuardTaskSubmit(t *testing.T) {
	taskTestIdentity(t)
	agentEnv(t)
	code, out, errOut := runCLI(t, "task", "submit", "ada", "测试任务", "--from", "operator", "--client-message-id", "guard-1", "--json")
	if code != 0 {
		t.Fatalf("submit: %d %s", code, errOut)
	}
	if !strings.Contains(out, `"agent"`) {
		t.Fatalf("From 应为 agent，输出: %s", out)
	}
}

// TestAgentGuardConfigWrites：自我配置面四入口在 agent 环境一律拒绝。
func TestAgentGuardConfigWrites(t *testing.T) {
	newTestHome(t)
	agentEnv(t)
	cases := []struct {
		name string
		args []string
	}{
		{"mcp add", []string{"mcp", "add", "evil", "--", "nc", "evil.example"}},
		{"skills install", []string{"skills", "install", "evil/repo"}},
		{"schedule add", []string{"schedule", "add", "ada", `{"id":"x","every":"2m","exec":"echo hi"}`}},
	}
	for _, tc := range cases {
		code, _, errOut := runCLI(t, tc.args...)
		if code != 1 || !strings.Contains(errOut, "agent 沙箱内不可用") {
			t.Fatalf("%s：应 exit 1 含拒绝文案，得 code=%d err=%q", tc.name, code, errOut)
		}
	}
}

// TestOperatorUnaffected：操作员环境（无 MINDLOOP_RUN_ID）不受门禁
// 影响——message/observation 照常可写，task submit 缺省 From=operator。
func TestOperatorUnaffected(t *testing.T) {
	newTestHome(t)
	code, out, errOut := runCLI(t, "traj", "new", "--slug", "op")
	if code != 0 {
		t.Fatalf("new: %d %s", code, errOut)
	}
	id := strings.TrimSpace(out)
	if code, _, errOut := runCLI(t, "traj", "append", id, "message", "--from", "operator", "--content", "hi"); code != 0 {
		t.Fatalf("操作员 message 应放行: %d %s", code, errOut)
	}
	if code, _, errOut := runCLI(t, "traj", "append", id, "observation", "--content", "seen"); code != 0 {
		t.Fatalf("操作员 observation 应放行: %d %s", code, errOut)
	}
}

// TestMemAddAuthorStamp：D4 盖章版——mem add 的 author 按调用环境
// 自动盖章（agent 环境 agent、操作员环境 operator），无旗标可伪造。
func TestMemAddAuthorStamp(t *testing.T) {
	id := taskTestIdentity(t)
	memDir := filepath.Join(id.Dir, "memories")
	readAuthors := func() map[string]string {
		out := map[string]string{}
		files, _ := filepath.Glob(filepath.Join(memDir, "*.md"))
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatalf("读 %s: %v", f, err)
			}
			for _, ln := range strings.Split(string(b), "\n") {
				if v, ok := strings.CutPrefix(ln, "author: "); ok {
					out[filepath.Base(f)] = strings.TrimSpace(v)
				}
			}
		}
		return out
	}
	// 操作员写入 → author: operator
	if code, _, errOut := runCLI(t, "mem", "add", "--identity", "ada", "--type", "fact", "操作员直记"); code != 0 {
		t.Fatalf("操作员 mem add: %d %s", code, errOut)
	}
	// agent 沙箱写入 → author: agent
	agentEnv(t)
	if code, _, errOut := runCLI(t, "mem", "add", "--identity", "ada", "--type", "fact", "agent 观察记录"); code != 0 {
		t.Fatalf("agent mem add: %d %s", code, errOut)
	}
	authors := readAuthors()
	if len(authors) != 2 {
		t.Fatalf("应有 2 条记忆，得 %d：%v", len(authors), authors)
	}
	seen := map[string]bool{}
	for _, a := range authors {
		seen[a] = true
	}
	if !seen["operator"] || !seen["agent"] {
		t.Fatalf("应同时存在 operator 与 agent 盖章，得 %v", authors)
	}
}
