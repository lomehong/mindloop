package mind

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"mindloop/internal/mem"
	"mindloop/internal/traj"
)

// TestRelatedMemoriesExposeIDAndSource：responder 的相关记忆检索
// 暴露记忆 ID 与来源步骤——检索结果是线索而非已验证事实，消费方
// 需要能追溯出处；唤醒提示语也要明确这层定位。
func TestRelatedMemoriesExposeIDAndSource(t *testing.T) {
	t.Setenv("MINDLOOP_HOME", t.TempDir())
	ctx := context.Background()
	tl, err := traj.Create(ctx, "mem-related")
	if err != nil {
		t.Fatal(err)
	}
	memDir := filepath.Join(t.TempDir(), "memories")
	store := mem.Store{Dir: memDir}
	added, err := store.AddWith(ctx, "fact", "操作员住在杭州，喜欢西湖边跑步", mem.AddOpts{Source: "msg00000042"})
	if err != nil {
		t.Fatal(err)
	}

	// 时间线写一条与记忆相关的自主步骤作为检索查询文本
	//（message/task 步骤会被 AutonomousSteps 过滤，用 observation）。
	s := traj.NewStep("observation")
	s.Fields["content"] = "杭州西湖今天的天气如何"
	if err := tl.Append(ctx, s); err != nil {
		t.Fatal(err)
	}

	m := NewMonolith(MonolithOptions{
		Timeline: tl,
		MemDir:   memDir,
		Thinker:  &scriptThinker{responses: []string{fence(`FINAL="IDLE"`)}},
	}).(*monolith)
	lines := m.relatedMemories(Wake{})
	if len(lines) == 0 {
		t.Fatal("应有相关记忆命中")
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, added.Memory.ID) {
		t.Fatalf("行应暴露记忆 ID: %q", joined)
	}
	if !strings.Contains(joined, "msg00000042") || !strings.Contains(joined, "来源") {
		t.Fatalf("行应暴露来源步骤: %q", joined)
	}

	// 唤醒提示把记忆段定位为"线索"（不是已验证事实）。
	prompt := m.wakeTask("test-wake", Wake{})
	if !strings.Contains(prompt, "线索") {
		t.Fatalf("唤醒提示应标注记忆为线索而非事实: %q", prompt)
	}
}
