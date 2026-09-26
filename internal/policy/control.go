package policy

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// 审批控制面——CLI approve 命令与 Web 审批接口共用的文件原语。
// 布局（dir = policy.Dir(轨迹目录)）：
//
//	request-<sha256>.json   等待中的授权请求（含脚本正文；决定后删除）
//	decision-<sha256>.json  批准/拒绝决定（等待方消费后删除）
//	audit.jsonl             授权事实审计（追加式；不含脚本正文）
//
// 文件名中的哈希一律先过 hex 校验——路径段只允许十六进制，不
// 可能构成目录穿越。

// PendingRequest 是一个等待批准的授权请求（也是展示面与 web JSON
// 的载荷）。
type PendingRequest struct {
	Hash    string    `json:"hash"`
	Script  string    `json:"script"`
	WorkDir string    `json:"work_dir"`
	RunID   string    `json:"run_id"`
	TaskID  string    `json:"task_id,omitempty"`
	Attempt int       `json:"attempt,omitempty"`
	Created time.Time `json:"created"`
	Expires time.Time `json:"expires"`
	Risks   []string  `json:"risks,omitempty"`
}

// hashRe 是完整脚本哈希（sha256 十六进制）；hashPrefixRe 是用户
// 可输入的前缀（至少 4 位，上限 64 位）。两者都只允许 hex——这是
// 路径穿越的硬边界。
var (
	hashRe       = regexp.MustCompile(`^[0-9a-f]{64}$`)
	hashPrefixRe = regexp.MustCompile(`^[0-9a-f]{4,64}$`)
)

// 哨兵错误：调用方（CLI/Web）按类映射，不依赖文本匹配。
var (
	// ErrBadHash：哈希段不是合法十六进制前缀。
	ErrBadHash = errors.New("policy: 非法脚本哈希")
	// ErrNoMatch：没有待批请求匹配该前缀。
	ErrNoMatch = errors.New("policy: 没有待批请求匹配")
	// ErrAmbiguous：前缀匹配多个待批请求，需要更长前缀。
	ErrAmbiguous = errors.New("policy: 前缀匹配多个待批请求")
)

// ListPending 读目录里的全部待批请求，按创建时间排序。目录不存在
// 返回空（没有人等待批准是正常状态）。
func ListPending(dir string) ([]PendingRequest, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []PendingRequest
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "request-") || !strings.HasSuffix(name, ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		var p PendingRequest
		if json.Unmarshal(data, &p) != nil || !hashRe.MatchString(p.Hash) {
			continue
		}
		// 文件名必须与内嵌哈希一致——不一致的残骸不进入展示面。
		if name != "request-"+p.Hash+".json" {
			continue
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.Before(out[j].Created) })
	return out, nil
}

// ResolveHash 把完整哈希或唯一前缀解析为待批请求的完整哈希。
// 歧义与未命中都明确报错——猜测是数据错误，不是便利。
func ResolveHash(dir, prefix string) (string, error) {
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	if !hashPrefixRe.MatchString(prefix) {
		return "", fmt.Errorf("%w %q（只接受十六进制前缀）", ErrBadHash, prefix)
	}
	pending, err := ListPending(dir)
	if err != nil {
		return "", err
	}
	match := ""
	for _, p := range pending {
		if p.Hash == prefix || strings.HasPrefix(p.Hash, prefix) {
			if match != "" && match != p.Hash {
				return "", fmt.Errorf("%w %q，请输入更长的前缀", ErrAmbiguous, prefix)
			}
			match = p.Hash
		}
	}
	if match == "" {
		return "", fmt.Errorf("%w %q", ErrNoMatch, prefix)
	}
	return match, nil
}

// Decide 批准或拒绝一个待批请求（哈希可用完整值或唯一前缀）。决定
// 以原子文件写回控制面，等待方消费后自动清理；对不存在的请求写
// 决定直接报错——决定必须有对象。
func Decide(dir, hash string, approve bool) error {
	full, err := ResolveHash(dir, hash)
	if err != nil {
		return err
	}
	decision := "deny"
	if approve {
		decision = "approve"
	}
	return writeJSONAtomic(filepath.Join(dir, "decision-"+full+".json"), map[string]any{
		"decision": decision,
		"ts":       time.Now().UTC().Format(time.RFC3339Nano),
	})
}

// decisionFile 是决定文件的解码形态。
type decisionFile struct {
	Decision string `json:"decision"`
}

// readDecision 读决定文件；不存在、半写或非法值都返回 false（下一
// 轮轮询再试）。
func readDecision(path string) (decisionFile, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return decisionFile{}, false
	}
	var d decisionFile
	if json.Unmarshal(data, &d) != nil {
		return decisionFile{}, false
	}
	if d.Decision != "approve" && d.Decision != "deny" {
		return decisionFile{}, false
	}
	return d, true
}

// writeJSONAtomic 同目录临时文件 + 原子改名。并发读者（等待方轮询
// 决定、web 列待批）永远不会读到半行 JSON。
func writeJSONAtomic(path string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.%d.tmp", path, os.Getpid())
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
