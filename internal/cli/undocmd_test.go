package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mindloop/internal/snapshot"
	"mindloop/internal/traj"
)

// writeFile 测试用写文件。
func writeFile(t *testing.T, path, content string, perm os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), perm); err != nil {
		t.Fatal(err)
	}
}

// setupUndoFixture 建一条轨迹 + 一次"运行"的快照：工作目录里
// notes.txt（改写）、gone.txt（删除）、fresh.txt（新增）。返回
// 轨迹 id 前缀（undo <traj> 参数用 UUID 前缀解析）。
func setupUndoFixture(t *testing.T) (tlID, workDir, snapDir string) {
	t.Helper()
	tl, err := traj.Create(context.Background(), "undo-test")
	if err != nil {
		t.Fatal(err)
	}
	workDir = filepath.Join(tl.Dir, "runs", "run-01")
	snapDir = filepath.Join(tl.Dir, "snapshots", "run-01")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(workDir, "notes.txt"), "before", 0o644)
	writeFile(t, filepath.Join(workDir, "gone.txt"), "doomed", 0o644)

	s, err := snapshot.Begin(workDir, snapDir)
	if err != nil {
		t.Fatal(err)
	}
	// "脚本"效果：改写、删除、新增。
	writeFile(t, filepath.Join(workDir, "notes.txt"), "after", 0o644)
	if err := os.Remove(filepath.Join(workDir, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(workDir, "fresh.txt"), "fresh", 0o644)
	if err := s.Finish(); err != nil {
		t.Fatal(err)
	}
	return tl.ID[:8], workDir, snapDir
}

// TestUndoListAndRestore：列表模式显示变更集；恢复模式把工作目录
// 拉回执行前；重复恢复被拒。
func TestUndoListAndRestore(t *testing.T) {
	newTestHome(t)
	tlID, workDir, _ := setupUndoFixture(t)

	// 列表模式。
	code, out, errOut := runCLI(t, "undo", tlID)
	if code != 0 {
		t.Fatalf("列表 exit = %d err=%s", code, errOut)
	}
	for _, want := range []string{"run-01", "modified", "notes.txt", "added", "fresh.txt", "deleted", "gone.txt"} {
		if !strings.Contains(out, want) {
			t.Fatalf("列表缺 %q:\n%s", want, out)
		}
	}

	// 恢复（--yes 免确认）。
	code, out, errOut = runCLI(t, "undo", tlID, "run-01", "--yes")
	if code != 0 {
		t.Fatalf("恢复 exit = %d out=%s err=%s", code, out, errOut)
	}
	if got, _ := os.ReadFile(filepath.Join(workDir, "notes.txt")); string(got) != "before" {
		t.Fatalf("notes.txt 应恢复: %q", got)
	}
	if _, err := os.Stat(filepath.Join(workDir, "gone.txt")); err != nil {
		t.Fatalf("gone.txt 应找回: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workDir, "fresh.txt")); !os.IsNotExist(err) {
		t.Fatalf("fresh.txt 应清除: %v", err)
	}

	// 重复恢复被拒（--force 才放行）。拒绝理由经 stderr 出来
	//（c.fail 的诊断通道）。
	code, _, errOut = runCLI(t, "undo", tlID, "run-01", "--yes")
	if code == 0 || !strings.Contains(errOut, "已撤销过") {
		t.Fatalf("重复恢复应被拒: code=%d err=%s", code, errOut)
	}
	code, out, _ = runCLI(t, "undo", tlID, "run-01", "--yes", "--force")
	if code != 0 {
		t.Fatalf("--force 恢复 exit = %d out=%s", code, out)
	}
}

// TestUndoRejectsBadRunID：运行 id 带路径片段直接用法错误。
func TestUndoRejectsBadRunID(t *testing.T) {
	newTestHome(t)
	tl, err := traj.Create(context.Background(), "undo-bad")
	if err != nil {
		t.Fatal(err)
	}
	code, out, _ := runCLI(t, "undo", tl.ID[:8], "../escape")
	if code != 2 {
		t.Fatalf("非法 run-id 应退出码 2: %d out=%s", code, out)
	}
}

// TestUndoEmptyRunReported：零变更的快照在恢复时如实说明。
func TestUndoEmptyRunReported(t *testing.T) {
	newTestHome(t)
	tl, err := traj.Create(context.Background(), "undo-empty")
	if err != nil {
		t.Fatal(err)
	}
	workDir := filepath.Join(tl.Dir, "runs", "run-02")
	snapDir := filepath.Join(tl.Dir, "snapshots", "run-02")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := snapshot.Begin(workDir, snapDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Finish(); err != nil {
		t.Fatal(err)
	}
	code, out, _ := runCLI(t, "undo", tl.ID[:8], "run-02", "--yes")
	if code != 0 || !strings.Contains(out, "没有改动任何文件") {
		t.Fatalf("零变更应如实说明: code=%d out=%s", code, out)
	}
}
