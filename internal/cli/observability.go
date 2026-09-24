package cli

import (
	"encoding/json"
	"os"
	"path/filepath"

	"mindloop/internal/llm"
	"mindloop/internal/traj"
)

// usageRecorder 返回接在 llm.Client.OnDone 上的观测回调：用量逐条
// 追加到 <dir>/usage/llm-usage.jsonl，健康标记写到 <dir>/llm-health
// .json（临时文件 + 原子改名——写一半的健康标记比没有更糟）。常驻
// 心智必须能看见花费和故障，这是观测面的落点。
func usageRecorder(dir string) func(llm.Usage, error) {
	return func(u llm.Usage, err error) {
		usageDir := filepath.Join(dir, "usage")
		_ = os.MkdirAll(usageDir, 0o755)

		rec := struct {
			TS               string `json:"ts"`
			PromptTokens     int    `json:"prompt_tokens"`
			CompletionTokens int    `json:"completion_tokens"`
			Error            string `json:"error,omitempty"`
		}{
			TS:               traj.NowString(),
			PromptTokens:     u.PromptTokens,
			CompletionTokens: u.CompletionTokens,
		}
		if err != nil {
			rec.Error = err.Error()
		}
		if line, jerr := json.Marshal(rec); jerr == nil {
			f, ferr := os.OpenFile(filepath.Join(usageDir, "llm-usage.jsonl"),
				os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
			if ferr == nil {
				f.Write(append(line, '\n'))
				f.Close()
			}
		}

		healthPath := filepath.Join(dir, "llm-health.json")
		health := loadHealth(healthPath)
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

// healthMarker 是健康标记文件的形态。
type healthMarker struct {
	LastOK            string `json:"last_ok,omitempty"`
	LastError         string `json:"last_error,omitempty"`
	LastErrorAt       string `json:"last_error_at,omitempty"`
	LastCheck         string `json:"last_check,omitempty"`
	ConsecutiveErrors int    `json:"consecutive_errors"`
}

func loadHealth(path string) healthMarker {
	var h healthMarker
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &h)
	}
	return h
}
