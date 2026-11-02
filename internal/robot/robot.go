// Package robot 是"身"的机括：屏幕的看（截屏）与触（点击/输入），
// 纯 Go 经 x/sys/windows 系统调用实现——设计红线"cgo 不进主二进制"
// 由此满足，robotgo 方案（需 MinGW-w64）存档不用。
//
// 安全链（perception.md Phase 5）：截屏与动作共用敏感窗口检测——
// 密码框/密码管理器画面命中即拒绝（拒绝是最保守的打码）；动作类
// 调用方（robotd）另负窗口白名单谓词与动作后强制回读验证。
package robot

import (
	"errors"
	"strings"
	"time"
)

// ErrUnsupported 是非 Windows 平台的统一降级：编译进跨平台二进制，
// 运行到非 Windows 上如实报错，绝不伪装成功。
var ErrUnsupported = errors.New("robot: 本平台不支持（仅 Windows）")

// sensitiveTitleKeys 是敏感窗口的标题关键词（大小写不敏感）：命中
// 的前台窗口里截屏/输入一律拒绝——密码框内容与凭据画面没有理由
// 进入任何回读通道。
//
// 2026-11-02 扩表（superagent-lessons §7.0 路径⑤）：图片通道不经
// 文本打码管线直发模型供应商，标题守卫是截屏侧唯一防线——补
// token/secret/.env/证书/私钥类关键词。裸 "key" 误杀面过大
// （keyboard/keymap），用组合词；"token" 命中开发窗口（如
// tokenize.go）按「宁可多拒」哲学接受。
var sensitiveTitleKeys = []string{
	"password", "passwd", "pwd", "凭据", "密码", "口令",
	"1password", "bitwarden", "keepass", "lastpass", "dashlane",
	"credential manager", "windows 安全", "windows security",
	"token", "secret", ".env", "api key", "apikey",
	"ssh key", "private key", ".pem", ".p12", ".pfx", "id rsa",
}

// IsSensitiveTitle 报告窗口标题是否命中敏感关键词。标题匹配是
// 尽责检测不是完备检测：误杀放行的代价不对等，宁可多拒。匹配前把
// `_`/`-` 归一化为空格（generate_ssh_key 与 "ssh key" 是同一凭据
// 画面的两种标题写法），词表统一用空格/连写形态。
func IsSensitiveTitle(title string) bool {
	t := strings.ToLower(title)
	norm := strings.NewReplacer("_", " ", "-", " ").Replace(t)
	for _, k := range sensitiveTitleKeys {
		if strings.Contains(t, k) || strings.Contains(norm, k) {
			return true
		}
	}
	return false
}

// ScreenInfo 是一次截屏的几何信息：全虚拟桌面（多显示器一体）
// 的原点与尺寸，坐标与点击/光标 API 同一坐标系。
type ScreenInfo struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

// OperatorIdle 返回距操作员最后一次键盘/鼠标输入的时长。"身"在
// 操作员手不离键盘/鼠标时拒绝注入动作——不与操作员抢键盘是安全
// 链的第一道（2026-10-03 实机事故：键入中途焦点被操作员抢走，
// 半截文本漏进操作员当时的前台窗口）。非 Windows 返回 ErrUnsupported，
// 由调用方决定降档策略。
func OperatorIdle() (time.Duration, error) {
	return lastInputAge()
}
