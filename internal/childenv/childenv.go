// Package childenv 为子进程（沙箱脚本、MCP stdio 服务器）构建环境：
// 明确允许的键集合 + 显式扩展，父环境里的敏感键（凭据类）绝不
// 下传。键名匹配一律大小写不敏感——Windows 环境变量不区分大小写，
// 大小写变体不能成为绕行通道（Linux 上更保守也无害）。
//
// 给子进程提供凭据的唯一通道是显式值（沙箱的 Request.Env、MCP 的
// mcp.json env），而不是父环境继承。
package childenv

import "strings"

// allow 是自动继承的键集合：明确运行所需的系统、用户、区域、代理
// 与自身定位变量。白名单之外的键默认不下传。
var allow = map[string]bool{
	// 进程启动与系统根
	"PATH":        true,
	"PATHEXT":     true,
	"COMSPEC":     true,
	"SYSTEMDRIVE": true,
	"SYSTEMROOT":  true,
	"WINDIR":      true,
	// 临时目录
	"TEMP":   true,
	"TMP":    true,
	"TMPDIR": true,
	// 用户与家目录
	"HOME":               true,
	"USERPROFILE":        true,
	"HOMEDRIVE":          true,
	"HOMEPATH":           true,
	"APPDATA":            true,
	"LOCALAPPDATA":       true,
	"PROGRAMDATA":        true,
	"PROGRAMFILES":       true,
	"PROGRAMFILES(X86)":  true,
	"COMMONPROGRAMFILES": true,
	// 用户标识（git 等工具读取）
	"USERNAME":   true,
	"USER":       true,
	"USERDOMAIN": true,
	// 区域、终端与并行度
	"LANG":                   true,
	"LANGUAGE":               true,
	"LC_ALL":                 true,
	"LC_CTYPE":               true,
	"TERM":                   true,
	"TZ":                     true,
	"MSYSTEM":                true,
	"NUMBER_OF_PROCESSORS":   true,
	"PROCESSOR_ARCHITECTURE": true,
	// 代理与证书（受限网络与自签环境的现实需求）
	"HTTP_PROXY":          true,
	"HTTPS_PROXY":         true,
	"NO_PROXY":            true,
	"ALL_PROXY":           true,
	"SSL_CERT_FILE":       true,
	"SSL_CERT_DIR":        true,
	"NODE_EXTRA_CA_CERTS": true,
	"REQUESTS_CA_BUNDLE":  true,
	"CURL_CA_BUNDLE":      true,
	// mindloop 自身定位（子进程里的 $MINDLOOP_EXE 需要找到 home）
	"MINDLOOP_HOME": true,
}

// sensitivePatterns 是凭据类键名的识别子串；sensitiveSuffixes 是
// 后缀规则。只识别明确的凭据词：AUTHOR 含 AUTH、KEYBOARD 含 KEY
// 这类子串陷阱不能进规则（误伤白名单键等于把执行环境切坏）。
var (
	sensitivePatterns = []string{"TOKEN", "SECRET", "PASSWORD", "PASSWD", "CREDENTIAL", "API_KEY", "APIKEY", "ACCESS_KEY"}
	sensitiveSuffixes = []string{"_KEY"}
)

// Sensitive 报告键名是否像凭据（大小写不敏感）。
func Sensitive(name string) bool {
	up := strings.ToUpper(name)
	for _, p := range sensitivePatterns {
		if strings.Contains(up, p) {
			return true
		}
	}
	for _, s := range sensitiveSuffixes {
		if strings.HasSuffix(up, s) {
			return true
		}
	}
	return false
}

// Inherit 从父环境 parent 中选择可下传的条目：键名在 allow 内或在
// extra（显式扩展）内、且不是敏感键。敏感键即使被 extra 点名也不
// 下传——凭据的通道是显式值，不是继承。
func Inherit(parent, extra []string) []string {
	out := make([]string, 0, len(parent))
	for _, kv := range parent {
		name, _, ok := strings.Cut(kv, "=")
		if !ok || name == "" {
			continue
		}
		if Sensitive(name) || !allowed(name, extra) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// allowed 报告键名是否在自动白名单或显式扩展内（大小写不敏感）。
func allowed(name string, extra []string) bool {
	if allow[strings.ToUpper(name)] {
		return true
	}
	for _, e := range extra {
		if strings.EqualFold(e, name) {
			return true
		}
	}
	return false
}

// List 解析显式扩展键名列表（逗号或分号分隔；空白与空项容忍）。
func List(s string) []string {
	var out []string
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' }) {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
