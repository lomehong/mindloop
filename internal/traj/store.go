package traj

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/lomehong/mindloop/internal/ids"
)

// 哨兵错误：调用方用 errors.Is 判断类别，而不是解析错误文本。
var (
	// ErrNotFound：id 前缀没有匹配任何轨迹。
	ErrNotFound = errors.New("mindloop: 没有轨迹匹配")
	// ErrAmbiguous：id 前缀匹配到多条轨迹。
	ErrAmbiguous = errors.New("mindloop: id 前缀有歧义")
	// ErrParentCycle：parent 链上出现环。
	ErrParentCycle = errors.New("mindloop: parent 链出现环")
)

const fileName = "trajectory.jsonl"

// DefaultLockTimeout 是 Append 等待目录锁的默认上限。可用
// Timeline.LockTimeout 逐实例覆盖。
const DefaultLockTimeout = 5 * time.Second

// Timeline 是一条轨迹的句柄：轨迹根目录下的一个目录，内含追加式的
// trajectory.jsonl。
type Timeline struct {
	ID   string // 完整 UUID；等于头行的 step_id
	Slug string
	Dir  string
	Path string // trajectory.jsonl

	// LockTimeout 覆盖 Append 等待目录锁的上限；零值表示使用
	// DefaultLockTimeout。等待期间 ctx 取消会先行返回。
	LockTimeout time.Duration
}

// Info 是供列举与跨轨迹查找使用的只读摘要。
type Info struct {
	ID    string
	Slug  string
	Dir   string
	Path  string
	Steps int
	Mod   time.Time
}

func (t *Timeline) lockTimeout() time.Duration {
	if t.LockTimeout > 0 {
		return t.LockTimeout
	}
	return DefaultLockTimeout
}

// Create 新建一条独立轨迹（在默认根目录下）。
func Create(ctx context.Context, slug string) (*Timeline, error) {
	return createAt(ctx, TrajRoot(), slug, nil)
}

// CreateAt 在指定根目录下新建轨迹——身份的轨迹住在身份目录里，
// 而不是全局 trajectories/ 下，这是 identity 包的布局需要。
func CreateAt(ctx context.Context, root, slug string) (*Timeline, error) {
	return createAt(ctx, root, slug, nil)
}

// CreateFork 新建一条从 parent 分叉出来的轨迹：子轨迹头携带
// parent_traj / parent_step / parent_traj_ref，父轨迹增加一个 fork
// 步骤，其 step_id 即子轨迹的 parent_step——双向引用，两个方向各
// 写一次。
func CreateFork(ctx context.Context, slug string, parent *Timeline) (*Timeline, error) {
	return createAt(ctx, TrajRoot(), slug, parent)
}

func createAt(ctx context.Context, root, slug string, parent *Timeline) (*Timeline, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("mindloop: mkdir %s: %w", root, err)
	}
	id := ids.NewUUID()
	slug = Slugify(slug, 40)
	dir := filepath.Join(root, DirName(id, slug))
	if err := os.Mkdir(dir, 0o755); err != nil {
		return nil, fmt.Errorf("mindloop: mkdir %s: %w", dir, err)
	}
	t := &Timeline{ID: id, Slug: slug, Dir: dir, Path: filepath.Join(dir, fileName)}
	header := Step{Type: TypeTrajectory, StepID: id, TS: NowString(), Fields: map[string]any{"slug": slug}}

	if parent == nil {
		if err := t.writeStep(header); err != nil {
			os.RemoveAll(dir)
			return nil, err
		}
		return t, nil
	}

	forkStep := ids.NewUUID()
	up, err := rel(t.Dir, parent.Dir)
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	down, err := rel(parent.Dir, t.Dir)
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	header.Fields["parent_traj"] = parent.ID
	header.Fields["parent_step"] = forkStep
	header.Fields["parent_traj_ref"] = up
	if err := t.writeStep(header); err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	fork := Step{Type: TypeFork, StepID: forkStep, TS: NowString(), Fields: map[string]any{
		"child":     t.ID,
		"child_ref": down,
	}}
	if err := parent.Append(ctx, fork); err != nil {
		os.RemoveAll(dir)
		return nil, fmt.Errorf("mindloop: 在父轨迹 %s 上记录 fork: %w", parent.ID, err)
	}
	return t, nil
}

// Header 返回并校验第一行。
func (t *Timeline) Header() (Step, error) {
	lines, err := readLines(t.Path)
	if err != nil {
		return Step{}, err
	}
	if len(lines) == 0 {
		return Step{}, fmt.Errorf("mindloop: %s 是空的", t.Path)
	}
	s, err := ParseStep(lines[0])
	if err != nil {
		return Step{}, fmt.Errorf("mindloop: %s 头行: %w", t.Path, err)
	}
	if s.Type != TypeTrajectory {
		return Step{}, fmt.Errorf("mindloop: %s 第一行类型是 %q，不是 %s", t.Path, s.Type, TypeTrajectory)
	}
	return s, nil
}

// Load 通过完整 UUID 或唯一前缀解析轨迹——搜索全部轨迹根（全局根
// + 每个身份的 trajectories/，见 Roots）：心智/身份的轨迹住在身份
// 目录下，人排查时只记得 id 前缀，不该被要求先知道轨迹归谁。
func Load(prefix string) (*Timeline, error) {
	var matches []*Timeline
	var searched []string
	for _, root := range Roots() {
		searched = append(searched, root)
		t, err := LoadAt(root, prefix)
		if err == nil {
			matches = append(matches, t)
			continue
		}
		if !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrAmbiguous) {
			return nil, err // 读不了根目录是环境问题，如实上抛
		}
		if errors.Is(err, ErrAmbiguous) {
			return nil, err // 同根前缀歧义，错误里已带候选
		}
	}
	// 同一 id 在多个根出现（历史迁移副本常见）：身份根优先于全局
	// 根（身份那份是操作员的命名数据，全局副本是迁移残留），同层
	// 取最近修改——活轨迹的心智在持续写它。
	byID := map[string]*Timeline{}
	rootRank := map[string]int{}
	for i, root := range Roots() {
		rootRank[root] = i // 0=全局根，越大越"身份"
	}
	rankBetter := func(a, b *Timeline) bool {
		ra, rb := rootRank[filepath.Dir(a.Dir)], rootRank[filepath.Dir(b.Dir)]
		if ra != rb {
			return ra > rb
		}
		return dirMod(a.Dir).After(dirMod(b.Dir))
	}
	for _, m := range matches {
		if prev, ok := byID[m.ID]; !ok || rankBetter(m, prev) {
			byID[m.ID] = m
		}
	}
	switch len(byID) {
	case 1:
		for _, m := range byID {
			return m, nil
		}
	case 0:
		return nil, fmt.Errorf("%w: %q（搜过 %s）", ErrNotFound, prefix, strings.Join(searched, "、"))
	default:
		shorts := make([]string, 0, len(byID))
		for _, m := range byID {
			shorts = append(shorts, fmt.Sprintf("%s@%s", ids.Short(m.ID, 8), m.Dir))
		}
		return nil, fmt.Errorf("%w: %q 匹配到 %s", ErrAmbiguous, prefix, strings.Join(shorts, ", "))
	}
	return nil, fmt.Errorf("%w: %q", ErrNotFound, prefix)
}

// Roots 返回全部轨迹根：全局根 + 每个身份目录下的 trajectories/。
// identities/ 是本包与 identity 包共享的磁盘布局（身份数据=身份名
// 目录），布局知识放这里避免反向依赖。
func Roots() []string {
	roots := []string{TrajRoot()}
	idents, err := os.ReadDir(filepath.Join(Home(), "identities"))
	if err != nil {
		return roots // 没有任何身份：只有全局根
	}
	for _, e := range idents {
		if e.IsDir() {
			roots = append(roots, filepath.Join(Home(), "identities", e.Name(), "trajectories"))
		}
	}
	return roots
}

// dirMod 读轨迹目录的修改时间（同层去重时选活的那份——活轨迹的
// 心智在持续写它）。读不到时间的目录视为最旧。
func dirMod(dir string) time.Time {
	if fi, err := os.Stat(dir); err == nil {
		return fi.ModTime()
	}
	return time.Time{}
}

// inIdentitiesLayout 报告轨迹目录是否在 identities 布局下
// （~/.mindloop/identities/<名>/trajectories/...）。
func inIdentitiesLayout(dir string) bool {
	home := Home()
	idents := filepath.Join(home, identitiesDirName)
	return strings.HasPrefix(dir, idents+string(filepath.Separator))
}

// identitiesDirName 是身份根目录名——本包与 identity 包共享的磁盘
// 布局（身份数据=身份名目录），布局知识放这里避免反向依赖。
const identitiesDirName = "identities"

// LoadAt 在指定根目录下用完整 UUID 或唯一前缀解析轨迹。id 永远
// 只是经根目录解析的名字——绝不是文件系统路径——这与 Headlong 的
// web 仪表盘出于同样原因执行的包含规则一致。
func LoadAt(root, prefix string) (*Timeline, error) {
	if prefix == "" {
		return nil, fmt.Errorf("mindloop: 空的轨迹 id")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("mindloop: 读取 %s: %w", root, err)
	}
	var matches []*Timeline
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		t := &Timeline{
			Dir:  filepath.Join(root, e.Name()),
			Path: filepath.Join(root, e.Name(), fileName),
		}
		header, err := t.Header()
		if err != nil {
			continue // 不可读的目录永不匹配
		}
		t.ID = header.StepID
		if s, ok := header.Field("slug"); ok {
			t.Slug = s
		}
		if strings.HasPrefix(t.ID, prefix) {
			matches = append(matches, t)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return nil, fmt.Errorf("%w: %q（根目录 %s）", ErrNotFound, prefix, root)
	default:
		shorts := make([]string, len(matches))
		for i, m := range matches {
			shorts[i] = ids.Short(m.ID, 8)
		}
		return nil, fmt.Errorf("%w: %q 匹配到 %s", ErrAmbiguous, prefix, strings.Join(shorts, ", "))
	}
}

// List 返回默认根目录下每条可读的轨迹，最新的在前。
func List() ([]Info, error) {
	var out []Info
	for _, root := range Roots() {
		infos, err := ListAt(root)
		if err != nil {
			return nil, err
		}
		out = append(out, infos...)
	}
	// 同 id 多根去重（规则同 Load：身份根优先，同层取最近修改）。
	byID := map[string]Info{}
	for _, in := range out {
		if prev, ok := byID[in.ID]; ok && !infoBetter(in, prev) {
			continue
		}
		byID[in.ID] = in
	}
	out = out[:0]
	for _, in := range byID {
		out = append(out, in)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Mod.After(out[j].Mod) })
	return out, nil
}

// infoBetter 与 Load 的去重规则同源：身份根（路径在 identities
// 布局下）优先于全局根，同层取最近修改。
func infoBetter(a, b Info) bool {
	ra := inIdentitiesLayout(a.Dir)
	rb := inIdentitiesLayout(b.Dir)
	if ra != rb {
		return ra
	}
	return a.Mod.After(b.Mod)
}

// ListAt 返回指定根目录下每条可读的轨迹，最新的在前。
func ListAt(root string) ([]Info, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("mindloop: 读取 %s: %w", root, err)
	}
	var out []Info
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		t := &Timeline{Dir: filepath.Join(root, e.Name()), Path: filepath.Join(root, e.Name(), fileName)}
		header, err := t.Header()
		if err != nil {
			continue
		}
		steps, err := t.Steps()
		if err != nil {
			continue
		}
		slug, _ := header.Field("slug")
		var mod time.Time
		if fi, err := e.Info(); err == nil {
			mod = fi.ModTime()
		}
		out = append(out, Info{
			ID:    header.StepID,
			Slug:  slug,
			Dir:   t.Dir,
			Path:  t.Path,
			Steps: len(steps),
			Mod:   mod,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Mod.After(out[j].Mod) })
	return out, nil
}

// Root 沿 parent_traj 链向上走到最顶层的轨迹——即本时间线分叉前的
// 思维日志。即使在分叉内写入，chat 与回复也总是指向根轨迹。
func (t *Timeline) Root() (*Timeline, error) {
	seen := map[string]bool{t.ID: true}
	cur := t
	for {
		header, err := cur.Header()
		if err != nil {
			return nil, err
		}
		pid, ok := header.Field("parent_traj")
		if !ok || pid == "" {
			return cur, nil
		}
		if seen[pid] {
			return nil, fmt.Errorf("%w: %s", ErrParentCycle, cur.ID)
		}
		seen[pid] = true
		next, err := Load(pid)
		if err != nil {
			return nil, fmt.Errorf("mindloop: 加载父轨迹 %s: %w", pid, err)
		}
		cur = next
	}
}
