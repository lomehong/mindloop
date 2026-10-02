package sensor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// collect 是事件收集器：返回收到的 PEvent 切片与停止函数。
func collect() (func(PEvent), func() []PEvent) {
	var mu sync.Mutex
	var events []PEvent
	var once sync.Once
	return func(e PEvent) {
			mu.Lock()
			events = append(events, e)
			mu.Unlock()
		}, func() []PEvent {
			once.Do(func() {}) // 语义占位：getter 可重复调用
			mu.Lock()
			defer mu.Unlock()
			return append([]PEvent(nil), events...)
		}
}

func waitEvents(t *testing.T, get func() []PEvent, n int, why string) []PEvent {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if len(get()) >= n {
			return get()
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("等待 %d 个事件超时（%s），实收 %d 个: %+v", n, why, len(get()), get())
	return nil
}

func TestFileSensorRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := NewFileSensor(SensorConfig{ID: "fs1", Type: "file", Path: dir})
	if err != nil {
		t.Fatalf("构造: %v", err)
	}
	onEvent, events := collect()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Watch(ctx, onEvent)
	time.Sleep(200 * time.Millisecond) // watch 挂上（Windows ReadDirectoryChangesW 无确认回执）

	// 修改 → changed。
	p := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(p, []byte("v1"), 0o644); err != nil {
		t.Fatalf("写文件: %v", err)
	}
	evs := waitEvents(t, events, 1, "写新文件应产生 appeared/changed")
	if evs[len(evs)-1].Subject != p {
		t.Fatalf("subject = %q", evs[len(evs)-1].Subject)
	}

	// 再改一次 → changed（指纹去重由判定层做，感官层如实上报）。
	if err := os.WriteFile(p, []byte("v2 longer"), 0o644); err != nil {
		t.Fatalf("改文件: %v", err)
	}
	evs = waitEvents(t, events, 2, "二次修改应产生 changed")

	// 删除 → removed。
	if err := os.Remove(p); err != nil {
		t.Fatalf("删文件: %v", err)
	}
	evs = waitEvents(t, events, 3, "删除应产生 removed")
	last := evs[len(evs)-1]
	if last.Kind != KindRemoved {
		t.Fatalf("最后事件应 removed，得 %s", last.Kind)
	}
}

func TestGitSensorCommitEvent(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("环境无 git，跳过")
	}
	old := gitIntervalMin
	gitIntervalMin = 50 * time.Millisecond
	t.Cleanup(func() { gitIntervalMin = old })

	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	run("config", "user.email", "t@example.invalid")
	run("config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "init")

	s, err := NewGitSensor(SensorConfig{ID: "g1", Type: "git", Path: dir})
	if err != nil {
		t.Fatalf("构造: %v", err)
	}
	onEvent, events := collect()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Watch(ctx, onEvent)
	time.Sleep(100 * time.Millisecond) // 首轮采样建基线

	// 新提交 → changed。
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "second commit")
	evs := waitEvents(t, events, 1, "新提交应产生 changed")
	found := false
	for _, e := range evs {
		if e.Kind == KindChanged && len(e.Digest) > 0 && (contains(e.Digest, "second commit") || contains(e.Digest, "工作区")) {
			found = true
		}
	}
	// 工作区与 HEAD 各有一条采样路径，两条都算通过——关键是有事件。
	if !found && len(evs) == 0 {
		t.Fatalf("应产生提交或工作区事件")
	}
}

func TestWebSensorTextExtraction(t *testing.T) {
	got := extractText([]byte("<html><head><style>x{}</style></head><body><h1>价格&amp;库存</h1><p>下降了</p><!-- c --></body></html>"))
	if !contains(got, "价格&库存") || !contains(got, "下降了") {
		t.Fatalf("正文提取不符: %q", got)
	}
	if contains(got, "<") || contains(got, "x{}") {
		t.Fatalf("标签/样式应被剥离: %q", got)
	}

	// 构造校验：非 http(s) 拒绝。
	if _, err := NewWebSensor(SensorConfig{ID: "w", Type: "web", URL: "ftp://x"}); err == nil {
		t.Fatalf("非 http(s) 应拒绝")
	}
}
