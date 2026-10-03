package web

// sensors_api.go — 感官管理 API（统一配置面的感知项，与 CLI
// sensors 同一份 sensor 包校验；sensors.json 是事实源，写盘即生效
// ——心智/宿主侧热加载兑现）：
//   GET    /api/identities/{名}/sensors                列表
//   POST   /api/identities/{名}/sensors                新增（整条 JSON）
//   PUT    /api/identities/{名}/sensors/{id}/enabled   启停 {"enabled":bool}
//   DELETE /api/identities/{名}/sensors/{id}           移除

import (
	cryptorand "crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/lomehong/mindloop/internal/connector/sensor"
	"github.com/lomehong/mindloop/internal/identity"
	"github.com/lomehong/mindloop/internal/obs"
)

// atomicWrite 原子写（临时文件 + 改名）。0600：sensors.json 含
// webhook HMAC 密钥（凭据文件，同 .env 待遇）。
func atomicWrite(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func itoaLen(n int) string { return strconv.Itoa(n) }

// newHookSecret 生成 webhook 的 HMAC 密钥（32 字节随机，base64）。
func newHookSecret() string {
	buf := make([]byte, 32)
	if _, err := cryptorand.Read(buf); err != nil {
		return ""
	}
	return base64.RawStdEncoding.EncodeToString(buf)
}

// routeIdentityOrSensors 是 identities 路由的包装：/sensors 与
// /taste 形态进感知面，其余交回既有 routeIdentity（注册点在
// routes.go——同一路径模式不能双注册）。
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
	if len(parts) == 2 && parts[1] == "taste" && r.Method == http.MethodGet {
		id, err := identity.Load(parts[0])
		if err != nil {
			writeError(w, http.StatusNotFound, "身份不存在")
			return
		}
		windowStart := time.Now().Add(-7 * 24 * time.Hour)
		sum, err := obs.DeriveTaste(id.Timeline, id.Name, windowStart)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		sum = sum.WithSuggestions()
		writeJSON(w, 200, sum)
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
		for i := range sensorViews {
			// 凭据不回显（system.md §3 红线）：HMAC 密钥只在创建
			// 响应里出现一次，列表永远剥除——刷新页面不能再次读出。
			sensorViews[i].Secret = ""
		}
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
		// webhook 的 HMAC 密钥在此生成（与 CLI 同规）：独立 per-sensor
		// 凭据，只随本次响应回显一次。
		var secretOnce string
		if cfg.Type == "webhook" && cfg.Secret == "" {
			cfg.Secret = newHookSecret()
			if cfg.Secret == "" {
				writeError(w, http.StatusInternalServerError, "sensors: HMAC 密钥生成失败，请重试")
				return
			}
			secretOnce = cfg.Secret
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
		resp := map[string]any{"added": cfg.ID}
		if secretOnce != "" {
			resp["secret"] = secretOnce
			resp["secret_note"] = "HMAC 签名密钥，只显示这一次"
		}
		writeJSON(w, 200, resp)
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
