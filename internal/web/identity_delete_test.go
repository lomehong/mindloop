package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lomehong/mindloop/internal/mind"
)

func deleteReq(t *testing.T, url string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// TestIdentityDeleteAPIFlow：DELETE 身份整目录消失、二次删除 404。
// 「刚有写入的心智」（轨迹 mtime 新鲜但无活进程）必须能立刻删除——
// 停机等待只看真活进程，不被 30s 存活窗挡住。
func TestIdentityDeleteAPIFlow(t *testing.T) {
	ts, _, id := newTaskTestRig(t)
	addMessage(t, id, "you", "ada", "最后一条") // 轨迹 mtime 新鲜

	start := time.Now()
	resp := deleteReq(t, ts.URL+"/api/identities/ada")
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("delete = %d", resp.StatusCode)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out["ok"] != true || out["name"] != "ada" || out["stopped"] != true {
		t.Fatalf("回执错位: %v", out)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("删除被 mtime 存活窗拖住: %v", elapsed)
	}
	if _, err := os.Stat(id.Dir); !os.IsNotExist(err) {
		t.Fatalf("身份目录未删除: %v", err)
	}

	resp2 := deleteReq(t, ts.URL+"/api/identities/ada")
	resp2.Body.Close()
	if resp2.StatusCode != 404 {
		t.Fatalf("二次删除应 404，得到 %d", resp2.StatusCode)
	}
	get, err := http.Get(ts.URL + "/api/identities/ada")
	if err != nil {
		t.Fatal(err)
	}
	get.Body.Close()
	if get.StatusCode != 404 {
		t.Fatalf("删除后 GET 应 404，得到 %d", get.StatusCode)
	}
}

// TestIdentityDeleteRefusesRunning：锁属主进程存活时停机等待到期仍
// 未退场 → 409，身份目录原样保留；锁清除后再删成功（同键重发语义
// 的安全侧：拒绝而不是硬删）。
func TestIdentityDeleteRefusesRunning(t *testing.T) {
	ts, _, id := newTaskTestRig(t)
	oldWait := deleteStopWait
	deleteStopWait = 500 * time.Millisecond
	t.Cleanup(func() { deleteStopWait = oldWait })

	lockDir := mind.RunLockDir(id.Timeline.Dir)
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		t.Fatal(err)
	}
	owner := fmt.Sprintf(`{"pid":%d}`, os.Getpid())
	if err := os.WriteFile(filepath.Join(lockDir, "owner.json"), []byte(owner), 0o644); err != nil {
		t.Fatal(err)
	}

	resp := deleteReq(t, ts.URL+"/api/identities/ada")
	resp.Body.Close()
	if resp.StatusCode != 409 {
		t.Fatalf("运行中应 409，得到 %d", resp.StatusCode)
	}
	if _, err := os.Stat(id.Dir); err != nil {
		t.Fatalf("拒删后身份目录不应消失: %v", err)
	}

	if err := os.RemoveAll(lockDir); err != nil {
		t.Fatal(err)
	}
	resp2 := deleteReq(t, ts.URL+"/api/identities/ada")
	resp2.Body.Close()
	if resp2.StatusCode != 200 {
		t.Fatalf("清锁后删除应 200，得到 %d", resp2.StatusCode)
	}
}
