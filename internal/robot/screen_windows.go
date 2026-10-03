//go:build windows

// Windows 实现：GDI 截屏 + SendInput 键鼠合成 + 窗口枚举，全部经
// golang.org/x/sys/windows 的 LazyProc 系统调用——无 cgo，主二进制
// 与 robotd 共用同一实现。截屏坐标是全虚拟桌面（多显示器一体）的
// 物理像素（进程声明 DPI aware，不被系统虚拟化缩放）。
package robot

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/png"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modUser32 = windows.NewLazySystemDLL("user32.dll")
	modGdi32  = windows.NewLazySystemDLL("gdi32.dll")

	procGetDC               = modUser32.NewProc("GetDC")
	procReleaseDC           = modUser32.NewProc("ReleaseDC")
	procGetSystemMetrics    = modUser32.NewProc("GetSystemMetrics")
	procSetProcessDPIAware  = modUser32.NewProc("SetProcessDPIAware")
	procGetForegroundWindow = modUser32.NewProc("GetForegroundWindow")
	procGetWindowTextW      = modUser32.NewProc("GetWindowTextW")
	procGetWindowRect       = modUser32.NewProc("GetWindowRect")
	procSetForegroundWindow = modUser32.NewProc("SetForegroundWindow")
	procEnumWindows         = modUser32.NewProc("EnumWindows")
	procIsWindowVisible     = modUser32.NewProc("IsWindowVisible")
	procSendInput           = modUser32.NewProc("SendInput")
	procSetCursorPos        = modUser32.NewProc("SetCursorPos")
	procGetCursorPos        = modUser32.NewProc("GetCursorPos")

	procCreateCompatibleDC     = modGdi32.NewProc("CreateCompatibleDC")
	procCreateCompatibleBitmap = modGdi32.NewProc("CreateCompatibleBitmap")
	procSelectObject           = modGdi32.NewProc("SelectObject")
	procDeleteObject           = modGdi32.NewProc("DeleteObject")
	procDeleteDC               = modGdi32.NewProc("DeleteDC")
	procBitBlt                 = modGdi32.NewProc("BitBlt")
	procGetDIBits              = modGdi32.NewProc("GetDIBits")
)

const (
	smXVirtualScreen  = 76
	smYVirtualScreen  = 77
	smCXVirtualScreen = 78
	smCYVirtualScreen = 79

	srccopy = 0x00CC0020
	biRGB   = 0
	dibRGB  = 0

	inputMouse    = 0
	inputKeyboard = 1

	mouseeventfLeftdown = 0x0002
	mouseeventfLeftup   = 0x0004
	mouseeventfRightdown = 0x0008
	mouseeventfRightup   = 0x0010

	keyeventfKeyup    = 0x0002
	keyeventfUnicode  = 0x0004
	vkReturn          = 0x0D
	vkTab             = 0x09
	vkMenu            = 0x12

	// inputUnionBytes 是 INPUT 联合体最大成员 MOUSEINPUT 在 64 位
	// 上的字节数（dx/dy/mouseData/dwFlags/time + 对齐 + dwExtraInfo）。
	inputUnionBytes = 32
)

var dpiOnce sync.Once

// ensureDPIAware 声明物理像素坐标：不声明的话 GetSystemMetrics 返回
// 被缩放虚拟化的逻辑值，截屏与点击坐标会在高 DPI 屏上错位。
func ensureDPIAware() {
	dpiOnce.Do(func() { procSetProcessDPIAware.Call() })
}

// screenBounds 返回虚拟桌面的原点与尺寸。
func screenBounds() ScreenInfo {
	get := func(i int) int { v, _, _ := procGetSystemMetrics.Call(uintptr(i)); return int(int32(v)) }
	return ScreenInfo{X: get(smXVirtualScreen), Y: get(smYVirtualScreen), Width: get(smCXVirtualScreen), Height: get(smCYVirtualScreen)}
}

// ScreenshotPNG 截取全虚拟桌面并编码为 PNG。返回的几何信息与
// Click/CursorPos 同一坐标系。受限桌面（UAC 提权框等）会截到黑屏
// 或整帧失败——GDI 的物理边界，调用方按回读内容自行判断。
func ScreenshotPNG() ([]byte, ScreenInfo, error) {
	if runtime.GOOS != "windows" {
		return nil, ScreenInfo{}, ErrUnsupported
	}
	ensureDPIAware()
	info := screenBounds()
	if info.Width <= 0 || info.Height <= 0 {
		return nil, info, fmt.Errorf("robot: 虚拟桌面尺寸异常 %dx%d（无交互桌面会话？）", info.Width, info.Height)
	}
	hdc, _, err := procGetDC.Call(0)
	if hdc == 0 {
		return nil, info, fmt.Errorf("robot: GetDC 失败: %v", err)
	}
	defer procReleaseDC.Call(0, hdc)

	hmem, _, err := procCreateCompatibleDC.Call(hdc)
	if hmem == 0 {
		return nil, info, fmt.Errorf("robot: CreateCompatibleDC 失败: %v", err)
	}
	defer procDeleteDC.Call(hmem)

	hbmp, _, err := procCreateCompatibleBitmap.Call(hdc, uintptr(int32(info.Width)), uintptr(int32(info.Height)))
	if hbmp == 0 {
		return nil, info, fmt.Errorf("robot: CreateCompatibleBitmap 失败: %v", err)
	}
	defer procDeleteObject.Call(hbmp)

	old, _, _ := procSelectObject.Call(hmem, hbmp)
	procBitBlt.Call(hmem, 0, 0, uintptr(int32(info.Width)), uintptr(int32(info.Height)),
		hdc, uintptr(int32(info.X)), uintptr(int32(info.Y)), srccopy)
	// GetDIBits 前把位图从 DC 摘下（GDI 文档的建议序列）。
	procSelectObject.Call(hmem, old)

	// BITMAPINFOHEADER（40 字节）+ 32bpp 不需要调色板，补 4 字节
	// 余量。biHeight 取负——自顶向下的行序，和 image.RGBA 一致。
	bi := make([]byte, 44)
	binary.LittleEndian.PutUint32(bi[0:], 40)
	binary.LittleEndian.PutUint32(bi[4:], uint32(int32(info.Width)))
	binary.LittleEndian.PutUint32(bi[8:], uint32(-int32(info.Height)))
	binary.LittleEndian.PutUint16(bi[12:], 1)
	binary.LittleEndian.PutUint16(bi[14:], 32)
	binary.LittleEndian.PutUint32(bi[16:], biRGB)

	raw := make([]byte, info.Width*info.Height*4)
	r, _, err := procGetDIBits.Call(hmem, hbmp, 0, uintptr(int32(info.Height)),
		uintptr(unsafe.Pointer(&raw[0])), uintptr(unsafe.Pointer(&bi[0])), dibRGB)
	if r == 0 {
		return nil, info, fmt.Errorf("robot: GetDIBits 失败: %v", err)
	}

	img := image.NewRGBA(image.Rect(0, 0, info.Width, info.Height))
	for i := 0; i < len(raw); i += 4 {
		img.Pix[i+0] = raw[i+2] // R ← B
		img.Pix[i+1] = raw[i+1] // G
		img.Pix[i+2] = raw[i+0] // B ← R
		img.Pix[i+3] = 255      // GDI 不给 alpha
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, info, fmt.Errorf("robot: PNG 编码: %w", err)
	}
	return buf.Bytes(), info, nil
}

// ForegroundTitle 返回前台窗口标题；无前台窗口（锁屏/安全桌面）
// 返回 ok=false。
func ForegroundTitle() (string, bool) {
	h, _, _ := procGetForegroundWindow.Call()
	if h == 0 {
		return "", false
	}
	return windowTitle(h), true
}

// windowTitle 读窗口标题（UTF-16 → string）。
func windowTitle(hwnd uintptr) string {
	buf := make([]uint16, 512)
	n, _, _ := procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return strings.TrimSpace(syscall.UTF16ToString(buf[:n]))
}

// WindowTitles 枚举可见且有标题的顶层窗口（新→旧顺序无保证，
// 调用方只做匹配不做顺序假设）。
func WindowTitles() []string {
	var out []string
	cb := syscall.NewCallback(func(hwnd, _ uintptr) uintptr {
		if v, _, _ := procIsWindowVisible.Call(hwnd); v == 0 {
			return 1
		}
		if t := windowTitle(hwnd); t != "" {
			out = append(out, t)
		}
		return 1
	})
	procEnumWindows.Call(cb, 0)
	return out
}

// FocusWindow 把标题含 substr 的第一个可见窗口带到前台，返回实际
// 聚焦的窗口标题——调用方拿它做回读验证（act → feel）。
func FocusWindow(substr string) (string, error) {
	if strings.TrimSpace(substr) == "" {
		return "", fmt.Errorf("robot: 窗口匹配串为空")
	}
	target, found := findWindow(substr)
	if !found {
		return "", fmt.Errorf("robot: 找不到标题含 %q 的可见窗口", substr)
	}
	focusAttempt(target)
	h, ok := getForeground()
	if !ok || h != target {
		// Windows 限制后台进程抢焦点：模拟一次 ALT 释放焦点锁再试。
		pressAltRelease()
		focusAttempt(target)
		h, ok = getForeground()
		if !ok || h != target {
			return "", fmt.Errorf("robot: 窗口 %q 拒绝聚焦（前台仍在 %q）", windowTitle(target), titleOfForeground())
		}
	}
	return windowTitle(target), nil
}

func findWindow(substr string) (uintptr, bool) {
	var target uintptr
	cb := syscall.NewCallback(func(hwnd, _ uintptr) uintptr {
		if target != 0 {
			return 1
		}
		if v, _, _ := procIsWindowVisible.Call(hwnd); v == 0 {
			return 1
		}
		if strings.Contains(windowTitle(hwnd), substr) {
			target = hwnd
		}
		return 1
	})
	procEnumWindows.Call(cb, 0)
	return target, target != 0
}

func focusAttempt(hwnd uintptr) { procSetForegroundWindow.Call(hwnd) }

func getForeground() (uintptr, bool) {
	h, _, _ := procGetForegroundWindow.Call()
	return h, h != 0
}

func titleOfForeground() string {
	h, ok := getForeground()
	if !ok {
		return ""
	}
	return windowTitle(h)
}

// pressAltRelease 释放其他线程的焦点锁（SetForegroundWindow 的
// 官方 workaround：注入一次 ALT 按下-抬起）。
func pressAltRelease() {
	sendKey(vkMenu, 0)
	sendKey(vkMenu, keyeventfKeyup)
}

// CursorPos 返回物理像素光标位置（虚拟桌面坐标系）。
func CursorPos() (int, int, error) {
	var pt struct{ X, Y int32 }
	r, _, err := procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	if r == 0 {
		return 0, 0, fmt.Errorf("robot: GetCursorPos 失败: %v", err)
	}
	return int(pt.X), int(pt.Y), nil
}

// Click 把光标移到 (x, y) 并点击主键（左键）。返回时光标位置被
// 回读验证——移动失败的静默盲点击是触觉事故的经典形态。
func Click(x, y int) error {
	return clickButton(x, y, mouseeventfLeftdown, mouseeventfLeftup)
}

// RightClick 同 Click，副键（右键）。
func RightClick(x, y int) error {
	return clickButton(x, y, mouseeventfRightdown, mouseeventfRightup)
}

func clickButton(x, y int, down, up uint32) error {
	if _, _, ok := clampToScreen(x, y); !ok {
		return fmt.Errorf("robot: 坐标 (%d, %d) 在虚拟桌面之外", x, y)
	}
	if r, _, err := procSetCursorPos.Call(uintptr(int32(x)), uintptr(int32(y))); r == 0 {
		return fmt.Errorf("robot: SetCursorPos 失败: %v", err)
	}
	// 光标回读：坐标被系统钳制或虚拟化时这里暴露偏差——盲点击
	// （不验证到达）是麻醉式自动化的经典形态，禁止。
	gx, gy, err := CursorPos()
	if err != nil {
		return err
	}
	if dx, dy := gx-x, gy-y; dx < -2 || dx > 2 || dy < -2 || dy > 2 {
		return fmt.Errorf("robot: 光标未到达目标（目标 %d,%d 实际 %d,%d）——动作中止，未点击", x, y, gx, gy)
	}
	sendMouse(down)
	sendMouse(up)
	return nil
}

func clampToScreen(x, y int) (int, int, bool) {
	b := screenBounds()
	if x < b.X || x >= b.X+b.Width || y < b.Y || y >= b.Y+b.Height {
		return x, y, false
	}
	return x, y, true
}

// input 是 INPUT 结构的原始空间布局：type + 对齐 + 联合体（按最大
// 成员 MOUSEINPUT 预留 32 字节）。字段用小端填充，避免 Go 表达 C
// 联合体的对齐陷阱。
type input struct {
	typ  uint32
	_    uint32
	data [inputUnionBytes]byte
}

func newMouseInput(flags uint32) input {
	var in input
	in.typ = inputMouse
	// MOUSEINPUT: dx, dy, mouseData, dwFlags, time, dwExtraInfo
	binary.LittleEndian.PutUint32(in.data[12:], flags)
	return in
}

func sendMouse(flags uint32) {
	in := newMouseInput(flags)
	procSendInput.Call(1, uintptr(unsafe.Pointer(&in)), uintptr(unsafe.Sizeof(in)))
}

func newKeyInput(vk, scan uint16, flags uint32) input {
	var in input
	in.typ = inputKeyboard
	// KEYBDINPUT: wVk, wScan, dwFlags, time, dwExtraInfo
	binary.LittleEndian.PutUint16(in.data[0:], vk)
	binary.LittleEndian.PutUint16(in.data[2:], scan)
	binary.LittleEndian.PutUint32(in.data[4:], flags)
	return in
}

func sendKey(vk uint16, extra uint32) {
	in := newKeyInput(vk, 0, extra)
	procSendInput.Call(1, uintptr(unsafe.Pointer(&in)), uintptr(unsafe.Sizeof(in)))
}

func sendUnicode(scan rune, extra uint32) {
	in := newKeyInput(0, uint16(scan), extra)
	procSendInput.Call(1, uintptr(unsafe.Pointer(&in)), uintptr(unsafe.Sizeof(in)))
}

// TypeText 以键盘输入逐字符发送文本。换行发 VK_RETURN，制表发
// VK_TAB；其余控制字符拒绝——合成键盘不是字节管道，注入不可见
// 控制码只会造成不可预期的快捷键触发。
func TypeText(s string) error {
	if s == "" {
		return fmt.Errorf("robot: 输入文本为空")
	}
	for _, r := range s {
		switch {
		case r == '\n':
			sendKey(vkReturn, 0)
			sendKey(vkReturn, keyeventfKeyup)
		case r == '\t':
			sendKey(vkTab, 0)
			sendKey(vkTab, keyeventfKeyup)
		case r < 0x20:
			return fmt.Errorf("robot: 拒绝输入控制字符 U+%04X（动作中止，后续字符未发送）", r)
		default:
			sendUnicode(r, keyeventfUnicode)
			sendUnicode(r, keyeventfUnicode|keyeventfKeyup)
		}
	}
	return nil
}
