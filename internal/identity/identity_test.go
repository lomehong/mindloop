package identity

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mindloop/internal/traj"
)

func TestCreateLoadRoundTrip(t *testing.T) {
	t.Setenv("MINDLOOP_HOME", t.TempDir())
	first, err := Create(context.Background(), "Ada Lovelace")
	if err != nil {
		t.Fatal(err)
	}
	if first.Name != "ada-lovelace" {
		t.Fatalf("name = %q（应为 slug 化）", first.Name)
	}

	loaded, err := Load("ada-lovelace")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Timeline.ID != first.Timeline.ID {
		t.Fatalf("根轨迹不一致: %s vs %s", loaded.Timeline.ID, first.Timeline.ID)
	}

	persona, err := loaded.Persona()
	if err != nil || !strings.Contains(persona, "ada-lovelace") {
		t.Fatalf("persona 缺失或不含名字: %q, %v", persona, err)
	}

	if !strings.Contains(loaded.Timeline.Dir, "identities") {
		t.Fatalf("根轨迹目录 = %q，应在身份目录下", loaded.Timeline.Dir)
	}

	if err := loaded.Timeline.Append(context.Background(), traj.NewStep("message")); err != nil {
		t.Fatal(err)
	}
	reloaded, err := Load("ada-lovelace")
	if err != nil {
		t.Fatalf("Load(确切名字): %v", err)
	}
	steps, err := reloaded.Timeline.Steps()
	if err != nil || len(steps) != 2 {
		t.Fatalf("重载后步骤数 = %d, %v（应为 2）", len(steps), err)
	}
}

func TestCreateIsIdempotent(t *testing.T) {
	t.Setenv("MINDLOOP_HOME", t.TempDir())
	first, err := Create(context.Background(), "dup")
	if err != nil {
		t.Fatal(err)
	}
	second, err := Create(context.Background(), "dup")
	if err != nil {
		t.Fatalf("重复 create 应幂等返回: %v", err)
	}
	if second.Timeline.ID != first.Timeline.ID {
		t.Fatalf("幂等返回了不同的轨迹: %s vs %s", second.Timeline.ID, first.Timeline.ID)
	}
}

func TestLoadMissing(t *testing.T) {
	t.Setenv("MINDLOOP_HOME", t.TempDir())
	if _, err := Load("nobody"); err == nil {
		t.Fatal("加载不存在的身份应报错")
	}
}

// TestRemoveOnlyDeletesIdentityDir：删除身份绝不动 .env ——
// 事故教训：之前心智根与配置混居，rm -rf 一键抹掉配置。
func TestRemoveOnlyDeletesIdentityDir(t *testing.T) {
	t.Setenv("MINDLOOP_HOME", t.TempDir())

	// 在心智根写一个绝对不会动的标记文件，模拟 .env
	marker := filepath.Join(t.TempDir(), "config-marker")
	if err := os.WriteFile(marker, []byte("KEEP"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(context.Background(), "ada"); err != nil {
		t.Fatal(err)
	}
	identityRoot := filepath.Join(os.Getenv("MINDLOOP_HOME"), "identities", "ada")
	if _, err := os.Stat(identityRoot); err != nil {
		t.Fatal(err)
	}

	if err := Remove("ada"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(identityRoot); !os.IsNotExist(err) {
		t.Fatalf("身份目录应被删除：%v", err)
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "KEEP" {
		t.Fatalf("心智根的配置文件不应被删：%q, %v", data, err)
	}
}

func TestRemoveRejectsMissingName(t *testing.T) {
	t.Setenv("MINDLOOP_HOME", t.TempDir())
	if err := Remove("nobody"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("删除不存在的身份应返回 ErrNotFound，得到：%v", err)
	}
}
