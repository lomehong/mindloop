package runner

import "testing"

// TestParseFinalContractTolerant：收口契约解析的鲁棒性——纯 JSON、
// 代码围栏、中文散文前缀/后缀（模型实证形态）都能提取成功；真正
// 缺 summary 的仍拒绝。
func TestParseFinalContractTolerant(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		summary string
		wantErr bool
	}{
		{"纯 JSON", `{"summary":"已完成","deliverables":[]}`, "已完成", false},
		{"代码围栏", "```json\n{\"summary\":\"已完成\",\"deliverables\":[]}\n```", "已完成", false},
		{"中文前缀", "以下是收口：\n{\"summary\":\"已推送测试卡片\",\"deliverables\":[]}", "已推送测试卡片", false},
		{"前后散文", "任务完成。结果：\n{\"summary\":\"卡片已上屏\",\"deliverables\":[\"reports/a.md\"]}\n以上。", "卡片已上屏", false},
		{"缺 summary", `{"deliverables":[]}`, "", true},
		{"纯文本无 JSON", "FINAL 外不会做判断", "", true},
	}
	for _, tc := range cases {
		summary, _, err := parseFinalContract(tc.raw)
		if tc.wantErr {
			if err == nil {
				t.Errorf("%s: 应失败，得 summary=%q", tc.name, summary)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: 意外失败 %v (raw=%q)", tc.name, err, tc.raw)
			continue
		}
		if summary != tc.summary {
			t.Errorf("%s: summary = %q，要 %q", tc.name, summary, tc.summary)
		}
	}
}
