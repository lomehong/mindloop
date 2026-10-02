package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mindloop/internal/snapshot"
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

// TestRunSnapshotsWriteScript：快照开启时，非只读脚本执行后快照
// 目录里出现变更集，Restore 把工作目录恢复到执行前；只读脚本不
// 产生快照。
func TestRunSnapshotsWriteScript(t *testing.T) {
	requireBash(t)
	tr := newTestTimeline(t)
	work := t.TempDir()
	writeFile(t, filepath.Join(work, "notes.txt"), "before", 0o644)

	thinker := &fakeThinker{responses: []string{
		fence("echo changed > notes.txt\necho extra > extra.txt"),
		fence(`FINAL="搞定"`),
	}}
	res, err := Run(context.Background(), Options{
		Timeline:  tr,
		Thinker:   thinker,
		Task:      "改文件",
		WorkDir:   work,
		Snapshots: true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	snapDir := filepath.Join(tr.Dir, "snapshots", res.RunID)
	changes, err := snapshot.Changes(snapDir)
	if err != nil {
		t.Fatalf("快照变更集应落盘: %v", err)
	}
	// 三项：notes.txt 改写、extra.txt 新增、.mindloop_final 哨兵新增
	//（运行级会话——两个迭代的差分合一，final 轮的哨兵也算运行效果）。
	if len(changes) != 3 {
		t.Fatalf("应有三项变更: %+v", changes)
	}
	ops := map[string]string{}
	for _, ch := range changes {
		ops[ch.Rel] = ch.Op
	}
	if ops["notes.txt"] != "modified" || ops["extra.txt"] != "added" {
		t.Fatalf("变更归类错: %v", ops)
	}

	skipped, err := snapshot.Restore(snapDir, work)
	if err != nil {
		t.Fatalf("恢复: %v", err)
	}
	if len(skipped) != 0 {
		t.Fatalf("不应有不可恢复项: %v", skipped)
	}
	got, err := os.ReadFile(filepath.Join(work, "notes.txt"))
	if err != nil || string(got) != "before" {
		t.Fatalf("notes.txt 应恢复到 before: %q %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(work, "extra.txt")); !os.IsNotExist(err) {
		t.Fatalf("extra.txt 应被清除: %v", err)
	}
}

// TestRunSkipsSnapshotForReadOnlyScript：只读脚本免拍——快照目录
// 根本不该出现。
func TestRunSkipsSnapshotForReadOnlyScript(t *testing.T) {
	requireBash(t)
	tr := newTestTimeline(t)
	thinker := &fakeThinker{responses: []string{
		fence("ls -la\ncat /dev/null"),
		fence(`FINAL="看完了"`),
	}}
	if _, err := Run(context.Background(), Options{
		Timeline:  tr,
		Thinker:   thinker,
		Task:      "只看看",
		Snapshots: true,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tr.Dir, "snapshots")); !os.IsNotExist(err) {
		t.Fatalf("只读脚本不应产生快照目录: %v", err)
	}
}

// TestRunSnapshotDisabledByDefault：开关关闭（零值）时写脚本也不拍。
func TestRunSnapshotDisabledByDefault(t *testing.T) {
	requireBash(t)
	tr := newTestTimeline(t)
	thinker := &fakeThinker{responses: []string{
		fence("echo x > out.txt"),
		fence(`FINAL="完成"`),
	}}
	if _, err := Run(context.Background(), Options{
		Timeline: tr,
		Thinker:  thinker,
		Task:     "写点东西",
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tr.Dir, "snapshots")); !os.IsNotExist(err) {
		t.Fatalf("未开启快照不应产生快照目录: %v", err)
	}
}

// TestRunSnapshotFailureDegrades：快照目录建不出来时执行照常继续
// ——刹车不能卡油门。
func TestRunSnapshotFailureDegrades(t *testing.T) {
	requireBash(t)
	tr := newTestTimeline(t)
	// 占用快照路径为普通文件，逼 Begin 失败。
	blocker := filepath.Join(tr.Dir, "snapshots")
	if err := os.MkdirAll(filepath.Dir(blocker), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blocker, []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	thinker := &fakeThinker{responses: []string{
		fence("echo ok > done-marker.txt"),
		fence(`FINAL="完成"`),
	}}
	res, err := Run(context.Background(), Options{
		Timeline:  tr,
		Thinker:   thinker,
		Task:      "写点东西",
		Snapshots: true,
	})
	if err != nil {
		t.Fatalf("快照失败不应阻断执行: %v", err)
	}
	if !strings.Contains(res.Final, "完成") && res.Iterations == 0 {
		t.Fatal("sanity")
	}
}
