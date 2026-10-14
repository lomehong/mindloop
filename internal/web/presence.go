// 操作员在场端点（theater.md §8）：仪表盘的在场芯片落轨迹的通道。
// GET 折叠当前态（前端打开舞台时同步一次——服务端是跨设备真相，
// localStorage 只是本机缓存）；POST 以 presence 步骤追加（结构性
// 类型，不继承 run id；与 scene/intent 同一写入纪律：只落轨迹，
// 不叫醒心智）。投递纪律（离开时 inform 镜像推企微）由 monolith
// 唤醒注入派生，本端点不直接触发任何发送。
package web

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/lomehong/mindloop/internal/identity"
	"github.com/lomehong/mindloop/internal/traj"
)

// routePresence 服务 GET/POST /api/identities/{name}/presence。
func (s *Server) routePresence(w http.ResponseWriter, r *http.Request, id *identity.Identity) {
	switch r.Method {
	case http.MethodGet:
		s.handlePresenceGet(w, r, id)
	case http.MethodPost:
		s.handlePresencePost(w, r, id)
	default:
		writeError(w, http.StatusMethodNotAllowed, "presence 只接受 GET/POST")
	}
}

// presenceView 是 GET 的响应形态：state=present|away + 本态起点。
type presenceView struct {
	State   string `json:"state"`
	SinceTS string `json:"since_ts,omitempty"`
	Updated string `json:"updated_ts,omitempty"`
	// Unstated 标记轨迹里从未声明过在场（前端此时用本机缓存展示，
	// 不冒充服务端已知）。
	Unstated bool `json:"unstated"`
}

func presenceViewOf(steps []traj.Step) presenceView {
	snap := foldPresenceSteps(steps)
	if snap.unstated {
		return presenceView{State: "present", Unstated: true}
	}
	state := "present"
	if snap.away {
		state = "away"
	}
	v := presenceView{State: state, Updated: snap.updated}
	if !snap.since.IsZero() {
		v.SinceTS = snap.since.UTC().Format(time.RFC3339)
	}
	return v
}

// presenceFold 是 web 侧的折叠视图（与 mind.FoldPresence 同一 schema
// 契约、各自实现——跨包共享折叠器会制造 web→mind 依赖边，与场景
// 折叠的解耦同构）。多带 updated（最后一次切换时刻）供前端展示。
type presenceFold struct {
	away     bool
	since    time.Time
	updated  string
	unstated bool
}

func foldPresenceSteps(steps []traj.Step) presenceFold {
	for i := len(steps) - 1; i >= 0; i-- {
		s := steps[i]
		if s.Type != traj.TypePresence {
			continue
		}
		state, _ := s.Field("state")
		if state != "away" && state != "present" {
			continue
		}
		f := presenceFold{away: state == "away"}
		if since, _ := s.Field("since"); since != "" {
			if t, err := time.Parse(time.RFC3339, since); err == nil {
				f.since = t
			}
		}
		if ts, err := time.Parse(time.RFC3339, s.TS); err == nil {
			f.updated = ts.UTC().Format(time.RFC3339)
		}
		return f
	}
	return presenceFold{unstated: true}
}

func (s *Server) handlePresenceGet(w http.ResponseWriter, r *http.Request, id *identity.Identity) {
	steps, err := id.Timeline.Steps()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读轨迹失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, presenceViewOf(steps))
}

func (s *Server) handlePresencePost(w http.ResponseWriter, r *http.Request, id *identity.Identity) {
	var req struct {
		Away bool `json:"away"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4*1024)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "presence 载荷解析失败: "+err.Error())
		return
	}
	now := time.Now()
	step := traj.NewStep(traj.TypePresence)
	step.Fields["state"] = "present"
	step.Fields["since"] = now.UTC().Format(time.RFC3339)
	if req.Away {
		step.Fields["state"] = "away"
	}
	if err := id.Timeline.Append(r.Context(), step); err != nil {
		writeError(w, http.StatusInternalServerError, "presence 落盘失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, presenceViewOf([]traj.Step{step}))
}
