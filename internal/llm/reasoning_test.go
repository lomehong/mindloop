package llm

import (
	"testing"
)

// TestIsReasoningModel 表驱动钉死预算分层判定：内置前缀、
// "-thinking" 中缀、MINDLOOP_REASONING_MODELS 追加与脏值容忍。
func TestIsReasoningModel(t *testing.T) {
	tests := []struct {
		name  string
		env   string
		model string
		want  bool
	}{
		{name: "内置前缀 glm-5", model: "glm-5", want: true},
		{name: "内置前缀带版本", model: "glm-4.5-air", want: true},
		{name: "内置前缀 deepseek-r", model: "deepseek-r1", want: true},
		{name: "内置前缀 o 系列", model: "o3-mini", want: true},
		{name: "内置前缀 qwq", model: "qwq-32b", want: true},
		{name: "-thinking 中缀", model: "deepseek-v3-thinking", want: true},
		{name: "大小写不敏感", model: "GLM-5", want: true},
		{name: "非思考型普通模型", model: "gpt-4.1-mini", want: false},
		{name: "空模型名", model: "", want: false},
		{name: "环境变量追加命中", env: "kimi-k2.5, mine-new", model: "mine-new-alpha", want: true},
		{name: "环境变量追加对其它模型不误伤", env: "mine-new", model: "gpt-4.1-mini", want: false},
		{name: "环境变量忽略空段与空白", env: "  , ,mine-x ,", model: "mine-x-1", want: true},
		{name: "环境变量大小写不敏感", env: "MINE-Y", model: "mine-y-9", want: true},
		{name: "环境变量为空不影响内置", env: "", model: "glm-5", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.env != "" {
				t.Setenv(ReasoningModelEnvVar, tt.env)
			} else {
				t.Setenv(ReasoningModelEnvVar, "")
			}
			if got := isReasoningModel(tt.model); got != tt.want {
				t.Fatalf("isReasoningModel(%q) = %v, 期望 %v", tt.model, got, tt.want)
			}
		})
	}
}

// TestFromEnvReasoningModelsBudget 端到端：追加前缀应改变预算分层。
func TestFromEnvReasoningModelsBudget(t *testing.T) {
	t.Setenv(ReasoningModelEnvVar, "mine-new")
	t.Setenv("MINDLOOP_MODEL", "mine-new-1")
	t.Setenv("MINDLOOP_PROVIDER", "echo")
	t.Setenv("MINDLOOP_MAX_TOKENS", "")
	c, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if !isReasoningModel(c.Model) || c.MaxTokens != 32768 {
		t.Fatalf("追加的思考型前缀应套用 32768 预算，得到 %d（model=%q）", c.MaxTokens, c.Model)
	}
}
