package web

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mindloop/internal/identity"
)

// TestArchiveExcluded：两条导出路径（单身份/全量）共用的排除谓词——
// .env 与 run/ 是铁律；top 区分 rel 形态（0=相对身份目录，1=相对
// 身份根、第 0 层是身份名）。
func TestArchiveExcluded(t *testing.T) {
	cases := []struct {
		rel  string
		top  int
		want bool
	}{
		// 单身份导出形态（top=0）。
		{".env", 0, true},
		{"run", 0, true},
		{"run/wake.monolith", 0, true},
		{"run/dispatcher.lock/owner.json", 0, true},
		{"runs", 0, false},
		{"runs/abc123/.mindloop_final", 0, false},
		{"runs/run", 0, false}, // runs 工作现场里恰巧叫 run 的文件
		{"persona.md", 0, false},
		{"memories/keep.md", 0, false},
		// 全量导出形态（top=1，第 0 层是身份名）。
		{"ada/.env", 1, true},
		{"ada/run", 1, true},
		{"ada/run/stop", 1, true},
		{"ada/runs/x/out.txt", 1, false},
		{"ada/persona.md", 1, false},
		{"ada/identity.txt", 1, false},
		// .env 按任意层保守排除（两种形态都覆盖）。
		{"runs/.env", 0, true},
		{"ada/runs/x/.env", 1, true},
	}
	for _, c := range cases {
		if got := archiveExcluded(c.rel, c.top); got != c.want {
			t.Errorf("archiveExcluded(%q, %d) = %v，应为 %v", c.rel, c.top, got, c.want)
		}
	}
}

// TestGlobalExportExcludesSecrets：M-1 回归钉——GET /api/export（全量
// 导出）此前不过滤 .env，把每个身份的 API key 打进下载产物。修复后
// 归档里不得出现 .env 与 run/ 条目，密钥明文不得出现在归档字节中。
func TestGlobalExportExcludesSecrets(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	home := identity.Home()
	id, err := identity.Create(context.Background(), "ada")
	if err != nil {
		t.Fatal(err)
	}

	// 密钥、控制面、正常文件各就各位。
	if err := os.WriteFile(filepath.Join(id.Dir, ".env"),
		[]byte("MINDLOOP_API_KEY=sk-leak-canary-9f3a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(id.Dir, "run"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(id.Dir, "run", "stop"), []byte("stop"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(id.Dir, "persona.md"), []byte("# ada\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ts, _ := newTestServer(t, home, "")

	resp, err := http.Get(ts.URL + "/api/export")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("GET /api/export = %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}

	// 字节级断言：密钥明文绝不出现在归档里。
	if bytes.Contains(body, []byte("sk-leak-canary-9f3a")) {
		t.Fatal("全量导出泄漏了 .env 中的密钥明文")
	}

	// 条目级断言：.env 与 run/ 不进归档；正常文件仍在。
	gr, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("不是有效 gzip: %v", err)
	}
	defer gr.Close()
	tr := tar.NewReader(gr)
	foundPersona, foundIdentity := false, false
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		name := hdr.Name
		if strings.HasSuffix(name, "/.env") || name == ".env" ||
			strings.HasSuffix(name, "/.env.tmp") {
			t.Fatalf("归档不应含 .env 条目: %s", name)
		}
		if name == "identities/ada/run" || strings.HasPrefix(name, "identities/ada/run/") {
			t.Fatalf("归档不应含 run/ 控制面条目: %s", name)
		}
		if strings.HasSuffix(name, "/persona.md") {
			foundPersona = true
		}
		if strings.HasSuffix(name, "/identity.txt") {
			foundIdentity = true
		}
	}
	if !foundPersona || !foundIdentity {
		t.Fatalf("正常文件不应被排除（persona=%v identity.txt=%v）", foundPersona, foundIdentity)
	}
}

// TestIdentityExportExcludesSecrets：单身份导出（同一条 archiveExcluded
// 谓词的另一条路径）——.env 同样不得出现。
func TestIdentityExportExcludesSecrets(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	home := identity.Home()
	id, err := identity.Create(context.Background(), "ada")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(id.Dir, ".env"),
		[]byte("MINDLOOP_API_KEY=sk-leak-canary-2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ts, _ := newTestServer(t, home, "")

	resp, err := http.Get(ts.URL + "/api/identities/ada/export")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(body, []byte("sk-leak-canary-2")) {
		t.Fatal("单身份导出泄漏了 .env 中的密钥明文")
	}
}
