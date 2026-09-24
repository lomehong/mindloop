package web

import (
	"encoding/json"
	"strconv"
	"time"

	"mindloop/internal/traj"
)

// stepToJSON 把一个轨迹步骤转为前端期望的字段平铺形态——envelope 优先，
// 字段按字典序并排序（与 viewer 的 step-colors.ts 期望一致）。
func stepToJSON(s traj.Step) map[string]any {
	m := map[string]any{
		"type":    s.Type,
		"step_id": s.StepID,
		"ts":      s.TS,
	}
	keys := make([]string, 0, len(s.Fields))
	for k := range s.Fields {
		keys = append(keys, k)
	}
	// 简单排序保持字段顺序确定性
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	for _, k := range keys {
		m[k] = s.Fields[k]
	}
	return m
}

// jsonBytes 是 raw JSON 透传（health marker 文件无需解析）。
func jsonBytes(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return map[string]any{"raw": string(b)}
	}
	return v
}

// toInt 解析十进制整数，失败返回 0 与错误——上层决定忽略。
func toInt(s string) (int, error) { return strconv.Atoi(s) }

// memStore 是当前唯一的内存工厂：identityDir/memories/。通过局部
// 函数注入避免循环导入——handlers.go 已经依赖了 identity/traj，再
// 引入 mem 形成瞬时 handler；不如这里行内包装。
func memStore(identityDir string) memStoreIface {
	return &realMemStore{dir: identityDir + "/memories"}
}

// memStoreIface 是 handlers.go 不感知 mem 包具体实现的接口——
// 防止循环导入。真实实现见 realMemStore.go。
type memStoreIface interface {
	List() ([]memoryRow, error)
	Search(query string, topK int) ([]memoryRow, error)
}

type memoryRow struct {
	ID      string
	Type    string
	Summary string
	Content string
	Created time.Time
	Path    string
}
