package schedule

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------- 测试替身 ----------

// fakeExec 记录 exec 条目的调用；block 非 nil 时阻塞到关闭。
type fakeExec struct {
	mu    sync.Mutex
	calls []string
	block chan struct{}
}

func (f *fakeExec) run(ctx context.Context, p Parsed) ExecOutcome {
	f.mu.Lock()
	f.calls = append(f.calls, p.Item.Exec)
	f.mu.Unlock()
	if f.block != nil {
		<-f.block
	}
	return ExecOutcome{}
}

func (f *fakeExec) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// fakeSubmit 记录 at 条目的提交；failN 次失败注入。
type fakeSubmit struct {
	mu    sync.Mutex
	calls []submitCall
	failN int
}

type submitCall struct{ content, key string }

func (f *fakeSubmit) submit(ctx context.Context, content, key string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, submitCall{content, key})
	if f.failN > 0 {
		f.failN--
		return "", errors.New("注入的提交失败")
	}
	return "task:fake", nil
}

func (f *fakeSubmit) snapshot() []submitCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]submitCall(nil), f.calls...)
}

func newRuntime(t *testing.T, body string, ex *fakeExec, sub *fakeSubmit) (*Runtime, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "schedule.json")
	if body != "" {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rt := New(Options{
		Path:      path,
		Dir:       dir,
		LogPath:   filepath.Join(dir, "run", "schedule.log"),
		StatePath: filepath.Join(dir, "run", "schedule-state.json"),
		Submit:    sub.submit,
		Execute:   ex.run,
	})
	return rt, dir
}

func waitCond(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("等待条件超时")
}

var t0 = time.Date(2026, 9, 27, 10, 0, 0, 0, time.Local)

// ---------- Parse ----------

func TestParseEveryExec(t *testing.T) {
	parsed, warns, err := Parse([]byte(`{"items":[{"id":"activity-sample","every":"2m","exec":"pwsh -File a.ps1"}]}`))
	if err != nil || len(warns) != 0 {
		t.Fatalf("err=%v warns=%v", err, warns)
	}
	if len(parsed) != 1 {
		t.Fatalf("条数 = %d", len(parsed))
	}
	p := parsed[0]
	if p.Kind != KindExec || p.Every != 2*time.Minute || !p.Enabled {
		t.Fatalf("解析结果 = %+v", p)
	}
	if p.Timeout != DefaultTimeout {
		t.Fatalf("默认 timeout = %v", p.Timeout)
	}
}

func TestParseAtTask(t *testing.T) {
	parsed, warns, err := Parse([]byte(`{"items":[{"id":"daily-report","at":"21:00","task":"做晚报 {{date}}"}]}`))
	if err != nil || len(warns) != 0 || len(parsed) != 1 {
		t.Fatalf("err=%v warns=%v n=%d", err, warns, len(parsed))
	}
	p := parsed[0]
	if p.Kind != KindTask || p.AtMinute != 21*60 || !p.Enabled {
		t.Fatalf("解析结果 = %+v", p)
	}
}

func TestParsePatrolTask(t *testing.T) {
	parsed, warns, err := Parse([]byte(`{"items":[{"id":"ci","every":"30m","task":"检查 CI"}]}`))
	if err != nil || len(warns) != 0 || len(parsed) != 1 {
		t.Fatalf("err=%v warns=%v n=%d", err, warns, len(parsed))
	}
	p := parsed[0]
	if p.Kind != KindPatrol || p.Every != 30*time.Minute || !p.Enabled {
		t.Fatalf("解析结果 = %+v", p)
	}
}

func TestParseQuiet(t *testing.T) {
	// at+task 的跨午夜窗口正常解析。
	parsed, warns, err := Parse([]byte(`{"items":[{"id":"n","at":"23:30","task":"x","quiet":"23:00-08:00"}]}`))
	if err != nil || len(warns) != 0 || len(parsed) != 1 {
		t.Fatalf("err=%v warns=%v n=%d", err, warns, len(parsed))
	}
	p := parsed[0]
	if !p.HasQuiet || p.QuietStart != 23*60 || p.QuietEnd != 8*60 {
		t.Fatalf("quiet 解析 = %+v", p)
	}
	// 巡检条目同样可以配 quiet。
	parsed, warns, err = Parse([]byte(`{"items":[{"id":"c","every":"30m","task":"x","quiet":"23:00-08:00"}]}`))
	if err != nil || len(warns) != 0 || !parsed[0].HasQuiet {
		t.Fatalf("巡检 quiet: err=%v warns=%v", err, warns)
	}
	// exec 配 quiet：警告并忽略字段，条目本身保留。
	parsed, warns, err = Parse([]byte(`{"items":[{"id":"e","every":"2m","exec":"x","quiet":"23:00-08:00"}]}`))
	if err != nil || len(parsed) != 1 || parsed[0].Kind != KindExec || parsed[0].HasQuiet {
		t.Fatalf("exec quiet: err=%v parsed=%+v", err, parsed)
	}
	if len(warns) != 1 {
		t.Fatalf("exec quiet 应有一条警告: %v", warns)
	}
	// quiet 格式坏或两端相等：警告并忽略。
	for _, bad := range []string{"25:00-08:00", "23:00-23:00", "9:00-10:00", "2300-0800"} {
		_, warns, err = Parse([]byte(`{"items":[{"id":"n","at":"21:00","task":"x","quiet":"` + bad + `"}]}`))
		if err != nil || len(warns) != 1 || parsed[0].HasQuiet {
			t.Fatalf("quiet %q 应警告忽略: warns=%v", bad, warns)
		}
	}
}

func TestParseInvalidItems(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"at 配 exec", `{"items":[{"id":"a","at":"21:00","exec":"x"}]}`},
		{"缺触发器", `{"items":[{"id":"a","exec":"x"}]}`},
		{"双触发器", `{"items":[{"id":"a","every":"2m","at":"21:00","exec":"x"}]}`},
		{"缺动作", `{"items":[{"id":"a","every":"2m"}]}`},
		{"非法 at", `{"items":[{"id":"a","at":"25:00","task":"x"}]}`},
		{"at 非两位", `{"items":[{"id":"a","at":"9:00","task":"x"}]}`},
		{"非法 every", `{"items":[{"id":"a","every":"0s","exec":"x"}]}`},
		{"every 过短", `{"items":[{"id":"a","every":"10ms","exec":"x"}]}`},
		{"巡检 every 过短", `{"items":[{"id":"a","every":"10ms","task":"x"}]}`},
		{"非法 id", `{"items":[{"id":"a b","every":"2m","exec":"x"}]}`},
		{"缺 id", `{"items":[{"id":"","every":"2m","exec":"x"}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parsed, warns, err := Parse([]byte(tc.body))
			if err != nil {
				t.Fatalf("意外文件级错误: %v", err)
			}
			if len(parsed) != 0 || len(warns) != 1 {
				t.Fatalf("parsed=%d warns=%v", len(parsed), warns)
			}
		})
	}
}

func TestParseDuplicateID(t *testing.T) {
	parsed, warns, err := Parse([]byte(`{"items":[
		{"id":"a","every":"2m","exec":"x"},
		{"id":"a","every":"3m","exec":"y"}]}`))
	if err != nil || len(parsed) != 1 || len(warns) != 1 {
		t.Fatalf("err=%v parsed=%d warns=%v", err, len(parsed), warns)
	}
}

func TestParseDisabled(t *testing.T) {
	parsed, warns, err := Parse([]byte(`{"items":[{"id":"a","enabled":false,"every":"2m","exec":"x"}]}`))
	if err != nil || len(warns) != 0 || len(parsed) != 1 || parsed[0].Enabled {
		t.Fatalf("err=%v warns=%v parsed=%+v", err, warns, parsed)
	}
}

func TestParseFileLevelError(t *testing.T) {
	if _, _, err := Parse([]byte("not json")); err == nil {
		t.Fatal("坏 JSON 应返回文件级错误")
	}
}

// ---------- exec 条目 ----------

func TestExecFiresImmediatelyThenAdvances(t *testing.T) {
	ex, sub := &fakeExec{}, &fakeSubmit{}
	rt, _ := newRuntime(t, `{"items":[{"id":"s","every":"2m","exec":"echo hi"}]}`, ex, sub)
	ctx := context.Background()

	rt.Recover(ctx, t0) // 装载：exec 条目 next = now
	rt.Tick(ctx, t0)    // 首拍立即触发
	waitCond(t, func() bool { return ex.count() == 1 })

	rt.Tick(ctx, t0.Add(time.Minute)) // 周期内不触发
	time.Sleep(30 * time.Millisecond)
	if ex.count() != 1 {
		t.Fatalf("周期内不应触发，calls=%d", ex.count())
	}

	rt.Tick(ctx, t0.Add(2*time.Minute)) // 到点触发
	waitCond(t, func() bool { return ex.count() == 2 })
}

func TestExecNoOverlap(t *testing.T) {
	ex, sub := &fakeExec{block: make(chan struct{})}, &fakeSubmit{}
	rt, _ := newRuntime(t, `{"items":[{"id":"s","every":"1s","exec":"echo hi"}]}`, ex, sub)
	ctx := context.Background()

	rt.Recover(ctx, t0)
	rt.Tick(ctx, t0)
	waitCond(t, func() bool { return ex.count() == 1 })

	rt.Tick(ctx, t0.Add(1*time.Second)) // 上一次仍在跑：跳过
	time.Sleep(30 * time.Millisecond)
	if ex.count() != 1 {
		t.Fatalf("重叠时不应触发，calls=%d", ex.count())
	}

	close(ex.block)
	waitCond(t, func() bool { return rt.runningCount() == 0 })
	rt.Tick(ctx, t0.Add(2*time.Second)) // 上一次已结束：恢复触发
	waitCond(t, func() bool { return ex.count() == 2 })
}

func TestExecDisabledSkipped(t *testing.T) {
	ex, sub := &fakeExec{}, &fakeSubmit{}
	rt, _ := newRuntime(t, `{"items":[{"id":"s","enabled":false,"every":"1s","exec":"echo hi"}]}`, ex, sub)
	ctx := context.Background()
	rt.Recover(ctx, t0)
	rt.Tick(ctx, t0)
	rt.Tick(ctx, t0.Add(time.Minute))
	time.Sleep(30 * time.Millisecond)
	if ex.count() != 0 {
		t.Fatalf("禁用条目不应触发，calls=%d", ex.count())
	}
}

// ---------- at 条目 ----------

func TestAtFiresOnceInWindow(t *testing.T) {
	ex, sub := &fakeExec{}, &fakeSubmit{}
	rt, _ := newRuntime(t, `{"items":[{"id":"daily","at":"21:00","task":"晚报 {{date}}"}]}`, ex, sub)
	ctx := context.Background()

	// 10:00 启动：回看窗口（48h）内的 9/25、9/26 两次错过被补提交
	// ——补提交靠幂等键去重，真实任务层会吸收已提交的重复。
	rt.Recover(ctx, t0)
	calls := sub.snapshot()
	if len(calls) != 2 || calls[0].key != "sched-daily-2026-09-25-2100" || calls[1].key != "sched-daily-2026-09-26-2100" {
		t.Fatalf("启动补提交不符: %v", calls)
	}

	rt.Tick(ctx, t0.Add(11*time.Hour)) // 恰在 21:00
	calls = sub.snapshot()
	if len(calls) != 3 {
		t.Fatalf("到点应提交一次: %v", calls)
	}
	last := calls[2]
	// 内容 = 模板渲染 + at 条目的完成回执注入行（Phase 2）。
	if last.key != "sched-daily-2026-09-27-2100" || !strings.HasPrefix(last.content, "晚报 2026-09-27\n\n（本任务由 schedule 定时触发") {
		t.Fatalf("提交内容 = %+v", last)
	}

	rt.Tick(ctx, t0.Add(11*time.Hour+5*time.Minute)) // 窗口已过：不重复
	time.Sleep(30 * time.Millisecond)
	if len(sub.snapshot()) != 3 {
		t.Fatalf("同日不应重复提交: %v", sub.snapshot())
	}

	rt.Tick(ctx, t0.Add(35*time.Hour)) // 次日 21:00
	calls = sub.snapshot()
	if len(calls) != 4 || calls[3].key != "sched-daily-2026-09-28-2100" {
		t.Fatalf("次日应再次提交: %v", calls)
	}
}

func TestRecoverSubmitsMissedAt(t *testing.T) {
	ex, sub := &fakeExec{}, &fakeSubmit{}
	rt, _ := newRuntime(t, `{"items":[{"id":"daily","at":"21:00","task":"x"}]}`, ex, sub)
	ctx := context.Background()

	// 21:30 启动：回看窗口内的 9/26、9/27 两次错过被补提交（9/25
	// 的在窗口起点之前，保持错过）。
	rt.Recover(ctx, t0.Add(11*time.Hour+30*time.Minute))
	calls := sub.snapshot()
	if len(calls) != 2 || calls[0].key != "sched-daily-2026-09-26-2100" || calls[1].key != "sched-daily-2026-09-27-2100" {
		t.Fatalf("补提交不符: %v", calls)
	}
	// 补提交后正常 Tick 不再重复
	rt.Tick(ctx, t0.Add(11*time.Hour+31*time.Minute))
	time.Sleep(30 * time.Millisecond)
	if len(sub.snapshot()) != 2 {
		t.Fatalf("补提交后不应重复: %v", sub.snapshot())
	}
}

func TestRecoverLookbackBounded(t *testing.T) {
	ex, sub := &fakeExec{}, &fakeSubmit{}
	rt, _ := newRuntime(t, `{"items":[{"id":"daily","at":"21:00","task":"x"}]}`, ex, sub)
	// 长期停机（10 天）后启动：最多补回看窗口内的最近两天——补提交
	// 不是重放历史。
	rt.Recover(context.Background(), t0.Add(10*24*time.Hour))
	calls := sub.snapshot()
	if len(calls) != 2 ||
		calls[0].key != "sched-daily-2026-10-05-2100" || calls[1].key != "sched-daily-2026-10-06-2100" {
		t.Fatalf("回看窗口应只补最近两天: %v", calls)
	}
}

func TestSubmitFailureRetries(t *testing.T) {
	ex, sub := &fakeExec{}, &fakeSubmit{failN: 1}
	rt, _ := newRuntime(t, `{"items":[{"id":"daily","at":"21:00","task":"x"}]}`, ex, sub)
	ctx := context.Background()

	// 21:30 启动：补提交两次，第一次（9/26）注入失败。
	rt.Recover(ctx, t0.Add(11*time.Hour+30*time.Minute))
	if len(sub.snapshot()) != 2 {
		t.Fatalf("启动应尝试两次补提交: %v", sub.snapshot())
	}
	rt.Tick(ctx, t0.Add(11*time.Hour+31*time.Minute)) // 失败键同日重试成功
	calls := sub.snapshot()
	if len(calls) != 3 {
		t.Fatalf("失败后应重试: %v", calls)
	}
	rt.Tick(ctx, t0.Add(11*time.Hour+32*time.Minute)) // 成功后不再提交
	time.Sleep(30 * time.Millisecond)
	if len(sub.snapshot()) != 3 {
		t.Fatalf("成功后不应再提交: %v", sub.snapshot())
	}
}

// ---------- 热加载 ----------

func TestReloadByMtime(t *testing.T) {
	ex, sub := &fakeExec{}, &fakeSubmit{}
	rt, dir := newRuntime(t, `{"items":[{"id":"s","every":"2m","exec":"echo v1"}]}`, ex, sub)
	path := filepath.Join(dir, "schedule.json")
	ctx := context.Background()

	rt.Recover(ctx, t0)
	rt.Tick(ctx, t0)
	waitCond(t, func() bool { return ex.count() == 1 })

	// 改为禁用并推进 mtime
	if err := os.WriteFile(path, []byte(`{"items":[{"id":"s","enabled":false,"every":"2m","exec":"echo v2"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(10 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}

	rt.Tick(ctx, t0.Add(6*time.Second)) // 超过 5s 节流：触发重载
	rt.Tick(ctx, t0.Add(2*time.Minute))
	time.Sleep(30 * time.Millisecond)
	if ex.count() != 1 {
		t.Fatalf("重载后禁用条目不应触发，calls=%d", ex.count())
	}
}

func TestMissingFileIsEmptySchedule(t *testing.T) {
	ex, sub := &fakeExec{}, &fakeSubmit{}
	rt, _ := newRuntime(t, "", ex, sub) // 不写文件
	ctx := context.Background()
	rt.Recover(ctx, t0)
	rt.Tick(ctx, t0)
	time.Sleep(30 * time.Millisecond)
	if ex.count() != 0 || len(sub.snapshot()) != 0 {
		t.Fatalf("无日程文件不应有动作")
	}
}

// ---------- 纯函数 ----------

func TestDateKeyAndRender(t *testing.T) {
	d := time.Date(2026, 9, 27, 21, 0, 0, 0, time.Local)
	if got := DateKey("daily", d); got != "sched-daily-2026-09-27-2100" {
		t.Fatalf("DateKey = %s", got)
	}
	if got := RenderTask("做 {{date}} 的晚报，{{date}}", d); got != "做 2026-09-27 的晚报，2026-09-27" {
		t.Fatalf("RenderTask = %s", got)
	}
}

func TestStateFileWritten(t *testing.T) {
	ex, sub := &fakeExec{}, &fakeSubmit{}
	rt, dir := newRuntime(t, `{"items":[{"id":"s","every":"2m","exec":"echo hi"}]}`, ex, sub)
	ctx := context.Background()
	rt.Recover(ctx, t0)
	rt.Tick(ctx, t0)
	waitCond(t, func() bool { return ex.count() == 1 })
	waitCond(t, func() bool {
		data, err := os.ReadFile(filepath.Join(dir, "run", "schedule-state.json"))
		return err == nil && strings.Contains(string(data), `"s"`)
	})
}

func TestLogFileRotates(t *testing.T) {
	ex, sub := &fakeExec{}, &fakeSubmit{}
	rt, dir := newRuntime(t, "", ex, sub)
	logPath := filepath.Join(dir, "run", "schedule.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, make([]byte, maxLogBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}
	rt.appendLog(t0, "旋转测试")
	if _, err := os.Stat(logPath + ".1"); err != nil {
		t.Fatalf("超限日志应轮转为 .1: %v", err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil || !strings.Contains(string(data), "旋转测试") {
		t.Fatalf("新日志文件应含新行: %v %s", err, data)
	}
}

// ---------- 手动触发（CLI schedule run）----------

func TestManualRunHelpers(t *testing.T) {
	ex, sub := &fakeExec{}, &fakeSubmit{}
	body := `{"items":[{"id":"s","every":"10m","exec":"echo v1"},{"id":"daily","at":"21:00","task":"x {{date}}"}]}`
	rt, _ := newRuntime(t, body, ex, sub)
	parsed, warns, err := Parse([]byte(body))
	if err != nil || len(warns) != 0 || len(parsed) != 2 {
		t.Fatalf("err=%v warns=%v n=%d", err, warns, len(parsed))
	}
	ctx := context.Background()

	// ExecuteNow 同步执行；不参与节拍（running 不置位）。
	out := rt.ExecuteNow(ctx, parsed[0])
	if out.Err != nil || ex.count() != 1 || rt.runningCount() != 0 {
		t.Fatalf("ExecuteNow: out=%+v calls=%d running=%d", out, ex.count(), rt.runningCount())
	}

	// SubmitNow 用与到点触发相同的幂等键与渲染结果。
	note, err := rt.SubmitNow(ctx, parsed[1], t0)
	if err != nil {
		t.Fatal(err)
	}
	calls := sub.snapshot()
	if len(calls) != 1 || calls[0].key != "sched-daily-2026-09-27-2100" || !strings.HasPrefix(calls[0].content, "x 2026-09-27") || note == "" {
		t.Fatalf("SubmitNow: %+v note=%s", calls, note)
	}
}

// ---------- quiet 顺延（effectiveFires 纯函数） ----------

// TestEffectiveFiresQuietPostponesAcrossMidnight 钉死跨午夜顺延的
// 验收案例：at=23:30 + quiet=23:00-08:00，9/27 23:30 的有效触发时刻
// 是 9/28 08:00，幂等键用前日计划时刻。
func TestEffectiveFiresQuietPostponesAcrossMidnight(t *testing.T) {
	p := Parsed{Kind: KindTask, AtMinute: 23*60 + 30, HasQuiet: true, QuietStart: 23 * 60, QuietEnd: 8 * 60}
	lastSeen := time.Date(2026, 9, 27, 23, 0, 0, 0, time.Local)
	now := time.Date(2026, 9, 28, 8, 0, 1, 0, time.Local)

	fires := effectiveFires(p, lastSeen, now)
	if len(fires) != 1 {
		t.Fatalf("应恰好一次有效触发: %+v", fires)
	}
	if got := fires[0].Planned.Format("2006-01-02 15:04"); got != "2026-09-27 23:30" {
		t.Fatalf("计划时刻 = %s", got)
	}
	if got := fires[0].At.Format("2006-01-02 15:04"); got != "2026-09-28 08:00" {
		t.Fatalf("有效时刻 = %s", got)
	}
	if got := DateKey("nightly", fires[0].Planned); got != "sched-nightly-2026-09-27-2330" {
		t.Fatalf("幂等键 = %s（应为前日计划时刻）", got)
	}
	// 顺延未到点：不触发。
	if fires := effectiveFires(p, lastSeen, time.Date(2026, 9, 28, 7, 59, 0, 0, time.Local)); len(fires) != 0 {
		t.Fatalf("窗口终点前不应触发: %+v", fires)
	}
	// 触发过后不重复。
	if fires := effectiveFires(p, now, now.Add(time.Minute)); len(fires) != 0 {
		t.Fatalf("已越过的不应重复: %+v", fires)
	}
}

// TestEffectiveFiresNoQuietDegenerates 无 quiet 时退化为既有语义：
// 当天 at 点越过即触发，计划与有效时刻相同。
func TestEffectiveFiresNoQuietDegenerates(t *testing.T) {
	p := Parsed{Kind: KindTask, AtMinute: 21 * 60}
	lastSeen := time.Date(2026, 9, 27, 20, 59, 0, 0, time.Local)
	now := time.Date(2026, 9, 27, 21, 1, 0, 0, time.Local)
	fires := effectiveFires(p, lastSeen, now)
	if len(fires) != 1 || !fires[0].Planned.Equal(fires[0].At) {
		t.Fatalf("无 quiet 应原时刻触发: %+v", fires)
	}
	// lastSeen 为零（未 Recover 先 Tick）：不触发（补提交是 Recover 的职责）。
	if fires := effectiveFires(p, time.Time{}, now); len(fires) != 0 {
		t.Fatalf("零 lastSeen 不应触发: %+v", fires)
	}
}

// TestQuietInWindowShift 窗口判定与顺延的边界：窗口内顺延到终点、
// 窗口外原样、跨午夜的两个部分各自正确。
func TestQuietInWindowShift(t *testing.T) {
	p := Parsed{Kind: KindTask, HasQuiet: true, QuietStart: 23 * 60, QuietEnd: 8 * 60}
	cases := []struct {
		at   string
		want string
	}{
		{"2026-09-27 23:30", "2026-09-28 08:00"}, // 晚间部分 → 明天终点
		{"2026-09-28 03:00", "2026-09-28 08:00"}, // 早间部分 → 今天终点
		{"2026-09-27 22:59", "2026-09-27 22:59"}, // 窗口前原样
		{"2026-09-28 08:00", "2026-09-28 08:00"}, // 终点即出窗（左闭右开）
	}
	for _, tc := range cases {
		tm, _ := time.ParseInLocation("2006-01-02 15:04", tc.at, time.Local)
		if got := shiftOutOfQuiet(tm, p).Format("2006-01-02 15:04"); got != tc.want {
			t.Fatalf("shift(%s) = %s，应 %s", tc.at, got, tc.want)
		}
	}
}

// ---------- 巡检（every+task） ----------

func TestPatrolSubmits(t *testing.T) {
	ex, sub := &fakeExec{}, &fakeSubmit{}
	rt, _ := newRuntime(t, `{"items":[{"id":"ci","every":"1m","task":"检查 {{date}}"}]}`, ex, sub)
	ctx := context.Background()

	rt.Recover(ctx, t0)
	// 网格注册即开始：首拍提交（与 exec 首拍立即触发同机制）。
	rt.Tick(ctx, t0)
	calls := sub.snapshot()
	if len(calls) != 1 || calls[0].key != DateKey("ci", t0) {
		t.Fatalf("首拍提交不符: %v", calls)
	}
	rt.Tick(ctx, t0.Add(time.Minute)) // 下一个网格点
	calls = sub.snapshot()
	if len(calls) != 2 || calls[1].key != DateKey("ci", t0.Add(time.Minute)) {
		t.Fatalf("巡检提交不符: %v（应键=计划网格点）", calls)
	}
	if calls[1].content != "检查 2026-09-27" {
		t.Fatalf("巡检内容 = %s", calls[1].content)
	}
}

func TestPatrolQuietSkipsInsideWindow(t *testing.T) {
	ex, sub := &fakeExec{}, &fakeSubmit{}
	body := `{"items":[{"id":"ci","every":"1m","task":"x","quiet":"09:00-11:00"}]}`
	rt, _ := newRuntime(t, body, ex, sub)
	ctx := context.Background()
	// t0 = 10:00 在窗口内：网格点蒸发；11:00 后第一个网格点照常提交。
	rt.Recover(ctx, t0)
	rt.Tick(ctx, t0) // 注册
	rt.Tick(ctx, t0.Add(time.Minute))
	time.Sleep(30 * time.Millisecond)
	if len(sub.snapshot()) != 0 {
		t.Fatalf("窗口内不应提交: %v", sub.snapshot())
	}
	rt.Tick(ctx, t0.Add(2*time.Hour)) // 12:00：网格点跳到窗口外
	rt.Tick(ctx, t0.Add(2*time.Hour+time.Minute))
	calls := sub.snapshot()
	if len(calls) != 1 {
		t.Fatalf("窗口后应提交一次: %v", calls)
	}
	if !strings.HasPrefix(calls[0].key, "sched-ci-2026-09-27-12") {
		t.Fatalf("窗口外的键应来自窗口外网格点: %s", calls[0].key)
	}
}

// ---------- exec 失败告警 ----------

type fakeAlert struct {
	mu    sync.Mutex
	calls []alertCall
}

type alertCall struct{ kind, entryID, content string }

func (f *fakeAlert) record(kind, entryID, content string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, alertCall{kind, entryID, content})
}

func (f *fakeAlert) snapshot() []alertCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]alertCall(nil), f.calls...)
}

func TestExecFailureAlerts(t *testing.T) {
	sub := &fakeSubmit{}
	alerts := &fakeAlert{}
	dir := t.TempDir()
	path := filepath.Join(dir, "schedule.json")
	if err := os.WriteFile(path, []byte(`{"items":[{"id":"s","every":"1m","exec":"false"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	rt := New(Options{
		Path:    path,
		Dir:     dir,
		Submit:  sub.submit,
		Execute: func(ctx context.Context, p Parsed) ExecOutcome { return ExecOutcome{Err: errors.New("boom")} },
		Alert:   alerts.record,
	})
	ctx := context.Background()
	rt.Recover(ctx, t0)
	rt.Tick(ctx, t0)
	waitCond(t, func() bool { return len(alerts.snapshot()) == 1 })
	call := alerts.snapshot()[0]
	if call.kind != "exec-failure" || call.entryID != "s" || !strings.Contains(call.content, "执行失败") {
		t.Fatalf("告警不符: %+v", call)
	}
}

// ---------- {{report_to}} 路由与完成回执注入（Phase 2） ----------

// TestReportToRenderAndInjection 钉死 Phase 2 的两个语义：模板的
// {{report_to}} 渲染为配置的外发地址（缺省 operator）；at+task 的
// 完成回执注入行指向同一地址，巡检条目不注入（无事不报的纪律
// 优先）。
func TestReportToRenderAndInjection(t *testing.T) {
	body := `{"items":[` +
		`{"id":"daily","at":"21:00","task":"写晚报 {{report_to}} {{date}}"},` +
		`{"id":"ci","every":"30m","task":"巡检 {{report_to}}"}]}`
	sub := &fakeSubmit{}
	dir := t.TempDir()
	path := filepath.Join(dir, "schedule.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	rt := New(Options{
		Path: path, Dir: dir, ReportTo: "wecom:HongYan",
		Submit: sub.submit, Execute: func(ctx context.Context, p Parsed) ExecOutcome { return ExecOutcome{} },
	})
	ctx := context.Background()
	// Recover：daily 条目 48h 回看补提交 9/25、9/26 两次；ci 巡检不
	// 回补。Tick：ci 注册即首拍提交。
	rt.Recover(ctx, t0)
	rt.Tick(ctx, t0)
	time.Sleep(30 * time.Millisecond)
	calls := sub.snapshot()
	if len(calls) != 3 {
		t.Fatalf("应提交 3 条（2 补提交 + 1 巡检首拍）: %v", calls)
	}
	for _, c := range calls {
		switch {
		case strings.HasPrefix(c.content, "写晚报"):
			if !strings.Contains(c.content, "wecom:HongYan 2026-09-27") {
				t.Fatalf("{{report_to}} 未按配置渲染: %q", c.content)
			}
			if !strings.Contains(c.content, "完成后向 wecom:HongYan 发一条") {
				t.Fatalf("at 条目应注入完成回执: %q", c.content)
			}
		case strings.HasPrefix(c.content, "巡检"):
			if !strings.Contains(c.content, "wecom:HongYan") || strings.Contains(c.content, "完成回执") {
				t.Fatalf("巡检应渲染地址但不注入回执: %q", c.content)
			}
		default:
			t.Fatalf("意外提交: %q", c.content)
		}
	}

	// 缺省 ReportTo：渲染与注入都落到 operator。
	sub2 := &fakeSubmit{}
	rt2 := New(Options{Path: path, Dir: dir, Submit: sub2.submit})
	rt2.Recover(ctx, t0)
	rt2.Tick(ctx, t0)
	time.Sleep(30 * time.Millisecond)
	for _, c := range sub2.snapshot() {
		if strings.HasPrefix(c.content, "写晚报") {
			if strings.Contains(c.content, "wecom:HongYan") || !strings.Contains(c.content, "向 operator 发一条") {
				t.Fatalf("缺省应渲染 operator: %q", c.content)
			}
		}
	}
}

// ---------- 星期触发器（days，Phase 3 周报调度前提） ----------

// TestWeeklyDaysTrigger 钉死星期限定语义：days=mon 的条目只在周一
// 触发，周二的 at 点直接跳过；下次触发展示跳到下一个周一。
func TestWeeklyDaysTrigger(t *testing.T) {
	// 2026-09-27 是周日；9/28 周一、9/29 周二。
	body := `{"items":[{"id":"weekly","at":"09:00","days":"mon","task":"出周报"}]}`
	sub := &fakeSubmit{}
	dir := t.TempDir()
	path := filepath.Join(dir, "schedule.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	rt := New(Options{Path: path, Dir: dir, Submit: sub.submit})
	ctx := context.Background()
	sunday := time.Date(2026, 9, 27, 8, 0, 0, 0, time.Local)

	// 周日启动：48h 回看窗口（周五 08:00 起）内没有允许星期（周六/
	// 周日均非 mon）→ 零补提交；非允许日的 at 点直接跳过。
	rt.Recover(ctx, sunday)
	if calls := sub.snapshot(); len(calls) != 0 {
		t.Fatalf("窗口内无允许星期应零补提交: %v", calls)
	}

	// 周二 09:00 越过：非允许日不触发。
	rt.Tick(ctx, sunday.Add(24*time.Hour+9*time.Hour)) // 周一 17:00（周一 09:00 已过）
	time.Sleep(30 * time.Millisecond)
	calls := sub.snapshot()
	if len(calls) != 1 || calls[0].key != "sched-weekly-2026-09-28-0900" {
		t.Fatalf("周一 09:00 应触发: %v", calls)
	}
	rt.Tick(ctx, sunday.Add(48*time.Hour+9*time.Hour)) // 周二 17:00
	time.Sleep(30 * time.Millisecond)
	if len(sub.snapshot()) != 1 {
		t.Fatalf("周二不应触发: %v", sub.snapshot())
	}

	// 下次触发展示：从周二看应跳到下周一 10/5 09:00。
	parsed, _, err := Load(path)
	if err != nil || len(parsed) != 1 {
		t.Fatal(err)
	}
	next := NextText(parsed[0], ItemState{}, time.Date(2026, 9, 29, 10, 0, 0, 0, time.Local))
	if !strings.HasPrefix(next, "2026-10-05 09:00:00") {
		t.Fatalf("下次触发应跳到下周一 10/5: %q", next)
	}
}
