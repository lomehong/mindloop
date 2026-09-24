package mem

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func newStore(t *testing.T) Store {
	t.Helper()
	return Store{Dir: t.TempDir()}
}

func TestAddListRoundTrip(t *testing.T) {
	s := newStore(t)
	m1, err := s.Add(context.Background(), "fact", "操作员的名字是张伟，住在杭州")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if m1.ID == "" || m1.Created.IsZero() {
		t.Fatalf("id/created 未生成: %+v", m1)
	}
	m2, err := s.Add(context.Background(), "preference", "Prefers concise answers over long essays")
	if err != nil {
		t.Fatal(err)
	}

	all, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("List = %d 条，应为 2", len(all))
	}
	// 新的在前
	if all[0].ID != m2.ID {
		t.Fatalf("排序错误: %s 应在 %s 前", m2.ID, all[0].ID)
	}
	// 往返内容一致
	if all[1].Content != m1.Content || all[1].Type != "fact" {
		t.Fatalf("往返不一致: %+v", all[1])
	}
	// frontmatter 的 summary 不含换行且保留首行
	if strings.Contains(all[1].Summary, "\n") {
		t.Fatalf("summary 含换行: %q", all[1].Summary)
	}
}

func TestAddRejectsBadType(t *testing.T) {
	s := newStore(t)
	if _, err := s.Add(context.Background(), "diary", "x"); err == nil {
		t.Fatal("未知类型应报错")
	}
	if _, err := s.Add(context.Background(), "fact", "  "); err == nil {
		t.Fatal("空内容应报错")
	}
}

func TestBM25SearchChineseAndEnglish(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	s.Add(ctx, "fact", "操作员张伟住在杭州，喜欢在西湖边跑步")
	s.Add(ctx, "fact", "项目 headlong 的部署目录是 /opt/shellm，systemd 单元名 headlong-thinkers")
	s.Add(ctx, "preference", "回复偏好：中文，简短直接")
	s.Add(ctx, "todo", "记住给操作员买生日蛋糕，日期十二月三日")

	// 中文查询：CJK 二元组切词命中
	hits, err := s.Search("操作员住在哪里", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("中文查询零命中")
	}
	if !strings.Contains(hits[0].Content, "张伟") {
		t.Fatalf("中文检索首位错误: %q", hits[0].Content)
	}

	// 英文查询：词元命中
	hits, err = s.Search("systemd deployment directory", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || !strings.Contains(hits[0].Content, "headlong-thinkers") {
		t.Fatalf("英文检索错误: %+v", hits)
	}

	// 检索随相关度排序：查询生日时 todo 应排在偏好前
	hits, err = s.Search("生日蛋糕 日期", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) < 1 || !strings.Contains(hits[0].Content, "蛋糕") {
		t.Fatalf("生日查询首位错误: %+v", hits)
	}
}

func TestSearchEmpty(t *testing.T) {
	s := newStore(t)
	hits, err := s.Search("anything", 5)
	if err != nil || hits != nil {
		t.Fatalf("空库检索 = %v, %v", hits, err)
	}
}

func TestForget(t *testing.T) {
	s := newStore(t)
	m, _ := s.Add(context.Background(), "note", "临时备忘")
	if err := s.Forget(m.ID); err != nil {
		t.Fatalf("Forget: %v", err)
	}
	if err := s.Forget(m.ID); err == nil {
		t.Fatal("重复删除应报错")
	}
	all, _ := s.List()
	if len(all) != 0 {
		t.Fatalf("删除后仍有 %d 条", len(all))
	}
}

// TestRapidAddsStrictOrder 是写序号的回归测试：同一毫秒内的连续
// 写入必须保持严格顺序（曾是随机 id 排序导致的 flake）。
func TestRapidAddsStrictOrder(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	var ids []string
	for i := 0; i < 12; i++ {
		m, err := s.Add(ctx, "note", itoaMsg(i))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, m.ID)
	}
	all, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 12 {
		t.Fatalf("%d 条，应为 12", len(all))
	}
	for i, m := range all { // all[0] 最新 = 最后写入
		want := ids[len(ids)-1-i]
		if m.ID != want {
			t.Fatalf("第 %d 条 = %s，应为 %s（写入顺序被打乱）", i, m.ID, want)
		}
	}
}

func itoaMsg(i int) string {
	return fmt.Sprintf("第 %d 条快速写入", i)
}
