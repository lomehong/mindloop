package traj

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"
)

// ErrNoStep：没有步骤匹配给定前缀。
var ErrNoStep = errors.New("mindloop: 没有步骤匹配")

// Steps 按文件顺序返回所有可解析的步骤。坏行被跳过，绝非致命：
// 一行损坏不能拖垮日志的所有读取方——与 Headlong 的每个读取方都
// 用 fromjson? 解析后继续的理由一致。
func (t *Timeline) Steps() ([]Step, error) {
	lines, err := readLines(t.Path)
	if err != nil {
		return nil, err
	}
	steps := make([]Step, 0, len(lines))
	for _, ln := range lines {
		s, err := ParseStep(ln)
		if err != nil {
			continue
		}
		steps = append(steps, s)
	}
	return steps, nil
}

// readLines 返回文件中所有非空物理行。
func readLines(path string) ([][]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var lines [][]byte
	for _, ln := range bytes.Split(data, []byte{'\n'}) {
		ln = bytes.TrimSuffix(ln, []byte{'\r'})
		if len(bytes.TrimSpace(ln)) == 0 {
			continue
		}
		lines = append(lines, ln)
	}
	return lines, nil
}

// Tail 返回最后 n 个步骤（0 = 全部），types 非空时按类型过滤。
// 本层的规模下整文件读取没问题；Headlong 在 context 里做的
// O(head+tail) 选择是下一层的职责，它以 cursor 为原语。
func (t *Timeline) Tail(n int, types []string) ([]Step, error) {
	steps, err := t.Steps()
	if err != nil {
		return nil, err
	}
	if len(types) > 0 {
		want := make(map[string]bool, len(types))
		for _, ty := range types {
			want[ty] = true
		}
		filtered := make([]Step, 0, len(steps))
		for _, s := range steps {
			if want[s.Type] {
				filtered = append(filtered, s)
			}
		}
		steps = filtered
	}
	if n > 0 && len(steps) > n {
		steps = steps[len(steps)-n:]
	}
	return steps, nil
}

// Cat 返回匹配全部 K=V 字符串过滤条件的步骤，types 非空时按类型
// 限定。
func (t *Timeline) Cat(filters map[string]string, types []string) ([]Step, error) {
	steps, err := t.Tail(0, types)
	if err != nil {
		return nil, err
	}
	if len(filters) == 0 {
		return steps, nil
	}
	var out []Step
	for _, s := range steps {
		match := true
		for k, want := range filters {
			if got, ok := s.Field(k); !ok || got != want {
				match = false
				break
			}
		}
		if match {
			out = append(out, s)
		}
	}
	return out, nil
}

// FindStep 在本轨迹内用前缀解析步骤 id。
func (t *Timeline) FindStep(prefix string) (Step, bool, error) {
	steps, err := t.Steps()
	if err != nil {
		return Step{}, false, err
	}
	for _, s := range steps {
		if strings.HasPrefix(s.StepID, prefix) {
			return s, true, nil
		}
	}
	return Step{}, false, nil
}

// FindStepAnywhere 在根目录下的所有轨迹中用前缀解析步骤 id。
func FindStepAnywhere(prefix string) (Step, *Timeline, error) {
	infos, err := List()
	if err != nil {
		return Step{}, nil, err
	}
	for _, in := range infos {
		t := &Timeline{ID: in.ID, Slug: in.Slug, Dir: in.Dir, Path: in.Path}
		if s, ok, err := t.FindStep(prefix); err == nil && ok {
			return s, t, nil
		}
	}
	return Step{}, nil, fmt.Errorf("%w: %q", ErrNoStep, prefix)
}

// Check 返回无法解析的行号（从 1 开始）——即 `traj check` 打印的
// 报告。修复（剥离并重写）此刻刻意缺席：Headlong 自己的事故记录
// 就有一条无防护的重写+改名与活跃写者相撞的事故；带防护的修复
// 需要后续引入的"先追加后切换"设计。
func (t *Timeline) Check() ([]int, error) {
	data, err := os.ReadFile(t.Path)
	if err != nil {
		return nil, err
	}
	var bad []int
	for i, ln := range bytes.Split(data, []byte{'\n'}) {
		ln = bytes.TrimSpace(ln)
		if len(ln) == 0 {
			continue
		}
		if _, err := ParseStep(ln); err != nil {
			bad = append(bad, i+1)
		}
	}
	return bad, nil
}
