package schedule

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newPrefetchRuntime 建一个开了预热目录的运行时（复用既有测试装配）。
func newPrefetchRuntime(t *testing.T, body string) (*Runtime, string) {
	t.Helper()
	rt, dir := newRuntime(t, body, &fakeExec{}, &fakeSubmit{})
	prefetch := filepath.Join(dir, "prefetch")
	rt.opts.PrefetchDir = prefetch
	return rt, prefetch
}

// parseItems 是测试内解析日程并折叠进运行时的辅助。
func parseItems(t *testing.T, rt *Runtime, body string) {
	t.Helper()
	items, _, err := Parse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	rt.mu.Lock()
	rt.items = items
	rt.mu.Unlock()
}

func execItemBody(id, every, exec string, prefetch *bool) string {
	flag := ""
	if prefetch != nil {
		flag = `,"prefetch":false`
		if *prefetch {
			flag = `,"prefetch":true`
		}
	}
	return `{"items":[{"id":"` + id + `","every":"` + every + `","exec":"` + exec + `"` + flag + "}]}"
}

func TestRecordPrefetchWritesOnSuccess(t *testing.T) {
	rt, dir := newPrefetchRuntime(t, `{"items":[{"id":"weather","every":"30m","exec":"curl wttr.in"}]}`)
	parseItems(t, rt, `{"items":[{"id":"weather","every":"30m","exec":"curl wttr.in"}]}`)
	p := rt.items[0]

	now := time.Now()
	rt.recordExec(p, now, ExecOutcome{ExitCode: 0, Output: "北京 晴 18°C"})

	data, err := os.ReadFile(filepath.Join(dir, "weather.json"))
	if err != nil {
		t.Fatalf("预热缓存未落盘: %v", err)
	}
	entry, err := parseEntry(data)
	if err != nil {
		t.Fatalf("缓存 JSON 解析: %v", err)
	}
	if entry.ID != "weather" || entry.Content != "北京 晴 18°C" {
		t.Fatalf("缓存内容不符: %+v", entry)
	}
	if entry.Seconds != 1800 {
		t.Fatalf("缓存周期应等于 every（1800s）: %d", entry.Seconds)
	}
	if _, err := time.Parse(time.RFC3339, entry.At); err != nil {
		t.Fatalf("at 不是 RFC3339: %v", err)
	}
}

func TestRecordPrefetchClearsOnFailureDisableEmpty(t *testing.T) {
	// 三种"不该有缓存"的情形都清掉旧文件：宁缺毋假。
	for name, outcome := range map[string]ExecOutcome{
		"执行失败": {ExitCode: 1, Err: errors.New("退出码 1")},
		"输出为空": {ExitCode: 0, Output: ""},
	} {
		rt, dir := newPrefetchRuntime(t, `{"items":[{"id":"w","every":"30m","exec":"x"}]}`)
		parseItems(t, rt, `{"items":[{"id":"w","every":"30m","exec":"x"}]}`)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "w.json"), []byte(`{"id":"w","at":"2026-01-01T00:00:00+08:00","seconds":1800,"content":"旧数据"}`), 0o644); err != nil {
			t.Fatal(err)
		}
		rt.recordExec(rt.items[0], time.Now(), outcome)
		if _, err := os.Stat(filepath.Join(dir, "w.json")); !os.IsNotExist(err) {
			t.Fatalf("%s: 旧缓存应被清除", name)
		}
	}

	// prefetch:false 显式关闭。
	rt, dir := newPrefetchRuntime(t, execItemBody("w", "30m", "x", boolPtr(false)))
	parseItems(t, rt, execItemBody("w", "30m", "x", boolPtr(false)))
	if rt.items[0].Prefetch {
		t.Fatal("prefetch:false 应折叠为 false")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "w.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	rt.recordExec(rt.items[0], time.Now(), ExecOutcome{ExitCode: 0, Output: "输出"})
	if _, err := os.Stat(filepath.Join(dir, "w.json")); !os.IsNotExist(err) {
		t.Fatal("prefetch:false 不应写缓存，且旧缓存应清除")
	}
}

func TestPrefetchEnabledByDefault(t *testing.T) {
	// 缺省开启是 ACI 零配置的根基——本测试钉死语义，防未来漂移。
	rt, _ := newPrefetchRuntime(t, `{"items":[{"id":"w","every":"30m","exec":"x"}]}`)
	parseItems(t, rt, `{"items":[{"id":"w","every":"30m","exec":"x"}]}`)
	if !rt.items[0].Prefetch {
		t.Fatal("prefetch 缺省应为 true（零配置预热）")
	}
}

func TestLoadPrefetchTTLAndOrder(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	// 新鲜（10m 前，周期 30m）+ 过期（2h 前，周期 30m）+ 坏文件。
	writeEntryFile(t, dir, "fresh", now.Add(-10*time.Minute), 1800, "新鲜内容")
	writeEntryFile(t, dir, "stale", now.Add(-2*time.Hour), 1800, "过期内容")
	writeEntryFile(t, dir, "broken", now.Add(-time.Minute), 1800, "x")
	if err := os.WriteFile(filepath.Join(dir, "broken.json"), []byte("not-json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("非 json 忽略"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := LoadPrefetch(dir, now)
	if len(got) != 1 || got[0].ID != "fresh" {
		t.Fatalf("应只保留新鲜的 fresh: %+v", got)
	}
}

func TestLoadPrefetchOrdersNewestFirst(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	writeEntryFile(t, dir, "old", now.Add(-20*time.Minute), 1800, "旧")
	writeEntryFile(t, dir, "new", now.Add(-2*time.Minute), 1800, "新")
	got := LoadPrefetch(dir, now)
	if len(got) != 2 || got[0].ID != "new" || got[1].ID != "old" {
		t.Fatalf("应新→旧排序: %+v", got)
	}
}

func TestRenderPrefetchFormat(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	writeEntryFile(t, dir, "weather", now.Add(-12*time.Minute), 1800, "北京 晴 18°C\n湿度 40%")

	seg := RenderPrefetch(dir, now)
	if seg == "" {
		t.Fatal("应有渲染段")
	}
	for _, want := range []string{
		"预取缓存",
		"不必重查",
		"[weather]",
		"12 分钟前",
		"\n  北京 晴 18°C",
		"\n  湿度 40%",
	} {
		if !strings.Contains(seg, want) {
			t.Errorf("渲染段缺 %q:\n%s", want, seg)
		}
	}

	// 空目录 → 空段。
	if empty := RenderPrefetch(t.TempDir(), now); empty != "" {
		t.Fatalf("空目录应渲染空段: %q", empty)
	}
}

func TestWritePrefetchTruncatesLongOutput(t *testing.T) {
	dir := t.TempDir()
	long := strings.Repeat("x", maxPrefetchBytes+5000)
	if err := writePrefetch(dir, "big", time.Now(), time.Minute, long); err != nil {
		t.Fatal(err)
	}
	got := LoadPrefetch(dir, time.Now())
	if len(got) != 1 {
		t.Fatalf("应有一条: %d", len(got))
	}
	if len(got[0].Content) > maxPrefetchBytes+100 {
		t.Fatalf("内容未截断: %d bytes", len(got[0].Content))
	}
	if !strings.Contains(got[0].Content, "超长截断") {
		t.Fatalf("截断标记缺失")
	}
}

// —— 辅助 ——

func parseEntry(data []byte) (PrefetchEntry, error) {
	var e PrefetchEntry
	err := json.Unmarshal(data, &e)
	return e, err
}

func writeEntryFile(t *testing.T, dir, id string, at time.Time, seconds int, content string) {
	t.Helper()
	body, err := json.Marshal(PrefetchEntry{ID: id, At: at.Format(time.RFC3339), Seconds: seconds, Content: content})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".json"), body, 0o644); err != nil {
		t.Fatal(err)
	}
}

func boolPtr(b bool) *bool { return &b }
