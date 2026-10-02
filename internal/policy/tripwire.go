package policy

import (
	"os"
	"regexp"
)

// 不可逆操作守卫（tripwire）——trusted 策略的最后防线。
//
// trusted 的语义是"用户显式选择，风险自担"，但它防不住一件事：
// 无人值守场景下，模型被注入内容诱导生成一条灾难命令——没人看，
// 也就没有审批这一关。守卫只拦极窄的灾难模式（删根、管道给
// shell、写裸设备、格盘、读 SSH 私钥、覆写自身 .env），命中率
// 优先精确：误伤合法命令的代价是把用户逼向关闭守卫，守卫就白装了。
//
// ask 策略不需要它拒绝——反正有人看正文；守卫的命中只作为醒目
// 风险提示进待批请求。关闭开关：MINDLOOP_EXEC_TRIPWIRE=0。

// TripwireFromEnv 读取 MINDLOOP_EXEC_TRIPWIRE：值 "0" 关闭守卫，
// 其余（含未设置）开启。
func TripwireFromEnv() bool { return os.Getenv("MINDLOOP_EXEC_TRIPWIRE") != "0" }

// tripwirePatterns 是灾难模式清单（v1 刻意极窄）。每条注释都要
// 能回答"它拦的是哪次事故"。
var tripwirePatterns = []riskPattern{
	// 下载即执行：远程内容直通 shell——注入的经典终点。理由文案
	// 与 riskPatterns 同名条目保持一致，审批卡去重后只显示一次。
	{regexp.MustCompile(`(?i)\b(curl|wget)\b[^|]*\|\s*(sudo\s+)?(ba|z|d|k)?sh\b`),
		"把网络内容直接管道给 shell 执行"},
	// 删根族：目标恰好是根、家目录或当前目录整体——-rf build/ 不在列。
	{regexp.MustCompile(`(?i)\brm\s+(-[a-zA-Z]*[rf][a-zA-Z]*\s+)+(/|/\*|~|~/\*|\$HOME|\$HOME/\.|\.)(\s|$)`),
		"递归删除根/家/当前目录（rm -rf /、~、.）"},
	// 裸设备写入与格盘：一条命令毁掉整块盘。
	{regexp.MustCompile(`(?i)\bdd\b[^|;\n]*\bof=/dev/`), "向裸设备写入（dd of=/dev/*）"},
	{regexp.MustCompile(`(?i)\bmkfs(\.[a-z0-9]+)?\b`), "格式化文件系统（mkfs）"},
	// fork 炸弹的经典形态。
	{regexp.MustCompile(`:\(\)\s*\{.*\|.*&.*\}\s*;\s*:`), "fork 炸弹"},
	// SSH 私钥外带的前置动作：读或打包私钥文件（公钥不在列）。
	{regexp.MustCompile(`(?i)\b(cat|cp|mv|tar|zip|base64|less)\b[^|;\n]*\.ssh/(id_|identity)`),
		"读取 SSH 私钥"},
	// 覆写自身配置：沙箱脚本改写 .env 等于 agent 给自己换凭据。
	{regexp.MustCompile(`(?i)(>\s*\.env(\s|$)|>>\s*\.env(\s|$)|\btee\s+(-a\s+)?[^|;\n]*\.env\b)`),
		"覆写/追加 .env 配置文件"},
}

// TripwireHits 返回脚本命中的守卫模式（人读的理由）。空切片 =
// 未命中，trusted 照常执行。
func TripwireHits(script string) []string {
	var out []string
	for _, p := range tripwirePatterns {
		if p.re.MatchString(script) {
			out = append(out, p.note)
		}
	}
	return out
}

// mergeRisks 合并展示用的风险提示并去重——守卫命中与 riskPatterns
// 启发式有交集，审批卡上同一条理由不该出现两次。
func mergeRisks(base, extra []string) []string {
	seen := map[string]bool{}
	for _, r := range base {
		seen[r] = true
	}
	for _, r := range extra {
		if !seen[r] {
			base = append(base, r)
			seen[r] = true
		}
	}
	return base
}
