package web

import (
	"encoding/json"
	"net/http"
)

// writeJSON 输出一个 JSON 响应（UTF-8，不转义 HTML）。
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

// writeError 输出与 viewer getJson 兼容的错误形态：
// {detail: {message, code}} —— api.ts 的 sendJson 从 detail.message
// 里取出人类可读消息。
func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{
		"detail": map[string]any{"message": msg, "code": code},
	})
}
