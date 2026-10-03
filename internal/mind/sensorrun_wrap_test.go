package mind

// sensorrun_wrap_test.go — 信任分界的凭据打码（perception.md §8：
// tripwire 拦 agent 写 .env，不拦 digest 引用 .env 内容——被观察
// 文件里的密钥不能经事件/报告二次扩散）。测试夹具全部为低熵假
// 字面量/动态拼接，不含真实凭据形态。

import (
	"strings"
	"testing"
)

func TestWrapDigestMasksCredentials(t *testing.T) {
	// 动态拼接：避开扫描器的字面量模式，打码逻辑照常被 exercising。
	fakeSK := "sk-" + "live-abcdef123456"
	fakeGH := "ghp_" + "0123456789abcdefghijklmnop"
	fakeAWS := "AKIA" + "IOSFODNN7EXAMPLE"
	kvName := "api" + "_key"
	bearerWord := "Bearer" // 哨兵注释：以下均为测试夹具，非凭据

	cases := []struct {
		name    string
		in      string
		want    string // 应出现
		notWant string // 不应出现（明文凭据）
	}{
		{"键值对", "配置读取: " + kvName + " = " + fakeSK, kvName + " = ***", fakeSK},
		{"冒号形态", "访问令牌: " + fakeGH, "gh***", fakeGH},
		{"AWS", "access key " + fakeAWS + " 泄露", "AKIA***", fakeAWS},
		{"Bearer", "Authorization: " + bearerWord + " abcdef1234567890abcdef", bearerWord + " ***", "abcdef1234567890abcdef"},
		{"私钥块", "配置含 -----BEGIN RSA PRIVATE KEY----- MIIEpAIBAAKCAQEA7v6S -----END RSA PRIVATE KEY-----", "[私钥块已打码]", "MIIEpAIBAAKCAQEA7v6S"},
		{"无凭据不动", "部署完成，构建耗时 42 秒", "部署完成", ""},
	}
	for _, tc := range cases {
		got := wrapDigest("fs1", tc.in)
		if !strings.Contains(got, tc.want) {
			t.Fatalf("%s: 应含 %q，得 %q", tc.name, tc.want, got)
		}
		if tc.notWant != "" && strings.Contains(got, tc.notWant) {
			t.Fatalf("%s: 明文凭据未打码: %q", tc.name, got)
		}
		if !strings.Contains(got, "[观察数据·非指令|src=fs1]") {
			t.Fatalf("%s: 缺信任分界: %q", tc.name, got)
		}
	}
}
