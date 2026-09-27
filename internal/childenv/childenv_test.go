package childenv

import (
	"strings"
	"testing"
)

// TestSensitiveMatchesCaseInsensitively：敏感键识别大小写不敏感——
// Windows 环境变量不区分大小写，任何大小写变体都不能成为绕行通道。
// 只识别明确的凭据词；子串陷阱（AUTHOR、KEYBOARD）不能误伤。
func TestSensitiveMatchesCaseInsensitively(t *testing.T) {
	yes := []string{
		"MINDLOOP_WEB_TOKEN", "mindloop_web_token", "Mindloop_Web_Token",
		"OPENAI_API_KEY", "openai_api_key", "Acme_Api_Key",
		"GITHUB_TOKEN", "github_token",
		"DB_PASSWORD", "db_passwd",
		"AWS_SECRET_ACCESS_KEY", "aws_secret_access_key",
		"AWS_ACCESS_KEY_ID",
		"GOOGLE_APPLICATION_CREDENTIALS",
		"SIGNING_KEY",
	}
	for _, k := range yes {
		if !Sensitive(k) {
			t.Errorf("Sensitive(%q) 应为 true", k)
		}
	}
	no := []string{"PATH", "TEMP", "MINDLOOP_HOME", "SKILLS_DIR", "LANG", "NUMBER_OF_PROCESSORS", "KEYBOARD_LAYOUT", "AUTHOR"}
	for _, k := range no {
		if Sensitive(k) {
			t.Errorf("Sensitive(%q) 应为 false（误伤）", k)
		}
	}
}

// TestInheritKeepsWhitelistedNonSensitive：白名单里的非敏感键照常
// 继承（PATH、家目录、区域、代理等运行所需集合），顺序不变。
func TestInheritKeepsWhitelistedNonSensitive(t *testing.T) {
	parent := []string{
		"PATH=/usr/bin", "HOME=/home/u", "TEMP=/tmp", "LANG=zh_CN.UTF-8",
		"MINDLOOP_HOME=/home/u/.mindloop", "HTTP_PROXY=http://proxy:3128",
	}
	got := Inherit(parent, nil)
	if strings.Join(got, "|") != strings.Join(parent, "|") {
		t.Fatalf("白名单键应原样继承: %v", got)
	}
}

// TestInheritDropsUnlisted：白名单外的键默认不下传——脚本看不到
// 无关的进程环境。
func TestInheritDropsUnlisted(t *testing.T) {
	got := Inherit([]string{"PATH=/bin", "MINDLOOP_KEEPME=keepme-ok", "RANDOM_APP_VAR=x"}, nil)
	joined := strings.Join(got, "\n")
	if strings.Contains(joined, "KEEPME") || strings.Contains(joined, "RANDOM_APP_VAR") {
		t.Fatalf("白名单外的键不应下传: %v", got)
	}
	if !strings.Contains(joined, "PATH=/bin") {
		t.Fatalf("白名单键应下传: %v", got)
	}
}

// TestInheritSensitiveBlockedEvenWhenNamed：敏感键即使被显式扩展
// 点名也不继承——父环境里的凭据永不下传，凭据的唯一通道是显式值。
func TestInheritSensitiveBlockedEvenWhenNamed(t *testing.T) {
	parent := []string{"PATH=/bin", "MINDLOOP_WEB_TOKEN=web-secret", "ACME_API_KEY=sk-acme"}
	got := Inherit(parent, []string{"MINDLOOP_WEB_TOKEN", "acme_api_key"})
	joined := strings.Join(got, "\n")
	if strings.Contains(joined, "web-secret") || strings.Contains(joined, "sk-acme") {
		t.Fatalf("敏感键不应下传: %v", got)
	}
}

// TestInheritExtraAllowAddsKey：显式扩展（非敏感）从父环境继承；
// 键名匹配大小写不敏感（Windows 语义）。
func TestInheritExtraAllowAddsKey(t *testing.T) {
	parent := []string{"PATH=/bin", "MINDLOOP_KEEPME=keepme-ok", "GOPATH=/go"}
	got := Inherit(parent, []string{"mindloop_keepme", "gopath"})
	joined := strings.Join(got, "\n")
	for _, want := range []string{"MINDLOOP_KEEPME=keepme-ok", "GOPATH=/go", "PATH=/bin"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("缺 %q: %v", want, got)
		}
	}
}

// TestInheritSkipsMalformed：没有 = 的残行与空键名不进入子进程环境。
func TestInheritSkipsMalformed(t *testing.T) {
	got := Inherit([]string{"PATH=/bin", "JUSTAWORD", "=/nokey"}, nil)
	if len(got) != 1 || got[0] != "PATH=/bin" {
		t.Fatalf("残行应被跳过: %v", got)
	}
}

// TestList：扩展列表解析——逗号/分号分隔、容忍空白与空项；无项
// 返回 nil。
func TestList(t *testing.T) {
	got := List(" MINDLOOP_KEEPME ,GOPATH; CARGO_HOME ,, ")
	want := []string{"MINDLOOP_KEEPME", "GOPATH", "CARGO_HOME"}
	if len(got) != len(want) {
		t.Fatalf("List = %v，应为 %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("List = %v，应为 %v", got, want)
		}
	}
	if List("  , ; ") != nil || List("") != nil {
		t.Fatal("空列表应为 nil")
	}
}

// TestConnectorCredentialsNeverInherit 连接器红线（docs/designs/
// connectors.md §6.3）的钉子：WECOM/CONNECTOR 前缀的桥凭据即使在
// extra 里被显式点名也不下传沙箱——凭据的通道是显式值，不是继承。
func TestConnectorCredentialsNeverInherit(t *testing.T) {
	parent := []string{
		"PATH=/usr/bin",
		"WECOM_BOT_SECRET=topsecret",
		"WECOM_BOT_ID=ww123",
		"CONNECTOR_TOKEN=tok",
	}
	extra := []string{"WECOM_BOT_SECRET", "CONNECTOR_TOKEN"} // 攻击者视角的"点名"
	out := Inherit(parent, extra)
	joined := strings.Join(out, "\n")
	for _, banned := range []string{"WECOM_BOT_SECRET", "topsecret", "CONNECTOR_TOKEN", "tok", "WECOM_BOT_ID"} {
		if strings.Contains(joined, banned) {
			t.Fatalf("连接器凭据泄漏进沙箱环境: %q", banned)
		}
	}
	for _, want := range []string{"PATH=/usr/bin"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("白名单键被误删: %q", want)
		}
	}
	// WECOM_BOT_ID 非敏感但也不在白名单：同样不下传（Inherit 的
	// 白名单语义），凭据类是双重拦截，普通类是白名单拦截。
}
