package runner

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"mindloop/internal/llm"
	"mindloop/internal/sandbox"
	"mindloop/internal/traj"
)

// requireBash 在没有【可用】bash 的环境干净跳过。BashPath 内部做
// 真实执行探测（bash -c true），所以这里是能力语义：只有 WSL 存根
// 的机器会干净 skip，而不是拿存根跑出一串假失败（存根缺陷：PATH
// 命中让 LookPath 成功，却执行不了任何脚本）。
func requireBash(t *testing.T) {
	t.Helper()
	if _, err := sandbox.BashPath(); err != nil {
		t.Skipf("跳过： %v", err)
	}
}

// fakeThinker 按脚本逐轮返回预设文本，供循环的确定性验证。
type fakeThinker struct {
	responses []string
	calls     int
	system    string
}

func (f *fakeThinker) Think(ctx context.Context, system string, msgs []llm.Message) (string, error) {
	f.system = system
	if f.calls >= len(f.responses) {
		t := f.responses[len(f.responses)-1]
		f.calls++
		return t, nil
	}
	r := f.responses[f.calls]
	f.calls++
	return r, nil
}

func newTestTimeline(t *testing.T) *traj.Timeline {
	t.Helper()
	t.Setenv("MINDLOOP_HOME", t.TempDir())
	tr, err := traj.Create(context.Background(), "runner-test")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return tr
}

func fence(code string) string {
	return "```bash\n" + code + "\n```"
}

func TestRunCompletesWithFinal(t *testing.T) {
	requireBash(t)
	tl := newTestTimeline(t)
	thinker := &fakeThinker{responses: []string{
		"我先看一下环境。\n" + fence("echo step-one\npwd"),
		fence("echo step-two\nFINAL=\"全部完成\""),
	}}
	res, err := Run(context.Background(), Options{
		Timeline:    tl,
		Thinker:     thinker,
		Task:        "演示任务",
		IdleTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Final != "全部完成" || res.Iterations != 2 {
		t.Fatalf("final=%q iterations=%d", res.Final, res.Iterations)
	}
	if !strings.HasPrefix(thinker.system, "You are the thinking core") {
		t.Fatalf("系统提示未传入: %q", thinker.system[:40])
	}

	steps, err := tl.Steps()
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	for _, s := range steps {
		types = append(types, s.Type)
	}
	want := []string{"trajectory", "run", "prompt", "reasoning", "shell-output", "reasoning", "shell-output", "final"}
	if !equalStrings(types, want) {
		t.Fatalf("步骤序列 = %v，应为 %v", types, want)
	}
	// shell-output 落盘了第一轮的输出
	out1 := steps[4]
	if content, _ := out1.Field("content"); !strings.Contains(content, "step-one") {
		t.Fatalf("第一轮输出未落盘: %q", content)
	}
	if code, _ := out1.Field("exit_code"); code != "0" {
		t.Fatalf("exit_code = %q", code)
	}
	// 全部步骤盖了 run_id 章
	for _, s := range steps[2:] {
		if rid, ok := s.Field("run_id"); !ok || rid != res.RunID {
			t.Fatalf("步骤 %s 缺 run_id", s.StepID)
		}
	}
}

// TestRunRealExecutionSmoke：真实沙箱冒烟——
// 脚本真的跑、stdout 真的落盘、FINAL 真的写哨兵。它断言的是执行
// 链本身，在"PATH 里的 bash 不能用"的环境下最先暴露断裂。
func TestRunRealExecutionSmoke(t *testing.T) {
	requireBash(t)
	tl := newTestTimeline(t)
	token := "runner-smoke-7351"
	thinker := &fakeThinker{responses: []string{
		fence("echo " + token + "\nFINAL=\"smoke done\""),
	}}
	res, err := Run(context.Background(), Options{
		Timeline:    tl,
		Thinker:     thinker,
		Task:        "真实执行冒烟",
		IdleTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Final != "smoke done" {
		t.Fatalf("final = %q", res.Final)
	}
	steps, err := tl.Steps()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range steps {
		if s.Type == "shell-output" {
			if c, _ := s.Field("content"); strings.Contains(c, token) {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("真实执行的 stdout 没有落盘")
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestRunStallGuard(t *testing.T) {
	requireBash(t)
	tl := newTestTimeline(t)
	// 三条【不同】的失败命令：触发连续失败上限，而不是同命令守卫。
	thinker := &fakeThinker{responses: []string{
		fence("exit 7"),
		fence("echo trying\nexit 8"),
		fence("exit 9"),
	}}
	_, err := Run(context.Background(), Options{
		Timeline:    tl,
		Thinker:     thinker,
		Task:        "会失败的任务",
		StallLimit:  3,
		IdleTimeout: 2 * time.Second,
	})
	if !errors.Is(err, ErrStalled) {
		t.Fatalf("应返回 ErrStalled，得到: %v", err)
	}
	// 3 次 shell-output + 1 条 error
	badCount := 0
	errCount := 0
	steps, _ := tl.Steps()
	for _, s := range steps {
		switch s.Type {
		case "shell-output":
			badCount++
		case "error":
			errCount++
		}
	}
	if badCount != 3 || errCount != 1 {
		t.Fatalf("shell-output=%d error=%d", badCount, errCount)
	}
}

func TestRunRepeatGuardStopsSpin(t *testing.T) {
	requireBash(t)
	tl := newTestTimeline(t)
	bad := fence("exit 9")
	thinker := &fakeThinker{responses: []string{bad, bad}}
	_, err := Run(context.Background(), Options{
		Timeline:    tl,
		Thinker:     thinker,
		Task:        "原地打转的任务",
		StallLimit:  99, // 关掉连续失败上限，单独验证同命令守卫
		IdleTimeout: 2 * time.Second,
	})
	if !errors.Is(err, ErrStalled) {
		t.Fatalf("同一命令失败两次应触发 ErrStalled，得到: %v", err)
	}
}

func TestRunMaxIterations(t *testing.T) {
	requireBash(t)
	tl := newTestTimeline(t)
	loop := fence("echo no-final")
	thinker := &fakeThinker{responses: []string{loop, loop, loop}}
	_, err := Run(context.Background(), Options{
		Timeline:      tl,
		Thinker:       thinker,
		Task:          "永不完成的任务",
		MaxIterations: 2,
		IdleTimeout:   2 * time.Second,
	})
	if !errors.Is(err, ErrMaxIterations) {
		t.Fatalf("应返回 ErrMaxIterations，得到: %v", err)
	}
}

func TestRunNoFenceFallbackAndTeaching(t *testing.T) {
	requireBash(t)
	tl := newTestTimeline(t)
	thinker := &fakeThinker{responses: []string{
		"echo 我忘了写代码块", // 无 fence：整段当命令执行
		fence("FINAL=\"ok\""),
	}}
	res, err := Run(context.Background(), Options{
		Timeline:    tl,
		Thinker:     thinker,
		Task:        "无 fence 兜底",
		IdleTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Final != "ok" {
		t.Fatalf("final = %q", res.Final)
	}
	// 第二轮的 shell-output 应携带教学提示
	steps, _ := tl.Steps()
	var found bool
	for _, s := range steps {
		if s.Type == "shell-output" {
			if c, _ := s.Field("content"); strings.Contains(c, "代码块") {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("教学提示没有回灌给模型")
	}
}

func TestExtractCodeHeredocAware(t *testing.T) {
	text := "前言\n```bash\ncat <<'EOF' > out.txt\n``` 这不是 fence 结束\nEOF\necho done\n```\n后记"
	ext := extractCode(text)
	if !strings.Contains(ext.Code, "``` 这不是 fence 结束") {
		t.Fatalf("heredoc 正文里的 fence 行被误判: %q", ext.Code)
	}
	if !strings.Contains(ext.Code, "echo done") || strings.Contains(ext.Code, "前言") {
		t.Fatalf("提取范围错误: %q", ext.Code)
	}
	if ext.Notice != "" {
		t.Fatalf("单块不应有提示: %q", ext.Notice)
	}
}

func TestExtractCodeMultiBlockNotice(t *testing.T) {
	ext := extractCode("```bash\necho one\n```\n中间话\n```bash\necho two\n```")
	if ext.Code != "echo one" {
		t.Fatalf("code = %q", ext.Code)
	}
	if ext.Notice == "" {
		t.Fatal("多块应有提示")
	}
}
