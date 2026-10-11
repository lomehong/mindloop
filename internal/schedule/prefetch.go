// 预热缓存（ACI，参考 BaiLongma 的 prefetch_cache）：every+exec 条目
// 成功执行的 stdout 落为身份目录下的 JSON 文件，心智组装唤醒上下文时
// 把有效期内（= 采集周期 every）的产物注入 prompt——模型开口前系统
// 已备好信息，省掉"想起来要查→调工具→等结果"的整条链路。
//
// 设计约束：
//   - 零配置：prefetch 缺省开启，条目级 "prefetch": false 显式关闭；
//     缓存生命周期天然对齐采集周期，不加 TTL 配置位。
//   - 宁缺毋假：执行失败、prefetch 关闭、输出为空 → 清掉旧缓存。
//     陈旧数据比没有数据更糟（模型会当真）。
//   - 文件即事实源：与 schedule-state.json 同哲学，写临时文件后
//     rename，多进程读侧永远看到完整文件。
package schedule

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// maxPrefetchBytes 是单条目缓存的内容上限：写盘时截断（超长输出
// 的价值密度低，注入预算另有总量控制）。
const maxPrefetchBytes = 8 * 1024

// PrefetchEntry 是预热缓存文件的落盘形态。
type PrefetchEntry struct {
	ID      string `json:"id"`
	At      string `json:"at"`      // RFC3339，采集时刻（本地时区）
	Seconds int    `json:"seconds"` // 采集周期（= 缓存有效期）
	Content string `json:"content"`
}

// prefetchPath 是条目的缓存文件路径。
func prefetchPath(dir, id string) string {
	return filepath.Join(dir, id+".json")
}

// writePrefetch 把成功输出落为预热缓存（原子写：tmp + rename）。
// 落盘失败只返回错误由调用方记日志——预热是增强，不能打断日程节拍。
func writePrefetch(dir, id string, now time.Time, every time.Duration, output string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if len(output) > maxPrefetchBytes {
		output = output[:maxPrefetchBytes] + "\n…（超长截断）"
	}
	entry := PrefetchEntry{
		ID:      id,
		At:      now.Format(time.RFC3339),
		Seconds: int(every.Seconds()),
		Content: output,
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, "."+id+".tmp")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, prefetchPath(dir, id))
}

// removePrefetch 清掉条目的缓存（执行失败/关闭/输出为空时调用）。
// 文件不存在不是错误。
func removePrefetch(dir, id string) {
	_ = os.Remove(prefetchPath(dir, id))
	_ = os.Remove(filepath.Join(dir, "."+id+".tmp"))
}

// LoadPrefetch 读取预热缓存目录的全部有效条目，按采集时间新→旧排序。
// 过期条目（age > 采集周期）跳过——它们会在下一次到点执行时被自然
// 刷新，不值得为读取方清理。目录不存在返回 nil（未启用预热）。
func LoadPrefetch(dir string, now time.Time) []PrefetchEntry {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []PrefetchEntry
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var entry PrefetchEntry
		if json.Unmarshal(data, &entry) != nil || entry.ID == "" || entry.Content == "" {
			continue
		}
		at, err := time.Parse(time.RFC3339, entry.At)
		if err != nil {
			continue
		}
		ttl := time.Duration(entry.Seconds) * time.Second
		if ttl <= 0 {
			ttl = 25 * time.Hour // 无周期信息的旧文件：宽松兜底一天
		}
		if now.Sub(at) > ttl {
			continue
		}
		out = append(out, entry)
	}
	// 新→旧：越新鲜的越值得保住预算。
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			ai, _ := time.Parse(time.RFC3339, out[i].At)
			aj, _ := time.Parse(time.RFC3339, out[j].At)
			if aj.After(ai) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// recordPrefetch 是 recordExec 的预热落点：按执行结果决定写或清。
// 手动执行（ExecuteNow）同样刷新缓存——内容过时想让模型拿到新数据
// 时，schedule run 是显式刷新入口。
func (r *Runtime) recordPrefetch(p Parsed, now time.Time, o ExecOutcome) {
	if r.opts.PrefetchDir == "" {
		return
	}
	switch {
	case o.Err != nil:
		removePrefetch(r.opts.PrefetchDir, p.Item.ID)
	case !p.Prefetch:
		removePrefetch(r.opts.PrefetchDir, p.Item.ID)
	case o.Output == "":
		removePrefetch(r.opts.PrefetchDir, p.Item.ID)
	default:
		if err := writePrefetch(r.opts.PrefetchDir, p.Item.ID, now, p.Every, o.Output); err != nil {
			r.logf("[%s] 预热缓存写入失败: %v", p.Item.ID, err)
		}
	}
}

// RenderPrefetch 把预热缓存渲染成心智上下文注入段：头部说明定位
// （已备好、不必重查、过期自动刷新），每条一行头 + 缩进正文，新→旧。
// 无有效条目返回空串。装配层经闭包把它交给 monolith（mind 包不
// import 本包——依赖倒置与 Schedule 接口同构）。
func RenderPrefetch(dir string, now time.Time) string {
	entries := LoadPrefetch(dir, now)
	if len(entries) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("预取缓存（日程预热产物，系统已备好——需要时直接引用，不必重查；过期后由日程自动刷新）：")
	for _, e := range entries {
		at, err := time.Parse(time.RFC3339, e.At)
		age := "刚刚"
		if err == nil {
			age = humanAge(now.Sub(at))
		}
		fmt.Fprintf(&b, "\n- [%s] %s", e.ID, age)
		for _, line := range strings.Split(strings.TrimRight(e.Content, "\n"), "\n") {
			b.WriteString("\n  ")
			b.WriteString(line)
		}
	}
	return b.String()
}

// humanAge 把时长折成人话（分钟内/小时内/天内）。
func humanAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "1 分钟内"
	case d < time.Hour:
		return fmt.Sprintf("%d 分钟前", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d 小时前", int(d.Hours()))
	default:
		return fmt.Sprintf("%d 天前", int(d.Hours()/24))
	}
}
