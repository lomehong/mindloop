// Package identity 实现身份的目录布局：一个身份就是一个目录，
// 全部是文本——persona 是 markdown，记忆是文件，轨迹是 JSONL。
// Headlong 的"身份是目录"论断的原样移植。
//
//	<home>/identities/<name>/
//	├── identity.txt        身份元数据（name、root_trajectory）
//	├── persona.md          人格（模型可见）
//	├── trajectories/       轨迹根（根轨迹在这里）
//	└── runs/               保留现场的工作目录
package identity

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mindloop/internal/ids"
	"mindloop/internal/traj"
)

// Identity 是一个持久身份的句柄。
type Identity struct {
	Name     string
	Dir      string
	Timeline *traj.Timeline // 根轨迹：思维流
}

// Home 返回身份的存放根：<home>/identities。
func Home() string { return filepath.Join(traj.Home(), "identities") }

// Create 新建身份；已存在时幂等返回现有身份（对使用方来说，
// "create ada" 两次的结果都应该是"ada 可用"）。
func Create(ctx context.Context, name string) (*Identity, error) {
	name = traj.Slugify(name, 40)
	if name == "" {
		return nil, fmt.Errorf("identity: 名字为空")
	}
	dir := filepath.Join(Home(), name)
	if _, err := os.Stat(filepath.Join(dir, "identity.txt")); err == nil {
		return Load(name)
	}
	if _, err := os.Stat(dir); err == nil {
		// 目录在但元数据缺失（半次创建的残骸）：清掉重来。
		if err := os.RemoveAll(dir); err != nil {
			return nil, fmt.Errorf("identity: 清理残骸: %w", err)
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("identity: mkdir: %w", err)
	}
	root := filepath.Join(dir, "trajectories")
	tl, err := traj.CreateAt(ctx, root, "mind-"+name)
	if err != nil {
		return nil, fmt.Errorf("identity: 建根轨迹: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "persona.md"), []byte(DefaultPersona(name)), 0o644); err != nil {
		return nil, fmt.Errorf("identity: 写 persona: %w", err)
	}
	meta := fmt.Sprintf("name=%s\nroot_trajectory=%s\ncreated=%s\n", name, tl.ID, traj.NowString())
	if err := os.WriteFile(filepath.Join(dir, "identity.txt"), []byte(meta), 0o644); err != nil {
		return nil, fmt.Errorf("identity: 写元数据: %w", err)
	}
	return &Identity{Name: name, Dir: dir, Timeline: tl}, nil
}

// Load 按名字加载身份。名字只经 identities/ 目录解析，不是路径。
func Load(name string) (*Identity, error) {
	name = traj.Slugify(name, 40)
	metaPath := filepath.Join(Home(), name, "identity.txt")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return nil, fmt.Errorf("identity: 加载 %s: %w", name, err)
	}
	var trajID string
	for _, ln := range strings.Split(string(data), "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(ln), "="); ok && k == "root_trajectory" {
			trajID = v
		}
	}
	if trajID == "" {
		return nil, fmt.Errorf("identity: %s 的元数据缺 root_trajectory", name)
	}
	tl, err := traj.LoadAt(filepath.Join(Home(), name, "trajectories"), trajID)
	if err != nil {
		return nil, err
	}
	return &Identity{Name: name, Dir: filepath.Join(Home(), name), Timeline: tl}, nil
}

// List 返回全部身份名。
func List() ([]string, error) {
	entries, err := os.ReadDir(Home())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

// Remove 删除身份目录（轨迹 + 记忆 + 元数据）。心智根（~/.mindloop
// 含 .env 配置）**永远不被触碰**——这条不变量是事故教训写下的。
func Remove(name string) error {
	name = traj.Slugify(name, 40)
	if name == "" {
		return errors.New("identity: 名字为空")
	}
	dir := filepath.Join(Home(), name)
	if _, err := os.Stat(filepath.Join(dir, "identity.txt")); err != nil {
		return ErrNotFound
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("identity: 删除 %s: %w", name, err)
	}
	return nil
}

// Persona 返回 persona.md 的内容（模型可见的人格文本）。
func (id *Identity) Persona() (string, error) {
	b, err := os.ReadFile(filepath.Join(id.Dir, "persona.md"))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// DefaultPersona 生成默认人格模板。{{name}} 由创建时填充——
// Headlong 的教训是模型生成 persona 必须 defensively validated，
// 这里干脆用确定性模板，个性交给后续的 persona 编辑。
func DefaultPersona(name string) string {
	return fmt.Sprintf(`# %s

You are %s, a persistent agent. Your mind keeps thinking between
human interactions in a self-guided loop: you set your own interests,
start small projects, and act in small verifiable steps.

Values:
- Curiosity with restraint: prefer doing one small real thing over
  planning many imaginary ones.
- Evidence over claims: never report success without command output
  that proves it.
- Leave a trace: everything you do lands in the trajectory log, which
  is your memory and your accountability.

Created %s (id %s).
`, name, name, time.Now().UTC().Format(traj.TimeFormat), ids.Short(ids.NewUUID(), 8))
}
