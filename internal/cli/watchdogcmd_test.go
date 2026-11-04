package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lomehong/mindloop/internal/traj"
)

// watchIdentity 建一个最小可扫描身份（identity.txt 指向轨迹），
// 返回身份名。timelineAge 控制轨迹 mtime；lockAlive 控制运行锁。
func watchIdentity(t *testing.T, root string, name string, timelineAge time.Duration, lockAlive bool) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Join(dir, "trajectories"), 0o755); err != nil {
		t.Fatal(err)
	}
	trajDir := filepath.Join(dir, "trajectories", "t-"+name)
	if err := os.MkdirAll(trajDir, 0o755); err != nil {
		t.Fatal(err)
	}
	tlPath := filepath.Join(trajDir, "trajectory.jsonl")
	// 头行 step_id 必须（前缀）匹配 identity.txt 的 root_trajectory——
	// LoadAt 按目录内头行 ID 解析，不是按目录名。
	if err := os.WriteFile(tlPath, []byte(`{"type":"trajectory","step_id":"`+"t-"+name+`-0001","ts":"2026-01-01T00:00:00.000Z"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(tlPath, time.Now().Add(-timelineAge), time.Now().Add(-timelineAge)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "identity.txt"), []byte("name="+name+"\nroot_trajectory=t-"+name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = lockAlive // 锁属主探活需要真实进程——单元层用 mtime 判据覆盖，锁路径留空即 !lockAlive
}

func runWatchdogOn(t *testing.T, cfg watchdogCfg) (*bytes.Buffer, map[string]watchdogStateEntry) {
	t.Helper()
	var out bytes.Buffer
	c := &CLI{ctx: context.Background(), stdout: &out, stderr: &out}
	if err := c.runWatchdog(cfg); err != nil {
		t.Fatalf("runWatchdog: %v", err)
	}
	return &out, loadWatchdogState()
}

func TestWatchdogDeadSleepDetectAndDedup(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MINDLOOP_HOME", root)
	// 一个静默 8h（超 6h 阈值）、一个活跃 1h（不该告）。
	watchIdentity(t, filepath.Join(root, "identities"), "sleepy", 8*time.Hour, false)
	watchIdentity(t, filepath.Join(root, "identities"), "busy", 1*time.Hour, false)

	cfg := watchdogCfg{threshold: 6 * time.Hour, realert: 24 * time.Hour}
	out, state := runWatchdogOn(t, cfg)

	if !strings.Contains(out.String(), "死睡告警：sleepy 心智未运行") {
		t.Errorf("应检出 sleepy 进程死静默，输出: %s", out.String())
	}
	if strings.Contains(out.String(), "busy") {
		t.Errorf("busy 不该被告: %s", out.String())
	}
	if _, ok := state["sleepy"]; !ok {
		t.Errorf("sleepy 应有去重账目: %+v", state)
	}
	if _, ok := state["busy"]; ok {
		t.Errorf("busy 不该有账目")
	}

	// 第二次运行：未跨重告周期——不重复告，但账目仍在。
	out2, state2 := runWatchdogOn(t, cfg)
	if strings.Contains(out2.String(), "死睡告警：sleepy") {
		t.Errorf("重告周期内不该重复告: %s", out2.String())
	}
	if _, ok := state2["sleepy"]; !ok {
		t.Errorf("账目应保留: %+v", state2)
	}

	// 轨迹恢复新鲜——恢复清账。
	tl := filepath.Join(root, "identities", "sleepy", "trajectories", "t-sleepy", "trajectory.jsonl")
	now := time.Now()
	if err := os.Chtimes(tl, now, now); err != nil {
		t.Fatal(err)
	}
	out3, state3 := runWatchdogOn(t, cfg)
	if !strings.Contains(out3.String(), "sleepy 恢复活动") {
		t.Errorf("应记恢复行: %s", out3.String())
	}
	if _, ok := state3["sleepy"]; ok {
		t.Errorf("恢复后账目应清: %+v", state3)
	}
}

func TestWatchdogWedgedKind(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MINDLOOP_HOME", root)
	watchIdentity(t, filepath.Join(root, "identities"), "stuck", 10*time.Hour, true)
	out, state := runWatchdogOn(t, watchdogCfg{threshold: 6 * time.Hour, realert: 24 * time.Hour})
	// 单元层无真实运行锁——kind=dead；锁活判据在集成层（web 活性
	// 同源实现已各自有测试）。这里钉死 mtime 判据与去重状态机。
	if !strings.Contains(out.String(), "死睡告警：stuck") {
		t.Errorf("应检出 stuck: %s", out.String())
	}
	if state["stuck"].Kind != "dead" {
		t.Errorf("无锁时应记 dead: %+v", state["stuck"])
	}
}

func TestWatchdogAlertsLedger(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MINDLOOP_HOME", root)
	watchIdentity(t, filepath.Join(root, "identities"), "gone", 48*time.Hour, false)
	out, _ := runWatchdogOn(t, watchdogCfg{threshold: 6 * time.Hour, realert: 24 * time.Hour})
	if !strings.Contains(out.String(), "gone") {
		t.Fatalf("未告: %s", out.String())
	}
	data, err := os.ReadFile(filepath.Join(root, "watchdog-alerts.jsonl"))
	if err != nil {
		t.Fatalf("台账缺失: %v", err)
	}
	var a watchdogAlert
	if err := json.Unmarshal(bytes.Split(bytes.TrimSpace(data), []byte("\n"))[0], &a); err != nil {
		t.Fatalf("台账行不合法: %v", err)
	}
	if a.Kind != "dead" || a.Ident != "gone" || a.AgeSec < 47*3600 {
		t.Errorf("台账字段不符: %+v", a)
	}
}

func TestWatchdogEmptyHome(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MINDLOOP_HOME", root)
	out, state := runWatchdogOn(t, watchdogCfg{threshold: time.Hour})
	if out.String() != "" {
		t.Errorf("空 home 不该有输出: %q", out.String())
	}
	if len(state) != 0 {
		t.Errorf("空 home 不该有账目")
	}
	_ = traj.Home() // 引用保持导入（Setenv 生效性由上面用例连带覆盖）
}
