package obs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mindloop/internal/llm"
)

// TestUsageRecorderWritesUsageAndHealth：一次调用落一行台账 + 刷新
// 健康标记；成功清零连续错误，失败累加并记录错误。
func TestUsageRecorderWritesUsageAndHealth(t *testing.T) {
	dir := t.TempDir()
	rec := UsageRecorder(dir, "glm-5", "openai-compatible", nil)

	rec(llm.Usage{PromptTokens: 10, CompletionTokens: 5}, nil)

	data, err := os.ReadFile(filepath.Join(dir, "usage", "llm-usage.jsonl"))
	if err != nil {
		t.Fatalf("台账未落盘: %v", err)
	}
	var line struct {
		PromptTokens     int    `json:"prompt_tokens"`
		CompletionTokens int    `json:"completion_tokens"`
		Model            string `json:"model"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &line); err != nil {
		t.Fatalf("台账行不可解析: %v", err)
	}
	if line.PromptTokens != 10 || line.CompletionTokens != 5 || line.Model != "glm-5" {
		t.Fatalf("台账内容不符: %+v", line)
	}

	health := LoadHealth(filepath.Join(dir, "llm-health.json"))
	if health.ConsecutiveErrors != 0 || health.LastOK == "" {
		t.Fatalf("成功后健康标记应为清零态: %+v", health)
	}

	rec(llm.Usage{}, contextCanceled)
	health = LoadHealth(filepath.Join(dir, "llm-health.json"))
	if health.ConsecutiveErrors != 1 || health.LastError == "" {
		t.Fatalf("失败后健康标记应累加错误: %+v", health)
	}
}

var contextCanceled = errCanceled{}

type errCanceled struct{}

func (errCanceled) Error() string { return "context canceled" }

// TestUsageRecorderReportsWriteFailure：观测面写失败必须经 logf
// 喊出来（dir 本身是文件 → MkdirAll 必败）——静默吞错等于账本
// 缺行无人知。
func TestUsageRecorderReportsWriteFailure(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(dir, []byte("i am a file"), 0o644); err != nil {
		t.Fatal(err)
	}
	called := make(chan string, 4)
	rec := UsageRecorder(dir, "m", "echo", func(format string, args ...any) {
		called <- format
	})
	rec(llm.Usage{PromptTokens: 1}, nil)
	select {
	case msg := <-called:
		if !strings.Contains(msg, "obs:") {
			t.Fatalf("日志应带 obs 前缀: %q", msg)
		}
	default:
		t.Fatal("写失败应经 logf 报告")
	}
}

// TestUsageRecorderCarriesAttribution：台账一行携带归因、时延、重试
// 与用量已知标记——审计时每笔账都能落到任务/运行/阶段。
func TestUsageRecorderCarriesAttribution(t *testing.T) {
	dir := t.TempDir()
	rec := UsageRecorder(dir, "glm-5", "openai-compatible", nil)
	rec(llm.Usage{
		PromptTokens: 10, CompletionTokens: 5, Known: true,
		LatencyMS: 1234, Retries: 2,
		Task: "task-7", Run: "run-7", Attempt: 3,
		Thinker: "monolith", Wake: "scheduled spontaneity", Phase: "task",
	}, nil)

	data, err := os.ReadFile(filepath.Join(dir, "usage", "llm-usage.jsonl"))
	if err != nil {
		t.Fatalf("台账未落盘: %v", err)
	}
	var line struct {
		UsageKnown bool   `json:"usage_known"`
		LatencyMS  int64  `json:"latency_ms"`
		Retries    int    `json:"retries"`
		Task       string `json:"task"`
		Run        string `json:"run"`
		Attempt    int    `json:"attempt"`
		Thinker    string `json:"thinker"`
		Wake       string `json:"wake"`
		Phase      string `json:"phase"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &line); err != nil {
		t.Fatalf("台账行不可解析: %v", err)
	}
	if !line.UsageKnown || line.LatencyMS != 1234 || line.Retries != 2 {
		t.Fatalf("记账字段: %+v", line)
	}
	if line.Task != "task-7" || line.Run != "run-7" || line.Attempt != 3 ||
		line.Thinker != "monolith" || line.Wake != "scheduled spontaneity" || line.Phase != "task" {
		t.Fatalf("归因字段: %+v", line)
	}
}

// TestUsageRecorderUnknownUsageExplicit：用量未知时 usage_known 显式
// 写出 false（无 omitempty）——读方据此显示"未知"，而不是把缺字段
// 与零混为一谈。
func TestUsageRecorderUnknownUsageExplicit(t *testing.T) {
	dir := t.TempDir()
	rec := UsageRecorder(dir, "m", "echo", nil)
	rec(llm.Usage{}, nil)

	data, err := os.ReadFile(filepath.Join(dir, "usage", "llm-usage.jsonl"))
	if err != nil {
		t.Fatalf("台账未落盘: %v", err)
	}
	if !strings.Contains(string(data), `"usage_known":false`) {
		t.Fatalf("未知用量应显式写出 usage_known:false: %s", data)
	}
}
