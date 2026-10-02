package web

// /api/identities/{id}/channels——外部渠道桥（bridge）的配置面：
// 读取身份 .env 的渠道键与出站游标状态；写入经 .env 原位合并
//（保留行序与注释、0600）。凭据只报"已配置与否"，绝不回显——
// 与 connections 页的 env/headers 同一红线。
//
// 渠道清单是静态注册表（channelWecom 视图 + applyWecom 写入）：
// 加渠道（微信客服/钉钉/飞书）时在此登记，前端表单按 channel 名
// 泛化渲染。

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/lomehong/mindloop/internal/childenv"
	"github.com/lomehong/mindloop/internal/identity"
)

// channelView 是一个渠道的配置态（机器消费面）。
type channelView struct {
	Channel   string   `json:"channel"`
	Label     string   `json:"label"`
	BotID     string   `json:"bot_id"`     // 非敏感标识，明文
	SecretSet bool     `json:"secret_set"` // 凭据只报 set 与否
	Allow     []string `json:"allow"`      // 白名单 userid
	Ready     bool     `json:"ready"`      // 三项齐备（可启动 bridge）
	Cursor    bool     `json:"cursor_exists"`
	Note      string   `json:"note"`
}

// wecomEnvKeys 是企微渠道在身份 .env 里的键名（与 CLI connector
// 命令同一约定）。
const (
	wecomKeyBotID  = "WECOM_BOT_ID"
	wecomKeySecret = "WECOM_BOT_SECRET"
	wecomKeyAllow  = "WECOM_ALLOW"
)

// handleChannels GET /api/identities/{id}/channels 与
// PUT /api/identities/{id}/channels/{channel}。
func (s *Server) handleChannels(w http.ResponseWriter, r *http.Request, id *identity.Identity, rest []string) {
	if len(rest) == 0 {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		writeJSON(w, 200, map[string]any{"channels": []channelView{s.wecomView(id)}})
		return
	}
	if !requireMethod(w, r, http.MethodPut) {
		return
	}
	switch rest[0] {
	case "wecom":
		s.handleChannelWecomPut(w, r, id)
	default:
		writeError(w, 404, "未知渠道: "+rest[0])
	}
}

// wecomView 从"显式进程环境 > 身份 .env"读企微配置（与 CLI
// connector 的加载语义一致），出站游标状态从 connectors 目录读。
func (s *Server) wecomView(id *identity.Identity) channelView {
	lookup := envLookup(identityEnvVars(id.Dir))
	botID := lookup(wecomKeyBotID)
	secret := lookup(wecomKeySecret)
	allow := childenv.List(lookup(wecomKeyAllow))
	if allow == nil {
		// nil 切片会被 JSON 序列化成 null，前端对它的 .length 访问
		// 直接崩进错误边界——空配置必须归一为 []。
		allow = []string{}
	}
	_, cursorErr := os.Stat(filepath.Join(id.Dir, "connectors", "wecom-outbound.cursor"))
	return channelView{
		Channel:   "wecom",
		Label:     "企业微信智能机器人",
		BotID:     botID,
		SecretSet: strings.TrimSpace(secret) != "",
		Allow:     allow,
		Ready:     botID != "" && strings.TrimSpace(secret) != "" && len(allow) > 0,
		Cursor:    cursorErr == nil,
		Note: "在企微管理后台「应用 → 智能机器人 → API 接收事件 → 长连接」获取 BotID 与 Secret；" +
			"白名单外的消息不落轨迹（fail-closed）。用户需先给机器人发过一条消息（解锁会话）才能收到主动推送。",
	}
}

// handleChannelWecomPut 写入企微渠道配置：bot_id 与 allow 整体替换，
// secret 留空 = 保持既有值（表单不回显凭据的自然推论）。
func (s *Server) handleChannelWecomPut(w http.ResponseWriter, r *http.Request, id *identity.Identity) {
	var req struct {
		BotID  string   `json:"bot_id"`
		Secret string   `json:"secret"`
		Allow  []string `json:"allow"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, 400, "请求体解析失败: "+err.Error())
		return
	}
	patch := map[string]string{}
	if botID := strings.TrimSpace(req.BotID); botID != "" {
		patch[wecomKeyBotID] = botID
	}
	if secret := strings.TrimSpace(req.Secret); secret != "" {
		patch[wecomKeySecret] = secret
	}
	// allow 允许传数组（前端表单逐项）或整段重置；空白项剔除。
	var allow []string
	for _, a := range req.Allow {
		if a = strings.TrimSpace(a); a != "" {
			allow = append(allow, a)
		}
	}
	if req.Allow != nil {
		if len(allow) == 0 {
			writeError(w, 400, "白名单不能为空（红线：无白名单不启动 bridge）")
			return
		}
		patch[wecomKeyAllow] = strings.Join(allow, ",")
	}
	if len(patch) == 0 {
		writeError(w, 400, "没有可写入的字段（secret 留空 = 保持既有值）")
		return
	}
	if err := writeIdentityEnvPatch(id.Dir, patch); err != nil {
		writeError(w, 500, "写入身份 .env 失败: "+err.Error())
		return
	}
	writeJSON(w, 200, s.wecomView(id))
}

// writeIdentityEnvPatch 把 patches 原位合并进身份 .env：已有键在
// 原行位置更新（保留行序与注释），新键追加文件尾；权限 0600——
// 密钥文件的对等形态。
func writeIdentityEnvPatch(idDir string, patches map[string]string) error {
	path := filepath.Join(idDir, ".env")
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	lines := strings.Split(string(data), "\n")
	seen := map[string]bool{}
	for i, ln := range lines {
		trimmed := strings.TrimSpace(ln)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if k, _, ok := strings.Cut(trimmed, "="); ok {
			k = strings.TrimSpace(k)
			if _, hit := patches[k]; hit {
				lines[i] = k + "=" + patches[k]
				seen[k] = true
			}
		}
	}
	var extra []string
	for k, v := range patches {
		if !seen[k] {
			extra = append(extra, k+"="+v)
		}
	}
	if len(extra) > 0 {
		if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
			lines = append(lines, "")
		}
		lines = append(lines, extra...)
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o600)
}
