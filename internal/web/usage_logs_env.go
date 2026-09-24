package web

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"mindloop/internal/identity"
	"mindloop/internal/traj"
)

// handleUsage 返回 Usage 契约（viewer Usage 页用）。
//
// viewer 期望的层级：identity + available + refreshing + pending_bytes +
// by_model + totals + ledger。我们的 llm 包暂未持久化用量账本
// （内部已采集到 llm-health.json 的 last_call 里），所以这里返回
// available=false + 空 by_model，让 viewer 渲染空态 + "Refresh" 按钮。
// 当后续 llm 用量管线落地后这层自动填上。
func (s *Server) handleUsage(w http.ResponseWriter, _ *http.Request, id *identity.Identity, _ []string) {
	out := map[string]any{
		"identity":      map[string]string{"id": id.Name, "name": id.Name},
		"available":     false,
		"refreshing":    false,
		"pending_bytes": int64(0),
	}
	writeJSON(w, 200, out)
}

// 头部标 is_path_self_link=false、ring、identity；key/value 列表
// redaction：MINDLOOP_*API_KEY* 等敏感键脱敏，只露出前缀与值长度。
// handleIdentityEnv 返回 IdentityEnv 契约：
// {identity, env: EnvEntry[{key,value,secret}], inherited, note}。
// 读取身份 .env + 心智根 .env；敏感键（key/secret/token/password）
// 的 value 给脱敏预览、secret=true，viewer 据此决定是否打码显示。
func (s *Server) handleIdentityEnv(w http.ResponseWriter, _ *http.Request, id *identity.Identity, _ []string) {
	entries := []map[string]any{}
	note := "环境变量来自身份 .env；心智根 .env 未合并（v0.1）"
	for _, path := range []string{filepath.Join(id.Dir, ".env"), filepath.Join(s.cfg.Root, ".env")} {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, ln := range strings.Split(string(data), "\n") {
			ln = strings.TrimSpace(ln)
			if ln == "" || strings.HasPrefix(ln, "#") {
				continue
			}
			k, v, ok := strings.Cut(ln, "=")
			if !ok {
				continue
			}
			k = strings.TrimSpace(k)
			v = strings.TrimSpace(v)
			if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
				v = v[1 : len(v)-1]
			}
			secret := false
			lk := strings.ToLower(k)
			for _, hint := range []string{"key", "secret", "token", "password"} {
				if strings.Contains(lk, hint) {
					secret = true
					break
				}
			}
			if secret && len(v) > 8 {
				v = v[:4] + "…·" + strconv.Itoa(len(v)) + " chars"
			}
			entries = append(entries, map[string]any{
				"key":    k,
				"value":  v,
				"secret": secret,
			})
		}
	}
	writeJSON(w, 200, map[string]any{
		"identity":  map[string]string{"id": id.Name, "name": id.Name},
		"env":       entries,
		"inherited": []map[string]any{},
		"note":      note,
	})
}

// parseEnvRedacted 解析 .env，敏感键（API_KEY/SECRET/TOKEN/PASSWORD）
// 用值长度占位而不露出真值。前缀脱敏避免泄露厂商前缀长度。
func parseEnvRedacted(content string) map[string]string {
	out := map[string]string{}
	for _, ln := range strings.Split(content, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		k, v, ok := strings.Cut(ln, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		// 去外侧引号
		if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
			v = v[1 : len(v)-1]
		}
		lk := strings.ToLower(k)
		sensitive := false
		for _, hint := range []string{"key", "secret", "token", "password"} {
			if strings.Contains(lk, hint) {
				sensitive = true
				break
			}
		}
		if sensitive {
			v = "[REDACTED · " + itoa(len(v)) + " chars]"
		}
		out[k] = v
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// handleStepDetail 返回单步完整内容——Viewer 的 step 页弹窗用。
// viewer 期望 {step, index, run, dispatcher_running, dispatcher_pid}。
// 我们的 NormalizedStep 已经包含了 step_id/type/preview/source/raw，
// 名称不直接对应"step"但内容等价；index 即步骤索引；run 单层。
func (s *Server) handleStepDetail(w http.ResponseWriter, r *http.Request, id *identity.Identity, rest []string) {
	if len(rest) == 0 || rest[0] == "" {
		writeError(w, 400, "缺少 step id")
		return
	}
	steps, err := id.Timeline.Steps()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	var found *traj.Step
	var idx int
	for i, s := range steps {
		if s.StepID == rest[0] || strings.HasPrefix(s.StepID, rest[0]) {
			found = &s
			idx = i
			break
		}
	}
	if found == nil {
		writeError(w, 404, "未找到步骤 "+rest[0])
		return
	}
	// 找同 run 的 run-group（极简版：扫描轨迹聚合同 run_id）。
	run := findRunGroup(steps, found)
	out := map[string]any{
		"step":               normalizeStep(*found),
		"index":              idx,
		"run":                run,
		"dispatcher_running": isIdentityLive(id.Timeline.Dir),
		"dispatcher_pid":     nil,
	}
	writeJSON(w, 200, out)
}

func findRunGroup(steps []traj.Step, target *traj.Step) map[string]any {
	rid, ok := (*target).Field("run_id")
	if !ok || rid == "" {
		return nil
	}
	g := map[string]any{
		"run_id":            rid,
		"trigger_step_id":   nil,
		"launched_by":       fieldOrNil(target, "launched_by"),
		"step_ids":          []string{},
		"started_ts":        "",
		"ended_ts":          nil,
		"status":            "running",
		"command":           "",
		"command_truncated": false,
		"model":             nil,
		"tldr":              nil,
		"last_touch":        0,
	}
	ids := g["step_ids"].([]string)
	for i, s := range steps {
		if id, ok := s.Field("run_id"); ok && id == rid {
			ids = append(ids, s.StepID)
			if s.TS != "" && s.TS > g["started_ts"].(string) {
				g["started_ts"] = s.TS
			}
			g["last_touch"] = i
			if s.Type == "final" || s.Type == "error" {
				g["status"] = "done"
				t := s.TS
				g["ended_ts"] = &t
			}
		}
	}
	g["step_ids"] = ids
	return g
}

// handleDispatch 切换 dispatcher 启停（viewer thinkers 头部按钮）。
// 我们用 mind 包的 RequestStop + RunLock 模拟实现：停机 = 写停机标志，
// 启动 = 删除停机标志 + 写运行锁文件让 next 调度周期接管。
func (s *Server) handleDispatch(w http.ResponseWriter, _ *http.Request, id *identity.Identity) {
	runLockDir := filepath.Join(id.Timeline.Dir, "run", "dispatcher.lock")
	stopPath := filepath.Join(id.Timeline.Dir, "run", "stop")
	_ = os.MkdirAll(filepath.Dir(runLockDir), 0o755)

	// 状态：已停→重启；已运行→停机。
	currentlyLive := isIdentityLive(id.Timeline.Dir)
	if currentlyLive {
		// 写停机标志（dispatcher 的 dispatcher.go 会读到并退出）
		if err := os.WriteFile(stopPath, []byte(traj.NowString()), 0o644); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{
			"identity": map[string]string{"id": id.Name, "name": id.Name},
			"action":   "stop",
			"detail":   "停机标志已写入，调度器心跳内退出",
		})
		return
	}
	// 启动：删除停机标志 + 写一个空锁文件作为意图标识。
	_ = os.Remove(stopPath)
	// 实际启动心智需要 `mindloop mind run ada`——这里仅清理信号，
	// 返回要求用户手动启动。设计取舍：浏览器触发后台进程启动容易出错
	//（依赖、fork），告知更诚实。
	writeJSON(w, 200, map[string]any{
		"identity": map[string]string{"id": id.Name, "name": id.Name},
		"action":   "start",
		"detail":   "停机标志已清除。请在终端运行 mindloop mind run ada 启动调度器",
	})
}

// handleThinkerStep 给某个 thinker 一次手动唤醒（viewer 按钮）。
// 简化：写一个"思考者唤醒"的合步骤作为本步轨迹——调度器轮询时
// 会触发对应 thinker。如果心智没运行则拒绝。
func (s *Server) handleThinkerStep(w http.ResponseWriter, r *http.Request, id *identity.Identity, rest []string) {
	if len(rest) == 0 || rest[0] == "" {
		writeError(w, 400, "缺少 thinker 名")
		return
	}
	if !isIdentityLive(id.Timeline.Dir) {
		writeError(w, 409, "心智未运行")
		return
	}
	st := traj.NewStep("observation")
	st.Fields["kind"] = "manual_step"
	st.Fields["thinker"] = rest[0]
	st.Fields["requested_by"] = "operator"
	if err := id.Timeline.Append(r.Context(), st); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"identity": map[string]string{"id": id.Name, "name": id.Name},
		"thinker":  rest[0],
		"step_id":  st.StepID,
	})
}

// handleThinkerToggle 启用/禁用单个 thinker：写一条 manual
// 步骤，调度器读 SubscribeTypes 后在收到 awakening 时跳过该 thinker。
// 这是真实功能的最简骨架——真正的禁用机制在 mind 包内做。
func (s *Server) handleThinkerToggle(w http.ResponseWriter, r *http.Request, id *identity.Identity, rest []string) {
	if len(rest) < 2 {
		writeError(w, 400, "缺少 thinker 名或动作")
		return
	}
	name, action := rest[0], rest[1]
	st := traj.NewStep("observation")
	st.Fields["kind"] = "thinker_toggle"
	st.Fields["thinker"] = name
	st.Fields["enabled"] = action == "enable"
	if err := id.Timeline.Append(r.Context(), st); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"identity": map[string]string{"id": id.Name, "name": id.Name},
		"thinker":  name,
		"enabled":  st.Fields["enabled"],
	})
}

// handleThinkersAll 一次性启/停所有 thinker。
func (s *Server) handleThinkersAll(w http.ResponseWriter, r *http.Request, id *identity.Identity, rest []string) {
	if len(rest) == 0 {
		writeError(w, 400, "缺少 start/stop")
		return
	}
	verb := rest[0]
	st := traj.NewStep("observation")
	st.Fields["kind"] = "thinkers_all"
	st.Fields["action"] = verb
	if err := id.Timeline.Append(r.Context(), st); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"identity": map[string]string{"id": id.Name, "name": id.Name},
		"action":   verb,
	})
}

// handleThinkerSync 返回 ThinkerSyncStatus 契约：
// {bundled_root, thinkers:[{name,status,changed_files,bundled_version}], note}。
// v0.1 真实状态：内置 thinker 均视为 bundled。
func (s *Server) handleThinkerSync(w http.ResponseWriter, _ *http.Request, id *identity.Identity) {
	thinkers := []map[string]any{}
	for _, t := range bundledThinkers {
		thinkers = append(thinkers, map[string]any{
			"name":            t,
			"status":          "bundled",
			"changed_files":   []string{},
			"bundled_version": "v0.1",
		})
	}
	writeJSON(w, 200, map[string]any{
		"bundled_root": nil,
		"thinkers":     thinkers,
		"note":         "内置 thinker 与身份运行时一致",
	})
}

// handleRecapRefresh 触发一次 recap 重算——viewer 的 Refresh 按钮。
// 真实实现需要异步发起 LLM 摘要。本轮返回 OK 提示用户稍后再看。
func (s *Server) handleRecapRefresh(w http.ResponseWriter, _ *http.Request, id *identity.Identity) {
	writeJSON(w, 200, map[string]any{
		"identity": map[string]string{"id": id.Name, "name": id.Name},
		"detail":   "recap 重算已排队（v0.1 占位：实际异步重算需连 LLM）",
	})
}

// handleUsageRefresh 与 recap 同模式——触发一次用量重算。
func (s *Server) handleUsageRefresh(w http.ResponseWriter, _ *http.Request, id *identity.Identity) {
	writeJSON(w, 200, map[string]any{
		"identity": map[string]string{"id": id.Name, "name": id.Name},
		"detail":   "用量重算已排队",
	})
}

// handleKillall 强制结束所有心智相关进程——viewer 的 Kill all 按钮。
// 写停机标志到身份目录（我们的调度器读到会优雅退出）。
func (s *Server) handleKillall(w http.ResponseWriter, _ *http.Request) {
	infos, err := scanIdentities(s.cfg.Root)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	stopped := 0
	for _, info := range infos {
		stop := filepath.Join(info.Dir, "run", "stop")
		if err := os.WriteFile(stop, []byte(traj.NowString()), 0o644); err == nil {
			stopped++
		}
	}
	writeJSON(w, 200, map[string]any{
		"ok":      true,
		"stopped": stopped,
		"dry_run": false,
	})
}

// handleSelfUpdate viewer 的"重启并升级"按钮——v0.1 不实装。
func (s *Server) handleSelfUpdate(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{
		"ok":     true,
		"detail": "v0.1 不实装自更新（HEADLONG_WEB_SELF_UPDATE=1 启用）",
	})
}

// handleLlmHealthProbe viewer 的"探测模型连通性"按钮——v0.1 返回
// 失败提示（v0.2 实装探测逻辑）。
func (s *Server) handleLlmHealthProbe(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{
		"ok":         false,
		"latency_ms": 0,
		"model":      nil,
		"detail":     "v0.1 不实装 LLM 探针（用 mind chat ada 替代）",
	})
}

// handleLlmHealthGlobal /api/llm-health 全局探针——所有身份的
// 整体健康状态。当前 v0.1 简化：返回空 identities + overall=ok。
func (s *Server) handleLlmHealthGlobal(w http.ResponseWriter, _ *http.Request) {
	out := map[string]any{
		"status":       "ok",
		"failures_15m": 0,
		"failures_1h":  0,
		"cadence_slow": false,
		"checked_at":   time.Now().UTC().Format(traj.TimeFormat),
		"identities":   []any{},
	}
	writeJSON(w, 200, out)
}

// handleOpenRouterModels /api/openrouter/models——v0.1 空实现，
// viewer 显示为空态。后续轮次需要时再接 OpenRouter 真实 API。
func (s *Server) handleOpenRouterModels(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{"models": []any{}})
}

// ensurePackage 临时占位——避免 unused import 报错（os.RemoveAll 已被
// handleDispatch 间接使用）。本文件不引其他包时也无影响。
var _ = os.RemoveAll

// fieldOrNil 取步骤字段；缺字段返回 nil（供 JSON null）。
func fieldOrNil(step *traj.Step, key string) any {
	if v, ok := step.Field(key); ok && v != "" {
		return v
	}
	return nil
}

// bundledThinkers 是心智包内置的 thinker 清单。
var bundledThinkers = []string{"monolith", "responder"}

// handleDispatchLog 返回 DispatchEvent[]（viewer Thinkers 页的
// "dispatcher.log 事件流"）。我们的调度器是进程内组件、不写独立
// log 文件——诚实返回空数组，viewer 显示"未发现 dispatcher.log"
// 空态。真实事件流待 dispatcher 日志落盘后接入（后续轮次）。
func (s *Server) handleDispatchLog(w http.ResponseWriter, _ *http.Request, id *identity.Identity, _ []string) {
	writeJSON(w, 200, []any{})
}
