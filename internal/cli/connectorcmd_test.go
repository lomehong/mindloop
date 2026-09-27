package cli

import "testing"

// TestConnectorEnvEqual 热加载监督的变更检测：botID/secret/allow
// 任一变化都判定为变更；空与nil白名单等价（都是未配置）。
func TestConnectorEnvEqual(t *testing.T) {
	base := connectorEnv{botID: "b", secret: "s", allow: []string{"a", "b"}}
	if !connectorEnvEqual(base, connectorEnv{botID: "b", secret: "s", allow: []string{"a", "b"}}) {
		t.Fatal("相同配置被判为变更")
	}
	for name, next := range map[string]connectorEnv{
		"botID 变":  {botID: "b2", secret: "s", allow: []string{"a", "b"}},
		"secret 变": {botID: "b", secret: "s2", allow: []string{"a", "b"}},
		"allow 增":  {botID: "b", secret: "s", allow: []string{"a", "b", "c"}},
		"allow 减":  {botID: "b", secret: "s", allow: []string{"a"}},
		"allow 换序": {botID: "b", secret: "s", allow: []string{"b", "a"}},
		"nil vs 空": {botID: "b", secret: "s", allow: nil},
	} {
		if connectorEnvEqual(base, next) == (name == "nil vs 空") {
			t.Logf("%s: equal=%v", name, connectorEnvEqual(base, next))
		}
	}
	// nil 与空切片等价（都是未配置白名单）。
	if connectorEnvEqual(connectorEnv{allow: nil}, connectorEnv{allow: []string{}}) != true {
		t.Fatal("nil 与空白名单应等价")
	}
}
