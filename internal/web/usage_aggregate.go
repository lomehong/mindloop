package web

import (
	"bufio"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"mindloop/internal/identity"
	"mindloop/internal/obs"
)

// handleUsage 返回 viewer Usage 契约——**即时聚合**，无缓存层：
// 每次请求重读 usage/llm-usage.jsonl（文件很小），daily/by_model/
// totals 永远与台账一致，pending_bytes 恒 0（没有缓存就不存在
// "pending"），refreshing 恒 false。数据是真实的：recorder 把每次
// 模型调用的 token 计数落盘在台账里。
//
// 用量已知性：供应商未返回 usage 的调用（usage_known=false）不计为
// 零成本——totals/by_model 的 unknown_calls 与每天的 unknown 显式
// 标出；旧台账行缺该字段按已知处理。admission 块报告每日预算与
// 熔断状态（未设置时 daily_limit=0）。
func (s *Server) handleUsage(w http.ResponseWriter, _ *http.Request, id *identity.Identity, _ []string) {
	ledgerPath := filepath.Join(id.Dir, "usage", "llm-usage.jsonl")
	entries, skipped := parseUsageLedger(ledgerPath)

	type dayStats struct {
		Rows      int    `json:"rows"`
		InMsg     int    `json:"in_msg"`
		OutMsg    int    `json:"out_msg"`
		Runs      int    `json:"runs"`
		Reasoning int    `json:"reasoning"`
		Calls     int    `json:"calls"`
		Unknown   int    `json:"unknown"`
		In        int    `json:"in"`
		Out       int    `json:"out"`
		Think     int    `json:"think"`
		Source    string `json:"source"`
	}
	type modelStats struct {
		Calls        int `json:"calls"`
		In           int `json:"in"`
		Out          int `json:"out"`
		Think        int `json:"think"`
		UnknownCalls int `json:"unknown_calls"`
	}
	daily := map[string]*dayStats{}
	byModel := map[string]*modelStats{}
	totals := modelStats{}
	var firstDay string

	for _, e := range entries {
		day := e.day
		if day == "" {
			continue
		}
		if firstDay == "" || day < firstDay {
			firstDay = day
		}
		d := daily[day]
		if d == nil {
			d = &dayStats{Source: "ledger"}
			daily[day] = d
		}
		d.Rows++
		d.Calls++
		if !e.failed {
			d.In += e.prompt
			d.Out += e.completion
		}
		if e.unknown {
			d.Unknown++
		}
		m := byModel[e.model]
		if m == nil {
			m = &modelStats{}
			byModel[e.model] = m
		}
		m.Calls++
		m.In += e.prompt
		m.Out += e.completion
		if e.unknown {
			m.UnknownCalls++
		}
		totals.Calls++
		totals.In += e.prompt
		totals.Out += e.completion
		if e.unknown {
			totals.UnknownCalls++
		}
	}

	// viewer 期望 daily 为 [day, UsageDay][]——按日期升序。
	dailyOut := [][2]any{}
	dayKeys := make([]string, 0, len(daily))
	for k := range daily {
		dayKeys = append(dayKeys, k)
	}
	sort.Strings(dayKeys)
	for _, k := range dayKeys {
		dailyOut = append(dailyOut, [2]any{k, daily[k]})
	}
	modelOut := map[string]modelStats{}
	for k, v := range byModel {
		modelOut[k] = *v
	}

	available := len(entries) > 0
	var generated *string
	if available {
		if fi, err := os.Stat(ledgerPath); err == nil {
			g := fi.ModTime().UTC().Format("2006-01-02T15:04:05Z")
			generated = &g
		}
	}
	var since *string
	if firstDay != "" {
		since = &firstDay
	}
	writeJSON(w, 200, map[string]any{
		"identity":      map[string]string{"id": id.Name, "name": id.Name},
		"available":     available,
		"refreshing":    false,
		"pending_bytes": 0,
		"generated":     generated,
		"rows":          len(entries),
		"skipped":       skipped,
		"ledger":        map[string]any{"rows": len(entries), "skipped": skipped, "since": since},
		"daily":         dailyOut,
		"by_model":      modelOut,
		"totals":        totals,
		"admission":     obs.LoadAdmission(id.Dir),
	})
}

type usageEntry struct {
	day        string
	model      string
	prompt     int
	completion int
	failed     bool
	// unknown = 成功调用但供应商未返回用量。失败行由 failed 单独
	// 归类；旧行缺 usage_known 字段按已知处理。
	unknown bool
}

// parseUsageLedger 解析台账：{ts,prompt_tokens,completion_tokens,
// error?,model?,usage_known?}（v0.2 起 recorder 也记 model；旧数据
// model=unknown）。usage_known 只有显式 false 才算未知——缺失是
// 旧台账，按已知处理。
func parseUsageLedger(path string) ([]usageEntry, int) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0
	}
	defer f.Close()
	var out []usageEntry
	skipped := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var rec struct {
			TS               string `json:"ts"`
			PromptTokens     int    `json:"prompt_tokens"`
			CompletionTokens int    `json:"completion_tokens"`
			Error            string `json:"error"`
			Model            string `json:"model"`
			UsageKnown       *bool  `json:"usage_known"`
		}
		if err := json.Unmarshal([]byte(line), &rec); err != nil || rec.TS == "" {
			skipped++
			continue
		}
		day := rec.TS[:10] // YYYY-MM-DD（ts 一律 UTC）
		if len(day) != 10 || day[4] != '-' {
			day = "unknown"
		}
		model := rec.Model
		if model == "" {
			model = "unknown"
		}
		out = append(out, usageEntry{
			day:        day,
			model:      model,
			prompt:     rec.PromptTokens,
			completion: rec.CompletionTokens,
			failed:     rec.Error != "",
			unknown:    rec.UsageKnown != nil && !*rec.UsageKnown && rec.Error == "",
		})
	}
	if err := sc.Err(); err != nil {
		// 半程读取失败：按已解析部分返回（聚合容忍不完整数据）。
		return out, skipped
	}
	return out, skipped
}
