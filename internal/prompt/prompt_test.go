package prompt

import (
	"strings"
	"testing"
	"unicode/utf8"

	"mindloop/internal/traj"
)

// mkSteps 生成 n 个带序号内容的 thought 步骤（含头行以外的类型按
// 需要在用例里覆盖）。
func mkSteps(n int) []traj.Step {
	steps := make([]traj.Step, 0, n)
	for i := 0; i < n; i++ {
		s := traj.NewStep("thought")
		s.Fields["content"] = strings.Repeat("x", 40) + " 第 " + string(rune('A'+i%26)) + " 组 #" + itoa(i)
		steps = append(steps, s)
	}
	return steps
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func countElided(msgs []Message) int {
	total := 0
	for _, m := range msgs {
		if strings.Contains(m.Content, "此处省略") {
			total++
		}
	}
	return total
}

func TestRenderSelectionAndMarker(t *testing.T) {
	steps := mkSteps(100)
	msgs := Render(steps, Options{Head: 1, Tail: 10})
	// 1 头 + 省略标记 + 10 尾 = 12 条消息（角色相同会合并，这里
	// 全是 thought/user，所以被合并得更少——只断言关键事实）。
	if n := countElided(msgs); n != 1 {
		t.Fatalf("省略标记出现 %d 次，应为 1", n)
	}
	rendered := 0
	for _, m := range msgs {
		rendered += strings.Count(m.Content, "第 ")
	}
	// 头 1 + 尾 10 = 11 个步骤被渲染。
	if rendered != 11 {
		t.Fatalf("渲染了 %d 个步骤，应为 11", rendered)
	}
}

func TestRenderSmallLogHasNoMarker(t *testing.T) {
	steps := mkSteps(5)
	msgs := Render(steps, Options{Head: 1, Tail: 10})
	if n := countElided(msgs); n != 0 {
		t.Fatalf("小日志不应有省略标记，出现 %d 次", n)
	}
	if got := strings.Count(msgs[0].Content, "["); got != 5 {
		t.Fatalf("5 个步骤应全部渲染，实际渲染 %d 个", got)
	}
}

func TestPinRescuesMiddleStep(t *testing.T) {
	steps := mkSteps(100)
	target := steps[50].StepID[:8]
	msgs := Render(steps, Options{Head: 1, Tail: 10, Pin: []string{target}})
	found := false
	for _, m := range msgs {
		if strings.Contains(m.Content, "["+target+"]") {
			found = true
		}
	}
	if !found {
		t.Fatalf("被点名的中段步骤 %s 没有被渲染", target)
	}
}

func TestRoleMappingAndMerging(t *testing.T) {
	steps := []traj.Step{
		traj.NewStep("thought"),   // user
		traj.NewStep("final"),     // assistant
		traj.NewStep("thought"),   // user
		traj.NewStep("thought"),   // user（与前一条合并）
		traj.NewStep("reasoning"), // assistant
	}
	msgs := Render(steps, Options{Tail: 10, AssistantTypes: []string{"final", "reasoning"}})
	var roles []Role
	for _, m := range msgs {
		roles = append(roles, m.Role)
	}
	want := []Role{RoleUser, RoleAssistant, RoleUser, RoleAssistant}
	if len(roles) != len(want) {
		t.Fatalf("角色序列 %v，应为 %v", roles, want)
	}
	for i := range want {
		if roles[i] != want[i] {
			t.Fatalf("角色序列 %v，应为 %v（相邻同角色必须合并）", roles, want)
		}
	}
}

// TestBandGridStableUnderGrowth 是缓存网格的核心断言：日志在同一个
// 块内增长时，既存行的档位一个都不变；跨越块边界的那次追加，恰好
// 只改写旧"新近块"里的行——每 Block 步一次重写，这正是设计承诺。
func TestBandGridStableUnderGrowth(t *testing.T) {
	opts := Options{Head: 1, Tail: 50, Block: 20, FieldLimit: 2048, BlockFieldLimit: 8192}
	n := 100
	// 块内增长（95→100，新近块起点都是 80）：一切档位不变。
	start95 := opts.windowStart(95)
	start100 := opts.windowStart(100)
	if start95 != start100 {
		t.Fatalf("块内增长不应移动窗口起点：%d vs %d", start95, start100)
	}
	for _, i := range []int{0, 5, 45, 80, 95, 99} {
		b1 := opts.bandFor(i, 95)
		b2 := opts.bandFor(i, 100)
		if b1 != b2 {
			t.Fatalf("行 %d 的档位在块内增长后变化：%+v vs %+v", i, b1, b2)
		}
	}
	// 跨越块边界（100→101，新块从下标 100 开始）：只有旧新近块
	// [80,100) 的行被改写，其余纹丝不动。
	for _, i := range []int{0, 5, 45, 79} {
		if opts.bandFor(i, 100) != opts.bandFor(i, 101) {
			t.Fatalf("边界跨越不应影响行 %d", i)
		}
	}
	for _, i := range []int{80, 95, 99} {
		if opts.bandFor(i, 100) == opts.bandFor(i, 101) {
			t.Fatalf("边界跨越应改写旧新近块的行 %d", i)
		}
	}
	// 档位分层正确：新近块放宽，窗口其余为常规档，中段省略。
	if got := opts.bandFor(95, n); got.limit != opts.BlockFieldLimit || got.elided {
		t.Fatalf("第 95 行应为新近整块档位，得到 %+v", got)
	}
	if got := opts.bandFor(45, n); got.limit != opts.FieldLimit || got.elided {
		t.Fatalf("第 45 行应为常规档位，得到 %+v", got)
	}
	if got := opts.bandFor(39, n); !got.elided {
		t.Fatalf("第 39 行应在省略区，得到 %+v", got)
	}
	// 窗口起点只在跨越对齐边界时移动（109→110：59→60 对齐后 40→60）。
	if opts.windowStart(109) == opts.windowStart(110) {
		t.Fatalf("窗口起点应在对齐边界处前移")
	}
}

func TestTruncationRespectsUTF8AndMarks(t *testing.T) {
	long := strings.Repeat("汉", 2000) // 6000 字节
	s := traj.NewStep("thought")
	s.Fields["content"] = long
	msgs := Render([]traj.Step{s}, Options{Tail: 10, FieldLimit: 2048})
	content := msgs[0].Content
	if !utf8.ValidString(content) {
		t.Fatal("截断产生了非法的 UTF-8")
	}
	if !strings.Contains(content, "已截断") || !strings.Contains(content, "traj show") {
		t.Fatalf("截断标记缺失：%q", tailOf(content, 120))
	}
	if strings.Count(content, "汉") > 700 {
		t.Fatalf("截断未生效，保留了 %d 个汉字", strings.Count(content, "汉"))
	}
}

func tailOf(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

func TestBudgetBinarySearchThenHardCut(t *testing.T) {
	steps := mkSteps(200) // 每条约 60 字节内容，总 12KB+
	budget := 4000
	msgs := Render(steps, Options{Tail: 0, Head: 0, FieldLimit: 4096, MaxBytes: budget})
	if got := bytesOf(msgs); got > budget {
		t.Fatalf("渲染 %d 字节超出预算 %d", got, budget)
	}
	if got := bytesOf(msgs); got < budget/4 {
		t.Fatalf("二分过度收缩：只用了 %d/%d 字节", got, budget)
	}
}

func TestHardCutDropsTailWithTrace(t *testing.T) {
	msgs := []Message{
		{Role: RoleUser, Content: strings.Repeat("a", 3000)},
		{Role: RoleAssistant, Content: strings.Repeat("b", 3000)},
	}
	out := hardCut(msgs, 3500)
	if got := bytesOf(out); got > 3500 {
		t.Fatalf("硬切后 %d 字节仍超预算", got)
	}
	joined := ""
	for _, m := range out {
		joined += m.Content
	}
	if !strings.Contains(joined, "预算超出") {
		t.Fatal("硬切必须留下可追查的痕迹")
	}
}

func TestMetaFieldsExcluded(t *testing.T) {
	s := traj.NewStep("message")
	s.Fields["from"] = "operator"
	s.Fields["run_id"] = "should-not-appear"
	s.Fields["parent_traj_ref"] = "also-hidden"
	s.Fields["secret"] = "hush"
	msgs := Render([]traj.Step{s}, Options{Tail: 10, ExcludeFields: []string{"secret"}})
	content := msgs[0].Content
	for _, banned := range []string{"should-not-appear", "also-hidden", "hush"} {
		if strings.Contains(content, banned) {
			t.Fatalf("字段 %q 泄漏进渲染", banned)
		}
	}
	if !strings.Contains(content, "from=operator") {
		t.Fatal("正常字段被误删")
	}
}
