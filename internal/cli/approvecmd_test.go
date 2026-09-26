package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mindloop/internal/policy"
	"mindloop/internal/traj"
)

// seedPendingApproval 在轨迹目录的审批控制面按下盘格式写入一条待批
// 请求，返回完整哈希——与 policy.Gate 的落盘同构（文件名与内嵌哈希
// 一致是控制面的硬校验）。
func seedPendingApproval(t *testing.T, tlDir, script string) string {
	t.Helper()
	dir := policy.Dir(tlDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	hash := policy.ScriptHash(script)
	p := policy.PendingRequest{
		Hash: hash, Script: script, WorkDir: `D:\work\x`,
		RunID: "run-9", TaskID: "task-1", Attempt: 2,
		Created: time.Now().UTC(), Expires: time.Now().Add(10 * time.Minute).UTC(),
		Risks: policy.RiskNotes(script),
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "request-"+hash+".json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return hash
}

// readDecision 读控制面里的决定文件（不存在返回空串）。
func readDecision(t *testing.T, tlDir, hash string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(policy.Dir(tlDir), "decision-"+hash+".json"))
	if err != nil {
		return ""
	}
	var d struct {
		Decision string `json:"decision"`
	}
	if json.Unmarshal(data, &d) != nil {
		return ""
	}
	return d.Decision
}

// TestApproveListsPendingScripts：无哈希时列出全部待批脚本——含哈希
// 前缀、工作目录、运行归属、风险提示与完整脚本正文（"执行前展示
// 正文"是 ask 策略的定义）。
func TestApproveListsPendingScripts(t *testing.T) {
	id := taskTestIdentity(t)
	hash := seedPendingApproval(t, id.Timeline.Dir, "rm -rf build/\necho done")
	code, out, errOut := runCLI(t, "approve", "ada")
	if code != 0 {
		t.Fatalf("approve 列表失败: code=%d stderr=%s", code, errOut)
	}
	for _, want := range []string{hash[:12], `D:\work\x`, "run-9", "rm -rf build/", "删除操作"} {
		if !strings.Contains(out, want) {
			t.Fatalf("列表缺少 %q: %q", want, out)
		}
	}
}

// TestApproveWritesDecision：给出哈希前缀即批准——决定以原子文件写
// 回控制面（等待方消费后删除；这里只验证写盘事实）。
func TestApproveWritesDecision(t *testing.T) {
	id := taskTestIdentity(t)
	hash := seedPendingApproval(t, id.Timeline.Dir, "echo hi")
	code, out, errOut := runCLI(t, "approve", "ada", hash[:8])
	if code != 0 {
		t.Fatalf("approve 失败: code=%d stderr=%s", code, errOut)
	}
	if !strings.Contains(out, "已批准") {
		t.Fatalf("应报告已批准: %q", out)
	}
	if got := readDecision(t, id.Timeline.Dir, hash); got != "approve" {
		t.Fatalf("决定 = %q，应为 approve", got)
	}
}

// TestApproveDenyWritesDecision：--deny 写拒绝决定。
func TestApproveDenyWritesDecision(t *testing.T) {
	id := taskTestIdentity(t)
	hash := seedPendingApproval(t, id.Timeline.Dir, "echo hi")
	code, out, errOut := runCLI(t, "approve", "ada", hash[:8], "--deny")
	if code != 0 {
		t.Fatalf("approve --deny 失败: code=%d stderr=%s", code, errOut)
	}
	if !strings.Contains(out, "已拒绝") {
		t.Fatalf("应报告已拒绝: %q", out)
	}
	if got := readDecision(t, id.Timeline.Dir, hash); got != "deny" {
		t.Fatalf("决定 = %q，应为 deny", got)
	}
}

// TestApproveUnknownHashFails：没有匹配的待批请求时明确失败——决定
// 必须有对象，猜测不是便利。
func TestApproveUnknownHashFails(t *testing.T) {
	taskTestIdentity(t)
	code, _, errOut := runCLI(t, "approve", "ada", "deadbeef")
	if code != 1 {
		t.Fatalf("未知哈希 exit = %d，应为 1", code)
	}
	if !strings.Contains(errOut, "没有待批请求匹配") {
		t.Fatalf("应说明未命中: %q", errOut)
	}
}

// TestApproveNoPendingFriendly：没有待批是正常状态，如实说明而不是
// 报错。
func TestApproveNoPendingFriendly(t *testing.T) {
	taskTestIdentity(t)
	code, out, errOut := runCLI(t, "approve", "ada")
	if code != 0 {
		t.Fatalf("无待批时 exit = %d，应为 0（stderr=%s）", code, errOut)
	}
	if !strings.Contains(out, "没有") {
		t.Fatalf("应如实说明没有待批: %q", out)
	}
}

// TestApproveResolvesTrajectoryID：目标解析兼容两种入口——身份名
// （心智场景，根轨迹在身份目录下）与全局轨迹 id（run 场景，轨迹在
// 全局 TrajRoot 下）。
func TestApproveResolvesTrajectoryID(t *testing.T) {
	newTestHome(t)
	tl, err := traj.Create(context.Background(), "approve-scope")
	if err != nil {
		t.Fatal(err)
	}
	hash := seedPendingApproval(t, tl.Dir, "echo hi")
	code, out, errOut := runCLI(t, "approve", tl.ID, hash[:8])
	if code != 0 {
		t.Fatalf("按轨迹 id 批准失败: code=%d stderr=%s", code, errOut)
	}
	if !strings.Contains(out, "已批准") {
		t.Fatalf("应报告已批准: %q", out)
	}
	if got := readDecision(t, tl.Dir, hash); got != "approve" {
		t.Fatalf("决定 = %q，应为 approve", got)
	}
}
