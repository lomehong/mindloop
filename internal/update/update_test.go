package update

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHasNewer(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"0.15.0", "v0.16.0", true},
		{"0.15.0", "0.15.0", false},     // 相等不升级
		{"v0.15.0", "0.15.0", false},    // 前缀归一
		{"0.15.0", "0.14.9", false},     // 更旧
		{"0.9.0", "0.10.0", true},       // 逐段数字比较（9 < 10）
		{"0.15.0", "0.16.1", true},      // 补丁位
		{"0.15.0", "1.0.0", true},       // 大版本
		{"0.15.0", "0.15.0-rc1", false}, // 非数字段按 0：不误报
	}
	for _, c := range cases {
		if got := HasNewer(c.current, c.latest); got != c.want {
			t.Errorf("HasNewer(%q, %q) = %v, want %v", c.current, c.latest, got, c.want)
		}
	}
}

// releaseServer 起一个假 GitHub API：releases/latest 返回给定 release。
func releaseServer(t *testing.T, tag, notes string, assets []Asset) *httptest.Server {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"tag_name":     tag,
		"html_url":     "https://github.com/lomehong/mindloop/releases/tag/" + tag,
		"published_at": "2026-10-16T00:00:00Z",
		"body":         notes,
		"assets":       assets,
	})
	if err != nil {
		t.Fatal(err)
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
}

// fakeFetch 让 Checker 的检测打到 httptest——fetch 是注入点。
func fakeFetch(srv *httptest.Server) func(context.Context, string) (Release, error) {
	return func(_ context.Context, _ string) (Release, error) {
		resp, err := srv.Client().Get(srv.URL + "/repos/lomehong/mindloop/releases/latest")
		if err != nil {
			return Release{}, err
		}
		defer resp.Body.Close()
		var raw struct {
			TagName string  `json:"tag_name"`
			HTMLURL string  `json:"html_url"`
			Body    string  `json:"body"`
			Assets  []Asset `json:"assets"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
			return Release{}, err
		}
		return Release{Tag: raw.TagName, URL: raw.HTMLURL, Notes: raw.Body, Assets: raw.Assets}, nil
	}
}

func TestCheckerCheckNow(t *testing.T) {
	assets := []Asset{
		{Name: "mindloop_windows_amd64.zip", URL: "https://example.invalid/zip"},
		{Name: "checksums.txt", URL: "https://example.invalid/sums"},
	}
	srv := releaseServer(t, "v0.16.0", "升级检测落地", assets)
	defer srv.Close()
	c := NewChecker("0.15.0", "lomehong/mindloop", 0)
	c.fetch = fakeFetch(srv)

	info := c.CheckNow(context.Background())
	if info.Error != "" {
		t.Fatalf("检测出错: %s", info.Error)
	}
	if !info.HasUpdate || info.LatestTag != "v0.16.0" {
		t.Errorf("HasUpdate=%v LatestTag=%q, want true/v0.16.0", info.HasUpdate, info.LatestTag)
	}
	if info.Current != "0.15.0" || info.URL == "" || info.Notes != "升级检测落地" {
		t.Errorf("读数不全: %+v", info)
	}
	if got := c.Snapshot(); got.LatestTag != "v0.16.0" {
		t.Errorf("Snapshot 未缓存: %+v", got)
	}
}

func TestCheckerCheckNowError(t *testing.T) {
	c := NewChecker("0.15.0", "lomehong/mindloop", 0)
	c.fetch = func(_ context.Context, _ string) (Release, error) {
		return Release{}, fmt.Errorf("GitHub API 403")
	}
	info := c.CheckNow(context.Background())
	if info.Error == "" {
		t.Fatal("失败必须如实携带 error")
	}
	if info.HasUpdate {
		t.Fatal("失败不得谎报有更新")
	}
}

// makeZip 造一个含指定文件的 zip 并返回其 sha256。
func makeZip(t *testing.T, dir, name string, files map[string]string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for n, content := range files {
		w, err := zw.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func sha256OfFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func serveBytes(t *testing.T, path string, data []byte) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc(path, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(data)
	})
	return httptest.NewServer(mux)
}

func TestStageAndReplace(t *testing.T) {
	dir := t.TempDir()
	zipData := "NEW-MINDLOOP-BINARY-ZIP-PLACEHOLDER"
	// 真实 zip：内含 mindloop.exe / robotd.exe 两个条目。
	zipPath := makeZip(t, dir, "mindloop_windows_amd64.zip", map[string]string{
		"mindloop.exe": "NEW-MINDLOOP-BINARY",
		"robotd.exe":   "NEW-ROBOTD-BINARY",
	})
	zipBytes, err := os.ReadFile(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	_ = zipData
	sum := sha256OfFile(t, zipPath)
	srv := serveBytes(t, "/", []byte(sum+"  mindloop_windows_amd64.zip\n"))
	defer srv.Close()
	zipSrv := serveBytes(t, "/", zipBytes)
	defer zipSrv.Close()

	rel := Release{Tag: "v0.16.0", Assets: []Asset{
		{Name: "mindloop_windows_amd64.zip", URL: zipSrv.URL + "/"},
		{Name: "checksums.txt", URL: srv.URL + "/"},
	}}
	env := Environment{Version: "0.15.0", GOOS: "windows", GOARCH: "amd64"}

	staged, err := Stage(context.Background(), nil, rel, env, t.TempDir())
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if staged.Mindloop == "" || staged.Robotd == "" {
		t.Fatalf("解压不全: %+v", staged)
	}

	// 现行二进制（假件）→ 替换舞步。
	live := filepath.Join(t.TempDir(), "mindloop.exe")
	if err := os.WriteFile(live, []byte("OLD-BINARY"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceBinary(live, staged.Mindloop); err != nil {
		t.Fatalf("ReplaceBinary: %v", err)
	}
	got, err := os.ReadFile(live)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "NEW-MINDLOOP-BINARY" {
		t.Fatalf("替换后内容不符: %q", got)
	}
	// 旧版保留在 .old（回滚保险），内容是旧版。
	old, err := os.ReadFile(live + ".old")
	if err != nil || string(old) != "OLD-BINARY" {
		t.Fatalf(".old 保险缺失或内容不符: %v %q", err, old)
	}

	// 校验失败必须拒绝（宁可不升级）。
	badSums := serveBytes(t, "/", []byte("deadbeef  mindloop_windows_amd64.zip\n"))
	defer badSums.Close()
	relBad := Release{Tag: "v0.16.0", Assets: []Asset{
		{Name: "mindloop_windows_amd64.zip", URL: zipSrv.URL + "/"},
		{Name: "checksums.txt", URL: badSums.URL + "/"},
	}}
	if _, err := Stage(context.Background(), nil, relBad, env, t.TempDir()); err == nil {
		t.Fatal("校验失败必须报错")
	}
}

func TestPickAssets(t *testing.T) {
	assets := []Asset{
		{Name: "mindloop_darwin_arm64.zip"},
		{Name: "mindloop_windows_amd64.zip"},
		{Name: "mindloop_0.16.0_x64-setup.exe", Size: 12},
		{Name: "checksums.txt"},
	}
	if a := PickAsset(assets, "windows", "amd64"); a == nil || a.Name != "mindloop_windows_amd64.zip" {
		t.Errorf("PickAsset zip: %+v", a)
	}
	if a := PickAsset(assets, "linux", "arm64"); a != nil {
		t.Errorf("不存在的平台应返回 nil: %+v", a)
	}
	if a := PickInstaller(assets); a == nil || !strings.HasSuffix(a.Name, "-setup.exe") {
		t.Errorf("PickInstaller: %+v", a)
	}
	if a := PickChecksums(assets); a == nil || a.Name != "checksums.txt" {
		t.Errorf("PickChecksums: %+v", a)
	}
}

func TestExcerpt(t *testing.T) {
	long := strings.Repeat("字", 500)
	got := excerpt(long, 280)
	if !strings.HasSuffix(got, "…") || len([]rune(strings.TrimSuffix(got, "…"))) != 280 {
		t.Errorf("excerpt 截断不符: %d", len([]rune(got)))
	}
	if got := excerpt("短", 280); got != "短" {
		t.Errorf("excerpt 短文不应截断: %q", got)
	}
}

func TestLookupChecksum(t *testing.T) {
	sums := "abc123  mindloop_windows_amd64.zip\ndef456  checksums.txt\n"
	if got, ok := lookupChecksum(sums, "mindloop_windows_amd64.zip"); !ok || got != "abc123" {
		t.Errorf("lookupChecksum = %q %v", got, ok)
	}
	if _, ok := lookupChecksum(sums, "missing.zip"); ok {
		t.Error("缺失项应返回 false")
	}
}
