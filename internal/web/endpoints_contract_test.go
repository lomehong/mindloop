package web

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"mindloop/internal/identity"
	"mindloop/internal/traj"
)

// 本文件补齐 viewer 两个高频只读端点的基础契约测试
// （这两个端点此前没有任何直接测试覆盖，而首页启动与身份状态栏
// 都消费它们的字段，回归即崩）：
//   - GET /api/config          → viewer 的 Config 形态
//   - GET /api/identities/{id}/status → viewer 的 IdentityStatus 形态
// 断言只钉 viewer 依赖的字段与取值，不钉 map 的键序。

// TestConfigEndpointContract：config 端点必须给出 viewer 启动所需的
// 全部字段——root 指向状态根、version 非空、controls_enabled 为真；
// 本后端产不出的字段给合理空值（viewer 按 falsy 处理），但键必须在。
func TestConfigEndpointContract(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	ts, _ := newTestServer(t, identity.Home(), "")

	resp, err := http.Get(ts.URL + "/api/config")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("GET /api/config = %d", resp.StatusCode)
	}
	var out struct {
		Root             string  `json:"root"`
		Version          string  `json:"version"`
		ControlsEnabled  bool    `json:"controls_enabled"`
		SelfUpdateEnable bool    `json:"self_update_enabled"`
		DefaultSendFrom  *string `json:"default_send_from"`
		GitCommit        *string `json:"git_commit"`
		GitBranch        *string `json:"git_branch"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("响应不是期望的 Config 形态: %v", err)
	}
	if out.Root != identity.Home() {
		t.Fatalf("root = %q，应为状态根 %q", out.Root, identity.Home())
	}
	if out.Version == "" {
		t.Fatal("version 不应为空")
	}
	if !out.ControlsEnabled {
		t.Fatal("controls_enabled 应为 true")
	}
}

// TestIdentityStatusEndpointContract：status 契约的两种状态。
// 刚建身份+落步骤 → mindlog 新鲜 → live（30s 心跳窗，liveness.go）；
// 把 mindlog mtime 回拨出窗 → 非 live。
// dispatcher_pid 恒为 null（精确 pid 由 mind run 的 owner.json 提供，
// 该端点不做进程探测）。
//
// 历史：本用例曾钓出 mtime 兜底判据失效的缺陷——isIdentityLive 的
// 参数语义是轨迹目录（web 全部调用点传 Timeline.Dir），旧实现却去
// 读其下不存在的 trajectories/ 子目录，导致生产中 live 恒 false；
// 已由 task-9 修复（改为直接 stat 轨迹目录内的 trajectory.jsonl）。
// 本用例的两个状态就是给这条判据的回归防线。
func TestIdentityStatusEndpointContract(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	id, err := identity.Create(t.Context(), "ada")
	if err != nil {
		t.Fatal(err)
	}
	// 落一条消息：step_count 应含头行 + message = 2。
	s := traj.NewStep("message")
	s.Fields["from"] = "you"
	s.Fields["content"] = "hello"
	if err := id.Timeline.Append(t.Context(), s); err != nil {
		t.Fatal(err)
	}

	ts, _ := newTestServer(t, identity.Home(), "")
	fetch := func() map[string]any {
		t.Helper()
		resp, err := http.Get(ts.URL + "/api/identities/ada/status")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("GET status = %d", resp.StatusCode)
		}
		var m map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
			t.Fatalf("响应不是对象: %v", err)
		}
		return m
	}

	fresh := fetch()
	if fresh["live"] != true || fresh["pid_alive"] != true {
		t.Fatalf("新鲜轨迹应判定 live——若为 false，检查 isIdentityLive 的 mtime 判据是否仍指向 trajectory.jsonl: %v", fresh)
	}
	if fresh["dispatcher_pid"] != nil {
		t.Fatalf("dispatcher_pid 应为 null: %v", fresh)
	}
	if n, ok := fresh["step_count"].(float64); !ok || n != 2 {
		t.Fatalf("step_count = %v，应为 2（头行 + message）", fresh["step_count"])
	}
	if bytesVal, ok := fresh["mindlog_bytes"].(float64); !ok || bytesVal <= 0 {
		t.Fatalf("mindlog_bytes 应为正数: %v", fresh["mindlog_bytes"])
	}
	if mt, ok := fresh["mindlog_mtime"].(string); !ok || mt == "" {
		t.Fatalf("mindlog_mtime 应为非空字符串: %v", fresh["mindlog_mtime"])
	}
	if _, err := time.Parse(traj.TimeFormat, fmtAnyString(fresh["mindlog_mtime"])); err != nil {
		t.Fatalf("mindlog_mtime 应可按 traj.TimeFormat 解析: %v", err)
	}

	// mindlog（trajectory.jsonl）mtime 回拨出 30s 心跳窗 → live=false。
	// （isIdentityLive 的 mtime 兜底判据：stat 轨迹目录内的
	// trajectory.jsonl——task-9 修复后与 Timeline.Dir 参数语义一致。）
	past := time.Now().Add(-time.Minute)
	if err := os.Chtimes(id.Timeline.Path, past, past); err != nil {
		t.Skipf("无法回拨 mtime: %v", err)
	}
	stale := fetch()
	if stale["live"] != false || stale["pid_alive"] != false {
		t.Fatalf("出窗后的轨迹应判定非 live: %v", stale)
	}
	if n, ok := stale["step_count"].(float64); !ok || n != 2 {
		t.Fatalf("回拨不改变 step_count: %v", stale["step_count"])
	}
}

// fmtAnyString 把 decode 出的 any 断言为字符串（测试内专用）。
func fmtAnyString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
