package recap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// P1-9 落点 c：分集渲染带窗口首步 id 的取回锚（traj show 一跳）。
func TestRenderLifeCarriesStepAnchor(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "recap"), 0o755)
	line := `{"start":1,"end":10,"title":"调试锁","summary":"修好了目录锁","model":"m","prompt_version":1,"step_from":"aaaaaaaa-bbbb-cccc-dddd-eeeeffff0001","step_to":"aaaaaaaa-bbbb-cccc-dddd-eeeeffff0010","created":"2026-11-03T00:00:00.000Z"}`
	if err := os.WriteFile(filepath.Join(dir, "recap", "episodes.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := RenderLife(dir, 0)
	if err != nil {
		t.Fatalf("RenderLife: %v", err)
	}
	if !strings.Contains(out, "traj show aaaaaaaa") {
		t.Fatalf("分集应带首步 id 锚: %q", out)
	}
	// 旧缓存（无 step_from）不附锚——向后兼容。
	line2 := `{"start":1,"end":10,"title":"t","summary":"s","model":"m","prompt_version":1,"created":"2026-11-03T00:00:00.000Z"}`
	dir2 := t.TempDir()
	os.MkdirAll(filepath.Join(dir2, "recap"), 0o755)
	os.WriteFile(filepath.Join(dir2, "recap", "episodes.jsonl"), []byte(line2+"\n"), 0o644)
	out2, err := RenderLife(dir2, 0)
	if err != nil {
		t.Fatalf("RenderLife 旧缓存: %v", err)
	}
	if strings.Contains(out2, "traj show") {
		t.Fatalf("旧缓存不应附锚: %q", out2)
	}
}
