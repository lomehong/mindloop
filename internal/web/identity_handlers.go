package web

import (
	"encoding/json"
	"net/http"
	"strings"

	"mindloop/internal/identity"
	"mindloop/internal/traj"
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
//   - messages 只含 message 步骤，按日志序；
//   - tail 参数截取最近 N 条（默认 200）；
//   - with 参数只保留与某人的对话（对方发来的 + 我回给对方的），
//     语义与 responder.history 一致；
//   - outcomes[step_id]：入站消息已被回复（存在 reply_to 指向它的
//     message 步骤）时为 "replied"；"no-reply"/"failed" 暂无日志
//     事实可判，留空 = 前端的 undecided 态（诚实而非编造）。
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request, id *identity.Identity) {
	steps, err := id.Timeline.Steps()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	tail := 200
	if v := r.URL.Query().Get("tail"); v != "" {
		if n, perr := toInt(v); perr == nil && n > 0 {
			tail = n
		}
	}
	with := r.URL.Query().Get("with")

	// 回复索引：被回复的入站 step_id → 有回复。
	replied := map[string]bool{}
	for _, st := range steps {
		if st.Type != "message" {
			continue
		}
		if rt, ok := st.Field("reply_to"); ok && rt != "" {
			replied[rt] = true
		}
	}

	msgs := make([]chatMessage, 0, len(steps))
	outcomes := map[string]string{}
	for _, st := range steps {
		if st.Type != "message" {
			continue
		}
		from, _ := st.Field("from")
		to, _ := st.Field("to")
		if with != "" && from != with && !(from == id.Name && to == with) {
			continue
		}
		content, _ := st.Field("content")
		m := chatMessage{
			TS:      st.TS,
			StepID:  st.StepID,
			From:    from,
			To:      to,
			Content: content,
		}
		if rt, ok := st.Field("reply_to"); ok {
			m.ReplyTo = &rt
		}
		if fn, ok := st.Field("filename"); ok {
			m.Filename = &fn
		}
		if su, ok := st.Field("source_url"); ok {
			m.SourceURL = &su
		}
		msgs = append(msgs, m)
		if from != id.Name && from != "" && replied[st.StepID] {
			outcomes[st.StepID] = "replied"
		}
	}
	if len(msgs) > tail {
		msgs = msgs[len(msgs)-tail:]
	}
	writeJSON(w, 200, map[string]any{
		"identity": map[string]string{"id": id.Name, "name": id.Name},
		"live":     isIdentityLive(id.Timeline.Dir),
		"messages": msgs,
		"outcomes": outcomes,
	})
}

// handleChatSend 接收人类消息——POST /api/identities/{name}/chat
// {content, from_name} → {ok, from, to}。消息是日志事实：落盘即投递，
// 运行中的调度器 feeder 下个心跳（≤200ms）拾取并路由给 responder。
// 心智未运行时消息同样落盘（talk 页的"睡眠中，消息会等它醒来"）。
func (s *Server) handleChatSend(w http.ResponseWriter, r *http.Request, id *identity.Identity) {
	var req struct {
		Content  string `json:"content"`
		FromName string `json:"from_name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, 400, "请求体必须是 {content, from_name} JSON")
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
	st := traj.NewStep("message")
	st.Fields["from"] = from
	st.Fields["to"] = id.Name
	st.Fields["source"] = "chat"
	st.Fields["content"] = req.Content
	if err := id.Timeline.Append(r.Context(), st); err != nil {
		writeError(w, 500, "消息落盘失败: "+err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"ok":   true,
		"from": from,
		"to":   id.Name,
	})
}

// handleMemories 列出身份的记忆库——viewer Memories 视图。
//
// GET /api/identities/{name}/memories?type=fact&q=keyword
func (s *Server) handleMemories(w http.ResponseWriter, r *http.Request, id *identity.Identity, rest []string) {
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
		})
	}
	writeJSON(w, 200, out)
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
