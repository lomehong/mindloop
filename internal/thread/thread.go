// Package thread 是话题级的工作线索（BaiLongma threads/commitments 的
// mindloop 最小形态）：多条开放线索并发 + 最近活跃指针，回答"我在跟进
// 什么、那个事的进度怎么样"。与既有层的分工钉死：
//
//	task = 强承诺（领取/状态机/幂等提交——执行授权走它）
//	mem  = 长期事实（可检索的知识）
//	recap= 历史（做过什么的粗层摘要）
//	thread = 话题指针（弱线索：还没到任务的量级，但不能忘）
//
// 状态落 <身份目录>/threads.json（单文件原子写——线索是状态指针，
// 不是历史；历史在轨迹与 recap 里）。心智经 `mindloop thread` CLI
// 自己维护（与 mem add 同款"自组合"哲学），wakeTask 把开放线索注入
// 唤醒上下文——指代解析（"那个网页""进度怎么样"）靠的是线索清单
// 在眼前，而不是靠模型记住。
package thread

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

)

// Status 是线索的生命周期：open（进行中）→ parked（搁置）→
// done（完结）。done 的线索保留在文件里供回溯，注入段只带未完结的。
type Status string

const (
	Open   Status = "open"
	Parked Status = "parked"
	Done   Status = "done"
)

// Thread 是一条工作线索。
type Thread struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Note   string `json:"note,omitempty"` // 最近进展一行（模型自己更新）
	Status Status `json:"status"`
	Opened string `json:"opened"` // RFC3339（UTC）
	Active string `json:"active"` // 最近活动时刻（排序键）
}

// File 是 threads.json 的结构。
type File struct {
	Items []Thread `json:"items"`
}

// Path 是线索文件路径：<身份目录>/threads.json。
func Path(identityDir string) string {
	return filepath.Join(identityDir, "threads.json")
}

// Load 读线索文件。不存在返回空（非错误——从零开始）。
func Load(identityDir string) ([]Thread, error) {
	data, err := os.ReadFile(Path(identityDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("thread: 解析失败（文件可手工修复）: %w", err)
	}
	return f.Items, nil
}

// Save 原子写线索文件（tmp + rename，与 schedule-state 同款）。
func Save(identityDir string, items []Thread) error {
	data, err := json.MarshalIndent(File{Items: items}, "", "  ")
	if err != nil {
		return err
	}
	tmp := Path(identityDir) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, Path(identityDir))
}

// validate 是线索标题的约束：非空、≤120 字节。
func validate(title string) error {
	if strings.TrimSpace(title) == "" {
		return fmt.Errorf("thread: 标题不能为空")
	}
	if len(title) > 120 {
		return fmt.Errorf("thread: 标题超限（%d > 120 字节）", len(title))
	}
	return nil
}

// Add 开一条新线索（open，活动时刻 = 现在）。标题重复不是错误——
// 同名线索往往是同一件事的延续，调用方应先 List 确认（模型自己看）。
func Add(identityDir, title string) (Thread, error) {
	if err := validate(title); err != nil {
		return Thread{}, err
	}
	items, err := Load(identityDir)
	if err != nil {
		return Thread{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	// ID 用纳秒时间戳：物理唯一，且 ID 之间不构成前缀链（前缀寻址
	// 的前提——毫秒戳 + "-1" 序号去重会让 a.ID 成为 b.ID 的前缀，
	// a 从此无法被唯一命中，容器 -race 实证）。
	t := Thread{
		ID:     fmt.Sprintf("th-%d", time.Now().UnixNano()),
		Title:  title,
		Status: Open,
		Opened: now,
		Active: now,
	}
	items = append(items, t)
	return t, Save(identityDir, items)
}

// Update 更新线索的状态与备注（ID 支持唯一前缀，与 task 的 hash
// 前缀同款）。note 为空不动备注。
func Update(identityDir, idPrefix string, status Status, note string) (Thread, error) {
	items, err := Load(identityDir)
	if err != nil {
		return Thread{}, err
	}
	for i := range items {
		if !strings.HasPrefix(items[i].ID, idPrefix) {
			continue
		}
		// 前缀必须唯一命中——多义前缀拒绝（与 policy.ResolveHash
		// 同款：歧义面前宁可报错，不猜测用户的意图）。
		for j := i + 1; j < len(items); j++ {
			if strings.HasPrefix(items[j].ID, idPrefix) {
				return Thread{}, fmt.Errorf("thread: 前缀 %q 匹配多条线索（%s、%s），请用更长前缀",
					idPrefix, items[i].ID, items[j].ID)
			}
		}
		if status != "" {
			items[i].Status = status
		}
		if note != "" {
			items[i].Note = note
		}
		items[i].Active = time.Now().UTC().Format(time.RFC3339)
		return items[i], Save(identityDir, items)
	}
	return Thread{}, fmt.Errorf("thread: 找不到前缀 %q 对应的线索", idPrefix)
}

// OpenSorted 返回未完结线索，最近活动在前（注入段的顺序）。
// done 的线索不进注入——完结的事实归轨迹与 recap。
func OpenSorted(items []Thread) []Thread {
	var open []Thread
	for _, t := range items {
		if t.Status == Open || t.Status == Parked {
			open = append(open, t)
		}
	}
	sort.Slice(open, func(i, j int) bool {
		return open[i].Active > open[j].Active
	})
	return open
}

// maxManifest 是注入段的条数上限：线索多了模型也记不住，
// 更早的靠 recap。
const maxManifest = 6

// Manifest 渲染开放线索的注入段（wakeTask 用）。无未完结线索返回
// 空串。active 距今的人话时长帮助模型判断"那个事的进度"该往哪接。
func Manifest(identityDir string, now time.Time) string {
	items, err := Load(identityDir)
	if err != nil {
		return "" // 文件损坏：线索是增强，不挡唤醒
	}
	open := OpenSorted(items)
	if len(open) == 0 {
		return ""
	}
	if len(open) > maxManifest {
		open = open[:maxManifest]
	}
	var b strings.Builder
	b.WriteString("开放线索（进行中的话题/工作，用户问\"进度怎么样\"\"那个事\"指的就是这些；新消息若延续某条线索，用 thread update 更新它）：")
	for _, t := range open {
		active, err := time.Parse(time.RFC3339, t.Active)
		age := ""
		if err == nil {
			age = "，" + humanAge(now.Sub(active))
		}
		note := ""
		if t.Note != "" {
			note = "：" + t.Note
		}
		fmt.Fprintf(&b, "\n- [%s] %s（%s%s）%s", t.ID, t.Title, t.Status, age, note)
	}
	return b.String()
}

// humanAge 把时长折成人话（与 schedule 包的口径一致但独立实现——
// 跨包共享一段 5 行函数不值得制造依赖边）。
func humanAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "1 分钟内活跃"
	case d < time.Hour:
		return fmt.Sprintf("%d 分钟前活跃", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d 小时前活跃", int(d.Hours()))
	default:
		return fmt.Sprintf("%d 天前活跃", int(d.Hours()/24))
	}
}
