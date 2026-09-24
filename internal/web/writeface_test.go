package web

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mindloop/internal/identity"
	"mindloop/internal/mind"
	"mindloop/internal/traj"
)

// newIdentityHome 建立隔离身份根并创建 ada。
func newIdentityHome(t *testing.T) (string, *identity.Identity) {
	t.Helper()
	t.Setenv("MINDLOOP_HOME", t.TempDir())
	id, err := identity.Create(context.Background(), "ada")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return id.Dir, id
}

// ---------- D: 用量聚合 ----------

func TestUsageAggregatesLedger(t *testing.T) {
	dir, _ := newIdentityHome(t)
	usageDir := filepath.Join(dir, "usage")
	if err := os.MkdirAll(usageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"ts":"2026-09-23T10:00:00.000Z","prompt_tokens":100,"completion_tokens":50,"model":"glm-5","provider":"openai-compatible"}`,
		`{"ts":"2026-09-23T11:00:00.000Z","prompt_tokens":200,"completion_tokens":80,"model":"glm-5"}`,
		`{"ts":"2026-09-24T09:00:00.000Z","prompt_tokens":50,"completion_tokens":10,"model":"gpt-4o-mini","error":"429"}`,
		`not-json-line`,
	}
	if err := os.WriteFile(filepath.Join(usageDir, "llm-usage.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ts, _ := newTestServer(t, identity.Home(), "")
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/api/identities/ada/usage")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Available bool     `json:"available"`
		Rows      int      `json:"rows"`
		Skipped   int      `json:"skipped"`
		Daily     [][2]any `json:"daily"`
		ByModel   map[string]struct {
			Calls int `json:"calls"`
			In    int `json:"in"`
			Out   int `json:"out"`
		} `json:"by_model"`
		Totals struct {
			Calls int `json:"calls"`
			In    int `json:"in"`
			Out   int `json:"out"`
		} `json:"totals"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if !out.Available || out.Rows != 3 || out.Skipped != 1 {
		t.Fatalf("available=%v rows=%d skipped=%d（应 true/3/1）", out.Available, out.Rows, out.Skipped)
	}
	if len(out.Daily) != 2 {
		t.Fatalf("daily 应有 2 天，得到 %d", len(out.Daily))
	}
	if out.ByModel["glm-5"].Calls != 2 || out.ByModel["glm-5"].In != 300 {
		t.Fatalf("by_model[glm-5] = %+v（应 calls=2 in=300）", out.ByModel["glm-5"])
	}
	if out.Totals.Calls != 3 || out.Totals.In != 350 {
		t.Fatalf("totals = %+v（应 calls=3 in=350）", out.Totals)
	}
}

// ---------- E: 导出 / 导入 ----------

func TestExportImportRoundTrip(t *testing.T) {
	_, id := newIdentityHome(t)
	addMindMessage(t, id, "导出前的记忆消息")

	ts, _ := newTestServer(t, identity.Home(), "")
	defer ts.Close()

	// 导出
	resp, err := http.Get(ts.URL + "/api/export")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Type"), "gzip") {
		t.Fatalf("导出失败: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	body := new(bytes.Buffer)
	body.ReadFrom(resp.Body)
	if body.Len() < 100 {
		t.Fatalf("导出体过小: %d 字节", body.Len())
	}
	// 归档必须包含 identity.txt
	gr, err := gzip.NewReader(bytes.NewReader(body.Bytes()))
	if err != nil {
		t.Fatalf("gzip 解开失败: %v", err)
	}
	tr := tar.NewReader(gr)
	foundMeta := false
	for {
		hdr, err := tr.Next()
		if err != nil {
			break
		}
		if strings.HasSuffix(hdr.Name, "identity.txt") {
			foundMeta = true
		}
	}
	if !foundMeta {
		t.Fatal("归档缺少 identity.txt")
	}

	// 导入（重命名）
	req, _ := http.NewRequest("POST", ts.URL+"/api/identities/import?name=ada2", bytes.NewReader(body.Bytes()))
	req.Header.Set("Content-Type", "application/gzip")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	var imp struct {
		OK       bool `json:"ok"`
		Imported []struct {
			Name string `json:"name"`
		} `json:"imported"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&imp); err != nil {
		t.Fatal(err)
	}
	if !imp.OK || len(imp.Imported) != 1 || imp.Imported[0].Name != "ada2" {
		t.Fatalf("imported=%+v", imp)
	}
	// 导入后的副本轨迹可读且含原消息
	cp, err := identity.Load("ada2")
	if err != nil {
		t.Fatalf("Load(ada2): %v", err)
	}
	steps, err := cp.Timeline.Steps()
	if err != nil || len(steps) < 2 {
		t.Fatalf("导入副本步骤 = %v, %v", len(steps), err)
	}
}

func TestImportRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	ts, _ := newTestServer(t, identity.Home(), "")
	defer ts.Close()

	// 构造含 ../ 的恶意 tar.gz
	buf := new(bytes.Buffer)
	gz := gzip.NewWriter(buf)
	tw := tar.NewWriter(gz)
	content := []byte("evil")
	hdr := &tar.Header{
		Name:     "identities/../../evil.txt",
		Mode:     0o644,
		Size:     int64(len(content)),
		Typeflag: tar.TypeReg,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	tw.Write(content)
	tw.Close()
	gz.Close()

	req, _ := http.NewRequest("POST", ts.URL+"/api/identities/import", bytes.NewReader(buf.Bytes()))
	req.Header.Set("Content-Type", "application/gzip")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 200 {
		t.Fatal("穿越路径的归档必须被拒绝")
	}
	if _, err := os.Stat(filepath.Join(dir, "..", "evil.txt")); err == nil {
		t.Fatal("evil.txt 被落盘了！")
	}
}

// ---------- E: env PUT / DELETE ----------

func TestEnvPutAndDelete(t *testing.T) {
	_, id := newIdentityHome(t)
	ts, _ := newTestServer(t, identity.Home(), "")
	defer ts.Close()

	// PUT 新键
	putBody, _ := json.Marshal(map[string]string{"key": "MINDLOOP_MODEL", "value": "glm-5"})
	req, _ := http.NewRequest("PUT", ts.URL+"/api/identities/ada/env", bytes.NewReader(putBody))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("PUT env = %d", resp.StatusCode)
	}
	data, err := os.ReadFile(filepath.Join(id.Dir, ".env"))
	if err != nil || !strings.Contains(string(data), "MINDLOOP_MODEL=glm-5") {
		t.Fatalf("env 未写入: %q, %v", data, err)
	}

	// 敏感键掩码
	putBody2, _ := json.Marshal(map[string]string{"key": "MINDLOOP_API_KEY", "value": "sk-supersecretvalue"})
	req2, _ := http.NewRequest("PUT", ts.URL+"/api/identities/ada/env", bytes.NewReader(putBody2))
	req2.Header.Set("Content-Type", "application/json")
	resp2, _ := http.DefaultClient.Do(req2)
	var entry struct {
		Value  string `json:"value"`
		Secret bool   `json:"secret"`
	}
	json.NewDecoder(resp2.Body).Decode(&entry)
	resp2.Body.Close()
	if !entry.Secret || strings.Contains(entry.Value, "supersecretvalue") {
		t.Fatalf("敏感键未掩码: %+v", entry)
	}

	// DELETE
	req3, _ := http.NewRequest("DELETE", ts.URL+"/api/identities/ada/env/MINDLOOP_MODEL", nil)
	resp3, _ := http.DefaultClient.Do(req3)
	resp3.Body.Close()
	if resp3.StatusCode != 200 {
		t.Fatalf("DELETE env = %d", resp3.StatusCode)
	}
	data, _ = os.ReadFile(filepath.Join(id.Dir, ".env"))
	if strings.Contains(string(data), "MINDLOOP_MODEL") {
		t.Fatal("键未被删除")
	}
}

// ---------- F: probe 真探测 ----------

func TestProbeRealCall(t *testing.T) {
	t.Setenv("MINDLOOP_PROVIDER", "echo") // 无网真实路径：echo 也是真走 Complete
	ts, _ := newTestServer(t, t.TempDir(), "")
	defer ts.Close()
	resp, err := http.Post(ts.URL+"/api/llm-health/probe", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		OK        bool   `json:"ok"`
		LatencyMS int    `json:"latency_ms"`
		Model     string `json:"model"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if !out.OK {
		t.Fatalf("echo 探测应成功: %+v", out)
	}
}

// ---------- B: thinkers 控制面 ----------

func TestThinkerControlSignals(t *testing.T) {
	dir, _ := newIdentityHome(t)
	tl := filepath.Join(dir, "trajectories")

	// 禁用名单
	if err := mind.SetThinkerEnabled(tl, "monolith", false); err != nil {
		t.Fatal(err)
	}
	if !mind.IsThinkerDisabled(tl, "monolith") {
		t.Fatal("禁用未生效")
	}
	if mind.IsThinkerDisabled(tl, "responder") {
		t.Fatal("误禁用了其他 thinker")
	}
	// 重新启用 → 名单清空
	if err := mind.SetThinkerEnabled(tl, "monolith", true); err != nil {
		t.Fatal(err)
	}
	if mind.IsThinkerDisabled(tl, "monolith") {
		t.Fatal("启用未生效")
	}

	// 唤醒信号
	if err := mind.SignalWake(tl, "responder"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(tl, "run", "wake.responder")); err != nil {
		t.Fatalf("唤醒信号文件缺失: %v", err)
	}
	// 消费后文件消失（通过信号计数函数——dispatcher 内部消费；
	// 这里验证信号文件的存在与可清理）
	if err := os.Remove(filepath.Join(tl, "run", "wake.responder")); err != nil {
		t.Fatal(err)
	}
}

// ---------- A: dispatch 事件流 ----------

func TestDispatchLogWriteAndRead(t *testing.T) {
	dir, _ := newIdentityHome(t)
	tl := filepath.Join(dir, "trajectories")

	// 模拟调度器写事件
	evlog := mind.NewDispatchLogForTest(tl)
	evlog.Append(map[string]any{"kind": "dispatch", "type": "message", "thinker": "monolith", "ts": traj.NowString()})
	evlog.Append(map[string]any{"kind": "step", "type": "monolith-wake", "thinker": "monolith", "synthetic": true, "ts": traj.NowString()})

	events, err := mind.ReadEvents(mind.DispatchLogPath(tl))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d，应为 2", len(events))
	}
	if events[0]["kind"] != "dispatch" || events[1]["thinker"] != "monolith" {
		t.Fatalf("事件内容错位: %+v", events)
	}
}

// ---------- 小工具 ----------

func addMindMessage(t *testing.T, id *identity.Identity, content string) {
	t.Helper()
	s := traj.NewStep("message")
	s.Fields["from"] = "operator"
	s.Fields["to"] = id.Name
	s.Fields["content"] = content
	if err := id.Timeline.Append(context.Background(), s); err != nil {
		t.Fatal(err)
	}
}
