package traj

import "testing"

// TestSlugifyDotNames：Slugify 对纯点号输入的原样保留行为——这正是
// identity 删库事故的根源：Slugify("..") 返回 ".."，Join(Home(), "..")
// 解析到心智根本身，一次删除请求能把整个状态根一并抹掉。此处钉死
// Slugify 的契约（它只做字符折叠，不做语义拒绝），拒绝职责在
// identity 的 validateName；两侧测试互为印证，防止任何一侧悄悄漂移。
func TestSlugifyDotNames(t *testing.T) {
	cases := map[string]string{
		".":       ".",
		"..":      "..",
		"...":     "...",
		"../..":   "..-..",
		"..\\..":  "..-..",
		"a/../..": "a-..-..",
	}
	for in, want := range cases {
		if got := Slugify(in, 40); got != want {
			t.Fatalf("Slugify(%q) = %q，应为 %q", in, got, want)
		}
	}
}

// TestSlugifyDotNamesHaveNoSeparator：无论输入如何，产出绝不含路径
// 分隔符——slug 永远是单一路径分量（穿越只能靠整段 "."/".." 语义，
// 由调用方的包含检查兜底）。
func TestSlugifyDotNamesHaveNoSeparator(t *testing.T) {
	for _, in := range []string{"../..", "..\\..", "a/b/c", "\\\\srv\\share", "..%2F.."} {
		got := Slugify(in, 40)
		for _, r := range got {
			if r == '/' || r == '\\' {
				t.Fatalf("Slugify(%q) = %q 含分隔符", in, got)
			}
		}
	}
}

// TestDirNameWithDotSlug：轨迹目录名是 "<hex8>-<slug>"，slug 即便是
// ".." 也被前缀消化成合法分量（"abcd1234-.."），不构成穿越。
func TestDirNameWithDotSlug(t *testing.T) {
	name := DirName("01920123-4567-789a-bcde-f123456789ab", "..")
	if want := "01920123-.."; name != want {
		t.Fatalf("DirName = %q，应为 %q", name, want)
	}
}
