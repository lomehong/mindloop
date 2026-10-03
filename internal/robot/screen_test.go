package robot

import (
	"bytes"
	"image/png"
	"runtime"
	"strings"
	"testing"
)

func TestIsSensitiveTitle(t *testing.T) {
	sensitive := []string{
		"密码管理器", "1Password - 主保险库", "Bitwarden Password Manager",
		"KeePass", "Windows 安全中心", "User Account Control: 凭据", "PASSWORD.vault",
	}
	for _, s := range sensitive {
		if !IsSensitiveTitle(s) {
			t.Fatalf("%q 应判敏感", s)
		}
	}
	normal := []string{"记事本", "Visual Studio Code", "mindloop 仪表盘", "README.md - Notepad"}
	for _, s := range normal {
		if IsSensitiveTitle(s) {
			t.Fatalf("%q 不应判敏感", s)
		}
	}
}

func TestScreenshotPNG(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("截屏仅 Windows")
	}
	data, info, err := ScreenshotPNG()
	if err != nil {
		t.Skipf("截屏失败（无交互桌面会话的 CI 也常见）: %v", err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("产出不是合法 PNG: %v", err)
	}
	if img.Bounds().Dx() != info.Width || img.Bounds().Dy() != info.Height {
		t.Fatalf("PNG 尺寸 %v 与 ScreenInfo %dx%d 不符", img.Bounds(), info.Width, info.Height)
	}
	if info.Width < 640 || info.Height < 480 {
		t.Fatalf("桌面尺寸异常小: %dx%d", info.Width, info.Height)
	}
	if len(data) < 1024 {
		t.Fatalf("PNG 过小 %d 字节，疑似黑屏失败", len(data))
	}
}

func TestForegroundAndWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("窗口 API 仅 Windows")
	}
	title, ok := ForegroundTitle()
	if ok && title == "" {
		t.Fatal("ok=true 时标题不应为空")
	}
	// 枚举只验证不 panic、无空标题条目。
	for _, s := range WindowTitles() {
		if strings.TrimSpace(s) == "" {
			t.Fatal("枚举不应包含空标题")
		}
	}
}

func TestFocusWindowEmptyReject(t *testing.T) {
	if _, err := FocusWindow("  "); err == nil {
		t.Fatal("空匹配串应被拒")
	}
}

func TestTypeTextRejectsControlChars(t *testing.T) {
	// 拒绝发生在任何键击合成之前——即使真的注入也不会发出。
	if err := TypeText("ok\x00断"); err == nil || !strings.Contains(err.Error(), "控制字符") {
		t.Fatalf("控制字符应被拒: %v", err)
	}
	if err := TypeText(""); err == nil {
		t.Fatal("空文本应被拒")
	}
	if err := TypeText("正常文本\n第二行\t缩进"); err != nil {
		t.Fatalf("常规文本（含换行/制表）不应被拒: %v", err)
	}
}

func TestClickOutsideScreen(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("仅 Windows")
	}
	// 越界拒绝发生在任何鼠标合成之前——不会真的点击。
	if err := Click(-999999, -999999); err == nil || !strings.Contains(err.Error(), "虚拟桌面") {
		t.Fatalf("越界坐标应被拒: %v", err)
	}
}
