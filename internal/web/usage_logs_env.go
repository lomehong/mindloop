package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"mindloop/internal/identity"
	"mindloop/internal/llm"
	"mindloop/internal/mind"
	"mindloop/internal/obs"
	"mindloop/internal/recap"
	"mindloop/internal/traj"
)

// handleIdentityEnv 返回 IdentityEnv 契约：
// {identity, env: EnvEntry[{key,value,secret}], inherited, note}。
// 读取身份 .env + 心智根 .env；敏感键（key/secret/token/password）
// 的 value 给脱敏预览、secret=true，viewer 据此决定是否打码显示。
func (s *Server) handleIdentityEnv(w http.ResponseWriter, _ *http.Request, id *identity.Identity, _ []string) {
	entries := []map[string]any{}
	note := "写入身份 .env；mind run / chat 启动时加载（优先级：显式环境变量 > 身份 .env > 全局 .env）"
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
			secret := isSecretEnvKey(k)
			if secret {
				// 敏感键命中即脱敏，无长度门槛——短密钥同样不回显
				// 任何内容字节（与 handleEnvPut/parseEnvRedacted
				// 同一形态）。
				v = maskSecretValue(v)
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
// 命中即脱敏（无长度门槛）：值不落任何内容字节，只给长度占位。
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
		if isSecretEnvKey(k) {
			v = maskSecretValue(v)
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
			// TLDR 从日志派生：final 正文即模型自己写的运行结论。
			if s.Type == traj.TypeFinal {
				if c, ok := s.Field("content"); ok && c != "" {
					one := traj.OneLine(c, 120)
					g["tldr"] = &one
				}
			}
			if s.Type == traj.TypeFinal || s.Type == traj.TypeError {
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
// 停机 = mind.RequestStopDir 写停机标志；启动 = 清除停机标志并告知
// 用户用终端拉起（浏览器触发后台进程启动容易出错，告知更诚实）。
func (s *Server) handleDispatch(w http.ResponseWriter, _ *http.Request, id *identity.Identity) {
	// 状态：已停→重启；已运行→停机。
	currentlyLive := isIdentityLive(id.Timeline.Dir)
	if currentlyLive {
		if err := mind.RequestStopDir(id.Timeline.Dir); err != nil {
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
	_ = os.Remove(filepath.Join(id.Timeline.Dir, "run", "stop"))
	writeJSON(w, 200, map[string]any{
		"identity": map[string]string{"id": id.Name, "name": id.Name},
		"action":   "start",
		"detail":   "停机标志已清除。请在终端运行 mindloop mind run ada 启动调度器",
	})
}

// handleThinkerStep 给某个 thinker 一次手动唤醒（viewer 按钮）：写
// wake 信号文件，调度器下个心跳（≤200ms）消费并精确投递给该
// thinker——不经过订阅匹配，因此不会误触发其他思考者。
func (s *Server) handleThinkerStep(w http.ResponseWriter, r *http.Request, id *identity.Identity, name string) {
	if !isIdentityLive(id.Timeline.Dir) {
		writeError(w, 409, "心智未运行")
		return
	}
	if err := mind.SignalWake(id.Timeline.Dir, name); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	// ControlResult 契约（viewer types.ts）：{ok, action, names}。
	writeJSON(w, 200, map[string]any{
		"ok":     true,
		"action": "step",
		"names":  []string{name},
		"stderr": "唤醒信号已发出，调度器心跳内投递",
	})
}

// handleThinkerToggle 启用/禁用单个 thinker：写禁用名单文件，调度
// 器路由时检查并跳过——真实生效，非记账。契约（viewer setThinkerEnabled）：
// {ok, name, disabled, needs_restart}——禁用在调度器每个心跳的路由
// 现场生效，永远不需要重启。
func (s *Server) handleThinkerToggle(w http.ResponseWriter, r *http.Request, id *identity.Identity, name string, enabled bool) {
	if err := mind.SetThinkerEnabled(id.Timeline.Dir, name, enabled); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"ok":            true,
		"name":          name,
		"disabled":      !enabled,
		"needs_restart": false,
	})
}

// thinkerControlReq 是 start/stop 请求体（viewer 发 {names, force}）。
type thinkerControlReq struct {
	Names []string `json:"names"`
	Force bool     `json:"force"`
}

// handleThinkersAll 启/停思考者，返回 viewer ControlResult 契约
// {ok, action, names, stderr}（stderr 末行会出现在 toast 描述里）。
//   - stop：写停机标志，调度器心跳内优雅退出（真实生效）。force
//     （Shift+点击"立即终止"）当前与优雅停机同路径——在途思考自然
//     收尾，不额外杀进程。
//   - start：detached spawn 一个 mind run 子进程接管（同二进制）；
//     运行锁保证不重复启动，3 秒内锁被持有即报成功。
func (s *Server) handleThinkersAll(w http.ResponseWriter, r *http.Request, id *identity.Identity, action string) {
	var req thinkerControlReq
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req)
	names := req.Names
	if names == nil {
		names = []string{}
	}
	switch action {
	case "stop":
		if err := mind.RequestStopDir(id.Timeline.Dir); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{
			"ok":     true,
			"action": "stop",
			"names":  names,
			"stderr": "停机标志已写入，调度器心跳内退出（在途思考自然收尾）",
		})
	case "start":
		if isIdentityLive(id.Timeline.Dir) {
			writeJSON(w, 200, map[string]any{
				"ok": true, "action": "start", "names": names,
				"stderr": "心智已在运行（未重复启动）",
			})
			return
		}
		exe, err := os.Executable()
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		// 清掉可能残留的停机标志，否则子进程启动即退出。
		_ = os.Remove(filepath.Join(id.Timeline.Dir, "run", "stop"))
		cmd := exec.Command(exe, "mind", "run", id.Name)
		cmd.Dir = ""
		// 与当前 web 进程同环境（.env 已在 web 进程加载）。
		cmd.Env = os.Environ()
		if err := cmd.Start(); err != nil {
			writeError(w, 500, "启动 mind run 子进程失败: "+err.Error())
			return
		}
		go func() { _ = cmd.Process.Release() }()
		// 等运行锁出现（≤3s）判定启动成功。
		ok := false
		for i := 0; i < 30; i++ {
			if isIdentityLive(id.Timeline.Dir) {
				ok = true
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if !ok {
			writeJSON(w, 200, map[string]any{
				"ok": true, "action": "start", "names": names,
				"stderr": "子进程已拉起但运行锁 3 秒内未出现——请检查 mindrun 日志",
			})
			return
		}
		writeJSON(w, 200, map[string]any{
			"ok": true, "action": "start", "names": names,
			"stderr": "心智已由子进程接管",
		})
	default:
		writeError(w, 400, "未知动作: "+action)
	}
}

// handleThinkerSync GET 返回同步状态（ThinkerSyncStatus 契约）。
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

// handleThinkerSyncPull POST 执行拉取（ThinkerSyncResult 契约：
// {ok, results:[{name, action, files}]}）。内置 thinker 与运行时
// 同源，永远 unchanged——viewer 按这个动作词过滤出"有变化"的
// 条目，全 unchanged 时它显示"已是最新"。
func (s *Server) handleThinkerSyncPull(w http.ResponseWriter, _ *http.Request, id *identity.Identity) {
	results := []map[string]any{}
	for _, t := range bundledThinkers {
		results = append(results, map[string]any{
			"name":   t,
			"action": "unchanged",
			"files":  []string{},
		})
	}
	writeJSON(w, 200, map[string]any{"ok": true, "results": results})
}

// handleRecapRefresh 真触发分集重算：异步跑 recap.Updater（Flush=true，
// 连尾窗一起算），完成后删除 refreshing 标志；GET /recap 据此上报
// refreshing 状态供前端轮询。
func (s *Server) handleRecapRefresh(w http.ResponseWriter, r *http.Request, id *identity.Identity) {
	flagPath := filepath.Join(id.Dir, "recap", ".refreshing")
	_ = os.MkdirAll(filepath.Dir(flagPath), 0o755)
	if _, err := os.Stat(flagPath); err == nil {
		writeJSON(w, 200, map[string]any{"ok": true, "refreshing": true, "detail": "重算已在进行中"})
		return
	}
	client, err := llm.FromEnv()
	if err != nil {
		writeError(w, 503, "模型未配置，无法重算: "+err.Error())
		return
	}
	client.OnDone = obs.UsageRecorder(id.Dir, client.Model, client.Provider, nil)
	if err := os.WriteFile(flagPath, []byte(nowISO()), 0o644); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	// 请求 ctx 在 handler 返回即被 net/http 取消——后台重算必须
	// 脱离它（WithoutCancel）并自带超时，否则每次都是"已开始"
	// 然后必然中途夭折。
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 30*time.Minute)
	go func() {
		defer cancel()
		defer os.Remove(flagPath)
		up := &recap.Updater{
			Timeline:     id.Timeline,
			Thinker:      mind.LLMThinker{Client: client},
			Flush:        true,
			MaxSummaries: 20,
		}
		_, _ = up.Update(ctx)
	}()
	writeJSON(w, 200, map[string]any{"ok": true, "refreshing": true, "detail": "分集重算已开始，稍后刷新查看"})
}

// handleUsageRefresh：usage GET 即时聚合（永不需要缓存失效），
// 本端点保留给 viewer 按钮，返回 ok 即可。
func (s *Server) handleUsageRefresh(w http.ResponseWriter, _ *http.Request, id *identity.Identity) {
	writeJSON(w, 200, map[string]any{"ok": true, "detail": "用量按需即时计算，已最新"})
}

// handleKillall 强制结束所有心智相关进程——viewer 的 Kill all 按钮。
// 请求体 {dry_run}：dry_run=true 只报告将停哪些、不写任何标志
// （viewer 先 dryRun 弹确认框，确认后再真停）；stdout 是确认框里
// 展示的人类可读摘要（KillallResult 契约 {ok, dry_run, stdout, stderr}）。
func (s *Server) handleKillall(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DryRun bool `json:"dry_run"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req)
	infos, err := scanIdentities(s.cfg.Root)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	var names []string
	for _, info := range infos {
		// live 探测与停机投递都必须用轨迹目录层级（与 mind.RunLockDir/
		// RequestStopDir 的参数语义一致）——此前传身份目录，stop 标志
		// 写到 <身份>/run/stop，调度器消费的是 <轨迹>/run/stop，等于
		// 永远收不到 killall。
		if info.TLDir == "" {
			continue
		}
		if isIdentityLive(info.TLDir) {
			names = append(names, info.Name)
		}
	}
	if req.DryRun {
		summary := "没有正在运行的心智。"
		if len(names) > 0 {
			summary = fmt.Sprintf("将向 %d 个运行中的心智写入停机标志: %s", len(names), strings.Join(names, ", "))
		}
		writeJSON(w, 200, map[string]any{
			"ok": true, "dry_run": true, "stdout": summary, "stderr": "",
		})
		return
	}
	stopped := 0
	for _, info := range infos {
		if info.TLDir == "" {
			continue
		}
		if err := mind.RequestStopDir(info.TLDir); err == nil {
			stopped++
		}
	}
	summary := fmt.Sprintf("已向 %d 个心智写入停机标志（调度器心跳内优雅退出）", stopped)
	if stopped == 0 {
		summary = "没有需要停止的心智。"
	}
	writeJSON(w, 200, map[string]any{
		"ok": true, "dry_run": false, "stdout": summary, "stderr": "",
	})
}

// handleSelfUpdate：本地工具不做自更新——给出可执行的替代（git pull）。
func (s *Server) handleSelfUpdate(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{
		"ok": false, "updated": false, "restarting": false,
		"detail": "本地部署不支持热更新：在仓库执行 git pull && go install ./cmd/mindloop 后重启 web",
	})
}

// handleLlmHealthProbe 真实发起一次最小 LLM 调用（一次 ping），
// 返回 LlmProbeResult 契约：{ok, latency_ms, model, provider, error}。
func (s *Server) handleLlmHealthProbe(w http.ResponseWriter, r *http.Request) {
	client, err := llm.FromEnv()
	if err != nil {
		writeJSON(w, 200, map[string]any{
			"ok": false, "latency_ms": 0, "model": nil, "provider": nil,
			"error": err.Error(),
		})
		return
	}
	start := time.Now()
	_, callErr := client.Complete(r.Context(), "Reply with the single word: pong",
		[]llm.Message{{Role: "user", Content: "ping"}})
	latency := int(time.Since(start).Milliseconds())
	out := map[string]any{
		"ok":         callErr == nil,
		"latency_ms": latency,
		"model":      client.Model,
		"provider":   client.Provider,
	}
	if callErr != nil {
		out["error"] = callErr.Error()
	}
	writeJSON(w, 200, out)
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

// handleOpenRouterModels 拉取 OpenRouter 公开模型目录（GET /
// api/v1/models 免鉴权）；网络不可达返回空目录 + detail。
func (s *Server) handleOpenRouterModels(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://openrouter.ai/api/v1/models", nil)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		writeJSON(w, 200, map[string]any{
			"models": []any{}, "detail": "无法访问 OpenRouter 目录: " + err.Error(),
		})
		return
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		writeJSON(w, 200, map[string]any{"models": []any{}, "detail": "读取失败: " + err.Error()})
		return
	}
	var payload struct {
		Data []struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Context *int   `json:"context_length"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		writeJSON(w, 200, map[string]any{"models": []any{}, "detail": "解析失败: " + err.Error()})
		return
	}
	models := make([]map[string]any, 0, len(payload.Data))
	for _, m := range payload.Data {
		row := map[string]any{"id": m.ID, "name": m.Name}
		if m.Context != nil {
			row["context_length"] = *m.Context
		}
		models = append(models, row)
	}
	writeJSON(w, 200, map[string]any{"models": models})
}

// fieldOrNil 取步骤字段；缺字段返回 nil（供 JSON null）。
func fieldOrNil(step *traj.Step, key string) any {
	if v, ok := step.Field(key); ok && v != "" {
		return v
	}
	return nil
}

// bundledThinkers 是心智包内置的 thinker 清单——直接消费 mind 包的
// 权威名单 ThinkerNames()（task-18 落地），web 侧不再自持硬编码副本。
var bundledThinkers = mind.ThinkerNames()

// handleDispatchLog 返回 DispatchEvent[]——读调度器落盘的 NDJSON
// 事件流（dispatcher.log）。文件不存在 = 调度器从未运行，返回空
// 数组（viewer 显示"暂无调度事件"空态，诚实）。
func (s *Server) handleDispatchLog(w http.ResponseWriter, _ *http.Request, id *identity.Identity, _ []string) {
	events, err := mind.ReadEvents(mind.DispatchLogPath(id.Timeline.Dir))
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if events == nil {
		events = []mind.DispatchEvent{}
	}
	writeJSON(w, 200, events)
}
