// Package obs 是观测面的落盘实现：LLM 用量台账 + 健康标记。lib 不
// 做 IO 的原则不适用于这里——它本来就是"把观测写下去"的专用包。
//
// 台账 usage/llm-usage.jsonl：每次模型调用一行（ts/model/tokens/
// error），仪表盘的用量页即时聚合它。
// 健康标记 llm-health.json：连续错误计数 + 最近成败时刻（临时文件
// + 原子改名——写一半的健康标记比没有更糟）。
package obs

import (
	"encoding/json"
	"os"
	"path/filepath"

	"mindloop/internal/llm"
	"mindloop/internal/traj"
)

// UsageRecorder 返回接在 llm.Client.OnDone 上的回调：追加用量台账
// 并刷新健康标记。model/provider 随调用闭包捕获（OnDone 只带 Usage）。
func UsageRecorder(dir, model, provider string) func(llm.Usage, error) {
	return func(u llm.Usage, err error) {
		usageDir := filepath.Join(dir, "usage")
		_ = os.MkdirAll(usageDir, 0o755)

		rec := struct {
			TS               string `json:"ts"`
			PromptTokens     int    `json:"prompt_tokens"`
			CompletionTokens int    `json:"completion_tokens"`
			Model            string `json:"model,omitempty"`
			Provider         string `json:"provider,omitempty"`
			Error            string `json:"error,omitempty"`
		}{
			TS:               traj.NowString(),
			PromptTokens:     u.PromptTokens,
			CompletionTokens: u.CompletionTokens,
			Model:            model,
			Provider:         provider,
		}
		if err != nil {
			rec.Error = err.Error()
		}
		if line, jerr := json.Marshal(rec); jerr == nil {
			f, ferr := os.OpenFile(filepath.Join(usageDir, "llm-usage.jsonl"),
				os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
			if ferr == nil {
				_, _ = f.Write(append(line, '\n'))
				f.Close()
			}
		}

		healthPath := filepath.Join(dir, "llm-health.json")
		health := LoadHealth(healthPath)
		now := traj.NowString()
		if err != nil {
			health.ConsecutiveErrors++
			health.LastError = err.Error()
			health.LastErrorAt = now
		} else {
			health.ConsecutiveErrors = 0
			health.LastOK = now
			health.LastError = ""
		}
		health.LastCheck = now
		if data, jerr := json.MarshalIndent(health, "", "  "); jerr == nil {
			tmp := healthPath + ".tmp"
			if os.WriteFile(tmp, data, 0o644) == nil {
				_ = os.Rename(tmp, healthPath)
			}
		}
	}
}

// Health 是健康标记文件的形态。
type Health struct {
	LastOK            string `json:"last_ok,omitempty"`
	LastError         string `json:"last_error,omitempty"`
	LastErrorAt       string `json:"last_error_at,omitempty"`
	LastCheck         string `json:"last_check,omitempty"`
	ConsecutiveErrors int    `json:"consecutive_errors"`
}

// LoadHealth 读取健康标记；不存在返回零值。
func LoadHealth(path string) Health {
	var h Health
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &h)
	}
	return h
}
