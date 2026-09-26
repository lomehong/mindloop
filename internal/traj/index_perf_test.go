package traj

// d5 基准与场景：为增量读取路径建立 1 万 / 10 万 / 100 万步骤的
// 成本基线。三组基准（-bench 运行）：冷构建、稳态尾窗查询、增量
// 追加；另有两项确定性约束在常规测试中守卫（不依赖耗时，任何
// 机器同结果）：
//
//   - 稳态：追平到文件末尾后，重复查询读取 0 新字节、不触发重建；
//   - 增量：追加一行后 offset 恰好前进该行的字节数。
//
// 完整数值报告（P50/P95、分配、读取字节数）由 MINDLOOP_PERF_REPORT=1
// 门控的 TestIndexPerfReport 打印——耗时只记录、不断言阈值。

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// perfSizes 是计划约定的三档基准规模（步骤数，不含头行）。
var perfSizes = []int{10_000, 100_000, 1_000_000}

// perfQueryDepth 是稳态查询的尾窗深度，与 web chat 端点的典型 tail 一致。
const perfQueryDepth = 50

// perfFiles 按规模生成并缓存轨迹文件与预热索引：进程内复用，
// 测试/基准结束后随临时目录整体删除。
type perfFiles struct {
	dir   string
	tls   map[int]*Timeline
	bytes map[int]int64
	ixs   map[int]*Index
}

func newPerfFiles(tb testing.TB) *perfFiles {
	tb.Helper()
	dir, err := os.MkdirTemp("", "mindloop-perf-")
	if err != nil {
		tb.Fatalf("创建临时目录: %v", err)
	}
	tb.Cleanup(func() { os.RemoveAll(dir) })
	return &perfFiles{
		dir:   dir,
		tls:   map[int]*Timeline{},
		bytes: map[int]int64{},
		ixs:   map[int]*Index{},
	}
}

// get 返回 n 步（另加 1 条头行）的轨迹：首次生成，之后复用同一文件。
func (pf *perfFiles) get(tb testing.TB, n int) (*Timeline, int64) {
	tb.Helper()
	if tl, ok := pf.tls[n]; ok {
		return tl, pf.bytes[n]
	}
	dir := filepath.Join(pf.dir, fmt.Sprintf("n%d", n))
	tl := &Timeline{ID: "tid00000000", Slug: "perf", Dir: dir, Path: filepath.Join(dir, "trajectory.jsonl")}
	if err := os.MkdirAll(tl.Dir, 0o755); err != nil {
		tb.Fatalf("创建轨迹目录: %v", err)
	}
	size := writePerfJournal(tb, tl, n)
	pf.tls[n] = tl
	pf.bytes[n] = size
	return tl, size
}

// warmIndex 返回 n 步轨迹的预热索引（已追平并做过一次尾窗查询）：
// 稳态与增量基准复用同一实例，避免每次校准都付冷构建成本。
func (pf *perfFiles) warmIndex(tb testing.TB, n int) *Index {
	tb.Helper()
	if ix, ok := pf.ixs[n]; ok {
		return ix
	}
	tl, _ := pf.get(tb, n)
	ix, err := OpenIndex(tl)
	if err != nil {
		tb.Fatalf("OpenIndex: %v", err)
	}
	if _, err := ix.ChatView(perfQueryDepth, "", "ada"); err != nil {
		tb.Fatalf("预热 ChatView: %v", err)
	}
	pf.ixs[n] = ix
	return ix
}

// writePerfJournal 生成 1 条头行 + n 条交织的 message/action 行
// （每 4 条含一条对早前消息的回复，让 outcomes 有真实内容），返回
// 文件字节数。行宽刻意精简，1M 步骤的文件规模落在百 MB 量级。
func writePerfJournal(tb testing.TB, tl *Timeline, n int) int64 {
	tb.Helper()
	f, err := os.Create(tl.Path)
	if err != nil {
		tb.Fatalf("创建 journal: %v", err)
	}
	w := bufio.NewWriterSize(f, 1<<20)
	ts := [4]string{tsA, tsB, tsC, tsD}
	fmt.Fprintf(w, `{"type":"trajectory","step_id":"tid00000000","ts":%q,"slug":"perf"}`+"\n", tsA)
	for i := 0; i < n; i++ {
		switch i % 4 {
		case 0:
			fmt.Fprintf(w, `{"type":"message","step_id":"msg%08d","ts":%q,"from":"operator","to":"ada","content":"request %d"}`+"\n", i, ts[i%4], i)
		case 1:
			fmt.Fprintf(w, `{"type":"action","step_id":"act%08d","ts":%q,"launched_by":"monolith","content":"work %d"}`+"\n", i, ts[i%4], i)
		case 2:
			fmt.Fprintf(w, `{"type":"message","step_id":"rep%08d","ts":%q,"from":"ada","to":"operator","content":"ack %d","reply_to":"msg%08d"}`+"\n", i, ts[i%4], i, i-2)
		default:
			fmt.Fprintf(w, `{"type":"action","step_id":"act%08d","ts":%q,"launched_by":"fast","content":"follow %d"}`+"\n", i, ts[i%4], i)
		}
	}
	if err := w.Flush(); err != nil {
		f.Close()
		tb.Fatalf("flush journal: %v", err)
	}
	if err := f.Close(); err != nil {
		tb.Fatalf("close journal: %v", err)
	}
	fi, err := os.Stat(tl.Path)
	if err != nil {
		tb.Fatalf("stat journal: %v", err)
	}
	return fi.Size()
}

// measurePerf 运行 samples 组、每组 ops 次 f，返回每组平均单次耗时
// （升序，供分位取数）。分组摊薄是为绕开 Windows 计时器粒度：单次
// 微秒级耗时会被截断为 0，组内累积后再除回即恢复到微秒精度。
func measurePerf(tb testing.TB, samples, ops int, f func(i int) error) []time.Duration {
	tb.Helper()
	ds := make([]time.Duration, 0, samples)
	k := 0
	for i := 0; i < samples; i++ {
		start := time.Now()
		for j := 0; j < ops; j++ {
			if err := f(k); err != nil {
				tb.Fatalf("第 %d 次: %v", k, err)
			}
			k++
		}
		ds = append(ds, time.Since(start)/time.Duration(ops))
	}
	sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
	return ds
}

// perfP 取升序耗时切片的分位值（nearest-rank）。
func perfP(ds []time.Duration, p int) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	i := (p*len(ds)+99)/100 - 1
	if i < 0 {
		i = 0
	}
	if i >= len(ds) {
		i = len(ds) - 1
	}
	return ds[i]
}

// perfIterBudget 按规模给冷启动类测量（冷构建/缓存恢复）确定迭代数：
// 小规模多轮取分位，大规模 1-2 轮（单轮约秒级）。
func perfIterBudget(n int) int {
	switch {
	case n <= 10_000:
		return 7
	case n <= 100_000:
		return 5
	default:
		return 2
	}
}

// TestIndexSteadyQueryReadsNoNewBytes：稳态的确定性约束——追平到
// 文件末尾后，重复查询既不推进 offset（0 新字节）也不触发重建；
// 追加一行后 offset 恰好前进该行字节数；缓存恢复 0 重放。
func TestIndexSteadyQueryReadsNoNewBytes(t *testing.T) {
	tl := rawTimeline(t)
	head := journalLine(TypeTrajectory, "tid00000000", tsA, map[string]string{"slug": "t"})
	m1 := journalLine(TypeMessage, "msg00000001", tsB, map[string]string{"from": "operator", "to": "ada", "content": "hi"})
	r1 := journalLine(TypeMessage, "msg00000002", tsB, map[string]string{"from": "ada", "to": "operator", "content": "yo", "reply_to": "msg00000001"})
	a1 := journalLine(TypeAction, "act00000001", tsC, map[string]string{"launched_by": "monolith"})
	fin := journalLine(TypeFinal, "fin00000001", tsD, map[string]string{"content": "done"})
	writeJournal(t, tl, head, m1, r1, a1, fin)

	fi, err := os.Stat(tl.Path)
	if err != nil {
		t.Fatalf("stat journal: %v", err)
	}

	ix := openIndex(t, tl) // 冷构建：offset 追平到文件末尾
	if ix.cur.offset != fi.Size() {
		t.Fatalf("冷构建后 offset = %d，应为文件大小 %d", ix.cur.offset, fi.Size())
	}
	for i := 0; i < 5; i++ {
		chat, err := ix.ChatView(50, "", "ada")
		if err != nil {
			t.Fatalf("ChatView: %v", err)
		}
		if len(chat.Messages) != 2 || chat.Outcomes["msg00000001"] != "replied" {
			t.Fatalf("第 %d 轮查询结果错位: %+v", i, chat)
		}
	}
	if got, err := ix.StepCount(); err != nil || got != 5 {
		t.Fatalf("StepCount = %d, %v，应为 5", got, err)
	}
	if n, err := ix.CatchUp(); err != nil || n != 0 {
		t.Fatalf("稳态追平 = %d 步, %v，应为 0（不得重放）", n, err)
	}
	if ix.cur.offset != fi.Size() {
		t.Fatalf("稳态查询推进 offset: %d -> %d，应 0 新字节", fi.Size(), ix.cur.offset)
	}
	if ix.Rewinds() != 0 {
		t.Fatalf("稳态查询触发 %d 次重建", ix.Rewinds())
	}

	// 追加一行：下次查询只消费新增字节（offset 恰前进一行）。
	line := journalLine(TypeMessage, "msg00000009", tsD, map[string]string{"from": "operator", "to": "ada", "content": "new"}) + "\n"
	appendJournal(t, tl, line)
	chat, err := ix.ChatView(50, "", "ada")
	if err != nil {
		t.Fatalf("追加后 ChatView: %v", err)
	}
	if len(chat.Messages) != 3 || chat.Messages[2].Content != "new" {
		t.Fatalf("追加后消息 = %+v", chat.Messages)
	}
	if ix.cur.offset != fi.Size()+int64(len(line)) {
		t.Fatalf("追加后 offset = %d，应为 %d（恰一行字节）", ix.cur.offset, fi.Size()+int64(len(line)))
	}
	if n, err := ix.CatchUp(); err != nil || n != 0 {
		t.Fatalf("追加消费后再追平 = %d 步, %v，应为 0", n, err)
	}

	// 缓存恢复：追平 0 重放、offset 不重扫。
	if err := ix.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	ix2 := openIndex(t, tl)
	if ix2.cur.offset != fi.Size()+int64(len(line)) {
		t.Fatalf("缓存恢复后 offset = %d，应为 %d", ix2.cur.offset, fi.Size()+int64(len(line)))
	}
	if n, err := ix2.CatchUp(); err != nil || n != 0 {
		t.Fatalf("缓存恢复后追平 = %d 步, %v，应为 0（不重放）", n, err)
	}
	if ix2.Rewinds() != 0 {
		t.Fatalf("缓存恢复触发 %d 次重建", ix2.Rewinds())
	}
	chat2, err := ix2.ChatView(50, "", "ada")
	if err != nil {
		t.Fatalf("恢复后 ChatView: %v", err)
	}
	if len(chat2.Messages) != 3 {
		t.Fatalf("恢复后消息 = %d 条，应为 3", len(chat2.Messages))
	}
}

// BenchmarkIndexColdBuild：无缓存全量重放（1 万/10 万/100 万步骤）。
func BenchmarkIndexColdBuild(b *testing.B) {
	pf := newPerfFiles(b)
	for _, n := range perfSizes {
		b.Run(fmt.Sprintf("steps=%d", n), func(b *testing.B) {
			tl, _ := pf.get(b, n)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				ix, err := OpenIndex(tl)
				if err != nil {
					b.Fatalf("OpenIndex: %v", err)
				}
				if i == 0 {
					if cnt, err := ix.StepCount(); err != nil || cnt != n+1 {
						b.Fatalf("冷构建后 StepCount = %d, %v，应为 %d", cnt, err, n+1)
					}
				}
			}
		})
	}
}

// BenchmarkIndexSteadyChatView：同一预热实例反复做尾窗查询——稳态
// 路径（追平 0 新字节，不随规模扫描整份日志）。
func BenchmarkIndexSteadyChatView(b *testing.B) {
	pf := newPerfFiles(b)
	for _, n := range perfSizes {
		b.Run(fmt.Sprintf("steps=%d", n), func(b *testing.B) {
			ix := pf.warmIndex(b, n)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := ix.ChatView(perfQueryDepth, "", "ada"); err != nil {
					b.Fatalf("ChatView: %v", err)
				}
			}
		})
	}
}

// BenchmarkIndexIncrementalAppend：写一行 + 追平——每轮只读该行的
// 字节数（offset 恰前进一行）。
func BenchmarkIndexIncrementalAppend(b *testing.B) {
	pf := newPerfFiles(b)
	for _, n := range perfSizes {
		b.Run(fmt.Sprintf("steps=%d", n), func(b *testing.B) {
			ix := pf.warmIndex(b, n)
			tl, _ := pf.get(b, n)
			f, err := os.OpenFile(tl.Path, os.O_APPEND|os.O_WRONLY, 0o644)
			if err != nil {
				b.Fatalf("打开 journal: %v", err)
			}
			defer f.Close()
			line := journalLine(TypeMessage, "msg99999999", tsD, map[string]string{"from": "operator", "to": "ada", "content": "incremental"}) + "\n"
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := f.WriteString(line); err != nil {
					b.Fatalf("追加: %v", err)
				}
				if got, err := ix.CatchUp(); err != nil || got != 1 {
					b.Fatalf("追平 = %d, %v，应为 1", got, err)
				}
			}
		})
	}
}

// TestIndexPerfReport：三档规模的成本报告（P50/P95、分配与读取
// 字节数），默认跳过——设置 MINDLOOP_PERF_REPORT=1 运行。断言的是
// 确定性不变量（稳态 0 新字节、恢复 0 重放、追加恰前进一行），
// 耗时只记录、不设阈值（不同机器的绝对值不可比）。
func TestIndexPerfReport(t *testing.T) {
	if os.Getenv("MINDLOOP_PERF_REPORT") != "1" {
		t.Skip("设置 MINDLOOP_PERF_REPORT=1 运行性能报告")
	}
	pf := newPerfFiles(t)
	for _, n := range perfSizes {
		tl, size := pf.get(t, n)
		reportPerfSize(t, tl, n, size)
	}
}

func reportPerfSize(t *testing.T, tl *Timeline, n int, size int64) {
	t.Helper()
	t.Logf("=== steps=%d file=%.1fMiB ===", n+1, float64(size)/(1<<20))

	// 1) 冷构建：无缓存全量重放。
	var ix *Index
	coldIters := perfIterBudget(n)
	cold := measurePerf(t, coldIters, 1, func(int) error {
		var err error
		ix, err = OpenIndex(tl)
		return err
	})
	if cnt, err := ix.StepCount(); err != nil || cnt != n+1 {
		t.Fatalf("冷构建后 StepCount = %d, %v，应为 %d", cnt, err, n+1)
	}
	t.Logf("  cold-build   p50=%-10v p95=%-10v %d 轮（%.0fns/step，读入 %d 字节）",
		perfP(cold, 50), perfP(cold, 95), coldIters,
		float64(perfP(cold, 50).Nanoseconds())/float64(n+1), size)

	// 2) 稳态尾窗查询：同一实例反复读取——0 新字节、0 重建。
	if ix.cur.offset != size {
		t.Fatalf("冷构建后 offset = %d，应为文件大小 %d", ix.cur.offset, size)
	}
	steady := measurePerf(t, 50, 200, func(int) error {
		_, err := ix.ChatView(perfQueryDepth, "", "ada")
		return err
	})
	allocs := testing.AllocsPerRun(20, func() {
		if _, err := ix.ChatView(perfQueryDepth, "", "ada"); err != nil {
			t.Fatalf("ChatView: %v", err)
		}
	})
	if got, err := ix.CatchUp(); err != nil || got != 0 {
		t.Fatalf("稳态追平 = %d, %v，应为 0（不得重放）", got, err)
	}
	if ix.cur.offset != size {
		t.Fatalf("稳态查询读取了新字节：offset %d -> %d", size, ix.cur.offset)
	}
	if ix.Rewinds() != 0 {
		t.Fatalf("稳态查询触发 %d 次重建", ix.Rewinds())
	}
	t.Logf("  steady-chat  p50=%-10v p95=%-10v 50×200 次（allocs=%.1f/op，读入 0 新字节）",
		perfP(steady, 50), perfP(steady, 95), allocs)

	// 3) 缓存恢复：Save 后从缓存重建实例（恢复不重放）。
	if err := ix.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	var ix2 *Index
	warmIters := perfIterBudget(n)
	warm := measurePerf(t, warmIters, 1, func(int) error {
		var err error
		ix2, err = OpenIndex(tl)
		return err
	})
	if got, err := ix2.CatchUp(); err != nil || got != 0 {
		t.Fatalf("缓存恢复后追平 = %d, %v，应为 0（不重放）", got, err)
	}
	chat2, err := ix2.ChatView(perfQueryDepth, "", "ada")
	if err != nil {
		t.Fatalf("恢复后 ChatView: %v", err)
	}
	if len(chat2.Messages) != perfQueryDepth {
		t.Fatalf("恢复后尾窗 = %d 条，应为 %d", len(chat2.Messages), perfQueryDepth)
	}
	t.Logf("  warm-restore p50=%-10v p95=%-10v %d 轮（追平 0 重放）",
		perfP(warm, 50), perfP(warm, 95), warmIters)

	// 4) 增量追加：写一行 + 追平——读入字节数恰为该行的字节数。
	f, err := os.OpenFile(tl.Path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("打开 journal: %v", err)
	}
	defer f.Close()
	line := journalLine(TypeMessage, "msg99999999", tsD, map[string]string{"from": "operator", "to": "ada", "content": "incremental"}) + "\n"
	app := measurePerf(t, 50, 200, func(int) error {
		before := ix2.cur.offset
		if _, err := f.WriteString(line); err != nil {
			return err
		}
		got, err := ix2.CatchUp()
		if err != nil {
			return err
		}
		if got != 1 {
			return fmt.Errorf("追平消费 %d 步，应为 1", got)
		}
		if delta := ix2.cur.offset - before; delta != int64(len(line)) {
			return fmt.Errorf("offset 前进 %d 字节，应为一行 %d", delta, len(line))
		}
		return nil
	})
	if ix2.Rewinds() != 0 {
		t.Fatalf("增量追平触发 %d 次重建", ix2.Rewinds())
	}
	t.Logf("  inc-append   p50=%-10v p95=%-10v 50×200 次（%d B/op，恰为追加字节）",
		perfP(app, 50), perfP(app, 95), len(line))
}
