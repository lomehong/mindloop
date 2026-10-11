// Package profile 是操作员画像：稳定特征的蒸馏层——"总是该在上下文
// 里"的长期属性（沟通风格/技术领域/偏好/角色），不依赖 BM25 命中
// （mem 是可检索层，画像是常驻层；一个回答"我该怎么跟 ta 说话"，
// 一个回答"ta 说过什么"）。BaiLongma 用户画像的最小形态：属性 +
// 证据来源，无置信度评分（v1——置信度等真实使用后再决定是否值得）。
//
// 谁来写：心智经 `mindloop profile set` 自维护（mem add 同款"自组
// 合"哲学——工具是它的也是人的）；每条属性带证据（来源轨迹步骤），
// 回注时标注"蒸馏产物·可被更新"的定位。状态落
// <身份目录>/profile.json（原子写）。
package profile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// 属性的载荷上限：key ≤64 字节，value ≤2KB（画像条目是蒸馏结论，
// 不是叙事——长内容该进 mem facts）。
const (
	maxKeyBytes   = 64
	maxValueBytes = 2 * 1024
)

// Attribute 是一条画像属性：key 是属性名（如 沟通风格/技术领域），
// evidence 是来源轨迹步骤 ID（可追溯；空 = 操作员手工设置）。
type Attribute struct {
	Key      string `json:"key"`
	Value    string `json:"value"`
	Evidence string `json:"evidence,omitempty"`
	Updated  string `json:"updated"` // RFC3339（UTC）
}

// File 是 profile.json 的结构。
type File struct {
	Attributes []Attribute `json:"attributes"`
}

// Path 是画像文件路径：<身份目录>/profile.json。
func Path(identityDir string) string {
	return filepath.Join(identityDir, "profile.json")
}

// Load 读画像。不存在返回空（非错误——从零开始）。
func Load(identityDir string) ([]Attribute, error) {
	data, err := os.ReadFile(Path(identityDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("profile: 解析失败（文件可手工修复）: %w", err)
	}
	return f.Attributes, nil
}

// Save 原子写画像（tmp + rename）。
func Save(identityDir string, attrs []Attribute) error {
	data, err := json.MarshalIndent(File{Attributes: attrs}, "", "  ")
	if err != nil {
		return err
	}
	tmp := Path(identityDir) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, Path(identityDir))
}

// Set 插入或更新一个属性（同 key 覆盖，updated 刷新；evidence 空
// 保留原证据——手工修订不该抹掉蒸馏来源）。key/value 非空校验 +
// 载荷上限。
func Set(identityDir, key, value, evidence string) (Attribute, error) {
	key = strings.TrimSpace(key)
	if key == "" || strings.TrimSpace(value) == "" {
		return Attribute{}, fmt.Errorf("profile: key 与 value 不能为空")
	}
	if len(key) > maxKeyBytes {
		return Attribute{}, fmt.Errorf("profile: key 超限（%d > %d 字节）", len(key), maxKeyBytes)
	}
	if len(value) > maxValueBytes {
		return Attribute{}, fmt.Errorf("profile: value 超限（%d > %d 字节）", len(value), maxValueBytes)
	}
	attrs, err := Load(identityDir)
	if err != nil {
		return Attribute{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for i := range attrs {
		if attrs[i].Key == key {
			ev := attrs[i].Evidence
			if evidence != "" {
				ev = evidence
			}
			attrs[i].Value = value
			attrs[i].Evidence = ev
			attrs[i].Updated = now
			return attrs[i], Save(identityDir, attrs)
		}
	}
	attr := Attribute{Key: key, Value: value, Evidence: evidence, Updated: now}
	attrs = append(attrs, attr)
	return attr, Save(identityDir, attrs)
}

// Remove 删除一个属性（大小写不敏感的 key 精确匹配）；不存在返回
// changed=false。
func Remove(identityDir, key string) (changed bool) {
	attrs, err := Load(identityDir)
	if err != nil {
		return false
	}
	out := attrs[:0]
	removed := false
	for _, a := range attrs {
		if strings.EqualFold(a.Key, key) {
			removed = true
			continue
		}
		out = append(out, a)
	}
	if removed {
		return Save(identityDir, out) == nil
	}
	return false
}

// Manifest 渲染画像注入段：属性按 key 排序（稳定呈现），头部标注
// 蒸馏定位。无属性返回空串。
func Manifest(identityDir string) string {
	attrs, err := Load(identityDir)
	if err != nil || len(attrs) == 0 {
		return ""
	}
	sort.Slice(attrs, func(i, j int) bool { return attrs[i].Key < attrs[j].Key })
	var b strings.Builder
	b.WriteString("用户画像（长期特征蒸馏，背景信息——据此调整语气与详略；与最新事实冲突时以对话为准）：")
	for _, a := range attrs {
		fmt.Fprintf(&b, "\n- %s：%s", a.Key, a.Value)
	}
	return b.String()
}
