// Package snapshot 是运行级的工作目录快照：非只读脚本执行前拍清单
// （路径→内容哈希，内容入共享 blob 库），执行后拍第二份并差分出
// 变更集；undo 时按变更集把工作目录恢复到执行前。
//
// 边界（如实声明，不装样子）：只覆盖沙箱工作目录之内——脚本以
// 用户全权限运行，写目录外的行为不进快照。这层刹车回答的是
// "agent 在它的地盘上干了什么、能不能反悔"，不是全盘备份。
package snapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// MaxBlobBytes 是单文件入blob库的上限——超过只记录不备份（大文件
// 的恢复代价与价值不成比例；v1 求刹车不求完备）。超限文件出现在
// 变更集里时标记 skipped，恢复时如实报告"该文件不可自动恢复"。
const MaxBlobBytes = 4 << 20

// Entry 是清单里的一条文件记录。
type Entry struct {
	Rel  string `json:"rel"`            // 相对工作目录的路径（/ 分隔）
	Size int64  `json:"size"`           // 字节
	Mode uint32 `json:"mode"`           // 权限位
	Hash string `json:"hash,omitempty"` // sha256；超限/非常规文件为空
}

// Change 是变更集的一条：op ∈ modified | deleted | added。
type Change struct {
	Rel        string `json:"rel"`
	Op         string `json:"op"`
	BeforeHash string `json:"before_hash,omitempty"` // modified/deleted 才有；恢复的取材
	BeforeMode uint32 `json:"before_mode,omitempty"` // 旧权限位
	AfterHash  string `json:"after_hash,omitempty"`  // modified/added 才有；审计对照
	Skipped    bool   `json:"skipped,omitempty"`     // 旧内容超限未备份，恢复时报告
}

// Session 是一次执行的快照会话。
type Session struct {
	dir    string // 快照落盘目录（<轨迹>/snapshots/<run_id>/）
	work   string // 工作目录
	before []Entry
}

// Begin 拍前清单并把新内容写入 blob 库（内容寻址，跨快照去重——
// 没变的文件哈希相同，不产生新 blob）。失败返回错误：调用方决定
// 降级继续（快照不可用不能卡住执行）。
func Begin(work, dir string) (*Session, error) {
	before, err := capture(work)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(dir, "store"), 0o755); err != nil {
		return nil, err
	}
	for _, e := range before {
		if e.Hash == "" {
			continue
		}
		if err := storeBlob(work, dir, e); err != nil {
			return nil, err
		}
	}
	return &Session{dir: dir, work: work, before: before}, nil
}

// Finish 拍后清单、差分变更集落 changed.json。空变更集也落盘——
// "这次运行没动文件"本身就是审计事实。
func (s *Session) Finish() error {
	after, err := capture(s.work)
	if err != nil {
		return err
	}
	changes := diff(s.before, after)
	data, err := json.MarshalIndent(changes, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.dir, "changed.json"), data, 0o644)
}

// Restore 把工作目录恢复到快照前状态：变更/删除的文件从 blob 库
// 取回旧内容，新增的文件删除。返回不可自动恢复（超限）的路径。
// done 标记存在时拒绝重复恢复（由调用方传 undone 检查）。
func Restore(dir, work string) ([]string, error) {
	changes, err := loadChanges(dir)
	if err != nil {
		return nil, err
	}
	var skipped []string
	for _, ch := range changes {
		switch ch.Op {
		case "added":
			// 执行前不存在 → 删掉。
			if err := os.Remove(filepath.Join(work, filepath.FromSlash(ch.Rel))); err != nil && !os.IsNotExist(err) {
				return skipped, fmt.Errorf("snapshot: 删除新增文件 %s: %w", ch.Rel, err)
			}
		case "modified", "deleted":
			// 恢复执行前的旧内容（deleted 也一样：blob 里有旧文）。
			if ch.BeforeHash == "" {
				skipped = append(skipped, ch.Rel)
				continue
			}
			if err := restoreBlob(dir, work, ch.Rel, ch.BeforeHash, ch.BeforeMode); err != nil {
				return skipped, err
			}
		}
	}
	// undone 标记最后写：恢复成功才算"已撤销"。
	marker, merr := os.Create(filepath.Join(dir, "undone"))
	if merr == nil {
		marker.Close()
	}
	return skipped, nil
}

// Undone 报告该快照是否已撤销过。
func Undone(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "undone"))
	return err == nil
}

// Changes 读变更集；无 changed.json（运行未完成/没有快照）报错。
func Changes(dir string) ([]Change, error) {
	return loadChanges(dir)
}

// loadChanges 读并校验变更集：rel 一律不得含 .. 与绝对路径——
// 变更集文件若被篡改成穿越路径，恢复就成了任意文件覆写。
func loadChanges(dir string) ([]Change, error) {
	data, err := os.ReadFile(filepath.Join(dir, "changed.json"))
	if err != nil {
		return nil, fmt.Errorf("snapshot: 读变更集: %w", err)
	}
	var changes []Change
	if err := json.Unmarshal(data, &changes); err != nil {
		return nil, fmt.Errorf("snapshot: 变更集损坏: %w", err)
	}
	for _, ch := range changes {
		if ch.Rel == "" || strings.HasPrefix(ch.Rel, "/") || strings.Contains(ch.Rel, "..") {
			return nil, fmt.Errorf("snapshot: 变更集含非法路径 %q", ch.Rel)
		}
		switch ch.Op {
		case "modified", "deleted", "added":
		default:
			return nil, fmt.Errorf("snapshot: 变更集含未知操作 %q（%s）", ch.Op, ch.Rel)
		}
	}
	return changes, nil
}

// fileHash 是文件内容的 sha256 十六进制——清单身份与 blob 库键名
// 同源。
func fileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// capture 拍工作目录清单：常规文件记录（相对路径 / 分隔），符号
// 链接跳过（链接目标的读写不属本目录的内容边界）；超限文件记录
// 但不哈希。目录不存在视为空清单（首轮运行的工作目录还没建）。
func capture(work string) ([]Entry, error) {
	var entries []Entry
	err := filepath.WalkDir(work, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil // 工作目录整体不存在 = 空清单
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(work, path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		e := Entry{
			Rel:  filepath.ToSlash(rel),
			Size: info.Size(),
			Mode: uint32(info.Mode().Perm()),
		}
		if info.Size() <= MaxBlobBytes {
			hash, herr := fileHash(path)
			if herr != nil {
				return herr
			}
			e.Hash = hash
		}
		entries = append(entries, e)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Rel < entries[j].Rel })
	return entries, nil
}

// diff 前后清单差分出变更集。旧清单不在、新清单在 = added；
// 反之 = deleted；两边都在但哈希不同 = modified（超限文件按
// 哈希缺失处理：大小或权限变化也算 modified，标记 skipped）。
func diff(before, after []Entry) []Change {
	oldIdx := make(map[string]Entry, len(before))
	for _, e := range before {
		oldIdx[e.Rel] = e
	}
	newIdx := make(map[string]Entry, len(after))
	for _, e := range after {
		newIdx[e.Rel] = e
	}
	var changes []Change
	for _, e := range after {
		old, ok := oldIdx[e.Rel]
		if !ok {
			changes = append(changes, Change{Rel: e.Rel, Op: "added", AfterHash: e.Hash})
			continue
		}
		if old.Hash != e.Hash || old.Size != e.Size || old.Mode != e.Mode {
			changes = append(changes, Change{
				Rel: e.Rel, Op: "modified",
				BeforeHash: old.Hash, BeforeMode: old.Mode, AfterHash: e.Hash,
				Skipped: old.Hash == "",
			})
		}
	}
	for _, e := range before {
		if _, ok := newIdx[e.Rel]; !ok {
			changes = append(changes, Change{
				Rel: e.Rel, Op: "deleted",
				BeforeHash: e.Hash, BeforeMode: e.Mode, Skipped: e.Hash == "",
			})
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Rel < changes[j].Rel })
	return changes
}

// storeBlob 把文件内容写入内容寻址 blob 库（已存在即跳过——
// 跨快照去重让没变的文件零成本）。取内容用条目的原始相对路径。
func storeBlob(work, dir string, e Entry) error {
	blob := filepath.Join(dir, "store", e.Hash)
	if _, err := os.Stat(blob); err == nil {
		return nil // 同内容已入库
	}
	src, err := os.Open(filepath.Join(work, filepath.FromSlash(e.Rel)))
	if err != nil {
		return err
	}
	defer src.Close()
	tmp := blob + fmt.Sprintf(".%d.tmp", os.Getpid())
	dst, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		os.Remove(tmp)
		return err
	}
	if err := dst.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, blob)
}

// restoreBlob 从 blob 库取回旧内容写到工作目录（含权限位）。父
// 目录可能已被脚本删掉——按需重建。
func restoreBlob(dir, work, rel, hash string, mode uint32) error {
	blob, err := os.Open(filepath.Join(dir, "store", hash))
	if err != nil {
		return fmt.Errorf("snapshot: 取回 %s 的旧内容: %w", rel, err)
	}
	defer blob.Close()
	target := filepath.Join(work, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	dst, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fs.FileMode(mode))
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, blob); err != nil {
		dst.Close()
		return err
	}
	return dst.Close()
}
