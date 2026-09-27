package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mindloop/internal/identity"
)

// putJSON 发一个 PUT {body} 并返回响应（body 由调用方关闭）。
func putJSON(url string, body any) (*http.Response, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return http.DefaultClient.Do(req)
}

// decodeBody 解 JSON 响应体。
func decodeBody(resp *http.Response, out any) error {
	return json.NewDecoder(resp.Body).Decode(out)
}

// writeSkillDir 造一个合法技能目录，返回 SKILL.md 路径。
func writeSkillDir(t *testing.T, parent, name, description string) string {
	t.Helper()
	dir := filepath.Join(parent, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	md := "---\nname: " + name + "\ndescription: " + description + "\n---\n\n# " + name + "\n\n正文。\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "SKILL.md")
}

func TestSkillsGetContent(t *testing.T) {
	_, id := newIdentityHome(t)
	path := writeSkillDir(t, filepath.Join(id.Dir, "skills"), "greet", "打招呼技能")

	ts, _ := newTestServer(t, identity.Home(), "")
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/api/identities/ada/skills/greet")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var out struct {
		Name     string `json:"name"`
		Source   string `json:"source"`
		Content  string `json:"content"`
		Editable bool   `json:"editable"`
	}
	if err := decodeBody(resp, &out); err != nil {
		t.Fatal(err)
	}
	if out.Name != "greet" || out.Source != "identity" || !out.Editable {
		t.Fatalf("元数据不符: %+v", out)
	}
	data, _ := os.ReadFile(path)
	if out.Content != string(data) {
		t.Fatalf("content 与磁盘不一致")
	}
}

func TestSkillsUpdateContent(t *testing.T) {
	_, id := newIdentityHome(t)
	path := writeSkillDir(t, filepath.Join(id.Dir, "skills"), "greet", "打招呼技能")

	ts, _ := newTestServer(t, identity.Home(), "")
	defer ts.Close()

	// 新正文换 description 与正文，CRLF 行尾应被归一为 LF。
	newMD := "---\r\nname: greet\r\ndescription: 打招呼技能（改）\r\n---\r\n\r\n# 改后正文\r\n"
	resp, err := putJSON(ts.URL+"/api/identities/ada/skills/greet", map[string]string{"content": newMD})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "\r\n") {
		t.Fatalf("落盘内容仍含 CRLF")
	}
	if !strings.Contains(string(data), "打招呼技能（改）") {
		t.Fatalf("新内容未落盘: %s", data)
	}

	// 再 GET，读到的是新内容。
	resp2, err := http.Get(ts.URL + "/api/identities/ada/skills/greet")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	var out struct {
		Description string `json:"description"`
		Content     string `json:"content"`
	}
	if err := decodeBody(resp2, &out); err != nil {
		t.Fatal(err)
	}
	if out.Description != "打招呼技能（改）" {
		t.Fatalf("description = %q", out.Description)
	}
}

func TestSkillsUpdateRejectsInvalid(t *testing.T) {
	_, id := newIdentityHome(t)
	path := writeSkillDir(t, filepath.Join(id.Dir, "skills"), "greet", "打招呼技能")
	before, _ := os.ReadFile(path)

	ts, _ := newTestServer(t, identity.Home(), "")
	defer ts.Close()

	cases := map[string]string{
		"缺 description": "---\nname: greet\n---\n正文\n",
		"name 与目录不一致":   "---\nname: other\ndescription: x\n---\n正文\n",
		"无 frontmatter": "随便一段文字\n",
		"非法 name 语法":    "---\nname: Greet!\ndescription: x\n---\n正文\n",
	}
	for label, content := range cases {
		resp, err := putJSON(ts.URL+"/api/identities/ada/skills/greet", map[string]string{"content": content})
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 400 {
			t.Fatalf("%s: status = %d, 要 400", label, resp.StatusCode)
		}
		after, _ := os.ReadFile(path)
		if string(after) != string(before) {
			t.Fatalf("%s: 旧内容被破坏", label)
		}
	}
}

func TestSkillsUpdateRejectsGlobalLayer(t *testing.T) {
	newIdentityHome(t) // 建 MINDLOOP_HOME；全局层是其 skills 兄弟目录
	globalDir := filepath.Join(filepath.Dir(identity.Home()), "skills")
	writeSkillDir(t, globalDir, "shared", "全局共享技能")

	ts, _ := newTestServer(t, identity.Home(), "")
	defer ts.Close()

	resp, err := putJSON(ts.URL+"/api/identities/ada/skills/shared",
		map[string]string{"content": "---\nname: shared\ndescription: 改\n---\n"})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("status = %d, 要 400", resp.StatusCode)
	}

	// 全局层可读，但 editable=false。
	resp2, err := http.Get(ts.URL + "/api/identities/ada/skills/shared")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	var out struct {
		Editable bool `json:"editable"`
	}
	if err := decodeBody(resp2, &out); err != nil {
		t.Fatal(err)
	}
	if out.Editable {
		t.Fatalf("全局层不应 editable")
	}
}

func TestSkillsGetAndUpdateMissingOrBadName(t *testing.T) {
	newIdentityHome(t)
	ts, _ := newTestServer(t, identity.Home(), "")
	defer ts.Close()

	for _, path := range []string{
		"/api/identities/ada/skills/nope",
		"/api/identities/ada/skills/..%5Cescape",
	} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode == 200 {
			t.Fatalf("GET %s 不应成功", path)
		}
	}
	resp, err := putJSON(ts.URL+"/api/identities/ada/skills/nope",
		map[string]string{"content": "---\nname: nope\ndescription: x\n---\n"})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("PUT 不存在的技能: status = %d, 要 404", resp.StatusCode)
	}
}
