package system

import (
	"testing"
)

func TestLoadMissingFile(t *testing.T) {
	t.Setenv("MINDLOOP_HOME", t.TempDir())
	c, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("缺失配置应返回零值而非错误: %v", err)
	}
	if c.Version != 0 || len(c.Identities) != 0 {
		t.Fatalf("缺失配置应为零值: %+v", c)
	}
}

func TestSaveLoadRoundTripAndValidate(t *testing.T) {
	home := t.TempDir()
	c := &Config{Version: 1,
		Web:        WebConfig{Enabled: true, Host: "127.0.0.1", Port: 8080},
		Identities: map[string]Ident{"ada": {Enabled: true, Mind: true, Wecom: false}},
	}
	if err := Save(home, c); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(home)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !got.Identities["ada"].Mind || got.Web.Port != 8080 {
		t.Fatalf("往返不符: %+v", got)
	}
	// 非法端口被拒。
	bad := &Config{Web: WebConfig{Port: 99999}}
	if err := Save(home, bad); err == nil {
		t.Fatalf("非法端口应被拒")
	}
}

func TestDiff(t *testing.T) {
	c := &Config{
		Web:        WebConfig{Enabled: true},
		Identities: map[string]Ident{"ada": {Enabled: true, Mind: true, Wecom: true}, "bob": {Enabled: false, Mind: true}},
	}
	running := map[ChildName]bool{WebChild: true, MindChild("ada"): true, MindChild("bob"): true, WecomChild("zoe"): true}
	p := c.Diff(running)
	if !p.Start[WecomChild("ada")] {
		t.Fatalf("ada 的企微桥应启动")
	}
	if !p.Stop[MindChild("bob")] || !p.Stop[WecomChild("zoe")] {
		t.Fatalf("bob 心智（身份停用）与 zoe 桥（不在配置）应停止")
	}
	if p.Start[WebChild] || p.Stop[WebChild] {
		t.Fatalf("web 应维持不动")
	}
}

func TestDesiredOrderAndArgs(t *testing.T) {
	c := &Config{Web: WebConfig{Enabled: true, Host: "0.0.0.0", Port: 9090},
		Identities: map[string]Ident{"ada": {Enabled: true, Mind: true}}}
	d := c.Desired()
	if !d[WebChild] || !d[MindChild("ada")] {
		t.Fatalf("期望集不符: %+v", d)
	}
	if args := Args(WebChild, c); len(args) != 5 || args[1] != "--host" || args[2] != "0.0.0.0" {
		t.Fatalf("web 参数不符: %v", args)
	}
	if args := Args(MindChild("ada"), c); len(args) != 3 || args[1] != "run" || args[2] != "ada" {
		t.Fatalf("mind 参数不符: %v", args)
	}
	if args := Args(WecomChild("ada"), c); len(args) != 4 || args[1] != "run" || args[3] != "ada" {
		t.Fatalf("wecom 参数不符: %v", args)
	}
	if IdentityOf(MindChild("ada")) != "ada" || IdentityOf(WebChild) != "" {
		t.Fatalf("IdentityOf 不符")
	}
}
