package cli

import (
	"strings"
	"testing"

	"github.com/lomehong/mindloop/internal/identity"
	"github.com/lomehong/mindloop/internal/traj"
)

// TestUISetListRemoveRoundtrip：ui set 落 scene 步骤、幂等判重不落
// 冗余步骤、--remove 落 remove 步骤、list 投影 manifest——工具面的
// 全链路行为（scene.md §5.1）。
func TestUISetListRemoveRoundtrip(t *testing.T) {
	taskTestIdentity(t)

	// set：落 upsert 步骤。
	code, out, errText := runCLI(t, "ui", "set", "ada", "weather-bj",
		"--kind", "text", "--intent", "ambient", "--summary", "北京 18° 晴",
		"--data", `{"title":"天气","body":"晴"}`)
	if code != 0 {
		t.Fatalf("set exit=%d stderr=%s", code, errText)
	}
	if !strings.Contains(out, "已声明卡片 weather-bj") {
		t.Fatalf("set 回显不符: %s", out)
	}

	// 同内容重复 set：判重，不落新步骤。
	code, out, _ = runCLI(t, "ui", "set", "ada", "weather-bj",
		"--kind", "text", "--intent", "ambient", "--summary", "北京 18° 晴",
		"--data", `{"body":"晴","title":"天气"}`) // 键序不同，canonical 后同内容
	if code != 0 || !strings.Contains(out, "无变化") {
		t.Fatalf("重复 set 应判重: code=%d out=%s", code, out)
	}

	// 内容变化：新步骤。
	code, out, _ = runCLI(t, "ui", "set", "ada", "weather-bj",
		"--kind", "text", "--intent", "inform", "--summary", "北京 21° 多云")
	if code != 0 || !strings.Contains(out, "已声明") {
		t.Fatalf("内容变化应落步骤: code=%d out=%s", code, out)
	}

	// list：manifest 投影。
	code, out, _ = runCLI(t, "ui", "list", "ada")
	if code != 0 || !strings.Contains(out, "weather-bj (text) 「北京 21° 多云」 inform") {
		t.Fatalf("list 投影不符: code=%d out=%s", code, out)
	}

	// remove：落 remove 步骤；再 remove 幂等安静。
	code, out, _ = runCLI(t, "ui", "set", "ada", "weather-bj", "--remove")
	if code != 0 || !strings.Contains(out, "已移除") {
		t.Fatalf("remove 回显不符: %s", out)
	}
	code, out, _ = runCLI(t, "ui", "set", "ada", "weather-bj", "--remove")
	if code != 0 || !strings.Contains(out, "不存在") {
		t.Fatalf("重复 remove 应幂等: %s", out)
	}

	// 轨迹核验：恰好 3 条 scene 步骤（upsert、变更 upsert、remove）。
	id, err := identity.Load("ada")
	if err != nil {
		t.Fatal(err)
	}
	steps, err := id.Timeline.Steps()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, s := range steps {
		if s.Type != traj.TypeScene {
			continue
		}
		n++
	}
	if n != 3 {
		t.Fatalf("scene 步骤数 = %d，要 3（判重生效）", n)
	}
}

// TestUISetValidation：缺 kind/summary、坏 JSON 拒绝（exit 1）。
func TestUISetValidation(t *testing.T) {
	taskTestIdentity(t)
	if code, _, _ := runCLI(t, "ui", "set", "ada", "x", "--summary", "s"); code != 1 {
		t.Fatal("缺 kind 应失败")
	}
	if code, _, _ := runCLI(t, "ui", "set", "ada", "x", "--kind", "text", "--summary", "s", "--data", "{bad"); code != 1 {
		t.Fatal("坏 JSON 应失败")
	}
	if code, _, _ := runCLI(t, "ui", "set", "ada", "x", "--kind", "text", "--intent", "urgent", "--summary", "s"); code != 1 {
		t.Fatal("非法 intent 应失败")
	}
}
