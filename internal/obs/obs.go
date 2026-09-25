// Package obs 是观测面的落盘实现：LLM 用量台账 + 健康标记。lib 不
// 做 IO 的原则不适用于这里——它本来就是"把观测写下去"的专用包。
//
// 台账 usage/llm-usage.jsonl：每次模型调用一行（ts/model/tokens/
// error），仪表盘的用量页即时聚合它。
// 健康标记 llm-health.json：连续错误计数 + 最近成败时刻（临时文件
// + 原子改名——写一半的健康标记比没有更糟）。
package obs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"mindloop/internal/llm"
	"mindloop/internal/traj"
)

// UsageRecorder 返回接在 llm.Client.OnDone 上的回调：追加用量台账
// 并刷新健康标记。model/provider 随调用闭包捕获（OnDone 只带 Usage）。
// logf 非空时落盘失败会喊出来——观测面写失败等于"账本缺一行"，
// 静默吞掉就是掩盖；调用方没有合适日志通道时传 nil 保持安静。
func UsageRecorder(dir, model, provider string, logf func(format string, args ...any)) func(llm.Usage, error) {
	fail := func(format string, args ...any) {
		if logf != nil {
			logf("obs: "+format, args...)
		}
	}
	return func(u llm.Usage, err error) {
		usageDir := filepath.Join(dir, "usage")
		if merr := os.MkdirAll(usageDir, 0o755); merr != nil {
			fail("创建用量目录失败: %v", merr)
		}

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
			if ferr != nil {
				fail("打开用量台账失败: %v", ferr)
			} else if _, werr := f.Write(append(line, '\n')); werr != nil {
				fail("写用量台账失败: %v", werr)
				f.Close()
			} else {
				f.Close()
			}
		} else {
			fail("序列化用量记录失败: %v", jerr)
		}

		// 健康标记是读-改-写：思考档与请求档两个 llm.Client 的
		// OnDone 会并发落到同一目录，必须持目录锁互斥，否则连续
		// 错误计数互相覆盖、固定 .tmp 名互相践踏。锁等待有界——
		// 观测面不能反过来卡住模型调用收尾。
		healthPath := filepath.Join(dir, "llm-health.json")
		release, lerr := traj.AcquireDirLock(context.Background(), healthPath+".lock", 5*time.Second)
		if lerr != nil {
			fail("健康标记加锁失败（本轮健康状态未更新）: %v", lerr)
		} else {
			defer release()
			health, corrupt := loadHealthChecked(healthPath)
			if corrupt != nil {
				fail("健康标记损坏，已按零值重建（旧内容: %v）", corrupt)
			}
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
				// tmp 名带 pid：即使锁失效（如 NFS）也不会两个写者
				// 践踏同一个临时文件。
				tmp := fmt.Sprintf("%s.%d.tmp", healthPath, os.Getpid())
				if werr := os.WriteFile(tmp, data, 0o644); werr != nil {
					fail("写健康标记失败: %v", werr)
				} else if rerr := os.Rename(tmp, healthPath); rerr != nil {
					fail("健康标记原子改名失败: %v", rerr)
				}
			} else {
				fail("序列化健康标记失败: %v", jerr)
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

// LoadHealth 读取健康标记；不存在或损坏返回零值（宽容形态，供
// 只读方使用；写方用 loadHealthChecked 以便对损坏告警）。
func LoadHealth(path string) Health {
	h, _ := loadHealthChecked(path)
	return h
}

// loadHealthChecked 读取健康标记并报告损坏：文件存在但解析失败时
// 返回零值 Health 与解析错误——连续错误计数被意外清零是掩盖故障，
// 调用方必须有机会喊出来。
func loadHealthChecked(path string) (Health, error) {
	var h Health
	data, err := os.ReadFile(path)
	if err != nil {
		return h, nil // 不存在 = 零值起步，合法
	}
	if jerr := json.Unmarshal(data, &h); jerr != nil {
		return Health{}, jerr
	}
	return h, nil
}
