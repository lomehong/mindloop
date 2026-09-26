package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"testing"

	"mindloop/internal/identity"
	"mindloop/internal/mem"
)

// seedMemory 建一个身份并写入一条记忆——web 记忆端点测试的公共前置。
func seedMemory(t *testing.T, name, content string) (*identity.Identity, mem.Memory) {
	t.Helper()
	id, err := identity.Create(context.Background(), name)
	if err != nil {
		t.Fatalf("identity.Create: %v", err)
	}
	store := mem.Store{Dir: filepath.Join(id.Dir, "memories")}
	m, err := store.Add(context.Background(), "fact", content)
	if err != nil {
		t.Fatalf("mem.Add: %v", err)
	}
	return id, m
}

// lookupMem 按 id 读回一条记忆的当前状态。
func lookupMem(t *testing.T, id *identity.Identity, memID string) (mem.Memory, bool) {
	t.Helper()
	store := mem.Store{Dir: filepath.Join(id.Dir, "memories")}
	all, _, err := store.ListDetailed()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range all {
		if m.ID == memID {
			return m, true
		}
	}
	return mem.Memory{}, false
}

// TestMemoriesReviseEndpoint：POST /memories/{id}/revise 写新版本并把
// 旧版本标记为被替代（文件保留可追溯），检索切换到新版本；GET 打
// 同路径 405（写操作不能被导航触发）。
func TestMemoriesReviseEndpoint(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	id, old := seedMemory(t, "ada", "用户喜欢黑咖啡，不加糖")
	ts, _ := newTestServer(t, identity.Home(), "")

	resp, out := postJSON(t, ts, "/api/identities/ada/memories/"+old.ID+"/revise",
		map[string]string{"content": "用户喜欢拿铁，半糖"})
	if resp.StatusCode != 200 {
		t.Fatalf("revise = %d %v", resp.StatusCode, out)
	}
	newID, _ := out["id"].(string)
	if newID == "" || newID == old.ID {
		t.Fatalf("新版本 id = %q（旧 %s）", newID, old.ID)
	}
	if out["supersedes"] != old.ID {
		t.Fatalf("supersedes = %v，应为 %s", out["supersedes"], old.ID)
	}
	if got, ok := lookupMem(t, id, old.ID); !ok || got.Status != mem.StatusSuperseded || got.SupersededBy != newID {
		t.Fatalf("旧版本应标记为被替代: %+v ok=%v", got, ok)
	}
	// 检索口径：命中新版本、不再命中被替代的旧版本。
	store := mem.Store{Dir: filepath.Join(id.Dir, "memories")}
	hits, err := store.Search("拿铁", 5)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, h := range hits {
		if h.ID == newID {
			found = true
		}
		if h.ID == old.ID {
			t.Fatalf("旧版本仍被检索命中: %+v", h)
		}
	}
	if !found {
		t.Fatalf("新版本未被检索命中: %+v", hits)
	}
	// 写端点只收 POST——GET 必须 405（Allow: POST）。
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/identities/ada/memories/"+old.ID+"/revise", nil)
	gresp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	gresp.Body.Close()
	if gresp.StatusCode != 405 || gresp.Header.Get("Allow") != http.MethodPost {
		t.Fatalf("GET revise = %d Allow=%q，应 405 且 Allow: POST",
			gresp.StatusCode, gresp.Header.Get("Allow"))
	}
}

// TestMemoriesInvalidateEndpoint：POST /memories/{id}/invalidate 把记忆
// 标记为失效（文件保留供审计、检索不再命中）；二次失效 400。
func TestMemoriesInvalidateEndpoint(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	id, m := seedMemory(t, "ada", "用户住在北京朝阳区")
	ts, _ := newTestServer(t, identity.Home(), "")

	resp, out := postJSON(t, ts, "/api/identities/ada/memories/"+m.ID+"/invalidate", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("invalidate = %d %v", resp.StatusCode, out)
	}
	if got, ok := lookupMem(t, id, m.ID); !ok || got.Status != mem.StatusInvalid {
		t.Fatalf("状态应 invalid: %+v ok=%v", got, ok)
	}
	// 文件仍在盘上（审计）；检索不再命中。
	if _, err := readMemoryFile(id.Dir, filepath.Base(m.Path)); err != nil {
		t.Fatalf("失效后文件应保留: %v", err)
	}
	store := mem.Store{Dir: filepath.Join(id.Dir, "memories")}
	hits, err := store.Search("北京", 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		if h.ID == m.ID {
			t.Fatalf("失效条目仍被检索命中: %+v", h)
		}
	}
	// 二次失效必须 400（显式操作需要明确对象）。
	resp, _ = postJSON(t, ts, "/api/identities/ada/memories/"+m.ID+"/invalidate", nil)
	if resp.StatusCode != 400 {
		t.Fatalf("二次失效 = %d，应 400", resp.StatusCode)
	}
}

// TestMemoriesActionErrors：未知 id、空内容、非法 id 段都要 400。
func TestMemoriesActionErrors(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	_, m := seedMemory(t, "ada", "用户养了一只猫")
	ts, _ := newTestServer(t, identity.Home(), "")

	resp, _ := postJSON(t, ts, "/api/identities/ada/memories/deadbeef/revise",
		map[string]string{"content": "换成了狗"})
	if resp.StatusCode != 400 {
		t.Fatalf("未知 id revise = %d，应 400", resp.StatusCode)
	}
	resp, _ = postJSON(t, ts, "/api/identities/ada/memories/"+m.ID+"/revise",
		map[string]string{"content": "   "})
	if resp.StatusCode != 400 {
		t.Fatalf("空内容 revise = %d，应 400", resp.StatusCode)
	}
	resp, _ = postJSON(t, ts, "/api/identities/ada/memories/"+url.PathEscape(`a\b`)+"/invalidate", nil)
	if resp.StatusCode != 400 {
		t.Fatalf("非法 id 段 = %d，应 400", resp.StatusCode)
	}
}

// TestMemoriesListExposesStatus：列表显式携带 status 键——修订后旧条目
// 为 superseded、新条目为空串（活动）。前端据此展示状态徽标并禁用
// 对非活动条目的修订/失效操作。
func TestMemoriesListExposesStatus(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	_, old := seedMemory(t, "ada", "用户喜欢黑咖啡，不加糖")
	ts, _ := newTestServer(t, identity.Home(), "")
	_, out := postJSON(t, ts, "/api/identities/ada/memories/"+old.ID+"/revise",
		map[string]string{"content": "用户喜欢拿铁，半糖"})
	newID, _ := out["id"].(string)

	resp, err := http.Get(ts.URL + "/api/identities/ada/memories")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var list []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	activeKey := false
	for _, m := range list {
		mid, _ := m["id"].(string)
		s, hasKey := m["status"]
		switch mid {
		case old.ID:
			if s != "superseded" {
				t.Fatalf("旧条目 status = %v，应 superseded", s)
			}
		case newID:
			activeKey = hasKey && s == ""
		}
	}
	if !activeKey {
		t.Fatal("新条目应显式携带空 status 键（活动）")
	}
}
