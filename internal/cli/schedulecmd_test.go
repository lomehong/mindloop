package cli

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lomehong/mindloop/internal/schedule"
	"github.com/lomehong/mindloop/internal/task"
)

// TestScheduleCommandRoundtrip 覆盖 add/list/remove 的完整回路：
// 落盘、回显、同 id 替换、--json 形态、原子写内容与删除。
func TestScheduleCommandRoundtrip(t *testing.T) {
	id := taskTestIdentity(t)
	schedulePath, _, _ := schedulePaths(id)

	// 无日程时 list 给出下一步提示。
	code, out, _ := runCLI(t, "schedule", "list", "ada")
	if code != 0 || !strings.Contains(out, "没有日程") {
		t.Fatalf("空日程 list: code=%d out=%q", code, out)
	}

	// add：落盘 + 回显。
	entry := `{"id":"daily","at":"21:00","task":"写晚报 {{date}}"}`
	code, out, errText := runCLI(t, "schedule", "add", "ada", entry)
	if code != 0 {
		t.Fatalf("add exit = %d, err=%s", code, errText)
	}
	if !strings.Contains(out, "已添加日程条目 daily") || !strings.Contains(out, "每天 21:00") {
		t.Fatalf("add 回显不足: %q", out)
	}

	// list：含条目与推算的下次触发。
	code, out, _ = runCLI(t, "schedule", "list", "ada")
	if code != 0 || !strings.Contains(out, "daily") || !strings.Contains(out, "at 21:00") {
		t.Fatalf("list: code=%d out=%q", code, out)
	}
	if !strings.Contains(out, "下次") {
		t.Fatalf("list 应给出下次触发: %q", out)
	}

	// add 同 id：整条替换。
	entry2 := `{"id":"daily","at":"22:30","task":"改到 22:30 {{date}}"}`
	code, out, _ = runCLI(t, "schedule", "add", "ada", entry2)
	if code != 0 || !strings.Contains(out, "已替换日程条目 daily") {
		t.Fatalf("替换 add: code=%d out=%q", code, out)
	}
	code, out, _ = runCLI(t, "schedule", "list", "ada")
	if strings.Count(out, "daily") != 1 || !strings.Contains(out, "at 22:30") {
		t.Fatalf("替换后 list: %q", out)
	}

	// --json 形态：机器消费面。
	code, out, _ = runCLI(t, "schedule", "list", "ada", "--json")
	if code != 0 {
		t.Fatalf("list --json exit=%d", code)
	}
	var views []scheduleEntryView
	if err := json.Unmarshal([]byte(out), &views); err != nil || len(views) != 1 || views[0].ID != "daily" {
		t.Fatalf("list --json: %v %s", err, out)
	}
	if views[0].NextRun == "" {
		t.Fatalf("list --json 应有 next_run: %s", out)
	}

	// 落盘文件确实只含替换后的那一条。
	data, err := os.ReadFile(schedulePath)
	if err != nil {
		t.Fatal(err)
	}
	var file schedule.File
	if err := json.Unmarshal(data, &file); err != nil || len(file.Items) != 1 || file.Items[0].At != "22:30" {
		t.Fatalf("落盘文件: %v %s", err, data)
	}

	// remove。
	code, out, _ = runCLI(t, "schedule", "remove", "ada", "daily")
	if code != 0 || !strings.Contains(out, "已删除日程条目 daily") {
		t.Fatalf("remove: code=%d out=%q", code, out)
	}
	code, out, _ = runCLI(t, "schedule", "list", "ada")
	if code != 0 || !strings.Contains(out, "没有日程") {
		t.Fatalf("删除后 list: %q", out)
	}

	// remove 不存在的 id：exit 1。
	code, _, _ = runCLI(t, "schedule", "remove", "ada", "ghost")
	if code != 1 {
		t.Fatalf("remove 不存在 exit = %d（应 1）", code)
	}
}

// TestScheduleAddValidation 坏条目必须显式拒绝（exit 2）且不落盘。
func TestScheduleAddValidation(t *testing.T) {
	id := taskTestIdentity(t)
	schedulePath, _, _ := schedulePaths(id)

	cases := []struct {
		name  string
		entry string
		want  string
	}{
		{"巡检 every 过短", `{"id":"a","every":"10ms","task":"x"}`, "every 非法"},
		{"at 配 exec", `{"id":"a","at":"21:00","exec":"x"}`, "at 只能与 task 组合"},
		{"非法时刻", `{"id":"a","at":"9:00","task":"x"}`, "at 非法"},
		{"非法 id", `{"id":"a b","at":"21:00","task":"x"}`, "id 非法"},
		{"坏 JSON", `{not json`, "解析失败"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, _, errText := runCLI(t, "schedule", "add", "ada", tc.entry)
			if code != 2 {
				t.Fatalf("exit = %d（应 2），stderr=%q", code, errText)
			}
			if !strings.Contains(errText, tc.want) {
				t.Fatalf("错误信息 = %q（应含 %q）", errText, tc.want)
			}
		})
	}
	// 全部被拒：文件不应产生。
	if _, err := os.Stat(schedulePath); !os.IsNotExist(err) {
		t.Fatalf("非法条目不应落盘: %v", err)
	}
}

// TestScheduleRunTask 手动触发 task 条目：提交今天的任务、幂等键与
// 到点触发一致、同一天重复 run 返回既有任务。
func TestScheduleRunTask(t *testing.T) {
	id := taskTestIdentity(t)
	entry := `{"id":"daily","at":"21:00","task":"写晚报 {{date}}"}`
	if code, _, errText := runCLI(t, "schedule", "add", "ada", entry); code != 0 {
		t.Fatalf("add: code=%d err=%s", code, errText)
	}
	code, out, errText := runCLI(t, "schedule", "run", "ada", "daily")
	if code != 0 {
		t.Fatalf("run: code=%d err=%s", code, errText)
	}
	today := time.Now().Format("2006-01-02")
	key := "sched-daily-" + today + "-2100"
	if !strings.Contains(out, key) {
		t.Fatalf("run 输出应含幂等键 %s: %q", key, out)
	}

	// 幂等：再次 run 不产生第二个任务。
	code, _, _ = runCLI(t, "schedule", "run", "ada", "daily")
	if code != 0 {
		t.Fatalf("二次 run exit = %d", code)
	}
	store := task.New(id.Timeline, id.Name)
	items, err := store.List(context.Background())
	if err != nil || len(items) != 1 {
		t.Fatalf("任务应恰好一条: err=%v n=%d", err, len(items))
	}
	if items[0].From != schedule.SourceName || items[0].ClientMessageID != key {
		t.Fatalf("任务来源不符: %+v", items[0])
	}
	// 内容 = 模板渲染 + at 条目的完成回执注入行（Phase 2）。
	if !strings.HasPrefix(items[0].Content, "写晚报 "+today+"\n\n（本任务由 schedule 定时触发") {
		t.Fatalf("任务内容未渲染: %q", items[0].Content)
	}

	// run 不存在的条目：exit 1。
	code, _, _ = runCLI(t, "schedule", "run", "ada", "ghost")
	if code != 1 {
		t.Fatalf("run 不存在 exit = %d（应 1）", code)
	}
}
