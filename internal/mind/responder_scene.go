package mind

// responder 对话面的界面卡片通路（scene.md §3.3/§8）：卡片是呈现
// 不是执行——responder 没有执行循环（普通聊天不是执行授权的红线
// 不破），这是它唯一的界面表达通道，宿主中介：校验 kind 词汇表、
// data/summary 上限、SceneStore 判重与密度上限，落 scene 步骤可审计。
// 协议形态：回复里嵌 ```scene 围栏块（JSON），发送前剥除——用户
// 看到的是纯文本回复 + 屏幕上的卡，不是代码。

import (
	"encoding/json"
	"fmt"
	"strings"
)

// sceneReplyKinds 是对话面允许的 kind 词汇表（scene.md §4 v1 子集）。
var sceneReplyKinds = map[string]bool{"text": true, "progress": true, "choice": true}

// sceneReplyDataMax / sceneReplySummaryMax 与 uicmd 的写入口同一保护
// （data ≤16KB、summary ≤80 字节）。
const (
	sceneReplyDataMax    = 16 * 1024
	sceneReplySummaryMax = 80
)

// sceneReplyRequest 是围栏块的载荷（scene 步骤字段的对话面子集）。
type sceneReplyRequest struct {
	Op      string          `json:"op"`
	ID      string          `json:"id"`
	Kind    string          `json:"kind"`
	Intent  string          `json:"intent"`
	Focus   bool            `json:"focus"`
	Summary string          `json:"summary"`
	Data    json.RawMessage `json:"data"`
}

// splitSceneBlocks 把回复拆成（纯文本, 围栏块体列表）。围栏允许
// 不闭合——模型偶发收尾遗漏（真实生成实证），开栏即意图，块体延伸
// 到下一个围栏或文本末尾。
func splitSceneBlocks(reply string) (string, []string) {
	var b strings.Builder
	var bodies []string
	for {
		i := strings.Index(reply, "```scene")
		if i < 0 {
			b.WriteString(reply)
			break
		}
		b.WriteString(reply[:i])
		rest := reply[i+len("```scene"):]
		if j := strings.Index(rest, "```"); j >= 0 {
			bodies = append(bodies, strings.TrimSpace(rest[:j]))
			reply = rest[j+len("```"):]
		} else {
			bodies = append(bodies, strings.TrimSpace(rest))
			reply = ""
		}
	}
	return strings.TrimSpace(b.String()), bodies
}

// applySceneBlocks 从回复中剥出 scene 围栏块，逐块校验后落 scene
// 步骤（SceneStore 判重/密度与 ui set 同一契约）。返回（剥除后的
// 纯文本, 成功落卡数, 被拒块数）。被拒不回改文本——坏块直接消失，
// 计数交由调用方决定是否附提示。
func (r *Responder) applySceneBlocks(reply string) (string, int, int) {
	text, bodies := splitSceneBlocks(reply)
	if len(bodies) == 0 {
		return reply, 0, 0
	}

	store := NewSceneStore()
	if steps, err := r.opts.Timeline.Steps(); err == nil {
		store.Replay(steps)
	}

	placed, bad := 0, 0
	for _, body := range bodies {
		var req sceneReplyRequest
		if json.Unmarshal([]byte(body), &req) != nil || strings.TrimSpace(req.ID) == "" {
			bad++
			continue
		}
		id := strings.TrimSpace(req.ID)
		if req.Op == "remove" {
			if !store.Remove(id) {
				bad++
				continue
			}
			if err := AppendSceneStep(r.opts.Timeline, map[string]any{"op": "remove", "id": id}); err != nil {
				bad++
				continue
			}
			placed++
			continue
		}
		if !sceneReplyKinds[req.Kind] {
			bad++
			continue
		}
		summary := strings.TrimSpace(req.Summary)
		if len([]rune(summary)) > sceneReplySummaryMax {
			summary = string([]rune(summary)[:sceneReplySummaryMax])
		}
		if len(req.Data) > sceneReplyDataMax {
			bad++
			continue
		}
		surface := Surface{
			ID:      id,
			Kind:    req.Kind,
			Intent:  req.Intent,
			Focus:   req.Focus,
			Summary: summary,
			Data:    json.RawMessage(req.Data),
		}
		changed, err := store.Upsert(surface)
		if err != nil {
			bad++ // 密度超限等：与 ui set 同一拒绝语义
			continue
		}
		if !changed {
			continue // 幂等无变化：不落步骤
		}
		fields := map[string]any{
			"op": "upsert", "id": id, "kind": req.Kind,
			"intent": req.Intent, "summary": summary,
			"data": string(req.Data),
		}
		if req.Focus {
			fields["focus"] = "true"
		}
		if err := AppendSceneStep(r.opts.Timeline, fields); err != nil {
			bad++
			continue
		}
		placed++
	}
	return text, placed, bad
}

// sceneBlockNote 是被拒块的附言（调用方拼在回复尾部）。
func sceneBlockNote(placed, bad int) string {
	if bad == 0 {
		return ""
	}
	return fmt.Sprintf("\n（有 %d 条界面卡片指令未生效——检查 kind 词汇表与 JSON 格式）", bad)
}
