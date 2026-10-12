package mind

// UI 披露段的契约测试（scene.md §5 + BaiLongma「description 即说明
// 书」的移植）：系统提示必须自带 ui set 的动词语义与身份名——模型
// 醒来即知自己的界面表达通道；manifest 段尾必须带动词教学句（每轮
// 复习怎么更新/怎么撤/背景不是触发器）。

import (
	"context"
	"strings"
	"testing"

	"github.com/lomehong/mindloop/internal/llm"
)

func TestSystemPromptIncludesUISection(t *testing.T) {
	tl := newTestTimeline(t)
	m := NewMonolith(MonolithOptions{Timeline: tl, SelfName: "ada",
		Thinker: taskModelFunc(func(context.Context, string, []llm.Message) (string, error) {
			return "", context.Canceled
		}),
	}).(*monolith)
	sp := m.systemPrompt("q", Wake{})
	for _, want := range []string{
		"## UI surfaces",
		`ui set ada`, // 身份名由装配注入，模型不必猜
		"one idempotent verb",
		"--intent ambient|inform|confront",
		"never go silent merely because something is already on screen",
	} {
		if !strings.Contains(sp, want) {
			t.Fatalf("系统提示缺 UI 披露段要素 %q", want)
		}
	}
}

func TestSceneManifestTeachesVerbs(t *testing.T) {
	st := NewSceneStore()
	if _, err := st.Upsert(Surface{ID: "w1", Kind: "text", Summary: "北京 18° 晴"}); err != nil {
		t.Fatal(err)
	}
	manifest := st.Manifest()
	for _, want := range []string{"背景状态，不是触发器", "对同一 id 再调 ui set", "ui set <id> --remove"} {
		if !strings.Contains(manifest, want) {
			t.Fatalf("manifest 缺动词教学句要素 %q: %s", want, manifest)
		}
	}
}
