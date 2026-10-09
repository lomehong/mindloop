package web

// GET/PUT /api/identities/{name}/budget —— 预算控制配置的读写入口。
// 预算值持久化在身份 .env 的 MINDLOOP_DAILY_TOKENS 行；null = 删行
// （不封顶）。写入是逐行原位替换（保留注释与其他变量）；重启心智后
// 生效（Gate 在心智启动时从环境构造）。

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/lomehong/mindloop/internal/identity"
)

// budgetEnvKey 是 .env 中的预算行键。
const budgetEnvKey = "MINDLOOP_DAILY_TOKENS"

type budgetConfig struct {
	// DailyLimit null = 不封顶；>0 = 每日 token 上限。
	DailyLimit *int64 `json:"daily_limit"`
}

func (s *Server) handleBudget(w http.ResponseWriter, r *http.Request, id *identity.Identity, rest []string) {
	if len(rest) != 0 {
		writeError(w, 404, "未知子路径: budget/"+strings.Join(rest, "/"))
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.handleBudgetGet(w, id)
	case http.MethodPut:
		s.handleBudgetPut(w, r, id)
	default:
		writeError(w, 405, "预算配置只认 GET/PUT")
	}
}

// readBudgetEnv 读身份 .env，返回 (预算值, 是否设置了预算)。
func readBudgetEnv(id *identity.Identity) (int64, bool) {
	data, err := os.ReadFile(envPath(id))
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, budgetEnvKey+"=") {
			val := strings.TrimPrefix(line, budgetEnvKey+"=")
			var n int64
			if _, err := fmt.Sscanf(val, "%d", &n); err == nil && n > 0 {
				return n, true
			}
			return 0, false
		}
	}
	return 0, false
}

// writeBudgetEnv 原位更新 .env 中的预算行：有则替换、无则追加、
// null 则删除。其余行原样保留。
func writeBudgetEnv(id *identity.Identity, limit *int64) error {
	path := envPath(id)
	var lines []string
	if data, err := os.ReadFile(path); err == nil {
		lines = strings.Split(string(data), "\n")
	}

	// 删旧行。
	filtered := lines[:0]
	for _, line := range lines {
		if !strings.HasPrefix(strings.TrimSpace(line), budgetEnvKey+"=") &&
			!strings.HasPrefix(strings.TrimSpace(line), "# "+budgetEnvKey) {
			filtered = append(filtered, line)
		}
	}
	lines = filtered

	// 加新行（null = 不加 = 不封顶）。
	if limit != nil {
		lines = append(lines, fmt.Sprintf("%s=%d", budgetEnvKey, *limit))
	}

	out := strings.Join(lines, "\n")
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return os.WriteFile(path, []byte(out), 0o644)
}

func envPath(id *identity.Identity) string {
	return id.Dir + string(os.PathSeparator) + ".env"
}

func (s *Server) handleBudgetGet(w http.ResponseWriter, id *identity.Identity) {
	limit, hasBudget := readBudgetEnv(id)
	cfg := budgetConfig{DailyLimit: nil}
	if hasBudget {
		cfg.DailyLimit = &limit
	}
	writeJSON(w, 200, cfg)
}

func (s *Server) handleBudgetPut(w http.ResponseWriter, r *http.Request, id *identity.Identity) {
	var body struct {
		DailyLimit *int64 `json:"daily_limit"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		writeError(w, 400, "请求体应为 JSON")
		return
	}
	if body.DailyLimit != nil && *body.DailyLimit <= 0 {
		writeError(w, 400, "daily_limit 必须为正整数或 null（不封顶）")
		return
	}
	if err := writeBudgetEnv(id, body.DailyLimit); err != nil {
		writeError(w, 500, "写入 .env 失败: "+err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"ok":          true,
		"daily_limit": body.DailyLimit,
		"note":        "重启心智后生效",
	})
}
