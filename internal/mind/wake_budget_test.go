package mind

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"mindloop/internal/mem"
	"mindloop/internal/recap"
	"mindloop/internal/traj"
)

// 唤醒任务的分段上限：超长人生分集被裁到 SummaryBytes（不是整个
// 唤醒失败——粗层材料缺失只让上下文变薄，不阻塞行动）。
func TestWakeTaskCapsRecapSection(t *testing.T) {
	tl := newTestTimeline(t)
	cache := recap.Cache{Path: filepath.Join(tl.Dir, "recap", "autonomous-v1.jsonl")}
	if err := cache.Append(context.Background(), recap.Episode{
		Start: 0, End: 10, Title: "大分集", Summary: strings.Repeat("回", 1000), PromptVersion: 1,
	}); err != nil {
		t.Fatal(err)
	}
	m := NewMonolith(MonolithOptions{Timeline: tl, EnableRecap: true, SummaryBytes: 200}).(*monolith)
	task := m.wakeTask("periodic check", Wake{Kind: WakeScheduled})
	if !strings.Contains(task, "人生分集") {
		t.Fatalf("唤醒任务应含人生分集段: %.80q", task)
	}
	// 原始分集约 3000 字节；裁剪到 200 后任务总长应远低于它。
	if len(task) > 200+300 {
		t.Fatalf("唤醒任务 %d 字节，分集段明显未被裁剪", len(task))
	}
}

// 相关记忆段受 MemoryBytes 约束：超长记忆被裁剪，唤醒继续。
func TestWakeTaskCapsMemorySection(t *testing.T) {
	tl := newTestTimeline(t)
	memDir := filepath.Join(t.TempDir(), "memories")
	store := mem.Store{Dir: memDir}
	if _, err := store.Add(context.Background(), "fact", strings.Repeat("记忆要点", 500)); err != nil {
		t.Fatal(err)
	}
	obs := traj.NewStep(traj.TypeObservation)
	obs.Fields["content"] = "记忆要点"
	if err := tl.Append(context.Background(), obs); err != nil {
		t.Fatal(err)
	}
	m := NewMonolith(MonolithOptions{Timeline: tl, MemDir: memDir, MemoryBytes: 100}).(*monolith)
	task := m.wakeTask("scheduled check", Wake{Kind: WakeScheduled})

	start := strings.Index(task, "相关记忆")
	if start < 0 {
		t.Fatalf("无法定位记忆段: %.200q", task)
	}
	rest := task[start:]
	end := strings.Index(rest, "\n\n持久化重要事实")
	if end <= 0 {
		t.Fatalf("无法定位记忆段结尾: %.200q", task)
	}
	// 标题（定位语，到“：”为止）不计入条目预算；条目部分应被
	// MemoryBytes(100) 裁剪。
	seg := rest[:end]
	if i := strings.Index(seg, "："); i >= 0 {
		seg = seg[i+len("："):]
	}
	if len(seg) > 100 {
		t.Fatalf("记忆段 %d 字节超出上限 100: %.120q", len(seg), seg)
	}
}
