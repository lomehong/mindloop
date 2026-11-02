package sensor

import "testing"

// P1-11 钉子：可选 Bearer 第二道鉴权的四态。
func TestVerifyHookBearer(t *testing.T) {
	if err := VerifyHookBearer("", ""); err != nil {
		t.Fatalf("未配置 bearer 应恒通过（单防线现状）: %v", err)
	}
	if err := VerifyHookBearer("", "Bearer anything"); err != nil {
		t.Fatalf("未配置 bearer 时带任意头也应通过: %v", err)
	}
	if err := VerifyHookBearer("s3cret", ""); err == nil {
		t.Fatal("配置后缺 Authorization 头应拒绝")
	}
	if err := VerifyHookBearer("s3cret", "Basic s3cret"); err == nil {
		t.Fatal("非 Bearer 方案应拒绝")
	}
	if err := VerifyHookBearer("s3cret", "Bearer wrong"); err == nil {
		t.Fatal("错值应拒绝")
	}
	if err := VerifyHookBearer("s3cret", "Bearer s3cret"); err != nil {
		t.Fatalf("对值应通过: %v", err)
	}
}
