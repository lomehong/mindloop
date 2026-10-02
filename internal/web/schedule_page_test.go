package web

// 日程面（/api/identities/{n}/schedule）的契约测试：列表/开关/
// 手动 run/删除 + 坏文件防护。exec 真执行依赖沙箱（本机无 bash
// 时跳过）；task 路径全离线可测。

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lomehong/mindloop/internal/identity"
	"github.com/lomehong/mindloop/internal/sandbox"
	"github.com/lomehong/mindloop/internal/schedule"
	"github.com/lomehong/mindloop/internal/task"
)

// scheduleEntryJSON 是测试侧对契约的镜像（字段名与 CLI --json 同构）。
type scheduleEntryJSON struct {
	ID           string `json:"id"`
	Enabled      bool   `json:"enabled"`
	Kind         string `json:"kind"`
	Trigger      string `json:"trigger"`
	Action       string `json:"action"`
	NextRun      string `json:"next_run"`
	LastRun      string `json:"last_run"`
	LastExitCode int    `json:"last_exit_code"`
	LastError    string `json:"last_error"`
	LastNote     string `json:"last_note"`
}

// seedScheduleItems 写入日程条目文件（测试脚手架）。
func seedScheduleItems(t *testing.T, id *identity.Identity, items ...schedule.Item) {
	t.Helper()
	path, _, _ := schedule.Paths(id.Dir, id.Timeline.Dir)
	if err := schedule.SaveFile(path, schedule.File{Items: items}); err != nil {
		t.Fatal(err)
	}
}

// doReq 发一个请求并返回响应（body 为空则不带体；响应由 Cleanup 关闭）。
func doReq(t *testing.T, method, url, body string) *http.Response {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rd)
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// getJSONInto 发 GET 并把 200 响应的 JSON 解进 out。
func getJSONInto(t *testing.T, url string, out any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET %s = %d: %s", url, resp.StatusCode, raw)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		t.Fatal(err)
	}
}

// TestScheduleListEmpty 无 schedule.json：空列表、无警告、给出文件路径。
func TestScheduleListEmpty(t *testing.T) {
	_, _ = newIdentityHome(t)
	ts, _ := newTestServer(t, identity.Home(), "")
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/identities/ada/schedule")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("list = %d", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	var out struct {
		File        string              `json:"file"`
		Entries     []scheduleEntryJSON `json:"entries"`
		Warnings    []string            `json:"warnings"`
		ParseError  string              `json:"parse_error"`
		MindRunning bool                `json:"mind_running"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Entries) != 0 || len(out.Warnings) != 0 || out.ParseError != "" {
		t.Fatalf("空日程响应异常: %s", raw)
	}
	// entries/warnings 必须是空数组（不是 null）——前端直接 .map/.length。
	if !strings.Contains(string(raw), `"entries":[]`) || !strings.Contains(string(raw), `"warnings":[]`) {
		t.Fatalf("空数组形态不符: %s", raw)
	}
	if !strings.HasSuffix(out.File, "schedule.json") {
		t.Fatalf("file 指向异常: %q", out.File)
	}
}

// TestScheduleListEntriesWithState 条目 + 状态投影：exec 用投影，
// task 无投影时推算 next_run。
func TestScheduleListEntriesWithState(t *testing.T) {
	_, id := newIdentityHome(t)
	seedScheduleItems(t, id,
		schedule.Item{ID: "daily", At: "21:00", Task: "写晚报 {{date}}"},
		schedule.Item{ID: "sample", Every: "2m", Exec: "echo hi"},
	)
	runDir := filepath.Join(id.Timeline.Dir, "run")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	state := `{"updated_at":"2026-09-27 09:00:00","items":{"sample":{"last_run":"2026-09-27 10:00:00","next_run":"2026-09-27 10:02:00","last_exit_code":3,"last_error":"boom"}}}`
	if err := os.WriteFile(filepath.Join(runDir, "schedule-state.json"), []byte(state), 0o644); err != nil {
		t.Fatal(err)
	}

	ts, _ := newTestServer(t, identity.Home(), "")
	defer ts.Close()
	var out struct {
		Entries []scheduleEntryJSON `json:"entries"`
	}
	getJSONInto(t, ts.URL+"/api/identities/ada/schedule", &out)
	if len(out.Entries) != 2 {
		t.Fatalf("entries = %d，应 2", len(out.Entries))
	}
	byID := map[string]scheduleEntryJSON{}
	for _, e := range out.Entries {
		byID[e.ID] = e
	}
	daily, ok := byID["daily"]
	if !ok {
		t.Fatalf("缺 daily: %+v", out.Entries)
	}
	if daily.Kind != "task" || daily.Trigger != "at 21:00" || !daily.Enabled {
		t.Fatalf("daily 形态异常: %+v", daily)
	}
	if !strings.HasPrefix(daily.Action, "task 写晚报") {
		t.Fatalf("daily action = %q", daily.Action)
	}
	if daily.NextRun == "" {
		t.Fatalf("daily 应推算 next_run: %+v", daily)
	}
	sample, ok := byID["sample"]
	if !ok {
		t.Fatalf("缺 sample: %+v", out.Entries)
	}
	if sample.Kind != "exec" || sample.Trigger != "every 2m0s" {
		t.Fatalf("sample 形态异常: %+v", sample)
	}
	if sample.NextRun != "2026-09-27 10:02:00" || sample.LastRun != "2026-09-27 10:00:00" ||
		sample.LastExitCode != 3 || sample.LastError != "boom" {
		t.Fatalf("sample 状态投影异常: %+v", sample)
	}
}

// TestScheduleListParseError 文件级损坏：200 + parse_error（页面
// 显示横幅而不是整页失败）。
func TestScheduleListParseError(t *testing.T) {
	_, id := newIdentityHome(t)
	path, _, _ := schedule.Paths(id.Dir, id.Timeline.Dir)
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	ts, _ := newTestServer(t, identity.Home(), "")
	defer ts.Close()
	var out struct {
		Entries    []scheduleEntryJSON `json:"entries"`
		ParseError string              `json:"parse_error"`
	}
	getJSONInto(t, ts.URL+"/api/identities/ada/schedule", &out)
	if out.ParseError == "" || !strings.Contains(out.ParseError, "解析失败") {
		t.Fatalf("parse_error: %q", out.ParseError)
	}
	if len(out.Entries) != 0 {
		t.Fatalf("坏文件 entries 应为空: %+v", out.Entries)
	}
}

// TestScheduleToggle 开关只翻转 enabled：false 写显式禁用，true
// 复原缺省（字段移除）；404/405/400 边界。
func TestScheduleToggle(t *testing.T) {
	_, id := newIdentityHome(t)
	seedScheduleItems(t, id, schedule.Item{ID: "daily", At: "21:00", Task: "x"})
	path, _, _ := schedule.Paths(id.Dir, id.Timeline.Dir)
	ts, _ := newTestServer(t, identity.Home(), "")
	defer ts.Close()
	base := ts.URL + "/api/identities/ada/schedule/daily/toggle"

	// 禁用 → 字段显式 false。
	if resp := doReq(t, "POST", base, `{"enabled":false}`); resp.StatusCode != 200 {
		t.Fatalf("禁用 toggle = %d", resp.StatusCode)
	}
	file, err := schedule.LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Items) != 1 || file.Items[0].Enabled == nil || *file.Items[0].Enabled {
		t.Fatalf("禁用未落盘: %+v", file.Items)
	}

	// 启用 → 字段移除（缺省即启用）。
	if resp := doReq(t, "POST", base, `{"enabled":true}`); resp.StatusCode != 200 {
		t.Fatalf("启用 toggle = %d", resp.StatusCode)
	}
	file, err = schedule.LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Items) != 1 || file.Items[0].Enabled != nil {
		t.Fatalf("启用未复原: %+v", file.Items)
	}

	// 不存在 → 404；GET → 405；缺字段/坏体 → 400。
	if resp := doReq(t, "POST", ts.URL+"/api/identities/ada/schedule/ghost/toggle", `{"enabled":true}`); resp.StatusCode != 404 {
		t.Fatalf("ghost toggle = %d，应 404", resp.StatusCode)
	}
	if resp := doReq(t, "GET", base, ""); resp.StatusCode != 405 {
		t.Fatalf("GET toggle = %d，应 405", resp.StatusCode)
	}
	if resp := doReq(t, "POST", base, `{}`); resp.StatusCode != 400 {
		t.Fatalf("缺字段 toggle = %d，应 400", resp.StatusCode)
	}
	if resp := doReq(t, "POST", base, "not json"); resp.StatusCode != 400 {
		t.Fatalf("坏体 toggle = %d，应 400", resp.StatusCode)
	}
}

// TestScheduleRemove 删除条目；不存在 404。
func TestScheduleRemove(t *testing.T) {
	_, id := newIdentityHome(t)
	seedScheduleItems(t, id,
		schedule.Item{ID: "daily", At: "21:00", Task: "x"},
		schedule.Item{ID: "sample", Every: "2m", Exec: "echo hi"},
	)
	path, _, _ := schedule.Paths(id.Dir, id.Timeline.Dir)
	ts, _ := newTestServer(t, identity.Home(), "")
	defer ts.Close()

	resp := doReq(t, "DELETE", ts.URL+"/api/identities/ada/schedule/sample", "")
	if resp.StatusCode != 200 {
		t.Fatalf("remove = %d", resp.StatusCode)
	}
	var out struct {
		OK      bool   `json:"ok"`
		Removed string `json:"removed"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if !out.OK || out.Removed != "sample" {
		t.Fatalf("remove 响应: %+v", out)
	}
	file, err := schedule.LoadFile(path)
	if err != nil || len(file.Items) != 1 || file.Items[0].ID != "daily" {
		t.Fatalf("删除后文件: %v %+v", err, file.Items)
	}
	if resp := doReq(t, "DELETE", ts.URL+"/api/identities/ada/schedule/ghost", ""); resp.StatusCode != 404 {
		t.Fatalf("ghost remove = %d，应 404", resp.StatusCode)
	}
}

// TestScheduleRunTask task 手动触发：幂等键与到点一致、内容渲染、
// 同一天重复 run 不产生第二个任务。
func TestScheduleRunTask(t *testing.T) {
	_, id := newIdentityHome(t)
	seedScheduleItems(t, id, schedule.Item{ID: "daily", At: "21:00", Task: "写晚报 {{date}}"})
	ts, _ := newTestServer(t, identity.Home(), "")
	defer ts.Close()
	runURL := ts.URL + "/api/identities/ada/schedule/daily/run"

	resp := doReq(t, "POST", runURL, "")
	if resp.StatusCode != 200 {
		t.Fatalf("run = %d", resp.StatusCode)
	}
	var out struct {
		OK      bool   `json:"ok"`
		Kind    string `json:"kind"`
		Note    string `json:"note"`
		TaskKey string `json:"task_key"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	today := time.Now().Format("2006-01-02")
	if !out.OK || out.Kind != "task" || out.TaskKey != "sched-daily-"+today+"-2100" {
		t.Fatalf("run 响应: %+v", out)
	}

	store := task.New(id.Timeline, id.Name)
	items, err := store.List(context.Background())
	if err != nil || len(items) != 1 {
		t.Fatalf("任务应恰好一条: err=%v n=%d", err, len(items))
	}
	if items[0].From != schedule.SourceName || items[0].ClientMessageID != "sched-daily-"+today+"-2100" {
		t.Fatalf("任务来源/幂等键不符: %+v", items[0])
	}
	// 内容 = 模板渲染 + at 条目的完成回执注入行（Phase 2）。
	if !strings.HasPrefix(items[0].Content, "写晚报 "+today) ||
		!strings.Contains(items[0].Content, "完成后向 operator 发一条") {
		t.Fatalf("任务内容未渲染: %q", items[0].Content)
	}

	// 二次 run：同载荷幂等，不产生第二个任务。
	if resp := doReq(t, "POST", runURL, ""); resp.StatusCode != 200 {
		t.Fatalf("二次 run = %d", resp.StatusCode)
	}
	if items, _ = store.List(context.Background()); len(items) != 1 {
		t.Fatalf("二次 run 后任务数 = %d，应 1", len(items))
	}
}

// TestScheduleRunExec exec 手动触发：沙箱真执行 + 日志落一行。
func TestScheduleRunExec(t *testing.T) {
	if _, err := sandbox.BashPath(); err != nil {
		t.Skip("本机无 bash，跳过 exec 真实执行")
	}
	_, id := newIdentityHome(t)
	seedScheduleItems(t, id, schedule.Item{ID: "sample", Every: "1h", Exec: "echo hello"})
	ts, _ := newTestServer(t, identity.Home(), "")
	defer ts.Close()

	resp := doReq(t, "POST", ts.URL+"/api/identities/ada/schedule/sample/run", "")
	if resp.StatusCode != 200 {
		t.Fatalf("exec run = %d", resp.StatusCode)
	}
	var out struct {
		OK         bool   `json:"ok"`
		Kind       string `json:"kind"`
		ExitCode   int    `json:"exit_code"`
		DurationMS int64  `json:"duration_ms"`
		Error      string `json:"error"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if !out.OK || out.Kind != "exec" || out.ExitCode != 0 {
		t.Fatalf("exec run 响应: %+v", out)
	}
	log, err := os.ReadFile(filepath.Join(id.Timeline.Dir, "run", "schedule.log"))
	if err != nil || !strings.Contains(string(log), "手动执行完成") {
		t.Fatalf("schedule.log 缺少手动执行记录: %v %q", err, log)
	}
}

// TestScheduleRunBadEntry 文件里存在但解析被跳过的条目：400 带
// 原因（不是 404——误导用户去加条目）；不存在的 id 才 404。
func TestScheduleRunBadEntry(t *testing.T) {
	_, id := newIdentityHome(t)
	seedScheduleItems(t, id, schedule.Item{ID: "broken", At: "9:00", Task: "x"})
	ts, _ := newTestServer(t, identity.Home(), "")
	defer ts.Close()

	resp := doReq(t, "POST", ts.URL+"/api/identities/ada/schedule/broken/run", "")
	if resp.StatusCode != 400 {
		t.Fatalf("坏条目 run = %d，应 400", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), "条目无效") {
		t.Fatalf("坏条目消息: %s", raw)
	}
	if resp := doReq(t, "POST", ts.URL+"/api/identities/ada/schedule/ghost/run", ""); resp.StatusCode != 404 {
		t.Fatalf("ghost run = %d，应 404", resp.StatusCode)
	}
}

// TestScheduleWriteGuards 文件级损坏：toggle/remove/run 一律拒绝
// （409），且原文件保持原样——用户的坏文件不能被 web 覆盖。
func TestScheduleWriteGuards(t *testing.T) {
	_, id := newIdentityHome(t)
	path, _, _ := schedule.Paths(id.Dir, id.Timeline.Dir)
	if err := os.WriteFile(path, []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	ts, _ := newTestServer(t, identity.Home(), "")
	defer ts.Close()

	if resp := doReq(t, "POST", ts.URL+"/api/identities/ada/schedule/x/toggle", `{"enabled":false}`); resp.StatusCode != 409 {
		t.Fatalf("坏文件 toggle = %d，应 409", resp.StatusCode)
	}
	if resp := doReq(t, "DELETE", ts.URL+"/api/identities/ada/schedule/x", ""); resp.StatusCode != 409 {
		t.Fatalf("坏文件 remove = %d，应 409", resp.StatusCode)
	}
	if resp := doReq(t, "POST", ts.URL+"/api/identities/ada/schedule/x/run", ""); resp.StatusCode != 409 {
		t.Fatalf("坏文件 run = %d，应 409", resp.StatusCode)
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != "{broken" {
		t.Fatalf("坏文件被改写: %v %q", err, raw)
	}
}
