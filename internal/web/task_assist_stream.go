package web

// POST /api/identities/{name}/task-assist/stream —— AI 起草的流式形态。
// 起草是分钟级推理：文本边生成边上屏（阶段性输出），操作员看得见
// 进度，不是对着一个转圈干等。与同步端点（task_assist.go）共用校验、
// 提示词与档位解析；校验失败在 SSE 头之前以普通 HTTP 错误返回，
// 前端拿得到状态码。开始后按 SSE 推送：
//
//	event: delta  data: {"text":"…"}    —— 增量（UTF-8 安全分段）
//	event: done   data: {"task":"全文"} —— 完整终稿
//	event: error  data: {"message":"…"} —— 失败（含中途失败）

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/lomehong/mindloop/internal/identity"
	"github.com/lomehong/mindloop/internal/llm"
)

func (s *Server) handleTaskAssistStream(w http.ResponseWriter, r *http.Request, id *identity.Identity) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var body struct {
		Draft string `json:"draft"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		writeError(w, 400, "请求体应为 JSON")
		return
	}
	draft := strings.TrimSpace(body.Draft)
	if draft == "" {
		writeError(w, 400, "草稿为空——先写下要它做的事，再让 AI 帮你写清楚")
		return
	}
	if runes := []rune(draft); len(runes) > taskAssistMaxDraft {
		writeError(w, 400, "草稿过长（上限 4000 字符）——AI 辅助面向意图草稿，不是全文粘贴")
		return
	}
	client, err := taskAssistClientFor(id)
	if err != nil {
		writeError(w, 409, "该身份未配置 LLM（providers.json / .env）——AI 辅助不可用，可手动写任务书")
		return
	}

	// 流式期间清掉服务器级写期限（SSE 同一先例）：连接总寿命由下面
	// 的 ctx 超时管，帧级活性由 sseWriter 的滚动期限管。
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	// 反向代理场景下禁用响应缓冲，保证逐增量到达。
	w.Header().Set("X-Accel-Buffering", "no")

	sw := newSSEWriter(w, 30*time.Second)
	writeEvent := func(event string, v any) error {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		return sw.writeFrame(sseFrame{event: event, data: string(b)})
	}

	ctx, cancel := context.WithTimeout(r.Context(), taskAssistTimeout)
	defer cancel()
	res, err := client.CompleteStream(ctx, llm.Request{
		System:   taskAssistPrompt,
		Messages: []llm.Message{{Role: "user", Content: "操作员草稿：\n" + draft}},
	}, func(delta string) {
		// 写帧失败 = 客户端已断开：停掉上游生成，不再空烧 token。
		if writeEvent("delta", map[string]string{"text": delta}) != nil {
			cancel()
		}
	})
	if err != nil {
		msg := "起草失败：" + err.Error()
		if errors.Is(err, context.DeadlineExceeded) {
			msg = "起草超时（120s）——模型较慢时可重试，或手写任务书"
		}
		_ = writeEvent("error", map[string]string{"message": msg})
		return
	}
	task := strings.TrimSpace(res.Text)
	if task == "" {
		_ = writeEvent("error", map[string]string{"message": "起草返回为空——请重试，或手写任务书"})
		return
	}
	_ = writeEvent("done", map[string]string{"task": task})
}
