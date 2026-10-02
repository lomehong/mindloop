// webhook.go — webhook 感官（耳）的验证与判定件。webhook 的观察者
// 不是 Watch 循环而是 HTTP 端点（宿主在 service/web 进程，收到的
// 事件按同一 event 步骤契约落轨迹，心智经 feeder 看见——日志即
// API，不需要跨进程 IPC）。本文件只放两块纯逻辑：
//   - HMAC 签名验证（独立 per-sensor 凭据，与控制面 token 隔离
//     ——评审钉死：控制面 bearer 是仪表盘管理员凭据，共用等于
//     把启停心智/killall 交给外部系统）；
//   - 钩子事件的判定（规则表 + 保守缺省 s2——显式配置的对端）。
package sensor

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"time"
)

// HookAuthWindow 是时间戳防重放的容差：签名材料含时间戳，超出
// 窗口的请求拒绝——截获的载荷不能无限期重放。
const HookAuthWindow = 5 * time.Minute

// VerifyHook 校验一条 webhook 请求：签名 = HMAC-SHA256(secret,
// "<ts>.<body>")，hex 编码；|now-ts| ≤ HookAuthWindow。比较走
// hmac.Equal（常数时间）。
func VerifyHook(secret, ts, sig string, body []byte, now time.Time) error {
	if secret == "" {
		return fmt.Errorf("hook: 感官未配置密钥")
	}
	tsN, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return fmt.Errorf("hook: 时间戳非法")
	}
	if d := now.Sub(time.Unix(tsN, 0)); d < -HookAuthWindow || d > HookAuthWindow {
		return fmt.Errorf("hook: 时间戳超出 ±%s 窗口（防重放）", HookAuthWindow)
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts))
	mac.Write([]byte("."))
	mac.Write(body)
	want := mac.Sum(nil)
	got, err := hex.DecodeString(sig)
	if err != nil {
		return fmt.Errorf("hook: 签名非 hex")
	}
	if !hmac.Equal(want, got) {
		return fmt.Errorf("hook: 签名不匹配")
	}
	return nil
}

// HookBodyLimit 是钩子载荷的 intake 上限——digest ≤200 字是反射层
// 之后的事，1GB 的 body 不能照收（评审钉死）。
const HookBodyLimit = 64 << 10

// JudgeHook 判定钩子事件：webhook 的议程就是端点本身（空视图），
// 缺省 s2（显式配置的对端），规则表可升 s3 或降 s1。静默降档
// （s0）由调用方在配置里写 salience.default。
func JudgeHook(cfg *SensorConfig, st *State, e PEvent, now time.Time) Decision {
	return Judge(cfg, st, ReflexView{}, e, now)
}
