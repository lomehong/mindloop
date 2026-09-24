package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"mindloop/internal/identity"
	"mindloop/internal/ids"
	"mindloop/internal/traj"
)

// nowStamp 供 traj new 的默认 slug 使用。
func nowStamp() string { return time.Now().UTC().Format("2006-01-02-15-04-05") }

// shortID 是 id8 的包内便捷别名。
func shortID(id string) string { return ids.Short(id, 8) }

// splitCSV 把逗号分隔串拆成去空白的非空切片。
func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := parts[:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// collectFieldFlags 把未注册的 `--key value` / `--key=value` /
// `--key` 旗标收编为载荷字段——`--from operator` 与
// `--field from=operator` 等价。写日志的步骤字段五花八门，要求每
// 个字段都预注册违背日志层的中立性；已知旗标（known）原样放行，
// 留给 pflag 解析。
func collectFieldFlags(args []string, known map[string]bool) ([]string, []string) {
	var rest []string
	var fields []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "--") {
			body := strings.TrimPrefix(a, "--")
			if eq := strings.Index(body, "="); eq >= 0 {
				k := body[:eq]
				if !known[k] {
					fields = append(fields, k+"="+body[eq+1:])
					continue
				}
			} else if !known[body] {
				if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
					fields = append(fields, body+"="+args[i+1])
					i++
				} else {
					fields = append(fields, body+"=true")
				}
				continue
			}
		}
		rest = append(rest, a)
	}
	return rest, fields
}

// parseValue 把 --field 的值解析为 JSON 对象/数组（其余保持字符串）。
func parseValue(v string) any {
	if strings.HasPrefix(v, "{") || strings.HasPrefix(v, "[") {
		var parsed any
		if err := json.Unmarshal([]byte(v), &parsed); err == nil {
			return parsed
		}
	}
	return v
}

// printJSONL 把步骤写为一行紧凑 JSON（不转义 HTML，URL 可读）。
func printJSONL(w io.Writer, s traj.Step) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return
	}
	w.Write(buf.Bytes()) // Encode 自带换行
}

// printPretty 输出人类可读的步骤摘要；full 时不截断字段值。
func printPretty(w io.Writer, s traj.Step, full bool) {
	fmt.Fprintf(w, "[%s] %s  %s\n", shortID(s.StepID), s.Type, s.TS)
	keys := make([]string, 0, len(s.Fields))
	for k := range s.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	const limit = 200
	for _, k := range keys {
		v, _ := s.Field(k)
		if !full {
			if r := []rune(v); len(r) > limit {
				v = string(r[:limit]) + "…"
			}
		}
		fmt.Fprintf(w, "  %-14s %s\n", k+":", v)
	}
}

// readInput 读取内容文件；'-' 表示 stdin。
func readInput(path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(path)
}

// loadIdentity 加载身份；失败时列出可用身份，把"名字打错"从死胡同
// 变成可自纠的提示（真实反馈：chat web 报找不到却不说有哪些）。
func (c *CLI) loadIdentity(name string) (*identity.Identity, error) {
	id, err := identity.Load(name)
	if err != nil {
		names, _ := identity.List()
		if len(names) > 0 {
			return nil, fmt.Errorf("身份 %q 不存在。可用: %s", name, strings.Join(names, ", "))
		}
		return nil, fmt.Errorf("身份 %q 不存在（还没有任何身份，先 mindloop identity create <名字>）", name)
	}
	return id, nil
}
