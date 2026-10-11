package profile

import (
	"strings"
	"testing"
)

// TestSetUpdateManifest：插入 → 覆盖（updated 刷新、evidence 保留）
// → manifest 排序与定位语 → remove 幂等。
func TestSetUpdateManifest(t *testing.T) {
	dir := t.TempDir()
	if _, err := Set(dir, "沟通风格", "直接、要点先行", "msg00000042"); err != nil {
		t.Fatal(err)
	}
	first, err := Load(dir)
	if err != nil || len(first) != 1 || first[0].Evidence != "msg00000042" {
		t.Fatalf("首次写入形态不符: %+v err=%v", first, err)
	}

	// 同 key 覆盖：无 evidence 保留原证据。
	if _, err := Set(dir, "沟通风格", "直接、要点先行、少客套", ""); err != nil {
		t.Fatal(err)
	}
	attrs, _ := Load(dir)
	if len(attrs) != 1 || attrs[0].Value != "直接、要点先行、少客套" || attrs[0].Evidence != "msg00000042" {
		t.Fatalf("覆盖后形态不符: %+v", attrs)
	}

	// manifest：key 排序 + 定位语；data 全文无（value 就是内容）。
	if _, err := Set(dir, "技术领域", "Go 与 AI Agent 工程化", ""); err != nil {
		t.Fatal(err)
	}
	m := Manifest(dir)
	for _, want := range []string{
		"用户画像",
		"以对话为准",
		"技术领域：Go 与 AI Agent 工程化",
		"沟通风格：直接、要点先行、少客套",
	} {
		if !strings.Contains(m, want) {
			t.Fatalf("manifest 缺 %q:\n%s", want, m)
		}
	}
	// manifest 按 key 排序（稳定呈现）。
	idxStyle := strings.Index(m, "沟通风格")
	idxTech := strings.Index(m, "技术领域")
	if idxStyle == -1 || idxTech == -1 || idxStyle < idxTech {
		t.Fatalf("manifest 应按 key 排序:\n%s", m)
	}

	// remove：幂等。
	if !Remove(dir, "技术领域") {
		t.Fatal("remove 应报 changed")
	}
	if Remove(dir, "技术领域") {
		t.Fatal("重复 remove 不应 changed")
	}
	if m := Manifest(dir); strings.Contains(m, "技术领域") {
		t.Fatalf("移除后 manifest 不应含该属性:\n%s", m)
	}
}

// TestSetValidation：空 key/value、超限载荷拒绝。
func TestSetValidation(t *testing.T) {
	dir := t.TempDir()
	if _, err := Set(dir, "", "v", ""); err == nil {
		t.Fatal("空 key 应拒绝")
	}
	if _, err := Set(dir, "k", "  ", ""); err == nil {
		t.Fatal("空 value 应拒绝")
	}
	if _, err := Set(dir, strings.Repeat("k", 65), "v", ""); err == nil {
		t.Fatal("超限 key 应拒绝")
	}
	if _, err := Set(dir, "k", strings.Repeat("v", 2*1024+1), ""); err == nil {
		t.Fatal("超限 value 应拒绝")
	}
}

// TestManifestEmpty：空/缺失画像 → 空串（调用方不注入空段）。
func TestManifestEmpty(t *testing.T) {
	if m := Manifest(t.TempDir()); m != "" {
		t.Fatalf("空画像应渲染空串: %q", m)
	}
}
