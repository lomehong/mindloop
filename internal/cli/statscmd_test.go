package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lomehong/mindloop/internal/identity"
	"github.com/lomehong/mindloop/internal/traj"
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

// TestStatsTasteLine：味觉证据面（舌的投影）进 stats——零信号给提示
// 行；有证据时撤销归因与漏报匹配成行（漏报带 ⚠）。这钉的是
// statscmd 的 printTaste 接线：DeriveTaste 有单测，但"stats 输出里
// 真的有一行"此前无人验证。
func TestStatsTasteLine(t *testing.T) {
	newTestHome(t)
	writeLedger(t, os.Getenv("MINDLOOP_HOME"), []float64{5}, -1)

	// 零信号（学习期常态）：一行提示而不是空白。
	code, out, errOut := runCLI(t, "stats", "--identity", "ada")
	if code != 0 {
		t.Fatalf("exit = %d\nout=%s\nerr=%s", code, out, errOut)
	}
	if !strings.Contains(out, "味觉    （窗口内无归因信号") {
		t.Fatalf("零信号应有提示行:\n%s", out)
	}

	// 证据面：S0 沉淀 + undo 归因 + operator 提及 → 阈值过紧 1、漏报 1。
	id, err := identity.Load("ada")
	if err != nil {
		t.Fatal(err)
	}
	appendStep := func(s traj.Step) {
		t.Helper()
		if err := id.Timeline.Append(context.Background(), s); err != nil {
			t.Fatal(err)
		}
	}
	ev := traj.NewStep(traj.TypeEvent)
	ev.Fields["source"] = "fs1"
	ev.Fields["subject"] = "D:/work/report-q3.xlsx"
	ev.Fields["salience"] = "s0"
	appendStep(ev)
	taste := traj.NewStep(traj.TypeTaste)
	taste.Fields["signal"] = "undo"
	taste.Fields["cause"] = "proposal-redundant"
	appendStep(taste)
	msg := traj.NewStep(traj.TypeMessage)
	msg.Fields["from"] = "operator"
	msg.Fields["to"] = "ada"
	msg.Fields["content"] = "report-q3.xlsx 改了你怎么没说？"
	appendStep(msg)

	code, out, errOut = runCLI(t, "stats", "--identity", "ada")
	if code != 0 {
		t.Fatalf("exit = %d\nout=%s\nerr=%s", code, out, errOut)
	}
	for _, want := range []string{
		"味觉    撤销 1（提案多余 1）",
		"⚠ 漏报匹配 1 次（D:/work/report-q3.xlsx）",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("味觉行缺 %q:\n%s", want, out)
		}
	}
}
