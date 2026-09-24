// Package traj 实现了 Headlong 风格的追加式 JSONL 轨迹日志：每条
// 时间线一个文件，每行一个扁平的 JSON 对象（"步骤"）。文件本身就是
// API——任何 JSON 工具（jq、grep、文本编辑器、其他进程）无需本包
// 即可读取。
package traj

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"mindloop/internal/ids"
)

// TimeFormat 是带毫秒精度的 RFC 3339。所有时间戳一律 UTC：
// Headlong 的日志曾混用过时区偏移，导致排序出现 8 小时的幻影间隔。
const TimeFormat = "2006-01-02T15:04:05.000Z07:00"

// reservedTypes 是结构性步骤类型，它们永远不会从环境继承 run id
// （与 Headlong 的 trajectory / shellm-run 规则相同，防止嵌套运行
// 被打上启动者的簿记标记）。
var reservedTypes = map[string]bool{
	"trajectory": true,
	"run":        true,
}

// IsReservedType 报告 typ 是否为结构性步骤类型。
func IsReservedType(typ string) bool { return reservedTypes[typ] }

// Step 是轨迹文件中的一行。信封（Type、StepID、TS）是结构性的；
// 其余全是载荷，放在 Fields 里。JSON 形态是扁平的——信封键在前，
// 其余字段按键排序——因此解析/序列化往返后字节稳定。
type Step struct {
	Type   string
	StepID string
	TS     string
	Fields map[string]any
}

// NewStep 用新 UUID 和当前 UTC 时间戳构建一个步骤。
func NewStep(typ string) Step {
	return Step{Type: typ, StepID: ids.NewUUID(), TS: NowString(), Fields: map[string]any{}}
}

// NowString 返回 TimeFormat 格式的当前 UTC 时间。
func NowString() string { return time.Now().UTC().Format(TimeFormat) }

// ParseStep 解码一行 JSONL。数字保持为 json.Number，使解析/序列化
// 往返不会重新格式化它们。容错留给调用方（读取方跳过坏行）——与
// Headlong 的 fromjson? 纪律一致：一行损坏绝不能拖垮读取方。
func ParseStep(line []byte) (Step, error) {
	var m map[string]any
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.UseNumber()
	if err := dec.Decode(&m); err != nil {
		return Step{}, fmt.Errorf("mindloop: 坏的步骤行: %w", err)
	}
	s := Step{}
	var ok bool
	if s.Type, ok = popString(m, "type"); !ok {
		return Step{}, errors.New("mindloop: 步骤缺少 type")
	}
	if s.StepID, ok = popString(m, "step_id"); !ok {
		return Step{}, errors.New("mindloop: 步骤缺少 step_id")
	}
	if s.TS, ok = popString(m, "ts"); !ok {
		return Step{}, errors.New("mindloop: 步骤缺少 ts")
	}
	if m == nil {
		m = map[string]any{}
	}
	s.Fields = m
	return s, nil
}

func popString(m map[string]any, key string) (string, bool) {
	v, ok := m[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	delete(m, key)
	return s, true
}

// MarshalJSON 写出扁平的行形式：type、step_id、ts，然后是其余字段
// 按键排序。与信封同名的载荷字段会被丢弃——信封优先。
func (s Step) MarshalJSON() ([]byte, error) {
	keys := make([]string, 0, len(s.Fields))
	for k := range s.Fields {
		switch k {
		case "type", "step_id", "ts":
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b bytes.Buffer
	b.WriteByte('{')
	write := func(key string, v any) error {
		kb, err := json.Marshal(key)
		if err != nil {
			return err
		}
		vb, err := json.Marshal(v)
		if err != nil {
			return err
		}
		if b.Len() > 1 {
			b.WriteByte(',')
		}
		b.Write(kb)
		b.WriteByte(':')
		b.Write(vb)
		return nil
	}
	if err := write("type", s.Type); err != nil {
		return nil, err
	}
	if err := write("step_id", s.StepID); err != nil {
		return nil, err
	}
	if err := write("ts", s.TS); err != nil {
		return nil, err
	}
	for _, k := range keys {
		if err := write(k, s.Fields[k]); err != nil {
			return nil, err
		}
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// Field 以字符串形式返回一个载荷字段。
func (s Step) Field(key string) (string, bool) {
	v, ok := s.Fields[key]
	if !ok || v == nil {
		return "", false
	}
	switch x := v.(type) {
	case string:
		return x, true
	case json.Number:
		return x.String(), true
	case bool:
		return strconv.FormatBool(x), true
	default:
		return fmt.Sprint(x), true
	}
}

// String 渲染一行人类可读的摘要："[id8] type :: content"。
func (s Step) String() string {
	summary, ok := s.Field("content")
	if !ok || summary == "" {
		keys := make([]string, 0, len(s.Fields))
		for k := range s.Fields {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if v, ok := s.Field(k); ok {
				summary = k + "=" + v
				break
			}
		}
	}
	return fmt.Sprintf("[%s] %s :: %s", ids.Short(s.StepID, 8), s.Type, oneLine(summary, 100))
}

// OneLine 把多行文本压成单行（换行转义 + 按 rune 截断 + 省略号）
// ——摘要展示的统一形态，mind/cli 共用这一份实现。
func OneLine(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", "\\n")
	r := []rune(s)
	if len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}

func oneLine(s string, max int) string { return OneLine(s, max) }
