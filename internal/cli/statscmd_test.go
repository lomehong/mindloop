package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mindloop/internal/identity"
	"mindloop/internal/traj"
)

// writeLedger 往身份目录写一份台账 fixture：watchdog 唤醒 2 轮
// （1 轮 30 秒后跟随 run = 干活，1 轮从不跟随 = 空转）+ chat 1 次。
func writeLedger(t *testing.T, home string, minsAgo []float64, idleIdx int) {
	t.Helper()
	id, err := identity.Create(context.Background(), "ada")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(id.Dir, "usage"), 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	var b strings.Builder
	for i, m := range minsAgo {
		ts := now.Add(-time.Duration(m * float64(time.Minute))).Format(traj.TimeFormat)
		switch {
		case i == idleIdx:
			fmt.Fprintf(&b, `{"ts":%q,"model":"glm-5","wake":"watchdog","phase":"wake","prompt_tokens":100,"completion_tokens":10}`+"\n", ts)
		default:
			// 其余行按序：唤醒、run 章、chat 各就各位的简化 fixture。
			fmt.Fprintf(&b, `{"ts":%q,"model":"glm-5","prompt_tokens":10,"completion_tokens":2}`+"\n", ts)
		}
	}
	writeHomeFile(t, home, filepath.Join("identities", "ada", "usage", "llm-usage.jsonl"), b.String())
}

// TestStatsIdentity：单身份统计——分账、空转率、无任务提示都到位。
func TestStatsIdentity(t *testing.T) {
	newTestHome(t)
	// 唤醒（-10min，从不跟随 = 空转）、唤醒（-5min）后 30 秒有 run
	//（= 干活）、chat 一次。
	writeLedger(t, os.Getenv("MINDLOOP_HOME"), []float64{10, 4.5, 4, 2}, 0)

	code, out, errOut := runCLI(t, "stats", "--identity", "ada")
	if code != 0 {
		t.Fatalf("exit = %d\nout=%s\nerr=%s", code, out, errOut)
	}
	for _, want := range []string{
		"== ada ==",
		"按唤醒", "按阶段", "按模型",
		"watchdog 唤醒 1 轮", "1 轮未跟随运行", "台账级代理",
		"（无任务记录）",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("统计缺 %q:\n%s", want, out)
		}
	}
}

// TestStatsAggregate：不带 --identity 时聚合全部身份；多身份给
// 合计块，单身份不给（合计与单块重复）。
func TestStatsAggregate(t *testing.T) {
	home := newTestHome(t)
	writeLedger(t, home, []float64{10, 2}, 0)

	code, out, errOut := runCLI(t, "stats")
	if code != 0 {
		t.Fatalf("exit = %d\nout=%s\nerr=%s", code, out, errOut)
	}
	if !strings.Contains(out, "== ada ==") {
		t.Fatalf("聚合视图缺身份块:\n%s", out)
	}
	if strings.Contains(out, "合计") {
		t.Fatalf("单身份不应有合计块:\n%s", out)
	}

	// 第二个身份（无台账也占一块）触发合计。
	if _, err := identity.Create(context.Background(), "bob"); err != nil {
		t.Fatal(err)
	}
	code, out, errOut = runCLI(t, "stats")
	if code != 0 {
		t.Fatalf("exit = %d\nout=%s\nerr=%s", code, out, errOut)
	}
	if !strings.Contains(out, "合计") {
		t.Fatalf("多身份应有合计块:\n%s", out)
	}
}

// TestStatsEmpty：零身份时给引导而不是空输出。
func TestStatsEmpty(t *testing.T) {
	newTestHome(t)
	code, out, _ := runCLI(t, "stats")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(out, "mindloop init") {
		t.Fatalf("应引导 init:\n%s", out)
	}
}
