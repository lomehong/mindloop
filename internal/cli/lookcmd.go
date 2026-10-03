package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/lomehong/mindloop/internal/robot"
	"github.com/lomehong/mindloop/internal/traj"
)

// newLookCmd 是字面模态的眼睛：截取当前屏幕，落盘到身份目录并写
// screen 步骤（路径引用，不背二进制）。心智在沙箱里调用
// $MINDLOOP_EXE look——下一轮 LLM 调用即带上这张截图作为图片消息
// （runner 的视觉回路）。敏感前台（密码/凭据画面）拒绝截取。
func (c *CLI) newLookCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "look",
		Short: "看一眼屏幕：截屏落盘 + 写 screen 步骤（下一轮思考即所见）",
		Long: `字面模态（屏幕视觉）的入口。截取全桌面 PNG 存到
<身份>/screens/，并往轨迹追加 screen 步骤（含窗口标题与几何）。
运行中的心智下一轮调用模型时，最新截图会作为图片消息进入上下文。

安全：前台窗口命中敏感词（密码/凭据画面）时拒绝截取——密码内容
不进任何通道。非 Windows 平台如实报错。`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := c.extensionIdentity(cmd)
			if err != nil {
				return c.fail(err)
			}
			if id == nil {
				return c.fail(fmt.Errorf("look: 需要身份（--identity X，或在心智沙箱内经 MINDLOOP_IDENTITY_DIR 自动解析）"))
			}
			why, _ := cmd.Flags().GetString("why")

			title, ok := robot.ForegroundTitle()
			if ok && robot.IsSensitiveTitle(title) {
				return c.fail(fmt.Errorf("look: 前台窗口 %q 命中敏感词——拒绝截取（密码画面不进任何通道）", title))
			}
			png, info, err := robot.ScreenshotPNG()
			if err != nil {
				return c.fail(err)
			}
			dir := filepath.Join(id.Dir, "screens")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return c.fail(fmt.Errorf("look: 建目录: %w", err))
			}
			name := "screen-" + time.Now().UTC().Format("20060102-150405.000") + ".png"
			abs := filepath.Join(dir, name)
			if err := os.WriteFile(abs, png, 0o644); err != nil {
				return c.fail(fmt.Errorf("look: 写截图: %w", err))
			}
			rel, err := filepath.Rel(id.Dir, abs)
			if err != nil {
				rel = abs // 兜底：跨盘等病态布局下退化为绝对路径
			}
			step := traj.NewStep(traj.TypeScreen)
			step.Fields["path"] = filepath.ToSlash(rel)
			step.Fields["bytes"] = len(png)
			step.Fields["width"] = info.Width
			step.Fields["height"] = info.Height
			if ok {
				step.Fields["title"] = title
			}
			if why != "" {
				step.Fields["why"] = why
			}
			if err := id.Timeline.Append(c.ctx, step); err != nil {
				return c.fail(fmt.Errorf("look: 写 screen 步骤: %w", err))
			}
			fmt.Fprintf(c.stdout, "已截屏 %s（%dx%d，%d 字节）→ %s\n前台=%q\n下一轮思考将看到这张截图。\n",
				step.StepID, info.Width, info.Height, len(png), rel, map[bool]string{true: title, false: "（无）"}[ok])
			return nil
		},
	}
	extensionFlags(cmd)
	cmd.Flags().String("why", "", "截屏缘由（写入 screen 步骤，供回溯）")
	return cmd
}
