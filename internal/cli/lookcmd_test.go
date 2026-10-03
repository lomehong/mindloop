package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lomehong/mindloop/internal/identity"
	"github.com/lomehong/mindloop/internal/traj"
)

// TestLookCapturesScreenAndWritesStep：真实截屏全链（本机有桌面；
// 无桌面会话的 CI 干净跳过）。验证：截图文件落盘、screen 步骤在
// 轨迹、path 字段是相对身份目录的斜杠路径。
func TestLookCapturesScreenAndWritesStep(t *testing.T) {
	if _, err := identity.Create(context.Background(), "ada"); err != nil {
		t.Fatalf("identity.Create: %v", err)
	}
	code, out, errOut := runCLI(t, "look", "--identity", "ada", "--why", "验证视觉回路")
	if code != 0 {
		t.Skipf("look 失败（无交互桌面的环境常见）: %d %q %q", code, out, errOut)
	}
	if !strings.Contains(out, "已截屏") || !strings.Contains(out, "下一轮思考将看到") {
		t.Fatalf("输出缺确认语义: %q", out)
	}
	id, err := identity.Load("ada")
	if err != nil {
		t.Fatal(err)
	}
	steps, err := id.Timeline.Steps()
	if err != nil {
		t.Fatal(err)
	}
	var scr traj.Step
	found := false
	for _, s := range steps {
		if s.Type == traj.TypeScreen {
			scr = s
			found = true
		}
	}
	if !found {
		t.Fatalf("轨迹里没有 screen 步骤: %q", out)
	}
	path, _ := scr.Field("path")
	if path == "" || strings.HasPrefix(path, "/") || strings.Contains(path, `\`) {
		t.Fatalf("path 应是相对斜杠路径: %q", path)
	}
	data, err := os.ReadFile(filepath.Join(id.Dir, filepath.FromSlash(path)))
	if err != nil {
		t.Fatalf("截图文件读不回: %v", err)
	}
	if len(data) < 100 || string(data[:4]) != "\x89PNG" {
		t.Fatalf("落盘的不是 PNG（%d 字节）", len(data))
	}
	if why, _ := scr.Field("why"); why != "验证视觉回路" {
		t.Fatalf("why 未入步骤: %q", why)
	}
}

// TestLookRequiresIdentity：无身份时明确报错（不静默写全局）。
func TestLookRequiresIdentity(t *testing.T) {
	t.Setenv("MINDLOOP_IDENTITY_DIR", "")
	code, _, errOut := runCLI(t, "look")
	if code == 0 {
		t.Fatal("无身份的 look 应失败")
	}
	if !strings.Contains(errOut, "身份") {
		t.Fatalf("错误应点名身份: %q", errOut)
	}
}
