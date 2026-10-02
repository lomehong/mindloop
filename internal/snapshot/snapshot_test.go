package snapshot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFile 测试用写文件（内容+权限）。
func writeFile(t *testing.T, path, content string, perm os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), perm); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestSnapshotRestoreRoundtrip：改/删/增/超限四类效果的全恢复——
// 改写与删除取回旧内容，新增被清除，超限大文件如实报不可恢复。
func TestSnapshotRestoreRoundtrip(t *testing.T) {
	work := t.TempDir()
	snapDir := t.TempDir()

	writeFile(t, filepath.Join(work, "a.txt"), "hello", 0o644)
	writeFile(t, filepath.Join(work, "sub", "b.txt"), "world", 0o644)
	big := strings.Repeat("B", MaxBlobBytes+1)
	writeFile(t, filepath.Join(work, "big.bin"), big, 0o644)

	s, err := Begin(work, snapDir)
	if err != nil {
		t.Fatal(err)
	}
	// "脚本"执行：改写、删除、新增、动大文件。
	writeFile(t, filepath.Join(work, "a.txt"), "changed!", 0o644)
	if err := os.Remove(filepath.Join(work, "sub", "b.txt")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(work, "new.txt"), "brand-new", 0o644)
	writeFile(t, filepath.Join(work, "big.bin"), strings.Repeat("C", MaxBlobBytes+2), 0o644)
	if err := s.Finish(); err != nil {
		t.Fatal(err)
	}

	changes, err := Changes(snapDir)
	if err != nil {
		t.Fatal(err)
	}
	ops := map[string]Change{}
	for _, ch := range changes {
		ops[ch.Rel] = ch
	}
	if ops["a.txt"].Op != "modified" || ops["a.txt"].BeforeHash == "" {
		t.Fatalf("a.txt 应为 modified 且带旧哈希: %+v", ops["a.txt"])
	}
	if ops["sub/b.txt"].Op != "deleted" {
		t.Fatalf("sub/b.txt 应为 deleted: %+v", ops["sub/b.txt"])
	}
	if ops["new.txt"].Op != "added" {
		t.Fatalf("new.txt 应为 added: %+v", ops["new.txt"])
	}
	if !ops["big.bin"].Skipped {
		t.Fatalf("超限大文件应标记 skipped: %+v", ops["big.bin"])
	}

	skipped, err := Restore(snapDir, work)
	if err != nil {
		t.Fatal(err)
	}
	if len(skipped) != 1 || skipped[0] != "big.bin" {
		t.Fatalf("不可恢复清单应只含 big.bin: %v", skipped)
	}
	if got := readFile(t, filepath.Join(work, "a.txt")); got != "hello" {
		t.Fatalf("a.txt 应恢复: %q", got)
	}
	if got := readFile(t, filepath.Join(work, "sub", "b.txt")); got != "world" {
		t.Fatalf("b.txt 应找回: %q", got)
	}
	if _, err := os.Stat(filepath.Join(work, "new.txt")); !os.IsNotExist(err) {
		t.Fatalf("新增文件应被清除: %v", err)
	}
	if !strings.HasPrefix(readFile(t, filepath.Join(work, "big.bin")), "CCC") {
		t.Fatal("超限文件不回滚（如实保留脚本效果）")
	}
	if !Undone(snapDir) {
		t.Fatal("恢复后应有 undone 标记")
	}
}

// TestSnapshotNoChanges：零变更也是审计事实——changed.json 照落。
func TestSnapshotNoChanges(t *testing.T) {
	work := t.TempDir()
	snapDir := t.TempDir()
	writeFile(t, filepath.Join(work, "keep.txt"), "same", 0o644)

	s, err := Begin(work, snapDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Finish(); err != nil {
		t.Fatal(err)
	}
	changes, err := Changes(snapDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 0 {
		t.Fatalf("零变更应为空集: %+v", changes)
	}
}

// TestRestoreRejectsTraversal：被篡改成穿越路径的变更集必须拒绝
// ——恢复是写操作，变更集是它的输入。
func TestRestoreRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	tampered := `[{"rel":"../../outside.txt","op":"deleted"}]`
	if err := os.WriteFile(filepath.Join(dir, "changed.json"), []byte(tampered), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(dir, t.TempDir()); err == nil {
		t.Fatal("穿越路径应被拒绝")
	}
}

// TestBlobDedup：跨快照的内容寻址去重——同内容只存一份 blob。
func TestBlobDedup(t *testing.T) {
	work := t.TempDir()
	shared := t.TempDir() // 两个快照共用一个库的形态：目录下共享 store
	snap1 := filepath.Join(shared, "s1")
	snap2 := filepath.Join(shared, "s2")
	writeFile(t, filepath.Join(work, "f.txt"), "shared-content", 0o644)

	s1, err := Begin(work, snap1)
	if err != nil {
		t.Fatal(err)
	}
	// 不改动文件，第二份快照的内容与第一份全同。
	s2, err := Begin(work, snap2)
	if err != nil {
		t.Fatal(err)
	}
	_ = s1.Finish()
	_ = s2.Finish()

	entries1, _ := os.ReadDir(filepath.Join(snap1, "store"))
	entries2, _ := os.ReadDir(filepath.Join(snap2, "store"))
	if len(entries1) != 1 || len(entries2) != 1 {
		t.Fatalf("同内容应各只有一份 blob: %d/%d", len(entries1), len(entries2))
	}
	if filepath.Base(entries1[0].Name()) != filepath.Base(entries2[0].Name()) {
		t.Fatal("两份快照的 blob 键应相同（内容寻址）")
	}
}
