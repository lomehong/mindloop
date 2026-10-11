package thread

import (
	"os"
	"strings"
	"testing"
	"time"
)

// TestAddUpdateManifestLifecycle：开线 → 推进 → 搁置 → 完结的全生命
// 周期；manifest 只带未完结线索、最近活动在前、done 不再注入。
func TestAddUpdateManifestLifecycle(t *testing.T) {
	dir := t.TempDir()

	t1, err := Add(dir, "周报自动化脚本调优")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(t1.ID, "th-") || t1.Status != Open {
		t.Fatalf("新线索形态不符: %+v", t1)
	}
	t2, err := Add(dir, "服务器磁盘告警排查")
	if err != nil {
		t.Fatal(err)
	}

	// 推进第一条（备注 + 保持 open），第二条搁置。
	if _, err := Update(dir, t1.ID, "", "脚本已写一半，卡在数据聚合"); err != nil {
		t.Fatal(err)
	}
	if _, err := Update(dir, t2.ID, Parked, ""); err != nil {
		t.Fatal(err)
	}

	// 完结第一条后 manifest 不再带它；搁置的仍带（parked 是"别忘了"）。
	if _, err := Update(dir, t1.ID, Done, "已上线"); err != nil {
		t.Fatal(err)
	}
	m := Manifest(dir, time.Now())
	if strings.Contains(m, "周报自动化") {
		t.Fatalf("完结线索不应注入:\n%s", m)
	}
	if !strings.Contains(m, "服务器磁盘告警排查") || !strings.Contains(m, "parked") {
		t.Fatalf("搁置线索应在 manifest:\n%s", m)
	}
	if !strings.Contains(m, "进度怎么样") {
		t.Fatalf("manifest 应带指代解析的定位说明:\n%s", m)
	}

	// 无未完结：manifest 空串。
	if _, err := Update(dir, t2.ID, Done, ""); err != nil {
		t.Fatal(err)
	}
	if m := Manifest(dir, time.Now()); m != "" {
		t.Fatalf("全部完结后 manifest 应为空: %q", m)
	}
}

// TestUpdatePrefixAmbiguity：前缀多义拒绝（与 policy.ResolveHash 同款
// ——歧义面前宁可报错）。
func TestUpdatePrefixAmbiguity(t *testing.T) {
	dir := t.TempDir()
	a, _ := Add(dir, "线索一")
	b, _ := Add(dir, "线索二")
	// 多义前缀：两条线索 ID 的公共前缀（ID = th-<毫秒时间戳>，同
	// 毫秒附加序号区分）。用第一条与第二条 ID 的公共前缀（截到分叉
	// 点之前）构造多义——前缀匹配到两条即必须拒绝。
	prefix := a.ID
	for len(prefix) > 2 {
		matches := 0
		for _, it := range []Thread{a, b} {
			if strings.HasPrefix(it.ID, prefix) {
				matches++
			}
		}
		if matches > 1 {
			break
		}
		prefix = prefix[:len(prefix)-1]
	}
	if _, err := Update(dir, prefix, Done, ""); err == nil {
		t.Fatalf("多义前缀 %q 应拒绝", prefix)
	}
	// 完整 ID 唯一命中。
	if _, err := Update(dir, a.ID, Done, ""); err != nil {
		t.Fatalf("完整 ID 应唯一命中: %v", err)
	}
}

// TestValidateAndPersistence：空标题拒绝；文件真实落盘（list 语义
// 的读侧一致）；损坏文件返回错误（fail-fast 而非静默空）。
func TestValidateAndPersistence(t *testing.T) {
	dir := t.TempDir()
	if _, err := Add(dir, "  "); err == nil {
		t.Fatal("空标题应拒绝")
	}
	long := strings.Repeat("长", 61) // 122 字节
	if _, err := Add(dir, long); err == nil {
		t.Fatal("超限标题应拒绝")
	}
	if _, err := Add(dir, "合法线索"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(Path(dir)); err != nil {
		t.Fatalf("threads.json 未落盘: %v", err)
	}
	if err := os.WriteFile(Path(dir), []byte("not-json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("损坏文件应报错（调用方 Manifest 侧选择静默降级）")
	}
}
