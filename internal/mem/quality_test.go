package mem

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"mindloop/internal/traj"
)

// rawMemory 手写一个记忆文件，覆盖任意 frontmatter 场景。
func rawMemory(t *testing.T, dir, fname, frontmatter, content string) {
	t.Helper()
	body := "---\n" + frontmatter + "---\n" + content + "\n"
	if err := os.WriteFile(filepath.Join(dir, fname), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestOptionalFieldsRoundTrip：source/expires 可选字段写入并读回；
// 未使用的可选字段不出现在 frontmatter（旧解析器无感）。
func TestOptionalFieldsRoundTrip(t *testing.T) {
	s := newStore(t)
	exp := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	a, err := s.AddWith(context.Background(), "todo", "记得复查部署", AddOpts{Source: "msg12345678", Expires: exp})
	if err != nil {
		t.Fatal(err)
	}
	if a.Duplicate || len(a.Conflicts) != 0 {
		t.Fatalf("首条写入不应报告重复/冲突: %+v", a)
	}
	if a.Memory.Source != "msg12345678" || !a.Memory.Expires.Equal(exp) {
		t.Fatalf("写入返回值缺可选字段: %+v", a.Memory)
	}
	all, err := s.List()
	if err != nil || len(all) != 1 {
		t.Fatalf("List = %v, %v", all, err)
	}
	if all[0].Source != "msg12345678" || !all[0].Expires.Equal(exp) {
		t.Fatalf("读回缺可选字段: %+v", all[0])
	}
	data, err := os.ReadFile(all[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	txt := string(data)
	if !strings.Contains(txt, "source: msg12345678") {
		t.Fatalf("frontmatter 缺 source: %q", txt)
	}
	if !strings.Contains(txt, "expires: \"2030-01-02T03:04:05.000Z\"") {
		t.Fatalf("frontmatter 缺 expires: %q", txt)
	}
	for _, absent := range []string{"supersedes:", "superseded_by:", "status:", "updated:"} {
		if strings.Contains(txt, absent) {
			t.Fatalf("未使用的字段 %q 不应写入: %q", absent, txt)
		}
	}
}

// TestLegacyFileCompat：旧格式（无新字段）按有效旧格式读取，缺省视为有效。
func TestLegacyFileCompat(t *testing.T) {
	dir := t.TempDir()
	rawMemory(t, dir, "000001_abc12345.md",
		"id: abc12345\ntype: fact\ncreated: 2026-01-02T03:04:05.000Z\nsummary: 旧格式摘要\n",
		"旧格式正文，含关键词橙子")
	s := Store{Dir: dir}
	all, err := s.List()
	if err != nil || len(all) != 1 {
		t.Fatalf("List = %v, %v", all, err)
	}
	if m := all[0]; m.Status != "" || !m.Active(time.Now()) {
		t.Fatalf("旧文件应视为有效: %+v", m)
	}
	hits, err := s.Search("橙子", 3)
	if err != nil || len(hits) != 1 {
		t.Fatalf("旧文件检索失败: %v, %v", hits, err)
	}
}

// TestSearchExcludesInactiveAndExpired：检索默认排除失效、过期与被替代项；
// 列表视图保留全部（供管理/展示）。
func TestSearchExcludesInactiveAndExpired(t *testing.T) {
	dir := t.TempDir()
	rawMemory(t, dir, "000001_aaa11111.md",
		"id: aaa11111\ntype: fact\ncreated: 2026-01-01T00:00:00.000Z\nsummary: 苹果派用肉桂\n", "苹果派配方用肉桂")
	rawMemory(t, dir, "000002_bbb22222.md",
		"id: bbb22222\ntype: fact\ncreated: 2026-01-01T00:00:01.000Z\nstatus: invalid\nsummary: 苹果派用丁香\n", "苹果派配方用丁香")
	rawMemory(t, dir, "000003_ccc33333.md",
		"id: ccc33333\ntype: fact\ncreated: 2026-01-01T00:00:02.000Z\nexpires: \"2000-01-01T00:00:00.000Z\"\nsummary: 苹果派用豆蔻\n", "苹果派配方用豆蔻")
	rawMemory(t, dir, "000004_ddd44444.md",
		"id: ddd44444\ntype: fact\ncreated: 2026-01-01T00:00:03.000Z\nstatus: superseded\nsuperseded_by: zzz99999\nsummary: 苹果派用柠皮\n", "苹果派配方用柠皮")
	s := Store{Dir: dir}
	hits, err := s.Search("苹果派 配方", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].ID != "aaa11111" {
		t.Fatalf("检索应只剩有效条目: %+v", hits)
	}
	all, err := s.List()
	if err != nil || len(all) != 4 {
		t.Fatalf("List 应保留全部（含失效）: %d, %v", len(all), err)
	}
}

// TestReviseSupersedesOldVersion：修订生成新条目并明确替代旧版本；
// 旧文件保留在盘上（修订记录），检索只见新版本。
func TestReviseSupersedesOldVersion(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	added, err := s.AddWith(ctx, "fact", "操作员住在杭州", AddOpts{Source: "msgAAAA"})
	if err != nil {
		t.Fatal(err)
	}
	old := added.Memory
	a, err := s.Revise(ctx, old.ID, "操作员搬到了上海")
	if err != nil {
		t.Fatal(err)
	}
	if a.Memory.Supersedes != old.ID || a.Memory.ID == old.ID {
		t.Fatalf("修订未明确替代旧版本: %+v", a.Memory)
	}
	if a.Memory.Type != "fact" || a.Memory.Source != "msgAAAA" {
		t.Fatalf("修订应继承类型与来源: %+v", a.Memory)
	}

	all, err := s.List()
	if err != nil || len(all) != 2 {
		t.Fatalf("修订应保留旧文件: %d, %v", len(all), err)
	}
	byID := map[string]Memory{}
	for _, m := range all {
		byID[m.ID] = m
	}
	o := byID[old.ID]
	if o.Status != StatusSuperseded || o.SupersededBy != a.Memory.ID {
		t.Fatalf("旧条未标记被替代: %+v", o)
	}
	if o.Updated.IsZero() {
		t.Fatalf("旧条缺修订时间: %+v", o)
	}

	// 检索排除被替代项：旧内容查不到，新内容查得到。
	if hits, _ := s.Search("杭州", 5); len(hits) != 0 {
		t.Fatalf("被替代的旧内容不应被检索: %+v", hits)
	}
	hits, err := s.Search("上海", 5)
	if err != nil || len(hits) != 1 || hits[0].ID != a.Memory.ID {
		t.Fatalf("新版本应可检索: %+v, %v", hits, err)
	}

	// 修订记录：旧文件保留原正文并带 superseded_by。
	data, err := os.ReadFile(o.Path)
	if err != nil {
		t.Fatal(err)
	}
	txt := string(data)
	if !strings.Contains(txt, "操作员住在杭州") || !strings.Contains(txt, "superseded_by: "+a.Memory.ID) {
		t.Fatalf("旧文件缺修订记录: %q", txt)
	}
	ndata, err := os.ReadFile(a.Memory.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ndata), "supersedes: "+old.ID) {
		t.Fatalf("新文件缺 supersedes: %q", ndata)
	}
}

// TestReviseRejectsInactiveAndUnknown：未知 id 与已失效/被替代条目
// 不能再修订（防止修订链分叉出第二个活动版本）。
func TestReviseRejectsInactiveAndUnknown(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if _, err := s.Revise(ctx, "nope1234", "x"); err == nil {
		t.Fatal("未知 id 修订应报错")
	}
	m, err := s.Add(ctx, "note", "第一条")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Revise(ctx, m.ID, "第二条"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Revise(ctx, m.ID, "第三条"); err == nil {
		t.Fatal("已被替代的条目不应能再次修订")
	}
	if _, err := s.Revise(ctx, "", "x"); err == nil {
		t.Fatal("空内容应报错")
	}
}

// TestInvalidateHidesFromSearch：失效操作保留文件（审计）但退出检索；
// 重复失效报错（与重复删除同一纪律）。
func TestInvalidateHidesFromSearch(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	m, err := s.Add(ctx, "todo", "买咖啡豆")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Invalidate(ctx, m.ID); err != nil {
		t.Fatalf("Invalidate: %v", err)
	}
	if hits, _ := s.Search("咖啡豆", 5); len(hits) != 0 {
		t.Fatalf("失效条目不应被检索: %+v", hits)
	}
	all, err := s.List()
	if err != nil || len(all) != 1 {
		t.Fatalf("失效应保留文件: %d, %v", len(all), err)
	}
	if all[0].Status != StatusInvalid || all[0].Updated.IsZero() {
		t.Fatalf("失效标记不完整: %+v", all[0])
	}
	if err := s.Invalidate(ctx, m.ID); err == nil {
		t.Fatal("重复失效应报错")
	}
	if err := s.Invalidate(ctx, "nope1234"); err == nil {
		t.Fatal("未知 id 失效应报错")
	}
}

// TestAddDeduplicatesIdenticalContent：完全相同内容不重复写盘，
// 返回已存在条目（首尾空白后比较）。
func TestAddDeduplicatesIdenticalContent(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	first, err := s.Add(ctx, "fact", "操作员住在杭州")
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.AddWith(ctx, "fact", "  操作员住在杭州  ", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !a.Duplicate || a.Memory.ID != first.ID {
		t.Fatalf("应命中已存在条目: %+v", a)
	}
	if len(a.Conflicts) != 0 {
		t.Fatalf("去重命中不应附带冲突候选: %+v", a.Conflicts)
	}
	if all, _ := s.List(); len(all) != 1 {
		t.Fatalf("不应重复写盘: %d 条", len(all))
	}
}

// TestAddReportsConflicts：与活动记忆高度相似时展示候选（提示显式
// 修订，不自动覆盖）；无关内容不报；失效条目不参与。
func TestAddReportsConflicts(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	old, err := s.Add(ctx, "fact", "操作员张伟住在杭州")
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.AddWith(ctx, "fact", "操作员张伟住在上海", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Conflicts) != 1 || a.Conflicts[0].Memory.ID != old.ID {
		t.Fatalf("应报告冲突候选: %+v", a.Conflicts)
	}
	if sim := a.Conflicts[0].Similarity; sim <= 0 || sim > 1 {
		t.Fatalf("相似度越界: %v", sim)
	}

	b, err := s.AddWith(ctx, "fact", "部署目录是 /opt/shellm", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Conflicts) != 0 {
		t.Fatalf("无关内容不应报冲突: %+v", b.Conflicts)
	}

	// 失效的旧条不参与冲突提示。
	doomed, err := s.Add(ctx, "note", "记住买牛奶给小猫喝")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Invalidate(ctx, doomed.ID); err != nil {
		t.Fatal(err)
	}
	c, err := s.AddWith(ctx, "note", "记住买牛奶给小猫", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Conflicts) != 0 {
		t.Fatalf("失效条目不应参与冲突提示: %+v", c.Conflicts)
	}
}

// fullRescanSearch 是测试侧的全量对照：即时重新切词、统计 df 与
// 平均长度后走同一打分函数（与 Search 的增量缓存路径对拍）。
func fullRescanSearch(t *testing.T, s Store, query string, topK int) []Scored {
	t.Helper()
	all, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) == 0 || strings.TrimSpace(query) == "" {
		return nil
	}
	qTerms := tokenize(query)
	if len(qTerms) == 0 {
		return nil
	}
	stats := make([]docStat, len(all))
	df := map[string]int{}
	totalLen := 0
	for i, m := range all {
		tf, length := tokenCounts(m)
		stats[i] = docStat{tf: tf, length: length}
		for term := range tf {
			df[term]++
		}
		totalLen += length
	}
	avgLen := float64(totalLen) / float64(len(all))
	// 候选集与 Search 一致：只对有效条目打分；语料统计（df/n/
	// avgLen）用全库——与索引的增量聚合口径对齐。
	now := time.Now()
	var actAll []Memory
	var actStats []docStat
	for i, m := range all {
		if m.Active(now) {
			actAll = append(actAll, m)
			actStats = append(actStats, stats[i])
		}
	}
	if len(actAll) == 0 {
		return nil
	}
	return scoreBM25(actAll, actStats, df, len(all), avgLen, qTerms, topK)
}

// TestSearchCacheMatchesFullRescan：增量缓存（分词/df/平均长度）
// 与全量重算对拍——包括外部编辑（改内容、删文件、加文件）之后。
func TestSearchCacheMatchesFullRescan(t *testing.T) {
	dir := t.TempDir()
	s := Store{Dir: dir}
	ctx := context.Background()
	samples := []struct{ typ, content string }{
		{"fact", "操作员张伟住在杭州，喜欢西湖"},
		{"fact", "部署目录是 /opt/shellm，单元名 headlong-thinkers"},
		{"preference", "回复偏好：中文，简短直接"},
		{"todo", "给操作员买生日蛋糕"},
	}
	for _, m := range samples {
		if _, err := s.Add(ctx, m.typ, m.content); err != nil {
			t.Fatal(err)
		}
	}
	assertMatches := func(q string) {
		t.Helper()
		got, err := s.Search(q, 10)
		if err != nil {
			t.Fatal(err)
		}
		want := fullRescanSearch(t, s, q, 10)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("查询 %q 增量结果与全量不一致:\n got=%+v\nwant=%+v", q, got, want)
		}
	}
	assertMatches("操作员 杭州")

	// 外部编辑：改一条内容、删一条、加一条（绕过 Store）。
	all, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	victim := all[len(all)-1]
	rawMemory(t, dir, filepath.Base(victim.Path),
		"id: "+victim.ID+"\ntype: fact\ncreated: "+victim.Created.Format(traj.TimeFormat)+"\nsummary: 操作员搬到上海\n",
		"操作员搬到了上海，喜欢黄浦江")
	if err := os.Remove(all[0].Path); err != nil {
		t.Fatal(err)
	}
	rawMemory(t, dir, "000099_ext99999.md",
		"id: ext99999\ntype: note\ncreated: 2026-01-01T00:00:00.000Z\nsummary: 外部新增条目\n",
		"外部新增：备用密钥放在云杉抽屉")

	assertMatches("上海 黄浦江")
	assertMatches("云杉 抽屉")
	assertMatches("操作员")
	assertMatches("部署目录 headlong")
	// 幂等：连续两次搜索结果一致。
	assertMatches("操作员 杭州")
}

// TestSearchCacheDfCounts：白盒检查增量统计——删除文件后 df 与
// 总长不残留。
func TestSearchCacheDfCounts(t *testing.T) {
	dir := t.TempDir()
	s := Store{Dir: dir}
	ctx := context.Background()
	m1, err := s.Add(ctx, "fact", "苹果派用肉桂")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(ctx, "fact", "苹果派用丁香"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Search("苹果", 5); err != nil {
		t.Fatal(err)
	}
	ix := indexFor(dir)
	ix.mu.Lock()
	if ix.df["苹果"] != 2 || len(ix.files) != 2 {
		t.Fatalf("预热后 df/files = %d/%d", ix.df["苹果"], len(ix.files))
	}
	lenBefore := ix.totalLen
	ix.mu.Unlock()

	if err := s.Forget(m1.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Search("苹果", 5); err != nil {
		t.Fatal(err)
	}
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if ix.df["苹果"] != 1 || len(ix.files) != 1 {
		t.Fatalf("删除后 df/files = %d/%d（应 1/1）", ix.df["苹果"], len(ix.files))
	}
	if ix.totalLen >= lenBefore {
		t.Fatalf("删除后总长未减少: %d → %d", lenBefore, ix.totalLen)
	}
}

// TestSearchCacheExternalEdits：绕过 Store 的外部手工编辑（改/
// 删/加）仍能被重新扫描发现。
func TestSearchCacheExternalEdits(t *testing.T) {
	dir := t.TempDir()
	s := Store{Dir: dir}
	ctx := context.Background()
	m, err := s.Add(ctx, "fact", "密码提示是蓝色雨伞")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Search("蓝色", 5); err != nil {
		t.Fatal(err)
	}

	// 外部改写同一文件（长度变化）。
	rawMemory(t, dir, filepath.Base(m.Path),
		"id: "+m.ID+"\ntype: fact\ncreated: "+m.Created.Format(traj.TimeFormat)+"\nsummary: 密码提示已更新\n",
		"密码提示是红色气球")
	if hits, err := s.Search("红色气球", 3); err != nil || len(hits) != 1 {
		t.Fatalf("外部改写未被发现: %+v, %v", hits, err)
	}
	if hits, _ := s.Search("蓝色雨伞", 3); len(hits) != 0 {
		t.Fatalf("旧内容不应再命中: %+v", hits)
	}

	// 外部删除。
	if err := os.Remove(m.Path); err != nil {
		t.Fatal(err)
	}
	if hits, _ := s.Search("红色气球", 3); len(hits) != 0 {
		t.Fatalf("外部删除未被发现: %+v", hits)
	}

	// 外部新增。
	rawMemory(t, dir, "000009_zzz99999.md",
		"id: zzz99999\ntype: note\ncreated: 2026-01-01T00:00:00.000Z\nsummary: 外部条目\n",
		"外部新增的词：云杉")
	if hits, err := s.Search("云杉", 3); err != nil || len(hits) != 1 || hits[0].ID != "zzz99999" {
		t.Fatalf("外部新增未被发现: %+v, %v", hits, err)
	}
}
