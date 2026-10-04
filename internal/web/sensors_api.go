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
	"github.com/lomehong/mindloop/internal/mcp"
	"github.com/lomehong/mindloop/internal/obs"
	"github.com/lomehong/mindloop/internal/traj"
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

// robotdStatus 从全局+身份 mcp.json 的合并视图读"身"（robotd）的
// 授权面——只报配置事实（是否接入、观察/动作模式、白名单子串），
// 不探测进程存活性（robotd 按调用起停，常驻与否不是状态）。
func robotdStatus(idDir string) map[string]any {
	cfg, err := mcp.LoadConfig(filepath.Join(traj.Home(), "mcp.json"), filepath.Join(idDir, "mcp.json"))
	if err != nil {
		return map[string]any{"configured": false, "error": err.Error()}
	}
	sc, ok := cfg.MCPServers["robotd"]
	if !ok {
		return map[string]any{"configured": false}
	}
	allow := parseWindowAllow(sc.Args)
	mode := "observe"
	if len(allow) > 0 {
		mode = "action"
	}
	return map[string]any{"configured": true, "mode": mode, "allow": allow}
}

// parseWindowAllow 扫 robotd 启动参数里的 --window-allow（空格与
// 等号两种形态都认）。
func parseWindowAllow(args []string) []string {
	out := []string{}
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--window-allow" && i+1 < len(args):
			out = append(out, args[i+1])
			i++
		case strings.HasPrefix(args[i], "--window-allow="):
			out = append(out, strings.TrimPrefix(args[i], "--window-allow="))
		}
	}
	return out
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
		writeJSON(w, 200, map[string]any{"sensors": sensorViews, "robotd": robotdStatus(id.Dir)})
	case len(rest) == 0 && r.Method == http.MethodPost:
		var cfg sensor.SensorConfig
		if json.NewDecoder(r.Body).Decode(&cfg) != nil {
			writeError(w, http.StatusBadRequest, "sensors: 配置不是合法 JSON")
			return
		}
		if cfg.ID == "" {
			// 自动取号要跳过删除史留下的缺号：len+1 会撞上仍在役的
			// 旧 id（2026-10-04 全系统测试 #5：409「感官 id 已存在」）。
			base := cfg.Type + "-" + id.Name + "-"
			for n := len(f.Sensors) + 1; ; n++ {
				if f.Get(base+itoaLen(n)) == nil {
					cfg.ID = base + itoaLen(n)
					break
				}
			}
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
