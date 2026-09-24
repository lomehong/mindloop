package traj

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// newTimeline 通过 MINDLOOP_HOME 给每个测试一个隔离的状态根目录
// （t.Setenv 禁止并行测试，对这些文件系统绑定的测试正合适），并
// 返回一条新的独立轨迹。
func newTimeline(t *testing.T) *Timeline {
	t.Helper()
	t.Setenv("MINDLOOP_HOME", filepath.Join(t.TempDir(), "home"))
	tr, err := Create(context.Background(), "test")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return tr
}

func mustAppend(t *testing.T, tr *Timeline, typ, content string) Step {
	t.Helper()
	s := NewStep(typ)
	if content != "" {
		s.Fields["content"] = content
	}
	if err := tr.Append(context.Background(), s); err != nil {
		t.Fatalf("Append(%s): %v", typ, err)
	}
	return s
}

func writeRawLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	var buf bytes.Buffer
	for _, ln := range lines {
		buf.WriteString(ln)
		buf.WriteByte('\n')
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("写入 %s: %v", path, err)
	}
}

func validLine(t *testing.T, typ string) string {
	t.Helper()
	b, err := json.Marshal(NewStep(typ))
	if err != nil {
		t.Fatalf("序列化步骤: %v", err)
	}
	return string(b)
}

func TestCreateAppendTail(t *testing.T) {
	tr := newTimeline(t)
	mustAppend(t, tr, "message", "hello")
	mustAppend(t, tr, "thought", "")
	mustAppend(t, tr, "final", "done")

	steps, err := tr.Steps()
	if err != nil {
		t.Fatalf("Steps: %v", err)
	}
	if len(steps) != 4 {
		t.Fatalf("得到 %d 个步骤，应为 4（头行 + 3）", len(steps))
	}
	if steps[0].Type != "trajectory" {
		t.Fatalf("第一个步骤类型 = %q，应为 trajectory", steps[0].Type)
	}
	if steps[0].StepID != tr.ID {
		t.Fatalf("头行 step_id %q != 轨迹 id %q", steps[0].StepID, tr.ID)
	}
	for _, s := range steps {
		if _, err := time.Parse(TimeFormat, s.TS); err != nil {
			t.Fatalf("步骤 %s 的时间戳 %q 无法解析: %v", s.StepID, s.TS, err)
		}
	}

	tail, err := tr.Tail(2, nil)
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}
	if len(tail) != 2 || tail[1].Type != "final" {
		t.Fatalf("Tail(2) = %d 个步骤，最后一个是 %q", len(tail), tail[len(tail)-1].Type)
	}

	typed, err := tr.Tail(0, []string{"message"})
	if err != nil {
		t.Fatalf("Tail 过滤: %v", err)
	}
	if len(typed) != 1 || typed[0].Type != "message" {
		t.Fatalf("过滤后的 tail = %d 个步骤", len(typed))
	}

	last, err := tr.LastStep()
	if err != nil || last.Type != "final" {
		t.Fatalf("LastStep = %v, %v", last, err)
	}
}

func TestConcurrentAppends(t *testing.T) {
	tr := newTimeline(t)
	const goroutines, perG = 8, 25
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				if err := tr.Append(context.Background(), NewStep("observation")); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("并发 Append: %v", err)
	}

	// 200 次并发追加后：行数正确、每行都是完整的 JSON——目录锁
	// 保证操作定序，O_APPEND 保证字节完整性。
	data, err := os.ReadFile(tr.Path)
	if err != nil {
		t.Fatal(err)
	}
	trimmed := bytes.TrimSuffix(data, []byte("\n"))
	lines := bytes.Split(trimmed, []byte("\n"))
	if len(lines) != goroutines*perG+1 {
		t.Fatalf("文件有 %d 行，应为 %d", len(lines), goroutines*perG+1)
	}
	bad, err := tr.Check()
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(bad) != 0 {
		t.Fatalf("并发追加损坏了行 %v", bad)
	}
}

// deadPID 返回一个已完全退出的进程的 pid，让锁测试能确定性地走
// "属主已死"的偷锁路径。
func deadPID(t *testing.T) int {
	t.Helper()
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/c", "exit 0")
	} else {
		cmd = exec.Command("true")
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("启动一次性进程: %v", err)
	}
	pid := cmd.Process.Pid
	_ = cmd.Wait()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			return pid
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Skip("没能及时观察到死 pid")
	return 0
}

func TestLockStealsDeadOwner(t *testing.T) {
	tr := newTimeline(t)
	lockDir := tr.Path + ".lock"
	if err := os.Mkdir(lockDir, 0o755); err != nil {
		t.Fatal(err)
	}
	owner := fmt.Sprintf(`{"pid":%d,"created":"%s","exe":"gone"}`, deadPID(t), NowString())
	if err := os.WriteFile(filepath.Join(lockDir, ownerFile), []byte(owner), 0o644); err != nil {
		t.Fatal(err)
	}
	release, err := acquireDirLock(context.Background(), lockDir, 2*time.Second)
	if err != nil {
		t.Fatalf("死属主的锁没被偷走: %v", err)
	}
	release()
}

func TestLockSkipsOwnerlessYoungLock(t *testing.T) {
	tr := newTimeline(t)
	lockDir := tr.Path + ".lock"
	if err := os.Mkdir(lockDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// 刚创建且无属主：正是崩溃窗口——宽限期内不能偷。
	if _, err := acquireDirLock(context.Background(), lockDir, 150*time.Millisecond); err == nil {
		t.Fatal("年轻的无属主锁被偷了；宽限窗口失效")
	}
	// 把修改时间回拨：现在它像是一个写者中途死掉的锁。
	past := time.Now().Add(-lockGrace - time.Second)
	if err := os.Chtimes(lockDir, past, past); err != nil {
		t.Skipf("无法回拨修改时间: %v", err)
	}
	release, err := acquireDirLock(context.Background(), lockDir, 2*time.Second)
	if err != nil {
		t.Fatalf("过期的无属主锁没被偷走: %v", err)
	}
	release()
}

func TestLockLiveOwnerNeverStolen(t *testing.T) {
	tr := newTimeline(t)
	lockDir := tr.Path + ".lock"
	release, err := acquireDirLock(context.Background(), lockDir, time.Second)
	if err != nil {
		t.Fatalf("第一次获取: %v", err)
	}
	// 我们自己（活着的进程）持有锁：第二个写者必须超时且拿到
	// ErrLockTimeout 哨兵，绝不偷活属主的锁。
	_, err = acquireDirLock(context.Background(), lockDir, 150*time.Millisecond)
	if err == nil {
		t.Fatal("活属主的锁被偷了")
	}
	if !errors.Is(err, ErrLockTimeout) {
		t.Fatalf("竞争超时应包装 ErrLockTimeout，得到: %v", err)
	}
	release()
	if _, err := acquireDirLock(context.Background(), lockDir, time.Second); err != nil {
		t.Fatalf("释放后再获取: %v", err)
	}
}

func TestAppendHonorsContextCancel(t *testing.T) {
	tr := newTimeline(t)
	tr.LockTimeout = 10 * time.Second
	release, err := acquireDirLock(context.Background(), tr.Path+".lock", time.Second)
	if err != nil {
		t.Fatalf("占用锁: %v", err)
	}
	defer release()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	if _, err := acquireDirLock(ctx, tr.Path+".lock", 10*time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("ctx 取消应传播为 context.Canceled，得到: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("取消后 %v 才返回；取消传播失效", elapsed)
	}
}

func newTestCursor(t *testing.T, path string) *Cursor {
	t.Helper()
	return NewCursor(path)
}

func readNewSteps(t *testing.T, c *Cursor) []Step {
	t.Helper()
	steps, err := c.ReadNew()
	if err != nil {
		t.Fatalf("ReadNew: %v", err)
	}
	return steps
}

func TestCursorFollow(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "t.jsonl")
	writeRawLines(t, p, validLine(t, "thought"), validLine(t, "thought"), validLine(t, "final"))

	c := newTestCursor(t, p)
	if steps := readNewSteps(t, c); len(steps) != 3 {
		t.Fatalf("第一次读取 = %d 个步骤，应为 3", len(steps))
	}
	if steps := readNewSteps(t, c); len(steps) != 0 {
		t.Fatalf("第二次读取 = %d 个步骤，应为 0", len(steps))
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(validLine(t, "message") + "\n")
	f.Close()
	if steps := readNewSteps(t, c); len(steps) != 1 || steps[0].Type != "message" {
		t.Fatalf("追加后读取 = %v，应为一条 message", steps)
	}
}

func TestCursorHoldsTornTail(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "t.jsonl")
	// 一个有效步骤减去结尾换行：必须保持未读。
	partial := `{"type":"thought","step_id":"11111111-1111-4111-8111-111111111111","ts":"2026-01-01T00:00:00.000Z"}`
	if err := os.WriteFile(p, []byte(partial), 0o644); err != nil {
		t.Fatal(err)
	}
	c := newTestCursor(t, p)
	if steps := readNewSteps(t, c); len(steps) != 0 {
		t.Fatalf("残缺半行交付了 %d 个步骤，应为 0", len(steps))
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("\n")
	f.Close()
	steps := readNewSteps(t, c)
	if len(steps) != 1 || steps[0].Type != "thought" {
		t.Fatalf("补全后得到 %v，应为一条 thought", steps)
	}
}

func TestCursorShrinkReplays(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "t.jsonl")
	writeRawLines(t, p, validLine(t, "thought"), validLine(t, "thought"), validLine(t, "thought"))
	c := newTestCursor(t, p)
	if steps := readNewSteps(t, c); len(steps) != 3 {
		t.Fatalf("第一次读取 = %d 个步骤，应为 3", len(steps))
	}
	// 文件被重写得变小（一次修复、一次重建）：cursor 必须归零并
	// 重放，而不是错位。
	writeRawLines(t, p, validLine(t, "final"))
	if steps := readNewSteps(t, c); len(steps) != 1 {
		t.Fatalf("收缩后读取 = %d 个步骤，应为重放的 1", len(steps))
	}
}

func TestCursorReplacedFileReplays(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "t.jsonl")
	writeRawLines(t, p, validLine(t, "thought"))
	c := newTestCursor(t, p)
	if steps := readNewSteps(t, c); len(steps) != 1 {
		t.Fatalf("第一次读取 = %d 个步骤，应为 1", len(steps))
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	writeRawLines(t, p, validLine(t, "message"), validLine(t, "final"))
	if f, err := os.Open(p); err == nil {
		same := fileIdentity(f) == c.fileID
		f.Close()
		if same {
			t.Skip("替换后的文件复用了旧文件身份；无可断言内容")
		}
	}
	if steps := readNewSteps(t, c); len(steps) != 2 {
		t.Fatalf("替换后读取 = %d 个步骤，应为重放的 2", len(steps))
	}
}

func TestCursorStartAtEndSkipsHistory(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "t.jsonl")
	writeRawLines(t, p, validLine(t, "thought"), validLine(t, "thought"), validLine(t, "final"))

	// feeder 的正确语义：冷启动只看"之后"发生的事，绝不重放历史
	// ——Headlong 的桥接在重放 130 条旧消息后加上了同样的对策。
	c := NewCursorAtEnd(p)
	if steps := readNewSteps(t, c); len(steps) != 0 {
		t.Fatalf("冷启动重放了 %d 条历史步骤，应为 0", len(steps))
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(validLine(t, "message") + "\n")
	f.Close()
	if steps := readNewSteps(t, c); len(steps) != 1 {
		t.Fatalf("启动后的新步骤未投递: %d", len(steps))
	}
}

func TestCursorSaveLoad(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "t.jsonl")
	cp := filepath.Join(dir, "t.cursor")
	writeRawLines(t, p, validLine(t, "thought"), validLine(t, "final"))

	c := newTestCursor(t, p)
	if steps := readNewSteps(t, c); len(steps) != 2 {
		t.Fatalf("第一次读取 = %d 个步骤，应为 2", len(steps))
	}
	if err := c.Save(cp); err != nil {
		t.Fatalf("Save: %v", err)
	}
	c2, err := LoadCursor(cp, p)
	if err != nil {
		t.Fatalf("LoadCursor: %v", err)
	}
	if steps := readNewSteps(t, c2); len(steps) != 0 {
		t.Fatalf("加载的 cursor 重投了 %d 个步骤，应为 0", len(steps))
	}
	// 为文件 A 保存的 cursor 指向文件 B：身份不匹配必须归零重来，
	// 绝不能把外来的状态套上去。
	other := filepath.Join(dir, "other.jsonl")
	writeRawLines(t, other, validLine(t, "message"))
	c3, err := LoadCursor(cp, other)
	if err != nil {
		t.Fatalf("LoadCursor other: %v", err)
	}
	if steps := readNewSteps(t, c3); len(steps) != 1 {
		t.Fatalf("外来 cursor 在新文件上 = %d 个步骤，应为干净重放的 1", len(steps))
	}
}

func TestForkMergeRoot(t *testing.T) {
	t.Setenv("MINDLOOP_HOME", filepath.Join(t.TempDir(), "home"))
	parent, err := Create(context.Background(), "parent")
	if err != nil {
		t.Fatalf("Create parent: %v", err)
	}
	child, err := CreateFork(context.Background(), "child-step", parent)
	if err != nil {
		t.Fatalf("CreateFork: %v", err)
	}

	hdr, err := child.Header()
	if err != nil {
		t.Fatalf("child Header: %v", err)
	}
	if got, _ := hdr.Field("parent_traj"); got != parent.ID {
		t.Fatalf("child parent_traj = %q，应为 %q", got, parent.ID)
	}
	forkStepID, _ := hdr.Field("parent_step")
	if forkStepID == "" {
		t.Fatal("child 头行缺少 parent_step")
	}

	psteps, err := parent.Steps()
	if err != nil {
		t.Fatalf("parent Steps: %v", err)
	}
	if len(psteps) != 2 || psteps[1].Type != "fork" {
		t.Fatalf("父轨迹有 %d 个步骤（最后 %q），应为头行+fork", len(psteps), psteps[len(psteps)-1].Type)
	}
	if psteps[1].StepID != forkStepID {
		t.Fatalf("fork 步骤 id %q != child 的 parent_step %q", psteps[1].StepID, forkStepID)
	}
	if ref, _ := psteps[1].Field("child"); ref != child.ID {
		t.Fatalf("fork child = %q，应为 %q", ref, child.ID)
	}

	mustAppend(t, child, "final", "subtask done")
	if _, err := parent.Merge(context.Background(), child, "subtask done"); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	psteps, err = parent.Steps()
	if err != nil {
		t.Fatalf("parent Steps: %v", err)
	}
	merge := psteps[len(psteps)-1]
	if merge.Type != "merge" {
		t.Fatalf("父轨迹最后一个步骤 = %q，应为 merge", merge.Type)
	}
	if got, _ := merge.Field("from_traj"); got != child.ID {
		t.Fatalf("merge from_traj = %q，应为 %q", got, child.ID)
	}

	root, err := child.Root()
	if err != nil {
		t.Fatalf("Root: %v", err)
	}
	if root.ID != parent.ID {
		t.Fatalf("child.Root() = %q，应为 %q", root.ID, parent.ID)
	}
}

func TestLoadPrefixSentinels(t *testing.T) {
	t.Setenv("MINDLOOP_HOME", filepath.Join(t.TempDir(), "home"))
	a, err := Create(context.Background(), "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Create(context.Background(), "beta"); err != nil {
		t.Fatal(err)
	}

	got, err := Load(a.ID[:8])
	if err != nil || got.ID != a.ID {
		t.Fatalf("Load(短前缀) = %v, %v", got, err)
	}
	got, err = Load(a.ID[:16])
	if err != nil || got.ID != a.ID {
		t.Fatalf("Load(长前缀) = %v, %v", got, err)
	}
	if _, err := Load("zzzzzzzz"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Load 未知前缀应返回 ErrNotFound，得到: %v", err)
	}

	infos, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("List = %d 条轨迹，应为 2", len(infos))
	}
}

func TestFindStepAnywhereAndCheck(t *testing.T) {
	tr := newTimeline(t)
	s := mustAppend(t, tr, "message", "findme")

	found, ok, err := tr.FindStep(s.StepID[:12])
	if err != nil || !ok {
		t.Fatalf("FindStep = %v, %v, %v", found, ok, err)
	}
	if _, _, err := FindStepAnywhere(s.StepID[:12]); err != nil {
		t.Fatalf("FindStepAnywhere: %v", err)
	}
	if _, _, err := FindStepAnywhere("ffffffff"); !errors.Is(err, ErrNoStep) {
		t.Fatalf("FindStepAnywhere 未命中应返回 ErrNoStep，得到: %v", err)
	}

	// 弄坏日志：读取方跳过，check 上报。
	f, err := os.OpenFile(tr.Path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("this is not json\n\n")
	f.Close()
	bad, err := tr.Check()
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(bad) != 1 {
		t.Fatalf("Check = %v，应有一行坏行", bad)
	}
	steps, err := tr.Steps()
	if err != nil {
		t.Fatalf("Steps: %v", err)
	}
	if len(steps) != 2 {
		t.Fatalf("损坏后 Steps = %d，应为 2（头行 + message，跳过坏行）", len(steps))
	}
}

func TestStepRoundTripIsStable(t *testing.T) {
	s := NewStep("message")
	s.Fields["from"] = "operator"
	s.Fields["count"] = json.Number("42")
	s.Fields["nested"] = map[string]any{"a": true}
	s.Fields["type"] = "MUST-NOT-APPEAR"

	b1, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("序列化: %v", err)
	}
	s2, err := ParseStep(b1)
	if err != nil {
		t.Fatalf("ParseStep: %v", err)
	}
	b2, err := json.Marshal(s2)
	if err != nil {
		t.Fatalf("重新序列化: %v", err)
	}
	if !bytes.Equal(b1, b2) {
		t.Fatalf("往返不稳定:\n%s\n%s", b1, b2)
	}
	if bytes.Contains(b1, []byte("MUST-NOT-APPEAR")) {
		t.Fatal("与信封同名的载荷字段泄漏进了行里")
	}
	if got, _ := s2.Field("count"); got != "42" {
		t.Fatalf("count = %q，应为 42", got)
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Hello World":      "hello-world",
		"  --padded name ": "padded-name",
		"file_2026.v2":     "file_2026.v2",
		"héllo wörld":      "h-llo-w-rld",
	}
	for in, want := range cases {
		if got := Slugify(in, 40); got != want {
			t.Fatalf("Slugify(%q) = %q，应为 %q", in, got, want)
		}
	}
	if got := Slugify(strings.Repeat("a", 100), 10); len(got) != 10 {
		t.Fatalf("Slugify 上限 = %d 字符，应为 10", len(got))
	}
}

func TestCatFilters(t *testing.T) {
	tr := newTimeline(t)
	m1 := mustAppend(t, tr, "message", "one")
	m1.Fields["from"] = "operator"
	if err := tr.Append(context.Background(), m1); err != nil {
		t.Fatal(err)
	}
	mustAppend(t, tr, "message", "two")
	steps, err := tr.Cat(map[string]string{"from": "operator"}, []string{"message"})
	if err != nil {
		t.Fatalf("Cat: %v", err)
	}
	if len(steps) != 1 || steps[0].StepID != m1.StepID {
		t.Fatalf("Cat 返回 %d 个步骤，应只有 m1", len(steps))
	}
}
