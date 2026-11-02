package mem

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// D4 盖章版钉子（2026-11-02）：写入者 provenance 的 frontmatter 往返
// 与修订保留。盖章不是防线（不降权），是「未核实降权」防线可建的
// 前提与审计面——降权属 roadmap §2.4 智能层批次。

func TestAuthorRoundTripAndRevise(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	a, err := s.AddWith(context.Background(), "fact", "agent 观察到磁盘紧张", AddOpts{Author: "agent", Expires: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatalf("AddWith: %v", err)
	}
	if a.Memory.Author != "agent" {
		t.Fatalf("Author 应随写入返回，得 %q", a.Memory.Author)
	}
	// frontmatter 落盘可审计。
	raw, err := os.ReadFile(filepath.Join(s.Dir, filepath.Base(a.Memory.Path)))
	if err != nil {
		t.Fatalf("读文件: %v", err)
	}
	if !strings.Contains(string(raw), "author: agent") {
		t.Fatalf("frontmatter 缺 author 章: %q", raw)
	}
	// 重新解析往返。
	all, err := s.List()
	if err != nil || len(all) != 1 {
		t.Fatalf("List: err=%v n=%d", err, len(all))
	}
	if all[0].Author != "agent" {
		t.Fatalf("解析后 Author 应为 agent，得 %q", all[0].Author)
	}
	// 修订保留原始作者章（修订是内容修正，不改变写入者）。
	r, err := s.Revise(context.Background(), a.Memory.ID, "agent 观察到磁盘已清理")
	if err != nil {
		t.Fatalf("Revise: %v", err)
	}
	if r.Memory.Author != "agent" {
		t.Fatalf("修订应保留 Author，得 %q", r.Memory.Author)
	}
}

func TestAuthorZeroValueLegacy(t *testing.T) {
	// 旧文件无 author 章：零值 = 未知，解析不报错（向后兼容）。
	s := newStoreWithLegacy(t)
	all, err := s.List()
	if err != nil || len(all) != 1 {
		t.Fatalf("List: err=%v n=%d", err, len(all))
	}
	if all[0].Author != "" {
		t.Fatalf("旧文件 Author 应为零值，得 %q", all[0].Author)
	}
}

func newStoreWithLegacy(t *testing.T) Store {
	t.Helper()
	dir := t.TempDir()
	body := "---\nid: legacy01\ntype: fact\ncreated: 2026-10-01T00:00:00.000Z\nsummary: \"旧记忆\"\n---\n旧记忆内容\n"
	if err := os.WriteFile(filepath.Join(dir, "000001_legacy01.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return Store{Dir: dir}
}
