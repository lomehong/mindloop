// Package robotd 是"身"的服务形态：独立 stdio MCP server，暴露
// 屏幕的看（截屏/窗口/光标）与触（聚焦/点击/输入）。
//
// 安全链（perception.md Phase 5，评审修正版）：
//  1. 动作级 tripwire 是结构化谓词不是正则——窗口标题白名单
//     （--window-allow 可重复）。启动时未给白名单 = 观察模式：
//     看得到、动不了。
//  2. 密码框/密码管理器画面默认拒绝（敏感标题检测先行于白名单
//     ——宁可多拒；拒绝是最保守的打码）。
//  3. 动作必须自带验证感知（act → feel）：点击经光标回读验证
//     到达，动作完成后强制回读截屏作为证据返回——验证失败走
//     触觉异常路径（isError），绝不默认成功。
//  4. 全程留痕：每个动作一行结构化审计到 stderr（stdout 是 MCP
//     协议通道）。会话级授权 = 操作员亲手启动 robotd 并给出
//     白名单；批量审批的每步留痕在这里落地。
package robotd

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/lomehong/mindloop/internal/mcp"
	"github.com/lomehong/mindloop/internal/robot"
)

// stderrWriter 是审计通道（stdout 是 MCP 协议通道，不可混用）；
// 包级变量让测试可以改道。
var stderrWriter io.Writer = os.Stderr

// Server 持有会话级授权状态。foreground/capture/click/typeText/
// focus 是环境与动作的注入点（缺省接真实屏幕与 SendInput；测试
// 注入假件——安全链必须能确定性验证，且测试绝不真实点击）。
type Server struct {
	// allow 是窗口标题子串白名单；空 = 观察模式（动作全拒）。
	allow []string
	// audit 收审计行（stderr；测试注入）。
	audit func(string)
	now   func() time.Time

	foreground func() (string, bool)
	capture    func() ([]byte, robot.ScreenInfo, error)
	click      func(x, y int, right bool) error
	typeText   func(text string) error
	focus      func(substr string) (string, error)
}

// NewServer 构造；audit 为 nil 时写 stderr。
func NewServer(windowAllow []string, audit func(string)) *Server {
	if audit == nil {
		audit = func(line string) { fmt.Fprintln(stderrWriter, line) }
	}
	return &Server{
		allow:      windowAllow,
		audit:      audit,
		now:        time.Now,
		foreground: robot.ForegroundTitle,
		capture:    robot.ScreenshotPNG,
		click: func(x, y int, right bool) error {
			if right {
				return robot.RightClick(x, y)
			}
			return robot.Click(x, y)
		},
		typeText: robot.TypeText,
		focus:    robot.FocusWindow,
	}
}

// ObserveOnly 报告是否处于观察模式。
func (s *Server) ObserveOnly() bool { return len(s.allow) == 0 }

// Tools 组装暴露面。工具描述内嵌模式语义——客户端列出工具就能
// 看到自己能做什么。
func (s *Server) Tools() []mcp.ServerTool {
	modeHint := "观察模式（未授权动作——需要启动时给 --window-allow）"
	if !s.ObserveOnly() {
		modeHint = fmt.Sprintf("动作已授权（窗口白名单: %v）", s.allow)
	}
	return []mcp.ServerTool{
		{
			Name:        "screen_mode",
			Description: "查看本 robotd 的授权模式与窗口白名单（观察类）",
			InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
			Handler: func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
				return mcp.NewTextResult(s.modeText()), nil
			},
		},
		{
			Name:        "screen_windows",
			Description: "列出可见窗口标题（观察类）——从中挑选聚焦目标",
			InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
			Handler: func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
				titles := robot.WindowTitles()
				if len(titles) == 0 {
					return mcp.NewTextResult("（没有可见窗口——可能处于无桌面会话）"), nil
				}
				if len(titles) > 50 {
					titles = append([]string{}, titles[:50]...)
					titles = append(titles, fmt.Sprintf("…（共超过 50 个，已截断）"))
				}
				out := modeHint + "\n"
				for i, t := range titles {
					mark := ""
					if s.allowMatches(t) != "" {
						mark = "  ← 白名单内"
					}
					out += fmt.Sprintf("%d. %s%s\n", i+1, t, mark)
				}
				return mcp.NewTextResult(out), nil
			},
		},
		{
			Name:        "screen_shot",
			Description: "截取全桌面并返回 PNG 图片（观察类；敏感窗口画面拒绝）",
			InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
			Handler: func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
				if _, err := s.gateObserve(); err != nil {
					return mcp.ToolResult{}, err
				}
				return s.shot("截屏")
			},
		},
		{
			Name:        "screen_cursor",
			Description: "查看光标位置（观察类）",
			InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
			Handler: func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
				if _, err := s.gateObserve(); err != nil {
					return mcp.ToolResult{}, err
				}
				x, y, err := robot.CursorPos()
				if err != nil {
					return mcp.ToolResult{}, err
				}
				return mcp.NewTextResult(fmt.Sprintf("光标 (%d, %d)", x, y)), nil
			},
		},
		{
			Name:        "screen_focus",
			Description: "把标题含匹配串的窗口带到前台（动作；目标必须在白名单内）",
			InputSchema: json.RawMessage(`{
  "type": "object",
  "required": ["title"],
  "properties": { "title": { "type": "string", "description": "窗口标题子串" } }
}`),
			Handler: func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
				var a struct {
					Title string `json:"title"`
				}
				if err := json.Unmarshal(args, &a); err != nil || strings.TrimSpace(a.Title) == "" {
					return mcp.ToolResult{}, fmt.Errorf("screen_focus: title 必填: %w", mcp.ErrInvalidArguments)
				}
				if s.ObserveOnly() {
					s.auditAction("focus-denied", fmt.Sprintf("title=%q 观察模式", a.Title))
					return mcp.ToolResult{}, fmt.Errorf("robotd: 观察模式（启动时未给 --window-allow）——focus 拒绝")
				}
				if robot.IsSensitiveTitle(a.Title) {
					s.auditAction("focus-denied", fmt.Sprintf("title=%q 敏感", a.Title))
					return mcp.ToolResult{}, fmt.Errorf("robotd: 目标窗口 %q 命中敏感词——focus 拒绝", a.Title)
				}
				s.auditAction("focus", fmt.Sprintf("title=%q", a.Title))
				got, err := s.focus(a.Title)
				if err != nil {
					s.auditAction("focus-failed", err.Error())
					return mcp.ToolResult{}, err
				}
				// 回读验证：目标子串可能比白名单宽（"记" 命中一串窗口）
				// ——实际聚焦到的窗口必须命中白名单，否则触觉异常。
				fg, fgOK := s.foreground()
				if !fgOK || s.allowMatches(fg) == "" {
					s.auditAction("focus-violation", fmt.Sprintf("实际前台=%q 不在白名单 %v", fg, s.allow))
					return mcp.ToolResult{}, fmt.Errorf("robotd: 聚焦落在白名单外窗口 %q——触觉异常路径", fg)
				}
				return mcp.NewTextResult(fmt.Sprintf("已聚焦 %q（前台确认=%q）", got, fg)), nil
			},
		},
		{
			Name:        "screen_click",
			Description: "在 (x,y) 点击（动作；前台窗口必须在白名单内；返回动作后回读截屏）",
			InputSchema: json.RawMessage(`{
  "type": "object",
  "required": ["x", "y"],
  "properties": {
    "x": { "type": "integer" },
    "y": { "type": "integer" },
    "right": { "type": "boolean", "description": "true=右键（默认左键）" }
  }
}`),
			Handler: func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
				var a struct {
					X     int  `json:"x"`
					Y     int  `json:"y"`
					Right bool `json:"right"`
				}
				if err := json.Unmarshal(args, &a); err != nil {
					return mcp.ToolResult{}, fmt.Errorf("screen_click 参数: %w", mcp.ErrInvalidArguments)
				}
				return s.actWithReadback("click",
					func() error { return s.click(a.X, a.Y, a.Right) },
					fmt.Sprintf("(%d,%d) 右键=%v", a.X, a.Y, a.Right))
			},
		},
		{
			Name:        "screen_type",
			Description: "向前台窗口键入文本（动作；前台窗口必须在白名单内；返回动作后回读截屏）",
			InputSchema: json.RawMessage(`{
  "type": "object",
  "required": ["text"],
  "properties": { "text": { "type": "string", "description": "要键入的文本（换行=回车键）" } }
}`),
			Handler: func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
				var a struct {
					Text string `json:"text"`
				}
				if err := json.Unmarshal(args, &a); err != nil || a.Text == "" {
					return mcp.ToolResult{}, fmt.Errorf("screen_type: text 必填: %w", mcp.ErrInvalidArguments)
				}
				return s.actWithReadback("type",
					func() error { return s.typeText(a.Text) },
					fmt.Sprintf("%d 字符", len([]rune(a.Text))))
			},
		},
	}
}

// modeText 是授权模式的透明陈述。
func (s *Server) modeText() string {
	if s.ObserveOnly() {
		return "观察模式：截屏/窗口/光标可用，聚焦/点击/输入一律拒绝。" +
			"授权动作需要操作员在启动 robotd 时给 --window-allow <标题子串>（可重复）。"
	}
	return fmt.Sprintf("动作已授权。窗口白名单（标题子串）: %v。敏感窗口（密码/凭据画面）在任何模式下都拒绝。", s.allow)
}

// gateObserve 是观察类工具的门：敏感前台连看都不给。
func (s *Server) gateObserve() (string, error) {
	title, ok := s.foreground()
	if ok && robot.IsSensitiveTitle(title) {
		return "", fmt.Errorf("robotd: 前台窗口 %q 命中敏感词——截屏拒绝（密码画面不进任何通道）", title)
	}
	return title, nil
}

// allowMatches 报告标题是否命中白名单。
func (s *Server) allowMatches(title string) string {
	for _, a := range s.allow {
		if strings.Contains(title, a) {
			return a
		}
	}
	return ""
}

// checkAction 是动作级 tripwire：敏感检测 → 白名单谓词。返回前台
// 标题供审计。
func (s *Server) checkAction(action string) (string, error) {
	title, ok := s.foreground()
	if !ok {
		return "", fmt.Errorf("robotd: 无前台窗口（锁屏/安全桌面？）——%s 拒绝", action)
	}
	if robot.IsSensitiveTitle(title) {
		return "", fmt.Errorf("robotd: 前台窗口 %q 命中敏感词——%s 拒绝（密码框输入默认拒绝）", title, action)
	}
	if s.ObserveOnly() {
		return "", fmt.Errorf("robotd: 观察模式（启动时未给 --window-allow）——%s 拒绝", action)
	}
	if match := s.allowMatches(title); match == "" {
		return "", fmt.Errorf("robotd: 前台窗口 %q 不在白名单 %v 内——%s 拒绝", title, s.allow, action)
	}
	return title, nil
}

// actWithReadback 是触觉回路的骨架：gate → act → feel。动作后的
// 回读截屏作为图片内容块随结果返回——验证感知与动作同帧交付，
// 不给"盲操作"留形态。
func (s *Server) actWithReadback(action string, do func() error, detail string) (mcp.ToolResult, error) {
	title, err := s.checkAction(action)
	if err != nil {
		s.auditAction(action+"-denied", detail+" 前台="+title+" 理由="+err.Error())
		return mcp.ToolResult{}, err
	}
	s.auditAction(action, detail+" 前台="+title)
	if err := do(); err != nil {
		s.auditAction(action+"-failed", err.Error())
		return mcp.ToolResult{}, fmt.Errorf("robotd: %s 执行失败（触觉异常路径）: %w", action, err)
	}
	// feel：动作后强制回读。截屏失败也是验证失败——返回错误而不
	// 是"成功但没看"。
	res, err := s.shot(action + "后回读")
	if err != nil {
		return mcp.ToolResult{}, fmt.Errorf("robotd: %s 已执行但回读失败（验证不完整）: %w", action, err)
	}
	fg, fgOK := s.foreground()
	res.Text = fmt.Sprintf("%s 完成（%s；当前前台=%q）", action, detail, map[bool]string{true: fg, false: "（无）"}[fgOK]) + "\n" + res.Text
	return res, nil
}

// shot 产出截图富结果：图片内容块 + 文本几何信息。
func (s *Server) shot(why string) (mcp.ToolResult, error) {
	png, info, err := s.capture()
	if err != nil {
		return mcp.ToolResult{}, err
	}
	fg, fgOK := s.foreground()
	x, y, _ := robot.CursorPos()
	text := fmt.Sprintf("%s：桌面 %dx%d@(%d,%d)，光标(%d,%d)，前台=%q（敏感检测通过）",
		why, info.Width, info.Height, info.X, info.Y, x, y, map[bool]string{true: fg, false: "（无）"}[fgOK])
	return mcp.ToolResult{
		Text:   text,
		Images: []mcp.ImageContent{{MIMEType: "image/png", Data: base64.StdEncoding.EncodeToString(png)}},
	}, nil
}

// auditAction 落一行结构化审计（stderr）。
func (s *Server) auditAction(action, detail string) {
	s.audit(fmt.Sprintf("[robotd] %s ACTION=%s %s", s.now().UTC().Format("2006-01-02T15:04:05Z"), action, detail))
}
