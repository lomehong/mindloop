package web

// sensors_api.go — 感官管理 API（统一配置面的感知项，与 CLI
// sensors 同一份 sensor 包校验；sensors.json 是事实源，写盘即生效
// ——心智/宿主侧热加载兑现）：
//   GET    /api/identities/{名}/sensors                列表
//   POST   /api/identities/{名}/sensors                新增（整条 JSON）
//   PUT    /api/identities/{名}/sensors/{id}/enabled   启停 {"enabled":bool}
//   DELETE /api/identities/{名}/sensors/{id}           移除

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lomehong/mindloop/internal/connector/sensor"
	"github.com/lomehong/mindloop/internal/identity"
)

// atomicWrite 原子写（临时文件 + 改名）。
func atomicWrite(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func itoaLen(n int) string { return strconv.Itoa(n) }

// routeIdentityOrSensors 是 identities 路由的包装：/sensors 形态
// 进感官管理，其余交回既有 routeIdentity（注册点在 routes.go——
// 同一路径模式不能双注册）。
func (s *Server) routeIdentityOrSensors(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/identities/")
	parts := strings.Split(rest, "/")
	if len(parts) >= 2 && parts[1] == "sensors" {
		id, err := identity.Load(parts[0])
		if err != nil {
			writeError(w, http.StatusNotFound, "身份不存在")
			return
		}
		s.handleSensors(w, r, id, parts[2:])
		return
	}
	s.routeIdentity(w, r)
}

func loadIdentitySensors(id *identity.Identity) (*sensor.File, error) {
	f, err := sensor.Load(filepath.Join(id.Dir, "sensors.json"))
	if err != nil {
		return nil, err
	}
	if f == nil {
		f = &sensor.File{Version: 1}
	}
	return f, nil
}

func saveIdentitySensors(id *identity.Identity, f *sensor.File) error {
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if err := f.Validate(); err != nil {
		return err
	}
	return atomicWrite(filepath.Join(id.Dir, "sensors.json"), data)
}

// handleSensors 按 rest 分流四个操作。
func (s *Server) handleSensors(w http.ResponseWriter, r *http.Request, id *identity.Identity, rest []string) {
	f, err := loadIdentitySensors(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	switch {
	case len(rest) == 0 && r.Method == http.MethodGet:
		sensorViews := make([]sensor.SensorConfig, len(f.Sensors))
		copy(sensorViews, f.Sensors)
		writeJSON(w, 200, map[string]any{"sensors": sensorViews})
	case len(rest) == 0 && r.Method == http.MethodPost:
		var cfg sensor.SensorConfig
		if json.NewDecoder(r.Body).Decode(&cfg) != nil {
			writeError(w, http.StatusBadRequest, "sensors: 配置不是合法 JSON")
			return
		}
		if cfg.ID == "" {
			cfg.ID = cfg.Type + "-" + id.Name + "-" + itoaLen(len(f.Sensors)+1)
		}
		if f.Get(cfg.ID) != nil {
			writeError(w, http.StatusConflict, "感官 id 已存在: "+cfg.ID)
			return
		}
		f.Sensors = append(f.Sensors, cfg)
		if err := saveIdentitySensors(id, f); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"added": cfg.ID})
	case len(rest) == 2 && rest[1] == "enabled" && r.Method == http.MethodPut:
		var body struct {
			Enabled bool `json:"enabled"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			writeError(w, http.StatusBadRequest, "sensors: body 需要 {\"enabled\":bool}")
			return
		}
		cfg := f.Get(rest[0])
		if cfg == nil {
			writeError(w, http.StatusNotFound, "感官不存在: "+rest[0])
			return
		}
		cfg.Enabled = &body.Enabled
		if err := saveIdentitySensors(id, f); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"id": rest[0], "enabled": body.Enabled})
	case len(rest) == 1 && r.Method == http.MethodDelete:
		for i, cfg := range f.Sensors {
			if cfg.ID == rest[0] {
				f.Sensors = append(f.Sensors[:i], f.Sensors[i+1:]...)
				if err := saveIdentitySensors(id, f); err != nil {
					writeError(w, http.StatusBadRequest, err.Error())
					return
				}
				writeJSON(w, 200, map[string]any{"removed": rest[0]})
				return
			}
		}
		writeError(w, http.StatusNotFound, "感官不存在: "+rest[0])
	default:
		writeError(w, http.StatusNotFound, "未知子路径: sensors/"+strings.Join(rest, "/"))
	}
}
