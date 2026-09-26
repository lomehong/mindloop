package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mindloop/internal/identity"
	"mindloop/internal/mcp"
	"mindloop/internal/traj"
)

func extensionTestHome(t *testing.T) (string, *identity.Identity) {
	t.Helper()
	home := newTestHome(t)
	t.Setenv("MINDLOOP_IDENTITY_DIR", "")
	id, err := identity.Create(context.Background(), "ada")
	if err != nil {
		t.Fatal(err)
	}
	return home, id
}

func extensionFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestExtensionIdentityFlagPositions(t *testing.T) {
	for _, group := range []string{"mcp", "skills"} {
		for _, before := range []bool{true, false} {
			args := []string{group, "list", "--identity", "missing"}
			if before {
				args = []string{group, "--identity", "missing", "list"}
			}
			t.Run(strings.Join(args, " "), func(t *testing.T) {
				extensionTestHome(t)
				code, out, stderr := runCLI(t, args...)
				if code == 0 || !strings.Contains(stderr, "missing") {
					t.Fatalf("无效身份未被拒绝: code=%d out=%q err=%q", code, out, stderr)
				}
			})
		}
	}
}

func TestExtensionScopePrecedence(t *testing.T) {
	home, id := extensionTestHome(t)
	extensionFixture(t, filepath.Join(home, "mcp.json"), `{"mcpServers":{"global-only":{"command":"global-command"},"shared":{"command":"global-shared"}}}`)
	extensionFixture(t, filepath.Join(id.Dir, "mcp.json"), `{"mcpServers":{"identity-only":{"command":"identity-command"},"shared":{"command":"identity-shared"}}}`)
	t.Setenv("MINDLOOP_IDENTITY_DIR", id.Dir)
	for _, tc := range []struct {
		name   string
		flags  []string
		want   []string
		absent []string
	}{
		{"环境身份", nil, []string{"global-command", "identity-command", "identity-shared"}, []string{"global-shared"}},
		{"显式全局", []string{"--global"}, []string{"global-command", "global-shared"}, []string{"identity-command", "identity-shared"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, out, stderr := runCLI(t, append([]string{"mcp", "list"}, tc.flags...)...)
			if code != 0 {
				t.Fatalf("code=%d: %s", code, stderr)
			}
			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Errorf("缺少 %q: %s", want, out)
				}
			}
			for _, absent := range tc.absent {
				if strings.Contains(out, absent) {
					t.Errorf("不应包含 %q: %s", absent, out)
				}
			}
		})
	}
	// 显式身份优先于损坏的环境目录；隐式读取则必须明确报错。
	t.Setenv("MINDLOOP_IDENTITY_DIR", filepath.Join(t.TempDir(), "ada"))
	if code, _, stderr := runCLI(t, "mcp", "list", "--identity", "ada"); code != 0 {
		t.Fatalf("显式身份未覆盖环境: %s", stderr)
	}
	if code, _, _ := runCLI(t, "mcp", "list"); code == 0 {
		t.Fatal("无效环境目录被静默忽略")
	}
	for _, group := range []string{"mcp", "skills"} {
		if code, _, _ := runCLI(t, group, "list", "--identity", "ada", "--global"); code == 0 {
			t.Fatalf("%s 未拒绝互斥的作用域", group)
		}
	}
}

func TestMCPToolsJSONAndTimeout(t *testing.T) {
	home, _ := extensionTestHome(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     *int64 `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		if req.ID == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		if r.URL.Path == "/slow" && req.Method != "initialize" {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(400 * time.Millisecond):
			}
		}
		var result any = map[string]any{"protocolVersion": "2025-06-18"}
		switch req.Method {
		case "tools/list":
			result = map[string]any{"tools": []mcp.Tool{{Name: "full_name", Description: strings.Repeat("详细说明", 50), InputSchema: json.RawMessage(`{"type":"object","properties":{"limit":{"type":"integer","minimum":1}},"required":["limit"]}`)}}}
		case "tools/call":
			result = map[string]any{"content": []map[string]any{{"type": "text", "text": "done"}}}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
	defer srv.Close()
	data, err := json.Marshal(mcp.Config{MCPServers: map[string]mcp.ServerConfig{"fast": {URL: srv.URL}, "slow": {URL: srv.URL + "/slow"}}})
	if err != nil {
		t.Fatal(err)
	}
	extensionFixture(t, filepath.Join(home, "mcp.json"), string(data))
	t.Run("完整JSON", func(t *testing.T) {
		code, out, stderr := runCLI(t, "mcp", "tools", "fast", "--json")
		if code != 0 {
			t.Fatalf("tools --json 失败: %s", stderr)
		}
		var tools []mcp.Tool
		if err := json.Unmarshal([]byte(out), &tools); err != nil {
			t.Fatalf("无效 JSON: %s: %v", out, err)
		}
		if len(tools) != 1 || tools[0].Name != "full_name" || tools[0].Description != strings.Repeat("详细说明", 50) || !strings.Contains(string(tools[0].InputSchema), `"minimum":1`) {
			t.Fatalf("名称、描述或 schema 丢失: %s", out)
		}
	})
	for _, operation := range [][]string{{"tools", "slow"}, {"call", "slow", "ping"}} {
		for _, before := range []bool{true, false} {
			args := append([]string{"mcp"}, operation...)
			if before {
				args = append([]string{"mcp", "--timeout", "80ms"}, operation...)
			} else {
				args = append(args, "--timeout", "80ms")
			}
			t.Run(strings.Join(args, " "), func(t *testing.T) {
				code, _, stderr := runCLI(t, args...)
				if code == 0 || !strings.Contains(stderr, "deadline") {
					t.Fatalf("自定义 timeout 未生效: code=%d err=%q", code, stderr)
				}
			})
		}
	}
}

func TestMCPMutationsStayInSelectedLayer(t *testing.T) {
	home, id := extensionTestHome(t)
	globalPath, localPath := filepath.Join(home, "mcp.json"), filepath.Join(id.Dir, "mcp.json")
	global := `{"mcpServers":{"global-only":{"command":"global-command"},"shared":{"command":"global-shared"}}}`
	local := `{"custom":{"keep":true},"mcpServers":{"shared":{"command":"identity-shared"},"keep":{"command":"untouched","futureSetting":{"enabled":true}}}}`
	extensionFixture(t, globalPath, global)
	extensionFixture(t, localPath, local)
	if code, _, stderr := runCLI(t, "mcp", "remove", "shared", "--identity", "ada"); code != 0 {
		t.Fatal(stderr)
	}
	data, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	var servers map[string]json.RawMessage
	if err := json.Unmarshal(doc["mcpServers"], &servers); err != nil {
		t.Fatal(err)
	}
	if len(servers) != 1 || !bytes.Contains(servers["keep"], []byte("futureSetting")) || len(doc["custom"]) == 0 {
		t.Errorf("局部修改丢失未知字段或复制了全局配置: %s", data)
	}
	if got, _ := os.ReadFile(globalPath); string(got) != global {
		t.Fatal("身份 remove 改动了全局层")
	}
	if code, _, _ := runCLI(t, "mcp", "remove", "global-only", "--identity", "ada"); code == 0 {
		t.Error("身份 remove 不应删除全局独有条目")
	}
	if got, _ := os.ReadFile(localPath); !bytes.Equal(got, data) {
		t.Error("失败的 remove 仍写入了身份配置")
	}
	if code, out, stderr := runCLI(t, "mcp", "add", "new-global", "--global", "--", "dummy", "--option"); code != 0 {
		t.Fatalf("全局 add 失败: %s %s", out, stderr)
	}
	if got, _ := os.ReadFile(localPath); !bytes.Equal(got, data) {
		t.Error("全局 add 改动了身份层")
	}
	cfg, err := mcp.LoadConfig(globalPath)
	if err != nil || cfg.MCPServers["new-global"].Command != "dummy" {
		t.Fatalf("全局 add 未写入目标: %+v %v", cfg, err)
	}
}

func TestMCPWriteHonorsLockAndCancellation(t *testing.T) {
	home, _ := extensionTestHome(t)
	path := filepath.Join(home, "mcp.json")
	before := `{"mcpServers":{"keep":{"command":"dummy"}}}`
	extensionFixture(t, path, before)
	release, err := traj.AcquireDirLock(context.Background(), path+".lock", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	var out, stderr bytes.Buffer
	code := Execute(ctx, []string{"mcp", "add", "new", "--", "dummy"}, &out, &stderr)
	if code == 0 {
		t.Error("配置写入没有等待跨进程锁")
	}
	if got, _ := os.ReadFile(path); string(got) != before {
		t.Errorf("取消后仍修改配置: %s", got)
	}
}

func TestMCPConcurrentAddPreservesAllServers(t *testing.T) {
	home, _ := extensionTestHome(t)
	path := filepath.Join(home, "mcp.json")
	extensionFixture(t, path, `{"mcpServers":{}}`)
	const count = 20
	start := make(chan struct{})
	results := make(chan error, count)
	for i := 0; i < count; i++ {
		go func(i int) {
			<-start
			var out, stderr bytes.Buffer
			code := Execute(context.Background(), []string{"mcp", "add", fmt.Sprintf("server-%02d", i), "--", "dummy"}, &out, &stderr)
			if code != 0 {
				results <- fmt.Errorf("%s", stderr.String())
				return
			}
			results <- nil
		}(i)
	}
	close(start)
	for i := 0; i < count; i++ {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}
	cfg, err := mcp.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.MCPServers) != count {
		t.Fatalf("并发 add 丢失写入: 得到 %d，期望 %d", len(cfg.MCPServers), count)
	}
}

func TestMCPMalformedConfigIsNotOverwritten(t *testing.T) {
	for _, content := range []string{`{broken`, `null`, `{"mcpServers":[]}`} {
		t.Run(content, func(t *testing.T) {
			home, _ := extensionTestHome(t)
			path := filepath.Join(home, "mcp.json")
			extensionFixture(t, path, content)
			if code, _, _ := runCLI(t, "mcp", "add", "new", "--", "dummy"); code == 0 {
				t.Error("不应覆盖无效配置")
			}
			if got, _ := os.ReadFile(path); string(got) != content {
				t.Errorf("无效配置被覆盖: %s", got)
			}
		})
	}
}

func TestMCPServeValidatesEnvironmentIdentity(t *testing.T) {
	extensionTestHome(t)
	t.Setenv("MINDLOOP_IDENTITY_DIR", filepath.Join(t.TempDir(), "missing"))
	stdin, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdin
	os.Stdin = stdin
	t.Cleanup(func() { os.Stdin = old; stdin.Close() })
	if code, _, stderr := runCLI(t, "mcp", "serve"); code == 0 || !strings.Contains(stderr, "MINDLOOP_IDENTITY_DIR") {
		t.Fatalf("serve 未验证环境身份: code=%d err=%q", code, stderr)
	}
}

func TestSkillsIdentityOverlay(t *testing.T) {
	home, id := extensionTestHome(t)
	for _, fixture := range []struct{ root, name, description string }{
		{home, "fallback", "全局独有技能"},
		{home, "shared", "全局同名技能"},
		{id.Dir, "shared", "身份覆盖技能"},
	} {
		extensionFixture(t, filepath.Join(fixture.root, "skills", fixture.name, "SKILL.md"), "---\nname: "+fixture.name+"\ndescription: "+fixture.description+"\n---\n正文\n")
	}
	code, out, stderr := runCLI(t, "skills", "list", "--identity", "ada")
	if code != 0 || !strings.Contains(out, "全局独有技能") || !strings.Contains(out, "身份覆盖技能") || strings.Contains(out, "全局同名技能") {
		t.Fatalf("技能读取层级错误: code=%d out=%q err=%q", code, out, stderr)
	}
	if code, _, _ := runCLI(t, "skills", "remove", "fallback", "--identity", "ada"); code == 0 {
		t.Fatal("身份级 remove 不应删除全局 fallback")
	}
	if _, err := os.Stat(filepath.Join(home, "skills", "fallback", "SKILL.md")); err != nil {
		t.Fatalf("全局技能被修改: %v", err)
	}
	t.Setenv("MINDLOOP_IDENTITY_DIR", id.Dir)
	if code, _, stderr := runCLI(t, "skills", "init", "local-new"); code != 0 {
		t.Fatal(stderr)
	}
	if _, err := os.Stat(filepath.Join(id.Dir, "skills", "local-new", "SKILL.md")); err != nil {
		t.Fatalf("环境身份写入层错误: %v", err)
	}
}
