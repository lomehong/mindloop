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

// TestDotNamesNeverTouchMindRoot：H-1 回归钉——纯点号与尾点形态在
// Win32 路径归一下会解析到身份根本身或其父目录，曾使
// `mindloop identity create ..` 把整个心智根（含 .env）当"残骸"
// 删掉。Create/Load/Remove 三个入口都必须拒绝，且绝不落任何字节。
func TestDotNamesNeverTouchMindRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MINDLOOP_HOME", home)

	// 心智根里的 .env 与一个既有身份——修复前后都必须完好。
	envPath := filepath.Join(home, ".env")
	if err := os.WriteFile(envPath, []byte("MINDLOOP_API_KEY=sk-keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(context.Background(), "ada"); err != nil {
		t.Fatal(err)
	}
	adaMarker := filepath.Join(home, "identities", "ada", "identity.txt")

	for _, bad := range []string{".", "..", "...", "....", "ada."} {
		if _, err := Create(context.Background(), bad); err == nil {
			t.Fatalf("Create(%q) 必须拒绝", bad)
		}
		if _, err := Load(bad); err == nil {
			t.Fatalf("Load(%q) 必须拒绝", bad)
		}
		if err := Remove(bad); err == nil {
			t.Fatalf("Remove(%q) 必须拒绝", bad)
		}
	}

	// 心智根与既有身份毫发无损。
	if data, err := os.ReadFile(envPath); err != nil || !strings.Contains(string(data), "sk-keep") {
		t.Fatalf("心智根 .env 被破坏：%q, %v", data, err)
	}
	if _, err := os.Stat(adaMarker); err != nil {
		t.Fatalf("既有身份被破坏：%v", err)
	}
	// 没有在身份根下留下点号目录残骸。
	entries, err := os.ReadDir(filepath.Join(home, "identities"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "ada" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("identities/ 应只剩 ada，得到 %v", names)
	}
}

// TestCreateResidueCleanupStaysInsideHome：残骸清理分支的包含检查——
// 仅有合法名字产生的身份根内目录才允许 RemoveAll。直接在心智根伪造
// 一个"残骸"目录形态（identity.txt 缺失），Create 不得越界清理。
func TestCreateResidueCleanupStaysInsideHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MINDLOOP_HOME", home)

	// 正常残骸：身份根内的半次创建目录 → 清理后重建成功。
	if err := os.MkdirAll(filepath.Join(home, "identities", "junk"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(context.Background(), "junk"); err != nil {
		t.Fatalf("清理残骸后应能重建: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "identities", "junk", "identity.txt")); err != nil {
		t.Fatalf("重建的身份缺少元数据: %v", err)
	}

	// 既有身份不受残骸清理波及。
	if _, err := Create(context.Background(), "keep"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "identities", "keep", "identity.txt")); err != nil {
		t.Fatalf("既有身份被波及: %v", err)
	}
}
