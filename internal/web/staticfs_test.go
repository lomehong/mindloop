package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// TestViewerFSServing：嵌入产物（release 构建）作为静态源时的服务
// 形态——SPA catch-all、资产永久缓存、PWA 辅助直出，与磁盘目录
// 同一语义。
func TestViewerFSServing(t *testing.T) {
	viewFS := fstest.MapFS{
		"index.html":           {Data: []byte("<html>spa-root</html>")},
		"sw.js":                {Data: []byte("self.registration")},
		"manifest.webmanifest": {Data: []byte(`{"name":"m"}`)},
		"assets/app-abc123.js": {Data: []byte("export default 1")},
	}
	s, err := New(Config{Root: t.TempDir(), ViewerFS: viewFS, Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)

	get := func(path string) (*http.Response, string) {
		t.Helper()
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("读响应: %v", err)
		}
		return resp, string(body)
	}

	// SPA catch-all：任意前端路由回 index.html，no-cache。
	resp, body := get("/some/spa/route")
	if resp.StatusCode != 200 || !strings.Contains(body, "spa-root") {
		t.Fatalf("SPA catch-all = %d %q", resp.StatusCode, body)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("index.html 应 no-cache，得到 %q", cc)
	}

	// 资产：内容哈希文件名，immutable 永久缓存。
	resp, body = get("/assets/app-abc123.js")
	if resp.StatusCode != 200 || !strings.Contains(body, "export default 1") {
		t.Fatalf("资产 = %d %q", resp.StatusCode, body)
	}
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Fatalf("资产应 immutable 缓存，得到 %q", cc)
	}

	// PWA 辅助直出真实内容（落 catch-all 会拿到 index.html）。
	if _, body = get("/sw.js"); !strings.Contains(body, "self.registration") {
		t.Fatalf("sw.js 应直出: %q", body)
	}
	if _, body = get("/manifest.webmanifest"); !strings.Contains(body, `"name":"m"`) {
		t.Fatalf("manifest 应直出: %q", body)
	}

	// API 前缀不被 catch-all 吞掉。
	if resp, _ = get("/api/nonexistent"); resp.StatusCode != 404 {
		t.Fatalf("/api 未匹配应 404，得到 %d", resp.StatusCode)
	}
}

// TestViewerDirOverridesFS：磁盘目录与嵌入产物同时设置时磁盘优先
// ——显式指定应能覆盖嵌入产物（对 release 二进制做前端热修）。
func TestViewerDirOverridesFS(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>from-disk</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	viewFS := fstest.MapFS{"index.html": {Data: []byte("<html>from-embed</html>")}}
	s, err := New(Config{Root: t.TempDir(), ViewerDir: dir, ViewerFS: viewFS, Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "from-disk") {
		t.Fatalf("磁盘目录应优先于嵌入产物: %q", body)
	}
}
