package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"mindloop/internal/identity"
)

// 本文件钉死 .env 视图的脱敏契约（.env 是密钥的常驻落点，仪表盘
// 把它渲染给人看，脱敏规则一旦回潮就是密钥直出）：
//
//   - parseEnvRedacted：敏感键（key/secret/token/password 子串命中，
//     大小写不敏感）一经命中立即脱敏——没有长度门槛；
//   - GET /api/identities/{id}/env：条目形状 {key,value,secret}，
//     敏感键的真值绝不能出现在响应体里。
//
// 注意：TestIdentityEnvShortSecretRedacted 钉的是「命中即脱敏、无
// len>8 门槛」的新行为（安全修复 task-9），在对应生产改动落地之前
// 该用例会红——这是有意的前置断言，不是测试缺陷。

// TestParseEnvRedacted：表驱动覆盖键名形态、大小写、引号、空值、
// 注释与坏行。长度期望统一由原始值长度推导，不硬编码。
func TestParseEnvRedacted(t *testing.T) {
	cases := []struct {
		name    string
		line    string
		wantKey string
		check   func(t *testing.T, got string)
	}{
		{
			name:    "非敏感键原样保留",
			line:    "MINDLOOP_MODEL=glm-5",
			wantKey: "MINDLOOP_MODEL",
			check: func(t *testing.T, got string) {
				if got != "glm-5" {
					t.Fatalf("value = %q，应为原文 glm-5", got)
				}
			},
		},
		{
			name:    "API_KEY 命中即脱敏（无长度门槛）",
			line:    "MINDLOOP_API_KEY=" + skFixture,
			wantKey: "MINDLOOP_API_KEY",
			check: func(t *testing.T, got string) {
				if got == skFixture {
					t.Fatal("敏感值原样透出")
				}
				if !strings.Contains(got, "[REDACTED") || !strings.Contains(got, strconv.Itoa(len(skFixture))) {
					t.Fatalf("脱敏占位应含标记与原始长度 %d: %q", len(skFixture), got)
				}
			},
		},
		{
			name:    "小写键同样命中",
			line:    "monitor_token=" + tokFixture,
			wantKey: "monitor_token",
			check: func(t *testing.T, got string) {
				if strings.Contains(got, tokFixture) {
					t.Fatal("小写敏感键的值原样透出")
				}
				if !strings.Contains(got, "[REDACTED") {
					t.Fatalf("应被脱敏: %q", got)
				}
			},
		},
		{
			name:    "混合大小写 password 命中且外侧引号被剥",
			line:    `Api_Password = "` + pwFixture + `"`,
			wantKey: "Api_Password",
			check: func(t *testing.T, got string) {
				if strings.Contains(got, pwFixture) {
					t.Fatal("带引号的敏感值原样透出")
				}
				if !strings.Contains(got, strconv.Itoa(len(pwFixture))) {
					t.Fatalf("长度应按剥引号后的值计 %d: %q", len(pwFixture), got)
				}
			},
		},
		{
			name:    "空值敏感键也脱敏（0 chars）",
			line:    "EMPTY_TOKEN=",
			wantKey: "EMPTY_TOKEN",
			check: func(t *testing.T, got string) {
				if !strings.Contains(got, "0 chars") {
					t.Fatalf("空敏感值应脱敏为 0 chars 占位: %q", got)
				}
			},
		},
		{
			name:    "值内含等号只按第一个等号切",
			line:    "MINDLOOP_BASE_URL=https://api.example.com/v1?a=b",
			wantKey: "MINDLOOP_BASE_URL",
			check: func(t *testing.T, got string) {
				if got != "https://api.example.com/v1?a=b" {
					t.Fatalf("value = %q，应为第一个等号后的原文", got)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseEnvRedacted(tc.line + "\n")
			v, ok := got[tc.wantKey]
			if !ok {
				t.Fatalf("键 %q 缺失，得到 %v", tc.wantKey, got)
			}
			tc.check(t, v)
		})
	}

	// 注释行与坏行绝不进结果；注释里藏敏感值同样不出现。
	out := parseEnvRedacted("# TOKEN=leakme\nno-equals-line\n\nPLAIN=value\n")
	if _, ok := out["TOKEN"]; ok {
		t.Fatalf("注释行不应被解析: %v", out)
	}
	if len(out) != 1 || out["PLAIN"] != "value" {
		t.Fatalf("坏行应被跳过、其余保留: %v", out)
	}
}

const (
	skFixture  = "sk-supersecret-1234567890"
	tokFixture = "tok-abcd-1234"
	pwFixture  = "p@ssw0rd-longenough"
)

// redactTestWriteEnv 把内容写成身份级 .env。
func redactTestWriteEnv(t *testing.T, id *identity.Identity, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(id.Dir, ".env"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// redactTestFindEntry 按键找 env 条目。
func redactTestFindEntry(t *testing.T, entries []map[string]any, key string) map[string]any {
	t.Helper()
	for _, e := range entries {
		if e["key"] == key {
			return e
		}
	}
	t.Fatalf("env 条目缺键 %q: %v", key, entries)
	return nil
}

// TestIdentityEnvContractRedactsSecrets：GET /api/identities/{id}/env
// 的契约形状（identity/env/note）与长敏感值的脱敏——真值不得出现在
// 响应体任何位置。此场景在旧实现（len>8 门槛）下已成立，作为不回退
// 的底线。
func TestIdentityEnvContractRedactsSecrets(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	id, err := identity.Create(t.Context(), "ada")
	if err != nil {
		t.Fatal(err)
	}
	redactTestWriteEnv(t, id, "MINDLOOP_MODEL=glm-5\nMINDLOOP_API_KEY="+skFixture+"\n")

	ts, _ := newTestServer(t, identity.Home(), "")
	resp, err := http.Get(ts.URL + "/api/identities/ada/env")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("GET env = %d", resp.StatusCode)
	}
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	body := string(bodyBytes)

	var out struct {
		Identity struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"identity"`
		Env  []map[string]any `json:"env"`
		Note string           `json:"note"`
	}
	if err := json.Unmarshal(bodyBytes, &out); err != nil {
		t.Fatal(err)
	}
	if out.Identity.ID != "ada" || out.Note == "" || len(out.Env) == 0 {
		t.Fatalf("契约形状错位: identity=%v note=%q env=%v", out.Identity, out.Note, out.Env)
	}

	// 非敏感键原样。
	model := redactTestFindEntry(t, out.Env, "MINDLOOP_MODEL")
	if model["value"] != "glm-5" || model["secret"] == true {
		t.Fatalf("非敏感键不应脱敏: %v", model)
	}

	// 长敏感键：secret=true，真值与完整原文都不在响应体里。
	key := redactTestFindEntry(t, out.Env, "MINDLOOP_API_KEY")
	if key["secret"] != true {
		t.Fatalf("API_KEY 应标 secret: %v", key)
	}
	if key["value"] == skFixture {
		t.Fatal("敏感值原样透出")
	}
	if strings.Contains(fmt.Sprintf("%v", out.Env), skFixture) {
		t.Fatal("完整敏感值出现在 env 列表中")
	}
	if strings.Contains(body, skFixture) {
		t.Fatal("完整敏感值出现在响应体中")
	}
	if !strings.Contains(fmt.Sprintf("%v", key["value"]), "chars") {
		t.Fatalf("脱敏值应带长度占位: %v", key["value"])
	}
}

// TestIdentityEnvShortSecretRedacted：钉死「命中即脱敏、无 len>8
// 门槛」的新行为（安全修复 task-9）——短敏感值（≤8 字符）也必须
// 脱敏。生产改动落地前本用例会红；断言只钉行为不钉预览格式。
func TestIdentityEnvShortSecretRedacted(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINDLOOP_HOME", dir)
	id, err := identity.Create(t.Context(), "ada")
	if err != nil {
		t.Fatal(err)
	}
	redactTestWriteEnv(t, id, "SHORT_KEY=abc\n")

	ts, _ := newTestServer(t, identity.Home(), "")
	resp, err := http.Get(ts.URL + "/api/identities/ada/env")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Env []map[string]any `json:"env"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	entry := redactTestFindEntry(t, out.Env, "SHORT_KEY")
	if entry["secret"] != true {
		t.Fatalf("SHORT_KEY 应标 secret: %v", entry)
	}
	if entry["value"] == "abc" {
		t.Fatalf("短敏感值未脱敏（len>8 门槛仍生效）: %v", entry)
	}
	if !strings.Contains(fmt.Sprintf("%v", entry["value"]), "3 chars") {
		t.Fatalf("脱敏值应带原始长度占位: %v", entry)
	}
}
