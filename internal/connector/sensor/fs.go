// fs.go — 文件感官（眼）：fsnotify 递归观察目录。Windows 病理的
// 处置（perception.md §9.2）：
//   - ReadDirectoryChangesW 队列定长溢出丢事件 → Errors 通道触发
//     全量重扫（快照 diff 补发差异）——丢了事件的眼睛比不看的
//     眼睛更危险；
//   - 编辑器原子保存（临时文件+rename）表现为 rename 对 /
//     removed+appeared → 事件本身带解释，判定层指纹去重吸收同次
//     保存的连发；
//   - 无递归 watch → 子目录逐个挂（上限 maxWatchDirs，跳过隐藏
//     与依赖目录），新建子目录即挂，挂上前的窗口由溢出重扫兜底。
package sensor

import (
	"context"
	"fmt"
	"os"
	pathpkg "path/filepath"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
)

// maxWatchDirs 是递归挂 watch 的目录数上限——防 node_modules 类
// 目录树拖垮句柄预算。超限不失败：能看多少看多少，重扫兜底。
const maxWatchDirs = 256

// skipDirNames 是不递归的目录名（依赖/版本噪声，事件洪泛源）。
var skipDirNames = map[string]bool{
	"node_modules": true, ".git": true, ".svn": true, ".hg": true,
	"vendor": true, "dist": true, "build": true, ".gradle": true,
	"target": true, "__pycache__": true, ".venv": true,
}

// FileSensor 观察一个目录（或单文件——挂其父目录过滤）。
type FileSensor struct {
	cfg SensorConfig

	mu        sync.Mutex
	watcher   *fsnotify.Watcher
	watched   int
	snap      map[string]string // 绝对路径 → 指纹（溢出重扫的 diff 基线）
	pointFile string            // 单文件模式的目标（空 = 目录模式）
	pointDir  string            // 单文件模式的父目录
}

// NewFileSensor 构造文件感官。
func NewFileSensor(cfg SensorConfig) (Sensor, error) {
	if cfg.Type != "file" {
		return nil, fmt.Errorf("sensor: %s 不是 file 类型", cfg.ID)
	}
	return &FileSensor{cfg: cfg}, nil
}

func (s *FileSensor) ID() string { return s.cfg.ID }

// fingerprint 是文件的变更指纹（size+mtime）——dedup 键的原料：
// 同一保存动作触发的多个事件同指纹，判定层去重窗口内只落一条。
func fingerprint(path string) string {
	fi, err := os.Stat(path)
	if err != nil {
		return "gone"
	}
	return fmt.Sprintf("%d:%d", fi.Size(), fi.ModTime().UnixNano())
}

func (s *FileSensor) Watch(ctx context.Context, onEvent func(PEvent)) error {
	root := s.cfg.Path
	fi, err := os.Stat(root)
	if err != nil {
		// 可重试：目标（如网络盘、待挂载目录）回来后由运行器退避
		// 重启恢复——非本地路径在此反复失败，doctor 会点名。
		return fmt.Errorf("观察目标不可达: %w", err)
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	s.mu.Lock()
	s.watcher = watcher
	s.watched = 0
	s.snap = map[string]string{}
	s.mu.Unlock()
	if !fi.IsDir() {
		s.mu.Lock()
		s.pointFile, s.pointDir = root, pathpkg.Dir(root)
		s.mu.Unlock()
		root = pathpkg.Dir(root)
	}

	// 初始基线：挂 watch + 建快照，不发事件。
	var build func(dir string)
	build = func(dir string) {
		if !s.addDir(dir) {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return // 读不了的子树跳过，不算失败
		}
		for _, e := range entries {
			p := pathpkg.Join(dir, e.Name())
			if e.IsDir() {
				if !skipDirName(e.Name()) {
					build(p)
				}
				continue
			}
			s.mu.Lock()
			s.snap[p] = fingerprint(p)
			s.mu.Unlock()
		}
	}
	build(root)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev, ok := <-watcher.Events:
			if !ok {
				return fmt.Errorf("事件通道关闭")
			}
			s.handleEvent(ev, onEvent)
		case err, ok := <-watcher.Errors:
			if !ok {
				return fmt.Errorf("错误通道关闭")
			}
			// 队列溢出等错误：全量重扫补差异——静默丢事件等于装了
			// 监控却以为在观察（重扫发的事件带"重扫"字样可追溯）。
			_ = err
			s.rescan(onEvent)
		}
	}
}

func skipDirName(name string) bool {
	return strings.HasPrefix(name, ".") || skipDirNames[name]
}

// addDir 挂一个目录的 watch（计一下上限）。返回 false = 没挂上。
func (s *FileSensor) addDir(dir string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.watcher == nil || s.watched >= maxWatchDirs {
		return false
	}
	if err := s.watcher.Add(dir); err != nil {
		return false
	}
	s.watched++
	return true
}

func (s *FileSensor) relevant(path string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pointFile == "" {
		return true // 目录模式：全收
	}
	return path == s.pointFile || strings.HasPrefix(path, s.pointDir+string(pathpkg.Separator))
}

func (s *FileSensor) handleEvent(ev fsnotify.Event, onEvent func(PEvent)) {
	path := ev.Name
	if !s.relevant(path) {
		return
	}
	isDir := false
	if fi, err := os.Stat(path); err == nil {
		isDir = fi.IsDir()
	}
	switch {
	case ev.Op&fsnotify.Create != 0:
		if isDir && s.pointFile == "" {
			// 新建子目录：递归挂 watch + 补快照基线。目录本身不发
			// 事件；挂上前的窗口由溢出重扫兜底。
			s.addSubTree(path)
			return
		}
		fp := fingerprint(path)
		s.mu.Lock()
		s.snap[path] = fp
		s.mu.Unlock()
		onEvent(PEvent{
			Kind: KindAppeared, Subject: path, Dedup: fp,
			Digest: fmt.Sprintf("新文件出现: %s", pathpkg.Base(path)),
		})
	case ev.Op&fsnotify.Write != 0:
		if isDir {
			return
		}
		fp := fingerprint(path)
		s.mu.Lock()
		old, ok := s.snap[path]
		if ok && old == fp {
			s.mu.Unlock()
			return // 指纹未变（同一次保存的连发写事件）
		}
		s.snap[path] = fp
		s.mu.Unlock()
		onEvent(PEvent{
			Kind: KindChanged, Subject: path, Dedup: fp,
			Digest: fmt.Sprintf("文件修改: %s", pathpkg.Base(path)),
		})
	case ev.Op&fsnotify.Remove != 0 || ev.Op&fsnotify.Rename != 0:
		s.mu.Lock()
		_, inSnap := s.snap[path]
		if inSnap {
			delete(s.snap, path)
		}
		s.mu.Unlock()
		if !inSnap && !isDir {
			return // 快照外的路径（编辑器临时文件等）：非目标噪声
		}
		onEvent(PEvent{
			Kind: KindRemoved, Subject: path, Dedup: "gone:" + pathpkg.Base(path),
			Digest: fmt.Sprintf("文件消失: %s（rename 多为编辑器原子保存）", pathpkg.Base(path)),
		})
	}
}

// addSubTree 递归挂新出现的子目录并补快照基线。
func (s *FileSensor) addSubTree(dir string) {
	if skipDirName(pathpkg.Base(dir)) {
		return
	}
	if !s.addDir(dir) {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		p := pathpkg.Join(dir, e.Name())
		if e.IsDir() {
			s.addSubTree(p)
			continue
		}
		s.mu.Lock()
		s.snap[p] = fingerprint(p)
		s.mu.Unlock()
	}
}

// rescan 全量重扫：与快照 diff 补发差异（溢出后的自愈路径）。差异
// 在锁内结算、锁外发送——回调不持锁，不与 handleEvent 互斥。
func (s *FileSensor) rescan(onEvent func(PEvent)) {
	root := s.cfg.Path
	if s.pointFile != "" {
		root = s.pointDir
	}
	now := map[string]string{}
	var walk func(dir string)
	walk = func(dir string) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			p := pathpkg.Join(dir, e.Name())
			if e.IsDir() {
				if !skipDirName(e.Name()) {
					walk(p)
				}
				continue
			}
			now[p] = fingerprint(p)
		}
	}
	walk(root)

	s.mu.Lock()
	var diffs []PEvent
	for p, fp := range now {
		old, ok := s.snap[p]
		if !ok {
			s.snap[p] = fp
			diffs = append(diffs, PEvent{Kind: KindAppeared, Subject: p, Dedup: fp,
				Digest: "重扫发现新文件: " + pathpkg.Base(p)})
			continue
		}
		if old != fp {
			s.snap[p] = fp
			diffs = append(diffs, PEvent{Kind: KindChanged, Subject: p, Dedup: fp,
				Digest: "重扫发现变化: " + pathpkg.Base(p)})
		}
	}
	for p := range s.snap {
		if _, ok := now[p]; !ok {
			delete(s.snap, p)
			diffs = append(diffs, PEvent{Kind: KindRemoved, Subject: p, Dedup: "gone:" + pathpkg.Base(p),
				Digest: "重扫发现消失: " + pathpkg.Base(p)})
		}
	}
	s.mu.Unlock()
	for _, e := range diffs {
		onEvent(e)
	}
}
