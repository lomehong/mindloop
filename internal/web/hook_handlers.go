package web

// /hook/<身份>/<感官id> — webhook 感官的 intake 端点（perception.md
// §5 Phase 2，耳）。观察者不在心智进程：端点验证 HMAC 后按同一
// event 步骤契约把事件落进身份轨迹，心智经 feeder 看见——日志即
// API，不需要跨进程 IPC。
//
// 安全形态（评审钉死）：
//   - 独立 per-sensor HMAC 凭据（sensors.json 的 secret 字段），
//     与控制面 bearer token 隔离——控制面凭据含启停心智/killall
//     的写端点，共用等于把控制面交给外部系统；
//   - 时间戳 ±5min 防重放，常数时间比较；
//   - intake 载荷 64KB 上限；
//   - 本路径豁免 sameOrigin 守卫（对外系统的服务器间调用没有
//     Origin 头，回环部署下 Host 校验也会误伤）——HMAC 就是它
//     的鉴权，豁免是显式的、只此一条路径。
//
// 事件判定在端点侧用同一份判定层（sensor.JudgeHook）：规则表命中
// 升档、缺省 s2；quiet 窗口降级 quiet-held。密钥/学习期状态由心智
// 进程的运行器持有，端点侧只做轻判定——两进程的判定差异以
// reason 字段可追溯。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lomehong/mindloop/internal/connector/sensor"
	"github.com/lomehong/mindloop/internal/identity"
	"github.com/lomehong/mindloop/internal/mind"
	"github.com/lomehong/mindloop/internal/traj"
)

// hookStates 是端点侧的判定状态（每感官一槽）：webhook 的去重靠
// 载荷指纹——同一系统重推同一载荷在去重窗内只落一条。
var hookStates = map[string]*sensor.State{}

// hookFirstSeen 读心智维护的学习期起点（sensors-state.json，只读：
// 该文件的写位归 mind 进程，mind 停着时 webhook 也沿用既有起点）。
func hookFirstSeen(identityDir, sensorID string) (time.Time, bool) {
	data, err := os.ReadFile(filepath.Join(identityDir, "sensors-state.json"))
	if err != nil {
		return time.Time{}, false
	}
	var st struct {
		FirstSeen map[string]string `json:"first_seen"`
	}
	if json.Unmarshal(data, &st) != nil {
		return time.Time{}, false
	}
	v, ok := st.FirstSeen[sensorID]
	if !ok {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

func (s *Server) registerHookRoute() {
	s.mux.HandleFunc("POST /hook/", s.handleHook)
}

func (s *Server) handleHook(w http.ResponseWriter, r *http.Request) {
	// 路径：/hook/<身份>/<感官id>
	rest := strings.TrimPrefix(r.URL.Path, "/hook/")
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		writeError(w, http.StatusNotFound, "hook: 路径形态 /hook/<身份>/<感官id>")
		return
	}
	idName, sensorID := parts[0], parts[1]
	id, err := identity.Load(idName)
	if err != nil {
		writeError(w, http.StatusNotFound, "hook: 身份不存在")
		return
	}
	file, err := sensor.Load(filepath.Join(id.Dir, "sensors.json"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "hook: 配置读取失败")
		return
	}
	var cfg *sensor.SensorConfig
	if file != nil {
		cfg = file.Get(sensorID)
	}
	if cfg == nil || cfg.Type != "webhook" || !cfg.IsEnabled() {
		// 不区分"不存在"与"未配置密钥"的细节——探测面最小化。
		writeError(w, http.StatusNotFound, "hook: 感官不可用")
		return
	}

	body := make([]byte, sensor.HookBodyLimit+1)
	n, err := io.ReadFull(r.Body, body)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		writeError(w, http.StatusBadRequest, "hook: 载荷读取失败")
		return
	}
	if n > sensor.HookBodyLimit {
		writeError(w, http.StatusRequestEntityTooLarge, "hook: 载荷超过 64KB 上限")
		return
	}
	body = body[:n]
	ts := r.Header.Get("X-Mindloop-Timestamp")
	sig := strings.TrimPrefix(r.Header.Get("X-Mindloop-Signature"), "sha256=")
	if err := sensor.VerifyHook(cfg.Secret, ts, sig, body, time.Now()); err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}

	// 事件形态：载荷是纯文本（外部系统自己决定语义）；subject/
	// digest 由端点构造，指纹 = 载荷哈希。
	sum := sha256.Sum256(body)
	text := strings.TrimSpace(string(body))
	e := sensor.PEvent{
		Kind:    sensor.KindChanged,
		Subject: "hook:" + sensorID,
		Dedup:   hex.EncodeToString(sum[:16]),
		Digest:  text,
	}

	now := time.Now()
	st, ok := hookStates[sensorID]
	if !ok {
		st = sensor.NewState()
		hookStates[sensorID] = st
	}
	dec := sensor.JudgeHook(cfg, st, e, now)
	if dec.Duplicate {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	sal, reason := dec.Salience, dec.Reason
	// 学习期（读心智维护的 sensors-state.json，只读不写——写位归
	// mind 进程）：新 webhook 前 N 天封顶 s1，与 Watch 型感官同规。
	if firstSeen, ok := hookFirstSeen(id.Dir, sensorID); ok &&
		time.Since(firstSeen) < time.Duration(cfg.LearnDays())*24*time.Hour && sal >= sensor.S2 {
		sal, reason = sensor.S1, reason+"+learning-cap"
	}
	if cfg.Quiet.Active(now.Local()) && sal >= sensor.S2 {
		sal, reason = sensor.S1, "quiet-held:"+reason
	}

	writeCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if sal == sensor.S3 {
		// s3 走告警通道（与 mind.SensorRunner.writeAlert 同一契约）：
		// 代码写入、无 launched_by 章、coalesced 按 source 分槽。
		step := traj.NewStep(traj.TypeAlert)
		step.Fields["from"] = id.Name
		step.Fields["to"] = "operator"
		step.Fields["source"] = sensorID
		step.Fields["kind"] = sensor.KindChanged
		step.Fields["subject"] = e.Subject
		step.Fields["salience"] = string(sensor.S3)
		step.Fields["reason"] = "hook:" + reason
		step.Fields["content"] = mind.WrapSensorDigest(sensorID, e.Digest)
		if err := id.Timeline.Append(writeCtx, step); err != nil {
			writeError(w, http.StatusInternalServerError, "hook: 落盘失败")
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	step := traj.NewStep(traj.TypeEvent)
	step.Fields["source"] = sensorID
	step.Fields["kind"] = sensor.KindChanged
	step.Fields["subject"] = e.Subject
	step.Fields["dedup"] = e.Dedup
	step.Fields["salience"] = string(sal)
	step.Fields["reason"] = "hook:" + reason
	step.Fields["digest"] = mind.WrapSensorDigest(sensorID, e.Digest)
	if err := id.Timeline.Append(writeCtx, step); err != nil {
		writeError(w, http.StatusInternalServerError, "hook: 落盘失败")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
