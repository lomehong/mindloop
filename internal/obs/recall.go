// 记忆召回台账 usage/mem-recall.jsonl：每次记忆检索一行（ts/source/
// query 摘要/命中/注入数/耗时/跳过原因）——BM25 召回质量的观测面。
// 此前检索是黑盒：命中了什么、注入了几条、失败因什么，全不可见；
// 台账让"记忆系统好不好用"成为可回答的问题（BaiLongma 记忆审计的
// 同款动机：本轮查询、命中 mem_id、注入数量、耗时、跳过原因）。
package obs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/lomehong/mindloop/internal/traj"
)

// RecallHit 是单条命中的台账形态（ID 供追溯，Score 供质量分布）。
type RecallHit struct {
	ID    string  `json:"id"`
	Type  string  `json:"type,omitempty"`
	Score float64 `json:"score"`
}

// RecallRow 是一次记忆检索的台账行。
type RecallRow struct {
	TS string `json:"ts"`
	// Source 是召回方：monolith（唤醒上下文）/ responder（对话回复）。
	Source string `json:"source"`
	// Query 摘要（截 120 字节）——台账在本机，摘要够定位是哪次检索，
	// 不存全文（消息原文已在轨迹上）。
	Query     string      `json:"query,omitempty"`
	QueryLen  int         `json:"query_len,omitempty"`
	HitCount  int         `json:"hit_count"`
	Hits      []RecallHit `json:"hits,omitempty"`
	Injected  int         `json:"injected"`
	ElapsedMS int64       `json:"elapsed_ms"`
	// Skipped 记录未注入的原因：empty_query / error / no_hits。
	Skipped string `json:"skipped,omitempty"`
	Error   string `json:"error,omitempty"`
}

// recallLedgerPath 是召回台账的落盘路径（与 llm-usage 同目录）。
func recallLedgerPath(dir string) string {
	return filepath.Join(dir, "usage", "mem-recall.jsonl")
}

// RecordRecall 追加一行召回台账。写失败静默吞掉还是喊出来由调用方
// 的 logf 决定——观测面缺一行不致命，但静默掩盖总量。
func RecordRecall(dir string, row RecallRow, logf func(format string, args ...any)) {
	fail := func(format string, args ...any) {
		if logf != nil {
			logf("obs: "+format, args...)
		}
	}
	if row.TS == "" {
		row.TS = traj.NowString()
	}
	usageDir := filepath.Join(dir, "usage")
	if err := os.MkdirAll(usageDir, 0o755); err != nil {
		fail("创建用量目录失败: %v", err)
		return
	}
	line, err := json.Marshal(row)
	if err != nil {
		fail("序列化召回记录失败: %v", err)
		return
	}
	f, err := os.OpenFile(recallLedgerPath(dir),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		fail("打开召回台账失败: %v", err)
		return
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		fail("写召回台账失败: %v", err)
	}
}

// LoadRecall 读台账尾部至多 limit 行（新→旧）。文件不存在或个别行
// 损坏不算错误——坏行跳过，台账是观测面不是事实源。
func LoadRecall(dir string, limit int) ([]RecallRow, error) {
	data, err := os.ReadFile(recallLedgerPath(dir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []RecallRow
	start := 0
	for i := 0; i <= len(data); i++ {
		if i == len(data) || data[i] == '\n' {
			if i > start {
				var row RecallRow
				if json.Unmarshal(data[start:i], &row) == nil && row.TS != "" {
					out = append(out, row)
				}
			}
			start = i + 1
		}
	}
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	// 新→旧：展示面直接可用。
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// RecallQuerySummary 是查询摘要的统一截断（台账与端点共用口径）。
func RecallQuerySummary(query string) string {
	b := []byte(query)
	if len(b) > 120 {
		b = b[:120]
	}
	return string(b) + func() string {
		if len(query) > 120 {
			return "…"
		}
		return ""
	}()
}

// RecallNow 是台账时间戳的统一出口（测试替换点）。
var RecallNow = func() string { return time.Now().UTC().Format(time.RFC3339Nano) }
