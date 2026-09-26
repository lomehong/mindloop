package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"mindloop/internal/identity"
	"mindloop/internal/mind"
)

// chatMessage 是 viewer ChatMessage 契约的 Go 形态。
type chatMessage struct {
	TS        string  `json:"ts"`
	StepID    string  `json:"step_id"`
	From      string  `json:"from"`
	To        string  `json:"to"`
	Content   string  `json:"content"`
	ReplyTo   *string `json:"reply_to"`
	Filename  *string `json:"filename"`
	SourceURL *string `json:"source_url"`
}

// handleChat 渲染对话视图——ChatLog 契约：
// {identity:{id,name}, live, messages, outcomes}。
//   - messages 只含 message 步骤，按日志序；状态收据（source=reply-status）
//     是元事实，不进对话流；
//   - tail 参数截取最近 N 条（默认 200）；
//   - with 参数只保留与某人的对话（对方发来的 + 我回给对方的），
//     语义与 responder.history 一致；
//   - outcomes[step_id]：返回窗口内入站消息的最终结局——存在真回复
//     → "replied"；responder 落盘的收据投影 "no-reply"/"failed"；两者
//     皆无留空 = 前端的 undecided 态（诚实而非编造）。replied 强于
//     收据：重试后真回复到达时以回复为准。窗口外的消息不返回结局
//     （消费方只渲染返回消息；全量结局随日志线性膨胀，属尾窗读取
//     的明确取舍）。
//
// 数据来自共享侧索引（ChatView 内部先追平到已观测末尾）。
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request, id *identity.Identity) {
	tail := 200
	if v := r.URL.Query().Get("tail"); v != "" {
		if n, perr := toInt(v); perr == nil && n > 0 {
			tail = n
		}
	}
	with := r.URL.Query().Get("with")

	ix, err := s.indexes.get(id.Timeline)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	data, err := ix.ChatView(tail, with, id.Name)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	msgs := make([]chatMessage, 0, len(data.Messages))
	for _, m := range data.Messages {
		cm := chatMessage{
			TS:      m.TS,
			StepID:  m.StepID,
			From:    m.From,
			To:      m.To,
			Content: m.Content,
		}
		if m.ReplyTo != "" {
			rt := m.ReplyTo
			cm.ReplyTo = &rt
		}
		if m.Filename != "" {
			fn := m.Filename
			cm.Filename = &fn
		}
		if m.SourceURL != "" {
			su := m.SourceURL
			cm.SourceURL = &su
		}
		msgs = append(msgs, cm)
	}
	writeJSON(w, 200, map[string]any{
		"identity": map[string]string{"id": id.Name, "name": id.Name},
		"live":     isIdentityLive(id.Timeline.Dir),
		"messages": msgs,
		"outcomes": data.Outcomes,
	})
}

// handleChatSend 接收人类消息——POST /api/identities/{name}/chat
// {content, from_name, client_message_id?} → {ok, from, to, step_id,
// client_message_id?}。消息是日志事实：落盘即投递，运行中的调度器
// feeder 下个心跳（≤200ms）拾取并路由给 responder。
// 心智未运行时消息同样落盘（talk 页的“睡眠中，消息会等它醒来”），
// 并因盖过协议章进入重启后的恢复窗口（非追答旧历史）。
//
// 带 client_message_id 时按 (from, cid) 幂等：同键同载荷重发返回原
// step_id（前端据此对账，响应丢失后可安全重发）；同键不同载荷是
// 409——客户端载荷不一致必须可见。
func (s *Server) handleChatSend(w http.ResponseWriter, r *http.Request, id *identity.Identity) {
	var req struct {
		Content         string `json:"content"`
		FromName        string `json:"from_name"`
		ClientMessageID string `json:"client_message_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, 400, "请求体必须是 {content, from_name, client_message_id?} JSON")
		return
	}
	if strings.TrimSpace(req.Content) == "" {
		writeError(w, 400, "消息内容不能为空")
		return
	}
	from := strings.TrimSpace(req.FromName)
	if from == "" {
		from = "operator"
	}
	// PostMessageOnce 是消息字段形态的权威定义，并盖协议章——客户端
	// 断开不中止已确认的落盘（消息一旦送达就必须等它醒来）。
	step, err := mind.PostMessageOnce(id.Timeline, from, id.Name, "chat", req.Content, strings.TrimSpace(req.ClientMessageID))
	if errors.Is(err, mind.ErrMessageConflict) {
		writeError(w, 409, "client_message_id 已用于不同内容的消息")
		return
	}
	if err != nil {
		writeError(w, 500, "消息落盘失败: "+err.Error())
		return
	}
	resp := map[string]any{
		"ok":      true,
		"from":    from,
		"to":      id.Name,
		"step_id": step.StepID,
	}
	if cid := strings.TrimSpace(req.ClientMessageID); cid != "" {
		resp["client_message_id"] = cid
	}
	writeJSON(w, 200, resp)
}

// handleMemories 列出身份的记忆库——viewer Memories 视图。
//
// GET /api/identities/{name}/memories?type=fact&q=keyword
func (s *Server) handleMemories(w http.ResponseWriter, r *http.Request, id *identity.Identity, rest []string) {
	// 子路径 /memories/{id}/revise|invalidate：修订与失效——写操作
	// 只收 POST（显式操作需要明确对象，且不能被导航触发）。
	if len(rest) >= 2 && (rest[1] == "revise" || rest[1] == "invalidate") {
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		s.handleMemoryAction(w, r, id, rest[0], rest[1])
		return
	}
	// 子路径 /memories/{filename}：返回单条全文 {name, content}
	if len(rest) > 0 && rest[0] != "" {
		s.handleMemoryOne(w, r, id, rest[0])
		return
	}
	store := memStore(id.Dir)
	all, err := store.List()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	tFilter := r.URL.Query().Get("type")
	if q != "" {
		hits, _ := store.Search(q, 20)
		all = hits
	}
	out := make([]map[string]any, 0, len(all))
	for _, m := range all {
		if tFilter != "" && m.Type != tFilter {
			continue
		}
		name := memoryFilename(m)
		out = append(out, map[string]any{
			"name":    name,
			"mtime":   m.Created.UnixMilli(),
			"id":      m.ID,
			"summary": m.Summary,
			"type":    m.Type,
			"created": m.Created.UTC().Format("2006-01-02T15:04:05Z"),
			"slug":    memorySlug(name),
			// status 空串 = 活动；invalid/superseded 在前端弱化展示
			// 并禁用修订/失效操作。
			"status": m.Status,
		})
	}
	writeJSON(w, 200, out)
}

// handleMemoryAction 修订或失效一条记忆：
//
//	POST /memories/{id}/revise      {"content":"..."} → {ok, id, supersedes}
//	POST /memories/{id}/invalidate                 → {ok, id, status}
//
// 修订写新版本并把旧版本标记为被替代；失效保留文件供审计但退出
// 检索。错误统一 400：非法段、未知或已失效的 id、空内容。
func (s *Server) handleMemoryAction(w http.ResponseWriter, r *http.Request, id *identity.Identity, memID, action string) {
	// memID 用于匹配记忆 ID（不拼路径），校验按 handleMemoryOne
	// 同一口径纵深防御。
	if memID == "" || strings.Contains(memID, "..") ||
		strings.Contains(memID, "/") || strings.Contains(memID, "\\") {
		writeError(w, 400, "非法记忆 ID")
		return
	}
	store := memStore(id.Dir)
	switch action {
	case "revise":
		var req struct {
			Content string `json:"content"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
			writeError(w, 400, "请求体必须是 {content} JSON")
			return
		}
		newID, err := store.Revise(r.Context(), memID, req.Content)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "id": newID, "supersedes": memID})
	case "invalidate":
		if err := store.Invalidate(r.Context(), memID); err != nil {
			writeError(w, 400, err.Error())
			return
		}
		// status 字面量与 mem.StatusInvalid 一致（本文件不引 mem 包）。
		writeJSON(w, 200, map[string]any{"ok": true, "id": memID, "status": "invalid"})
	}
}

// handleMemoryOne 返回单条记忆文件全文。
func (s *Server) handleMemoryOne(w http.ResponseWriter, _ *http.Request, id *identity.Identity, name string) {
	if strings.Contains(name, "..") || strings.Contains(name, "/") || strings.Contains(name, "\\") {
		writeError(w, 400, "非法文件名")
		return
	}
	data, err := readMemoryFile(id.Dir, name)
	if err != nil {
		writeError(w, 404, "记忆文件不存在: "+name)
		return
	}
	writeJSON(w, 200, map[string]any{
		"name":    name,
		"content": string(data),
	})
}
