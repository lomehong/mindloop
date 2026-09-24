package web

import (
	"net/http"
	"strings"

	"mindloop/internal/identity"
)

// handleChat 渲染对话视图：只返回 message 步骤，按时间排序。
func (s *Server) handleChat(w http.ResponseWriter, _ *http.Request, id *identity.Identity) {
	steps, err := id.Timeline.Steps()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(steps))
	for _, s := range steps {
		if s.Type != "message" {
			continue
		}
		out = append(out, stepToJSON(s))
	}
	writeJSON(w, 200, map[string]any{
		"identity": id.Name,
		"messages": out,
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
