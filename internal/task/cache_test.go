package task

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/lomehong/mindloop/internal/traj"
)

// TestProjectionCacheMatchesFullReplay：缓存投影与无缓存全量重放
// 必须逐字段一致。操作序列覆盖 submit/claim/advance/cancel/retry/
// recover 全部写入面；每步之后用"另一个全新 Store"（无缓存，全量
// 重放）做基准，断言带缓存的 Store 看到完全相同的事实。
func TestProjectionCacheMatchesFullReplay(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	fresh := func() *Store { return New(s.tl, s.identity) }

	submit(t, s, "k1")
	submit(t, s, "k2")
	submit(t, s, "k3")
	if _, err := s.Claim(ctx, "run-1"); err != nil {
		t.Fatal(err)
	}
	first := mustFirstID(t, s)
	if _, err := s.Advance(ctx, first, Event{Attempt: 1, RunID: "run-1", State: AwaitingApproval}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Advance(ctx, first, Event{Attempt: 1, RunID: "run-1", State: Running}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Advance(ctx, first, Event{Attempt: 1, RunID: "run-1", State: Succeeded, Result: "完成", ResultKind: "model-final", EvidenceStepIDs: []string{"s1"}, Deliverables: []string{"a.txt"}}); err != nil {
		t.Fatal(err)
	}
	// k2 走失败重试线（Succeeded 不可 retry——状态机钉死的语义）。
	if _, err := s.Claim(ctx, "run-2"); err != nil {
		t.Fatal(err)
	}
	second := mustNthID(t, s, 1)
	if _, err := s.Advance(ctx, second, Event{Attempt: 1, RunID: "run-2", State: Failed, Result: "中途失败"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Retry(ctx, second, 1, "operator", "r1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Retry(ctx, second, 1, "operator", "r1"); err != nil {
		t.Fatal(err) // 幂等重放：同键同请求必须成功返回
	}
	if _, err := s.Claim(ctx, "run-3"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Cancel(ctx, second, 2, "operator", "c1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Advance(ctx, second, Event{Attempt: 2, RunID: "run-3", State: Canceled, Reason: "执行已退出"}); err != nil {
		t.Fatal(err)
	}

	cached, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := fresh().List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(cached) != len(baseline) {
		t.Fatalf("任务数不一致：缓存 %d vs 重放 %d", len(cached), len(baseline))
	}
	for i := range cached {
		if !reflect.DeepEqual(cached[i], baseline[i]) {
			cj, _ := json.Marshal(cached[i])
			bj, _ := json.Marshal(baseline[i])
			t.Fatalf("任务 %d 不一致：\n缓存: %s\n重放: %s", i, cj, bj)
		}
	}
	// 单点 Get 也一致（含 Events 深拷贝路径）。
	for _, task := range baseline {
		got, err := s.Get(ctx, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, task) {
			t.Fatalf("Get(%s) 与重放不一致: %+v vs %+v", task.ID, got, task)
		}
	}
}

func mustFirstID(t *testing.T, s *Store) string {
	return mustNthID(t, s, 0)
}

func mustNthID(t *testing.T, s *Store, n int) string {
	t.Helper()
	all, err := s.List(context.Background())
	if err != nil || len(all) <= n {
		t.Fatalf("List: %v %d", err, len(all))
	}
	return all[n].ID
}

// TestProjectionCacheRebuildsOnRewrite：同长度原地重写（fileID 与
// 大小都不变）靠 mtime 检测——缓存必须重建，不得返回旧内容。
func TestProjectionCacheRebuildsOnRewrite(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	submit(t, s, "k1")
	// 预热缓存。
	if _, err := s.List(ctx); err != nil {
		t.Fatal(err)
	}
	// 原位重写：等长的一行替换内容字段（同 fileID、同 size、
	// mtime 前进）。
	raw, err := os.ReadFile(s.tl.Path)
	if err != nil {
		t.Fatal(err)
	}
	lines := splitLines(raw)
	if len(lines) < 2 {
		t.Fatalf("行数不足: %d", len(lines))
	}
	// 原位重写：等长替换 content 字段值（同 fileID、同 size、
	// mtime 前进）——两个 8 汉字串 UTF-8 等长。
	old := []byte(`"content":"完整的任务要求"`)
	new := []byte(`"content":"重写后的任务书"`)
	if len(old) != len(new) {
		t.Fatalf("替换串必须等长: %d vs %d", len(old), len(new))
	}
	lines[1] = bytes.Replace(lines[1], old, new, 1)
	if bytes.Equal(lines[1], new) || !bytes.Contains(lines[1], new) {
		t.Fatalf("原行里没有预期的 content 字段: %s", lines[1])
	}
	if err := os.WriteFile(s.tl.Path, joinLines(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	all, err := s.List(ctx)
	if err != nil || len(all) != 1 {
		t.Fatalf("List: %v %d", err, len(all))
	}
	if all[0].Content != "重写后的任务书" {
		t.Fatalf("缓存返回了重写前的旧内容: %q", all[0].Content)
	}
}

// TestProjectionCacheTruncationRebuilds：文件收缩（consumed 越过
// 末尾）必须触发全量重建。
func TestProjectionCacheTruncationRebuilds(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	submit(t, s, "k1")
	submit(t, s, "k2")
	if _, err := s.List(ctx); err != nil {
		t.Fatal(err)
	}
	// 截断掉第二个任务（保留 trajectory 头 + 第一行）。
	raw, err := os.ReadFile(s.tl.Path)
	if err != nil {
		t.Fatal(err)
	}
	lines := splitLines(raw)
	if len(lines) < 3 {
		t.Fatalf("行数不足: %d", len(lines))
	}
	if err := os.WriteFile(s.tl.Path, joinLines(lines[:2]), 0o644); err != nil {
		t.Fatal(err)
	}
	all, err := s.List(ctx)
	if err != nil || len(all) != 1 {
		t.Fatalf("截断后 List: %v %d", err, len(all))
	}
}

// TestProjectionCacheTornTailKeepsRejecting：撕裂尾在缓存存在时
// 必须与全量重放同样永久拒绝，且缓存不推进——修复半行后还能继续。
func TestProjectionCacheTornTailKeepsRejecting(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	submit(t, s, "k1")
	if _, err := s.List(ctx); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(s.tl.Path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"type":"task-event"`); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, err := s.List(ctx); !isCorrupt(err) {
		t.Fatalf("撕裂尾应 ErrCorrupt: %v", err)
	}
	if _, err := s.List(ctx); !isCorrupt(err) {
		t.Fatalf("撕裂尾必须持续拒绝: %v", err)
	}
	// 修复：补全该行（删除后追加合法内容）。
	raw, err := os.ReadFile(s.tl.Path)
	if err != nil {
		t.Fatal(err)
	}
	lines := splitLines(raw)
	if err := os.WriteFile(s.tl.Path, joinLines(lines[:len(lines)-1]), 0o644); err != nil {
		t.Fatal(err)
	}
	all, err := s.List(ctx)
	if err != nil || len(all) != 1 {
		t.Fatalf("修复后 List: %v %d", err, len(all))
	}
}

// TestProjectionCacheConcurrentReadersAndWriter：List（无锁读）与
// transaction（持锁写）并发，-race 下缓存无数据竞争、结果无丢失。
func TestProjectionCacheConcurrentReadersAndWriter(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	for i := 0; i < 5; i++ {
		submit(t, s, fmtKey(i))
	}
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				all, err := s.List(ctx)
				if err != nil {
					t.Errorf("并发 List: %v", err)
					return
				}
				// 写入方持续把 5 涨到 25：任何瞬时值都在区间内
				// 才正常；越界 = 丢任务或读到未提交的幻影。
				if len(all) < 5 || len(all) > 25 {
					t.Errorf("任务数越界: %d", len(all))
					return
				}
			}
		}()
	}
	for i := 5; i < 25; i++ {
		if _, err := s.Submit(ctx, Submission{From: "operator", ClientMessageID: fmtKey(i), Content: "要求"}); err != nil {
			t.Errorf("并发写期间 Submit: %v", err)
		}
	}
	close(stop)
	wg.Wait()
	all, err := s.List(ctx)
	if err != nil || len(all) != 25 {
		t.Fatalf("最终 List: %v %d", err, len(all))
	}
}

// TestProjectionCachePerformance：缓存的目标场景——大量任务 + 高频
// 只读查询。断言带缓存 Store 的 N 次 List 明显快于每次全量重放
// （比例断言用宽松下界，避免机器差异导致 flaky：缓存至少快 3 倍）。
func TestProjectionCachePerformance(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	const n = 300
	for i := 0; i < n; i++ {
		if _, err := s.Submit(ctx, Submission{From: "operator", ClientMessageID: fmtKey(i), Content: "要求"}); err != nil {
			t.Fatal(err)
		}
	}
	// 基准：每次新建 Store（全量重放）。
	start := time.Now()
	for i := 0; i < 50; i++ {
		if _, err := New(s.tl, s.identity).List(ctx); err != nil {
			t.Fatal(err)
		}
	}
	full := time.Since(start)
	// 缓存路径：同一 Store 预热一次后连续 List。
	if _, err := s.List(ctx); err != nil {
		t.Fatal(err)
	}
	start = time.Now()
	for i := 0; i < 50; i++ {
		if _, err := s.List(ctx); err != nil {
			t.Fatal(err)
		}
	}
	cached := time.Since(start)
	t.Logf("300 任务 × 50 次 List：全量重放 %v vs 缓存 %v（%.1f×）",
		full, cached, float64(full)/float64(cached+1))
	if cached > full/3 {
		t.Fatalf("缓存未达到 3× 加速：全量 %v 缓存 %v", full, cached)
	}
}

func fmtKey(i int) string {
	return "key-" + string(rune('a'+i%26)) + string(rune('0'+i/26))
}

func isCorrupt(err error) bool {
	for err != nil {
		if err.Error() == "task: 轨迹事实损坏，拒绝推断或追加任务状态" {
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func splitLines(raw []byte) [][]byte {
	var out [][]byte
	start := 0
	for i, b := range raw {
		if b == '\n' {
			out = append(out, raw[start:i])
			start = i + 1
		}
	}
	if start < len(raw) {
		out = append(out, raw[start:])
	}
	return out
}

func joinLines(lines [][]byte) []byte {
	var out []byte
	for _, l := range lines {
		out = append(out, l...)
		out = append(out, '\n')
	}
	return out
}

var _ = traj.TypeMessage
