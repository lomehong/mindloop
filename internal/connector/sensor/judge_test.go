package sensor

import (
	"fmt"
	"testing"
	"time"
)

func mustCfg(t *testing.T, data string) *SensorConfig {
	t.Helper()
	f, err := Parse([]byte(`{"version":1,"sensors":[` + data + `]}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return &f.Sensors[0]
}

func TestJudgeDefaultAndDedup(t *testing.T) {
	cfg := mustCfg(t, `{"id":"w","type":"web","url":"https://example.com"}`)
	st := NewState()
	now := time.Now()
	e := PEvent{Kind: KindChanged, Subject: "https://example.com", Dedup: "abc", Digest: "标题变了"}

	d := Judge(cfg, st, ReflexView{}, e, now)
	if d.Duplicate {
		t.Fatalf("首个事件不应判重")
	}
	if d.Salience != S1 || d.Reason != "default" {
		t.Fatalf("web 缺省应 s1/default，得 %s/%s", d.Salience, d.Reason)
	}
	// 同指纹窗口内 = 重复。
	if d := Judge(cfg, st, ReflexView{}, e, now.Add(time.Minute)); !d.Duplicate {
		t.Fatalf("同指纹 1 分钟后应判重")
	}
	// 指纹变了（内容再变）= 新事件。
	e2 := e
	e2.Dedup = "def"
	if d := Judge(cfg, st, ReflexView{}, e2, now.Add(2*time.Minute)); d.Duplicate {
		t.Fatalf("指纹变了不应判重")
	}
}

func TestJudgeRulesCooldownAndAgenda(t *testing.T) {
	cfg := mustCfg(t, `{"id":"repo","type":"file","path":"D:/work/app",
		"salience":{"rules":[{"name":"prod-fire","keywords":["prod","生产"],"salience":"s3"}]}}`)
	st := NewState()
	now := time.Now()

	// 议程关联：主体落在观察路径下，无规则命中 → 保底 s1。
	d := Judge(cfg, st, ReflexView{Paths: []string{"D:/work"}}, PEvent{Subject: `D:\work\app\main.go`}, now)
	if d.Salience != S1 || d.Reason != "agenda-match" {
		t.Fatalf("路径关联应 s1/agenda-match，得 %s/%s", d.Salience, d.Reason)
	}

	// 规则命中 → s3 + reason=rule:名。
	d = Judge(cfg, st, ReflexView{}, PEvent{Subject: "D:/work/app/x.log", Digest: "PROD 环境报错"}, now)
	if d.Salience != S3 || d.Reason != "rule:prod-fire" {
		t.Fatalf("规则命中应 s3/rule:prod-fire，得 %s/%s", d.Salience, d.Reason)
	}

	// 冷却语义（按主体）：同主体的变体指纹绕过去重也不能风暴；
	// 不同主体各自升档（冷却管变体，去重管重复）。
	e2 := PEvent{Subject: "D:/work/app/x.log", Dedup: "other", Digest: "生产又炸了"}
	d = Judge(cfg, st, ReflexView{}, e2, now.Add(time.Minute))
	if d.Salience == S3 {
		t.Fatalf("同主体冷却期内不应再升到 s3")
	}
	d = Judge(cfg, st, ReflexView{}, PEvent{Subject: "D:/work/app/y.log", Dedup: "y1", Digest: "生产又炸了"}, now.Add(2*time.Minute))
	if d.Salience != S3 {
		t.Fatalf("不同主体应各自升档，得 %s/%s", d.Salience, d.Reason)
	}
}

func TestJudgeViewKeywords(t *testing.T) {
	cfg := mustCfg(t, `{"id":"w","type":"web","url":"https://x.example","salience":{"default":"s0"}}`)
	st := NewState()
	now := time.Now()
	d := Judge(cfg, st, ReflexView{Keywords: []string{"mindloop"}}, PEvent{Subject: "https://x.example", Digest: "MindLoop 发新版本"}, now)
	if d.Salience != S1 || d.Reason != "agenda-match" {
		t.Fatalf("关键词关联应 s1/agenda-match，得 %s/%s", d.Salience, d.Reason)
	}
}

func TestStateRebuildSuppressesWakeAfterRestart(t *testing.T) {
	cfg := mustCfg(t, `{"id":"w","type":"web","url":"https://x.example"}`)
	now := time.Now()
	// 第一次进程： fired 一条 s2 事件。
	st1 := NewState()
	Judge(cfg, st1, ReflexView{}, PEvent{Kind: KindChanged, Subject: "u", Dedup: "k1"}, now)

	// 从重建记录恢复：同指纹在窗口内不重复。
	records := []EventRecord{{TS: now.Add(time.Minute), Source: "w", Kind: KindChanged, Subject: "u", Dedup: "k1", Salience: string(S2)}}
	st2 := NewState()
	st2.Rebuild(records, now.Add(-2*time.Hour))
	d := Judge(cfg, st2, ReflexView{}, PEvent{Kind: KindChanged, Subject: "u", Dedup: "k1"}, now.Add(2*time.Minute))
	if !d.Duplicate {
		t.Fatalf("重启重建后同指纹应仍判重（重启不重复唤醒）")
	}
	// S0 沉淀不占去重窗。
	st3 := NewState()
	st3.Rebuild([]EventRecord{{TS: now, Source: "w", Kind: KindChanged, Subject: "u", Dedup: "k2", Salience: string(S0)}}, now.Add(-2*time.Hour))
	d = Judge(cfg, st3, ReflexView{}, PEvent{Kind: KindChanged, Subject: "u", Dedup: "k2"}, now.Add(time.Minute))
	if d.Duplicate {
		t.Fatalf("S0 沉淀不应占去重窗")
	}
}

// TestStateRebuildRuleCooldown：重启重建的规则冷却必须与 Judge 同键
// （含主体）——键不一致等于重建白做，重启后同主体变体再次升档；
// reason 的复合后缀（+spike）也不得污染规则名。
func TestStateRebuildRuleCooldown(t *testing.T) {
	cfg := mustCfg(t, `{"id":"f","type":"file","path":".","learning_days":-1,
		"salience":{"rules":[{"name":"prod","keywords":["生产"],"salience":"s2"}]}}`)
	now := time.Now()

	records := []EventRecord{
		{TS: now.Add(-time.Minute), Source: "f", Kind: KindChanged, Subject: "a.txt",
			Dedup: "k1", Salience: string(S2), Reason: "rule:prod+spike"},
	}
	st := NewState()
	st.Rebuild(records, now.Add(-2*time.Hour))

	// 同主体变体：重建的冷却应压住升档，reason 保留冷却信息。
	d := Judge(cfg, st, ReflexView{}, PEvent{Subject: "a.txt", Dedup: "k2", Digest: "生产又炸"}, now)
	if d.Salience >= S2 || !contains(d.Reason, "冷却") {
		t.Fatalf("重启后同主体规则冷却应生效（冷却键含主体），得 %s/%s", d.Salience, d.Reason)
	}
	// 不同主体照常各自升档（冷却管主体，不是全局）。
	d = Judge(cfg, st, ReflexView{}, PEvent{Subject: "b.txt", Dedup: "k3", Digest: "生产告警"}, now)
	if d.Salience != S2 || d.Reason != "rule:prod" {
		t.Fatalf("不同主体应各自升档，得 %s/%s", d.Salience, d.Reason)
	}
}

func TestQuietWindow(t *testing.T) {
	loc := time.FixedZone("t", 0)
	base := time.Date(2026, 10, 2, 23, 30, 0, 0, loc)
	q := &QuietWindow{Start: "23:00", End: "08:00"}
	if !q.Active(base) {
		t.Fatalf("23:30 应在 23:00-08:00 窗口内")
	}
	if q.Active(base.Add(9 * time.Hour)) {
		t.Fatalf("08:30 不应在窗口内")
	}
	// 跨午夜边界。
	if !q.Active(time.Date(2026, 10, 3, 7, 59, 0, 0, loc)) {
		t.Fatalf("07:59 应在窗口内")
	}
	if q.Active(time.Date(2026, 10, 3, 8, 0, 0, 0, loc)) {
		t.Fatalf("08:00 整不应在窗口内（窗口半开）")
	}
}

// TestJudgeRuleCooldownPerSubject：冷却键含主体——同一主体的变体
// 风暴被压住，不同主体各自升档（黑盒验收实证：全局冷却键把目录里
// 不同的命中文件全部吞掉）。
func TestJudgeRuleCooldownPerSubject(t *testing.T) {
	cfg := mustCfg(t, `{"id":"w","type":"file","path":".","learning_days":-1,
		"salience":{"rules":[{"name":"prod","keywords":["生产"],"salience":"s2"}]}}`)
	st := NewState()
	now := time.Now()

	// 主体 A 首次命中 → s2。
	d := Judge(cfg, st, ReflexView{}, PEvent{Subject: "D:/w/a.txt", Dedup: "k1", Digest: "生产告警"}, now)
	if d.Salience != S2 || d.Reason != "rule:prod" {
		t.Fatalf("首命中应 s2/rule:prod，得 %s/%s", d.Salience, d.Reason)
	}
	// 同主体变体（冷却期内）→ 不升档，reason 保留冷却信息。
	d = Judge(cfg, st, ReflexView{Paths: []string{"D:/w"}}, PEvent{Subject: "D:/w/a.txt", Dedup: "k2", Digest: "生产告警2"}, now.Add(time.Minute))
	if d.Salience >= S2 {
		t.Fatalf("同主体冷却期内不应再升 s2，得 %s", d.Salience)
	}
	if !contains(d.Reason, "冷却") {
		t.Fatalf("冷却信息应留在 reason，得 %q", d.Reason)
	}
	// 不同主体 → 各自升档（去重管同内容重复，冷却管同主体变体）。
	d = Judge(cfg, st, ReflexView{}, PEvent{Subject: "D:/w/b.txt", Dedup: "k3", Digest: "生产日报"}, now.Add(2*time.Minute))
	if d.Salience != S2 || d.Reason != "rule:prod" {
		t.Fatalf("不同主体应各自升档，得 %s/%s", d.Salience, d.Reason)
	}
}

// TestJudgeThresholdRule：阈值越界（Min）——词面命中但数值不够不
// 升档，达到门槛才升。
func TestJudgeThresholdRule(t *testing.T) {
	cfg := mustCfg(t, `{"id":"m","type":"file","path":".","learning_days":-1,
		"salience":{"rules":[{"name":"err-rate","keywords":["错误"],"min":100,"salience":"s2"}]}}`)
	st := NewState()
	now := time.Now()

	d := Judge(cfg, st, ReflexView{}, PEvent{Subject: "app.log", Dedup: "1", Digest: "错误数 5 条"}, now)
	if d.Salience >= S2 {
		t.Fatalf("数值未达门槛不应升档，得 %s/%s", d.Salience, d.Reason)
	}
	d = Judge(cfg, st, ReflexView{}, PEvent{Subject: "app.log", Dedup: "2", Digest: "错误数 500 条"}, now.Add(time.Second))
	if d.Salience != S2 || d.Reason != "rule:err-rate" {
		t.Fatalf("达门槛应升档，得 %s/%s", d.Salience, d.Reason)
	}
}

// TestJudgeFirstSeenAndSpike：鼻的首次出现（appeared 新主体保底 s1）
// 与速率突增（同 kind 5 分钟 ≥5 条且 ≥ 前窗 3 倍 → 保底 s2）。
func TestJudgeFirstSeenAndSpike(t *testing.T) {
	cfg := mustCfg(t, `{"id":"f","type":"file","path":"."}`)
	st := NewState()
	now := time.Now()

	// appeared 新主体 → s1 +first-seen。
	d := Judge(cfg, st, ReflexView{}, PEvent{Kind: KindAppeared, Subject: "new.txt", Dedup: "n1"}, now)
	if d.Salience != S1 || !contains(d.Reason, "first-seen") {
		t.Fatalf("appeared 新主体应 s1+first-seen，得 %s/%s", d.Salience, d.Reason)
	}
	// changed 类首见不抬档。
	d = Judge(cfg, st, ReflexView{}, PEvent{Kind: KindChanged, Subject: "c.txt", Dedup: "c1"}, now)
	if d.Salience != S0 || contains(d.Reason, "first-seen") {
		t.Fatalf("changed 首见不应抬档，得 %s/%s", d.Salience, d.Reason)
	}

	// 速率突增：30 分钟窗内先垫 1 条旧行为（>5m 前），再在 5m 内
	// 打满 5 条 → recent=5 ≥ 5 且 ≥ 3*older=3 → 保底 s2。
	for i := 0; i < 1; i++ {
		Judge(cfg, st, ReflexView{}, PEvent{Kind: KindChanged, Subject: fmt.Sprintf("old%d.txt", i), Dedup: fmt.Sprintf("o%d", i)}, now.Add(-20*time.Minute))
	}
	var d2 Decision
	for i := 0; i < 5; i++ {
		d2 = Judge(cfg, st, ReflexView{}, PEvent{Kind: KindChanged, Subject: fmt.Sprintf("burst%d.txt", i), Dedup: fmt.Sprintf("b%d", i)}, now.Add(time.Duration(i)*time.Second))
	}
	if d2.Salience < S2 || !contains(d2.Reason, "spike") {
		t.Fatalf("速率突增应保底 s2+spike，得 %s/%s", d2.Salience, d2.Reason)
	}
}

func TestConfigValidation(t *testing.T) {
	cases := []struct {
		name    string
		data    string
		wantErr string
	}{
		{"id 缺失", `{"type":"file","path":"."}`, "id 不能为空"},
		{"未知类型", `{"id":"a","type":"camera","path":"."}`, "未知类型"},
		{"web 缺 url", `{"id":"a","type":"web"}`, "需要 url"},
		{"url 非法", `{"id":"a","type":"web","url":"ftp://x"}`, "http(s)"},
		{"规则缺档", `{"id":"a","type":"file","path":".","salience":{"rules":[{"name":"r","keywords":["x"]}]}}`, "非法"},
		{"quiet 非法", `{"id":"a","type":"file","path":".","quiet":{"start":"25:00","end":"08:00"}}`, "quiet.start"},
		{"id 重复", `{"id":"a","type":"file","path":"."},{"id":"a","type":"web","url":"https://x"}`, "重复"},
	}
	for _, tc := range cases {
		_, err := Parse([]byte(`{"version":1,"sensors":[` + tc.data + `]}`))
		if err == nil || !contains(err.Error(), tc.wantErr) {
			t.Fatalf("%s: 应报 %q，得 %v", tc.name, tc.wantErr, err)
		}
	}
	// 合法配置整包通过。
	if _, err := Parse([]byte(`{"version":1,"sensors":[
		{"id":"w","type":"file","path":".","enabled":false,"dedup_window":"5m","rule_cooldown":"1h"},
		{"id":"g","type":"web","url":"https://x","interval":"5m","expect_every":"24h"}
	]}`)); err != nil {
		t.Fatalf("合法配置不应报错: %v", err)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || index(s, sub) >= 0)
}

func index(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestBucket(t *testing.T) {
	now := time.Now()
	b := NewBucket(60, 3, now) // 60/分钟，突发 3
	for i := 0; i < 3; i++ {
		if ok, _ := b.Allow(now); !ok {
			t.Fatalf("突发窗口第 %d 个应放行", i+1)
		}
	}
	if ok, dropped := b.Allow(now); ok || dropped != 0 {
		t.Fatalf("突发耗尽应拒绝，得 ok=%v dropped=%d", ok, dropped)
	}
	// 拒绝调用按设计不携带累计数（计数在下一次放行时折叠返回）。
	if ok, dropped := b.Allow(now.Add(500 * time.Millisecond)); ok || dropped != 0 {
		t.Fatalf("持续超限应拒绝，得 ok=%v dropped=%d", ok, dropped)
	}
	// 补充满一个令牌后放行，丢弃数折叠返回（+5s 远超 1 token 的补充）。
	if ok, dropped := b.Allow(now.Add(5 * time.Second)); !ok || dropped != 2 {
		t.Fatalf("补充后应放行并折叠丢弃数，得 ok=%v dropped=%d", ok, dropped)
	}
}
