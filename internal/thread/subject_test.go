package thread

import "testing"

// P1-10：subject 锚定归属——精确命中取最近活跃；无锚/已完结不归属。
func TestMatchSubject(t *testing.T) {
	items := []Thread{
		{ID: "th-1", Title: "旧线索", Status: Open, Active: "2026-11-01T00:00:00Z", Subjects: []string{"https://x.example"}},
		{ID: "th-2", Title: "新线索", Status: Open, Active: "2026-11-02T00:00:00Z", Subjects: []string{"https://x.example", "E:\\proj"}},
		{ID: "th-3", Title: "完结", Status: Done, Active: "2026-11-03T00:00:00Z", Subjects: []string{"https://x.example"}},
	}
	// 双开放命中：取最近活跃（th-2）。
	if id, ok := MatchSubject(items, "https://x.example"); !ok || id != "th-2" {
		t.Fatalf("应归属 th-2（最近活跃），得 %q ok=%v", id, ok)
	}
	if id, ok := MatchSubject(items, "E:\\proj"); !ok || id != "th-2" {
		t.Fatalf("第二锚应归属 th-2，得 %q ok=%v", id, ok)
	}
	// 无锚不归属（不做语义猜测）。
	if _, ok := MatchSubject(items, "https://other.example"); ok {
		t.Fatal("无锚 subject 不应归属")
	}
	// 单独的开放线索 th-1 也可归属。
	only1 := []Thread{items[0]}
	if id, ok := MatchSubject(only1, "https://x.example"); !ok || id != "th-1" {
		t.Fatalf("th-1 单独开放应归属，得 %q ok=%v", id, ok)
	}
	alone := []Thread{items[2]}
	if _, ok := MatchSubject(alone, "https://x.example"); ok {
		t.Fatal("完结线索不应接收归属")
	}
}
