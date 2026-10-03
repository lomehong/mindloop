package web

// system_page_test.go — /system 直出页的 XSS 回归钉子：配置数据
//（身份名/感官 id/target）曾以字符串拼接进 innerHTML 与内联事件
// handler，构成存储型 XSS→控制面令牌窃取的链路。此测试锁死这些
// 危险形态不得回潮。

import (
	"strings"
	"testing"
)

func TestSystemPageNoRawConfigInterpolation(t *testing.T) {
	// 危险形态：内联事件 handler 里拼动态值、innerHTML 直接拼接
	// 身份名/id——出现任一即为回潮。
	for _, bad := range []string{
		`onclick="'+`,    // 内联 handler 拼接动态实参
		`onchange="'+`,   // 同上
		`'+s.id+'`,       // 感官 id 裸拼
		`'<b>'+n+'</b>'`, // 身份名裸拼
	} {
		if strings.Contains(systemPageHTML, bad) {
			t.Fatalf("/system 页出现未转义的动态插值形态: %q", bad)
		}
	}
	for _, want := range []string{
		"let esc =",        // 转义入口存在
		"esc(n)",           // 身份名过转义
		"esc(e.message)",   // 错误消息转义
		"textContent=s.id", // 感官 id 走 textContent
	} {
		if !strings.Contains(systemPageHTML, want) {
			t.Fatalf("/system 页缺少转义形态: %q", want)
		}
	}
}
