package mind

// responder 对话面的界面卡片通路（responder_scene.go）：围栏块剥除、
// 宿主校验落卡、remove、坏块剥除并附提示、密度与幂等。

import (
	"context"
	"strings"
	"testing"

	"github.com/lomehong/mindloop/internal/traj"
)

func TestResponderSceneBlockPlacesCardAndStripsFence(t *testing.T) {
	r, tl := newResponder(t)
	r.opts.Thinker = &cannedThinker{reply: "北京今天晴，25°。\n```scene\n{\"id\":\"weather-bj\",\"kind\":\"text\",\"intent\":\"inform\",\"summary\":\"北京 18° 晴\",\"data\":{\"title\":\"天气\",\"body\":\"晴，25°\"}}\n```"}

	trig := inboundMessage(t, tl, "operator", "在仪表盘发一张天气卡")
	out := r.Wake(context.Background(), Wake{Step: trig, Kind: WakeStep})
	if !strings.Contains(out.Note, "已回复") {
		t.Fatalf("outcome = %+v", out)
	}

	steps, err := tl.Steps()
	if err != nil {
		t.Fatal(err)
	}
	var card, reply *traj.Step
	for i := range steps {
		switch steps[i].Type {
		case traj.TypeScene:
			card = &steps[i]
		case traj.TypeMessage:
			if from, _ := steps[i].Field("from"); from == "ada" {
				reply = &steps[i]
			}
		}
	}
	if card == nil {
		t.Fatal("围栏块应落 scene upsert 步骤")
	}
	if id, _ := card.Field("id"); id != "weather-bj" {
		t.Fatalf("卡片 id=%q", id)
	}
	if data, _ := card.Field("data"); !strings.Contains(data, "25°") {
		t.Fatalf("卡片 data 不符: %s", data)
	}
	if reply == nil {
		t.Fatal("回复缺失")
	}
	content, _ := reply.Field("content")
	if strings.Contains(content, "```scene") {
		t.Fatalf("给用户的文本不应含围栏块: %s", content)
	}
	if !strings.Contains(content, "北京今天晴") {
		t.Fatalf("文本回复应独立成立: %s", content)
	}
}

func TestResponderSceneRemoveAndBadBlock(t *testing.T) {
	r, tl := newResponder(t)
	// 预置一张卡（上一回合发的）。
	if err := AppendSceneStep(tl, map[string]any{"op": "upsert", "id": "c1", "kind": "text", "summary": "旧卡"}); err != nil {
		t.Fatal(err)
	}
	r.opts.Thinker = &cannedThinker{reply: "收起旧卡。\n```scene\n{\"op\":\"remove\",\"id\":\"c1\"}\n```\n```scene\n{\"id\":\"bad\",\"kind\":\"iframe\"}\n```"}

	trig := inboundMessage(t, tl, "operator", "把那张卡撤了")
	_ = r.Wake(context.Background(), Wake{Step: trig, Kind: WakeStep})

	steps, _ := tl.Steps()
	removes, badKind := 0, false
	for _, s := range steps {
		if s.Type != traj.TypeScene {
			continue
		}
		if op, _ := s.Field("op"); op == "remove" {
			if id, _ := s.Field("id"); id == "c1" {
				removes++
			}
		}
		if k, _ := s.Field("kind"); k == "iframe" {
			badKind = true
		}
	}
	if removes != 1 {
		t.Fatalf("remove 应恰好落一歩, got %d", removes)
	}
	if badKind {
		t.Fatal("词汇表外的 kind 应被拒（不落步骤）")
	}
	// 回复含坏块提示，但不含围栏本身。
	var content string
	for _, s := range steps {
		if s.Type == traj.TypeMessage {
			if from, _ := s.Field("from"); from == "ada" {
				content, _ = s.Field("content")
			}
		}
	}
	if strings.Contains(content, "```scene") || !strings.Contains(content, "未生效") {
		t.Fatalf("回复应剥围栏并提示坏块: %s", content)
	}
}

func TestResponderSceneIdempotentNoStep(t *testing.T) {
	r, tl := newResponder(t)
	block := "```scene\n{\"id\":\"same\",\"kind\":\"text\",\"summary\":\"同一张\"}\n```"
	r.opts.Thinker = &cannedThinker{reply: "第一句。\n" + block}
	trig := inboundMessage(t, tl, "operator", "发卡")
	_ = r.Wake(context.Background(), Wake{Step: trig, Kind: WakeStep})

	r.opts.Thinker = &cannedThinker{reply: "第二句（内容相同）。\n" + block}
	trig2 := inboundMessage(t, tl, "operator", "再发一次")
	_ = r.Wake(context.Background(), Wake{Step: trig2, Kind: WakeStep})

	steps, _ := tl.Steps()
	n := 0
	for _, s := range steps {
		if s.Type == traj.TypeScene {
			if id, _ := s.Field("id"); id == "same" {
				n++
			}
		}
	}
	if n != 1 {
		t.Fatalf("同内容重复发卡应判重不落步骤, got %d", n)
	}
}

func TestResponderSceneSingleLineFence(t *testing.T) {
	// 真模型实证：围栏可能写成单行（```scene {json}```，空格而非换行）——
	// 正则必须两种形态都接住。
	r, tl := newResponder(t)
	r.opts.Thinker = &cannedThinker{reply: "天气卡已放上仪表盘。```scene {\"id\":\"weather-demo\",\"kind\":\"text\",\"intent\":\"inform\",\"summary\":\"示例天气卡\",\"data\":{\"title\":\"天气\",\"body\":\"晴\"}}```"}

	trig := inboundMessage(t, tl, "operator", "在仪表盘发一张天气卡")
	_ = r.Wake(context.Background(), Wake{Step: trig, Kind: WakeStep})

	steps, _ := tl.Steps()
	placed := false
	var content string
	for i := range steps {
		if steps[i].Type == traj.TypeScene {
			if id, _ := steps[i].Field("id"); id == "weather-demo" {
				placed = true
			}
		}
		if steps[i].Type == traj.TypeMessage {
			if from, _ := steps[i].Field("from"); from == "ada" {
				content, _ = steps[i].Field("content")
			}
		}
	}
	if !placed {
		t.Fatal("单行围栏应被识别并落卡")
	}
	if strings.Contains(content, "```scene") {
		t.Fatalf("单行围栏应从文本中剥除: %s", content)
	}
}

func TestResponderSceneUnclosedFence(t *testing.T) {
	// 真模型实证 #2：围栏可能忘了闭合——开栏即意图，块体到文本末尾。
	r, tl := newResponder(t)
	r.opts.Thinker = &cannedThinker{reply: "卡片放上去了。\n```scene\n{\"id\":\"unclosed\",\"kind\":\"text\",\"summary\":\"示例\",\"data\":{\"body\":\"晴\"}}"}

	trig := inboundMessage(t, tl, "operator", "发卡")
	_ = r.Wake(context.Background(), Wake{Step: trig, Kind: WakeStep})

	steps, _ := tl.Steps()
	placed, content := false, ""
	for i := range steps {
		if steps[i].Type == traj.TypeScene {
			if id, _ := steps[i].Field("id"); id == "unclosed" {
				placed = true
			}
		}
		if steps[i].Type == traj.TypeMessage {
			if from, _ := steps[i].Field("from"); from == "ada" {
				content, _ = steps[i].Field("content")
			}
		}
	}
	if !placed {
		t.Fatal("未闭合围栏应被识别并落卡")
	}
	if strings.Contains(content, "```scene") || strings.Contains(content, "unclosed") {
		t.Fatalf("未闭合围栏应整体剥除: %s", content)
	}
}
