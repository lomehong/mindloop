package web

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mindloop/internal/identity"
)

func TestAuthenticatedMissingExportDownload(t *testing.T) {
	_, _ = newIdentityHome(t)
	s, err := New(Config{Root: identity.Home(), Addr: "127.0.0.1:0", Token: "test-access"})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	defer ts.Close()
	req, _ := http.NewRequest("GET", ts.URL+"/api/export-jobs/missing-job/download", nil)
	req.Header.Set("Authorization", "Bearer test-access")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("缺失下载应返回 404，不应断开连接：%v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("status=%d，应为 404", resp.StatusCode)
	}
}

func TestBlobReadBoundaries(t *testing.T) {
	_, id := newIdentityHome(t)
	blobs := filepath.Join(id.Timeline.Dir, "blobs")
	if err := os.MkdirAll(blobs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blobs, ".env"), []byte("不可读"), 0o600); err != nil {
		t.Fatal(err)
	}
	ts, _ := newTestServer(t, identity.Home(), "")
	base := ts.URL + "/api/identities/ada/traj/" + id.Timeline.ID + "/blob/"
	for _, suffix := range []string{"missing.stdout", ".env", "..%5c.env", "sample.stdout/extra", "sample.stdout:stream"} {
		t.Run(suffix, func(t *testing.T) {
			resp, err := ts.Client().Get(base + suffix)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != 404 {
				t.Errorf("status=%d，应为 404", resp.StatusCode)
			}
		})
	}
	t.Run("unknown-trajectory", func(t *testing.T) {
		resp, err := ts.Client().Get(ts.URL + "/api/identities/ada/traj/other/blob/sample.stdout")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 404 {
			t.Errorf("status=%d，应为 404", resp.StatusCode)
		}
	})
	t.Run("symlink-escape", func(t *testing.T) {
		outside := filepath.Join(t.TempDir(), "outside.stdout")
		if err := os.WriteFile(outside, []byte("不可泄漏"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(blobs, "escape.stdout")); err != nil {
			t.Skipf("当前环境不支持符号链接：%v", err)
		}
		resp, err := ts.Client().Get(base + "escape.stdout")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 404 {
			t.Errorf("status=%d，应为 404", resp.StatusCode)
		}
	})
}

func TestTokenProtectsAllTransports(t *testing.T) {
	_, id := newIdentityHome(t)
	blobDir := filepath.Join(id.Timeline.Dir, "blobs")
	if err := os.MkdirAll(blobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blobDir, "sample.stdout"), []byte("完整输出"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{Root: identity.Home(), Addr: "127.0.0.1:0", Token: "test-access"})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	defer ts.Close()
	client := &http.Client{Timeout: 3 * time.Second}
	blobURL := "/api/identities/ada/traj/" + id.Timeline.ID + "/blob/sample.stdout"
	endpoints := []struct{ method, path, body string }{
		{"GET", "/api/config", ""},
		{"POST", "/api/identities/ada/chat", `{"content":"测试消息","from_name":"test"}`},
		{"POST", "/api/identities/import", "invalid-gzip"},
		{"GET", "/api/export", ""},
		{"GET", "/api/identities/ada/export", ""},
		{"GET", "/api/export-jobs/missing-job/download", ""},
		{"GET", blobURL, ""},
		{"GET", "/api/identities/ada/replies/stream", ""},
	}
	for _, ep := range endpoints {
		for _, auth := range []string{"", "Bearer wrong-access", "test-access", "Basic test-access"} {
			t.Run(ep.method+ep.path+"/"+auth, func(t *testing.T) {
				req, _ := http.NewRequest(ep.method, ts.URL+ep.path+"?token=test-access", strings.NewReader(ep.body))
				req.Header.Set("Authorization", auth)
				resp, err := client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				if resp.StatusCode != 401 {
					t.Fatalf("status=%d，应为 401", resp.StatusCode)
				}
			})
		}
		for _, guard := range []string{"origin", "host"} {
			t.Run(ep.path+"/"+guard, func(t *testing.T) {
				req, _ := http.NewRequest(ep.method, ts.URL+ep.path, strings.NewReader(ep.body))
				req.Header.Set("Authorization", "Bearer test-access")
				if guard == "origin" {
					req.Header.Set("Origin", "https://untrusted.invalid")
				} else {
					req.Host = "untrusted.invalid"
				}
				resp, err := client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				if resp.StatusCode != 403 {
					t.Fatalf("status=%d，应为 403", resp.StatusCode)
				}
			})
		}
	}
	steps, err := id.Timeline.Steps()
	if err != nil || len(steps) != 1 {
		t.Fatalf("未授权请求不应追加消息：%d, %v", len(steps), err)
	}

	request := func(method, path string, body []byte) (int, http.Header, []byte) {
		t.Helper()
		req, _ := http.NewRequest(method, ts.URL+path, bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer test-access")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, resp.Header, data
	}
	if code, _, _ := request("GET", "/api/config", nil); code != 200 {
		t.Fatalf("config=%d", code)
	}
	if code, _, _ := request("POST", "/api/identities/ada/chat", []byte(endpoints[1].body)); code != 200 {
		t.Fatalf("chat=%d", code)
	}
	steps, err = id.Timeline.Steps()
	if err != nil || len(steps) != 2 {
		t.Fatalf("认证消息应落盘一次：%d, %v", len(steps), err)
	}
	code, header, archive := request("GET", "/api/export", nil)
	if code != 200 || header.Get("Content-Type") != "application/gzip" {
		t.Fatalf("export=%d, %v", code, header)
	}
	code, _, body := request("POST", "/api/identities/import?name=copy", archive)
	if code != 200 {
		t.Fatalf("import=%d %s", code, body)
	}
	if _, err := identity.Load("copy"); err != nil {
		t.Fatalf("认证上传未创建身份：%v", err)
	}
	code, _, body = request("GET", blobURL, nil)
	if code != 200 || string(body) != "完整输出" {
		t.Errorf("blob=%d %s", code, body)
	}

	code, _, body = request("POST", "/api/identities/ada/export-jobs", []byte(`{"slim":true}`))
	if code != 200 {
		t.Fatalf("start export=%d %s", code, body)
	}
	var job struct {
		ID     string `json:"job_id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(body, &job); err != nil {
		t.Fatal(err)
	}
	defer request("DELETE", "/api/export-jobs/"+job.ID, nil)
	deadline := time.Now().Add(3 * time.Second)
	for job.Status == "running" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		_, _, body = request("GET", "/api/export-jobs/"+job.ID, nil)
		if err := json.Unmarshal(body, &job); err != nil {
			t.Fatal(err)
		}
	}
	if job.Status != "done" {
		t.Fatalf("export status=%s", job.Status)
	}
	code, header, body = request("GET", "/api/export-jobs/"+job.ID+"/download", nil)
	if code != 200 || header.Get("Content-Type") != "application/gzip" || len(body) == 0 {
		t.Fatalf("job download=%d", code)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/api/identities/ada/replies/stream", nil)
	req.Header.Set("Authorization", "Bearer test-access")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("SSE=%d", resp.StatusCode)
	}
	buf := make([]byte, 128)
	n, err := resp.Body.Read(buf)
	if err != nil || !strings.Contains(string(buf[:n]), "event: status") {
		t.Fatalf("缺少 SSE 首帧：%q %v", buf[:n], err)
	}
}
