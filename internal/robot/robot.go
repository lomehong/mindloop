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
)

// ErrUnsupported 是非 Windows 平台的统一降级：编译进跨平台二进制，
// 运行到非 Windows 上如实报错，绝不伪装成功。
var ErrUnsupported = errors.New("robot: 本平台不支持（仅 Windows）")

// sensitiveTitleKeys 是敏感窗口的标题关键词（大小写不敏感）：命中
// 的前台窗口里截屏/输入一律拒绝——密码框内容与凭据画面没有理由
// 进入任何回读通道。
var sensitiveTitleKeys = []string{
	"password", "passwd", "pwd", "凭据", "密码", "口令",
	"1password", "bitwarden", "keepass", "lastpass", "dashlane",
	"credential manager", "windows 安全", "windows security",
}

// IsSensitiveTitle 报告窗口标题是否命中敏感关键词。标题匹配是
// 尽责检测不是完备检测：误杀放行的代价不对等，宁可多拒。
func IsSensitiveTitle(title string) bool {
	t := strings.ToLower(title)
	for _, k := range sensitiveTitleKeys {
		if strings.Contains(t, k) {
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
