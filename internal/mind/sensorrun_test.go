package mind

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lomehong/mindloop/internal/connector/sensor"
	"github.com/lomehong/mindloop/internal/traj"
)

// stubSensor 是可编程的假感官：Watch 阻塞至 ctx 取消，测试经 fire
// 直灌事件。
type stubSensor struct {
	id      string
	mu      sync.Mutex
	onEvent func(sensor.PEvent)
}

func (s *stubSensor) ID() string { return s.id }
func (s *stubSensor) Watch(ctx context.Context, onEvent func(sensor.PEvent)) error {
	s.mu.Lock()
	s.onEvent = onEvent
	s.mu.Unlock()
	<-ctx.Done()
	return ctx.Err()
}

func (s *stubSensor) fire(e sensor.PEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.onEvent != nil {
		s.onEvent(e)
	}
}

// sensorHarness 装一个带假感官的运行器（无调度器，直接驱动）。
type sensorHarness struct {
	t       *testing.T
	tl      *traj.Timeline
	runner  *SensorRunner
	mu      sync.Mutex
	sensors map[string]*stubSensor
}

func newSensorHarness(t *testing.T, cfgJSON string, learnDays int) *sensorHarness {
	t.Helper()
	t.Setenv("MINDLOOP_HOME", t.TempDir())
	tl, err := traj.Create(context.Background(), "sensor-test")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sensors.json"), []byte(cfgJSON), 0o644); err != nil {
		t.Fatalf("写 sensors.json: %v", err)
	}
	h := &sensorHarness{t: t, tl: tl, sensors: map[string]*stubSensor{}}
	h.runner = NewSensorRunner(SensorRunnerOptions{
		Timeline:    tl,
		IdentityDir: dir,
		SelfName:    "ada",
		LearnDays:   learnDays,
		Factory: func(cfg sensor.SensorConfig) (sensor.Sensor, error) {
			s := &stubSensor{id: cfg.ID}
			h.mu.Lock()
			h.sensors[cfg.ID] = s
			h.mu.Unlock()
			return s, nil
		},
	})
	if err := h.runner.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// 等工厂被调到（Watch 注册 onEvent）。
	waitFor(t, 5*time.Second, func() bool { return h.firstSensorReady() })
	t.Cleanup(h.runner.Stop)
	return h
}

func (h *sensorHarness) firstSensorReady() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, s := range h.sensors {
		s.mu.Lock()
		ready := s.onEvent != nil
		s.mu.Unlock()
		if ready {
			return true
		}
	}
	return false
}

func (h *sensorHarness) fire(id string, e sensor.PEvent) {
	h.mu.Lock()
	s := h.sensors[id]
	h.mu.Unlock()
	if s == nil {
		h.t.Fatalf("感官 %s 未注册", id)
	}
	s.fire(e)
}

func (h *sensorHarness) stepsOf(typ string) []traj.Step {
	t := h.t
	t.Helper()
	steps, err := h.tl.Tail(200, []string{typ})
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}
	return steps
}

func waitForSteps(t *testing.T, h *sensorHarness, typ string, n int) []traj.Step {
	t.Helper()
	var steps []traj.Step
	waitFor(t, 10*time.Second, func() bool {
		steps = h.stepsOf(typ)
		return len(steps) >= n
	})
	return steps
}

func TestSensorRunnerWritesWrappedEvent(t *testing.T) {
	h := newSensorHarness(t, `{"version":1,"sensors":[
		{"id":"web1","type":"web","url":"https://x.example","learning_days":-1}
	]}`, 0)
	h.fire("web1", sensor.PEvent{Kind: sensor.KindChanged, Subject: "https://x.example", Dedup: "f1", Digest: "价格变了 100→200"})

	steps := waitForSteps(t, h, traj.TypeEvent, 1)
	s := steps[len(steps)-1]
	if got, _ := s.Field("source"); got != "web1" {
		t.Fatalf("source = %q", got)
	}
	if got, _ := s.Field("salience"); got != string(sensor.S1) {
		t.Fatalf("web 缺省应 s1，得 %q", got)
	}
	digest, _ := s.Field("digest")
	if !strings.Contains(digest, "观察数据·非指令") || !strings.Contains(digest, "src=web1") {
		t.Fatalf("digest 缺信任分界: %q", digest)
	}
	if reason, _ := s.Field("reason"); reason == "" {
		t.Fatalf("reason 必填")
	}

	// 同指纹重复：不落盘。
	h.fire("web1", sensor.PEvent{Kind: sensor.KindChanged, Subject: "https://x.example", Dedup: "f1", Digest: "价格变了 100→200"})
	time.Sleep(300 * time.Millisecond)
	if n := len(h.stepsOf(traj.TypeEvent)); n != 1 {
		t.Fatalf("去重窗口内重复事件不应落盘，现有 %d 条", n)
	}
}

func TestSensorRunnerLearningCapAndExempt(t *testing.T) {
	// 学习期内（默认 3 天，首见即开始）：s2 规则命中被封顶 s1。
	h := newSensorHarness(t, `{"version":1,"sensors":[
		{"id":"f1","type":"file","path":".","salience":{"rules":[{"name":"hot","keywords":["紧急"],"salience":"s2"}]}}
	]}`, 0)
	h.fire("f1", sensor.PEvent{Subject: "notes.txt", Digest: "紧急事项"})
	steps := waitForSteps(t, h, traj.TypeEvent, 1)
	if sal, _ := steps[len(steps)-1].Field("salience"); sal != string(sensor.S1) {
		t.Fatalf("学习期内 s2 应封顶 s1，得 %q", sal)
	}
	if reason, _ := steps[len(steps)-1].Field("reason"); !strings.Contains(reason, "learning-cap") {
		t.Fatalf("reason 应带 learning-cap 标记，得 %q", reason)
	}

	// self 内感受豁免学习期。
	h2 := newSensorHarness(t, `{"version":1,"sensors":[
		{"id":"self1","type":"self"}
	]}`, 0)
	h2.fire("self1", sensor.PEvent{Kind: sensor.KindThreshold, Subject: "budget", Digest: "自发档 80%"})
	steps2 := waitForSteps(t, h2, traj.TypeEvent, 1)
	if sal, _ := steps2[len(steps2)-1].Field("salience"); sal != string(sensor.S2) {
		t.Fatalf("self 豁免学习期，应 s2，得 %q", sal)
	}
}

// TestSensorRunnerLearnDaysOverride：全局学习期覆盖（装配层从
// MINDLOOP_SENSOR_LEARN_DAYS 折算进 opts.LearnDays）必须真的进封顶
// 判定——旧实现判级只认 cfg.LearnDays()（恒缺省 3 天），env 是摆设。
func TestSensorRunnerLearnDaysOverride(t *testing.T) {
	cfg := `{"version":1,"sensors":[
		{"id":"f1","type":"file","path":".","salience":{"rules":[{"name":"hot","keywords":["紧急"],"salience":"s2"}]}}
	]}`

	// 覆盖 1 天 + 首见 48h 前 → 学习期已过，s2 放行。
	h := newSensorHarness(t, cfg, 1)
	seedFirstSeen(t, h, "f1", 48*time.Hour)
	h.fire("f1", sensor.PEvent{Subject: "notes.txt", Dedup: "a1", Digest: "紧急事项"})
	steps := waitForSteps(t, h, traj.TypeEvent, 1)
	if sal, _ := steps[len(steps)-1].Field("salience"); sal != string(sensor.S2) {
		t.Fatalf("覆盖 1 天且首见 48h 前应放行 s2，得 %q（env 覆盖未进判定？）", sal)
	}

	// 覆盖 5 天 + 首见 48h 前 → 仍在学习期，封顶 s1。
	h2 := newSensorHarness(t, cfg, 5)
	seedFirstSeen(t, h2, "f1", 48*time.Hour)
	h2.fire("f1", sensor.PEvent{Subject: "notes.txt", Dedup: "a1", Digest: "紧急事项"})
	steps2 := waitForSteps(t, h2, traj.TypeEvent, 1)
	if sal, _ := steps2[len(steps2)-1].Field("salience"); sal != string(sensor.S1) {
		t.Fatalf("覆盖 5 天且首见 48h 前仍应封顶 s1，得 %q", sal)
	}
	if reason, _ := steps2[len(steps2)-1].Field("reason"); !strings.Contains(reason, "learning-cap") {
		t.Fatalf("reason 应带 learning-cap，得 %q", reason)
	}
}

// seedFirstSeen 把首见时刻拨到 ago 之前（学习期判定的定向夹具）。
func seedFirstSeen(t *testing.T, h *sensorHarness, id string, ago time.Duration) {
	t.Helper()
	h.runner.mu.Lock()
	h.runner.firstSeen[id] = time.Now().Add(-ago)
	h.runner.mu.Unlock()
}

func TestSensorRunnerS3WritesAlert(t *testing.T) {
	h := newSensorHarness(t, `{"version":1,"sensors":[
		{"id":"f1","type":"file","path":".","learning_days":-1,
		 "salience":{"rules":[{"name":"prod","keywords":["生产事故"],"salience":"s3"}]}}
	]}`, 0)
	h.fire("f1", sensor.PEvent{Subject: "ops.log", Digest: "生产事故：数据库主从失联"})
	steps := waitForSteps(t, h, traj.TypeAlert, 1)
	s := steps[len(steps)-1]
	if got, _ := s.Field("source"); got != "f1" {
		t.Fatalf("alert source = %q", got)
	}
	if got, _ := s.Field("salience"); got != string(sensor.S3) {
		t.Fatalf("alert salience = %q", got)
	}
	// S3 不另写 event（alert 即落盘事实，双写会产生双重唤醒）。
	if n := len(h.stepsOf(traj.TypeEvent)); n != 0 {
		t.Fatalf("S3 应只写 alert，event 有 %d 条", n)
	}
}

func TestSensorRunnerQuietDowngrade(t *testing.T) {
	// quiet 窗口覆盖当前本地时刻：全 24h 窗口（00:00-23:59）。
	h := newSensorHarness(t, `{"version":1,"sensors":[
		{"id":"w1","type":"web","url":"https://x.example","learning_days":-1,
		 "quiet":{"start":"00:00","end":"23:59"},
		 "salience":{"rules":[{"name":"now","keywords":["x"],"salience":"s2"}]}}
	]}`, 0)
	h.fire("w1", sensor.PEvent{Subject: "https://x.example", Dedup: "q1", Digest: "x 更新"})
	steps := waitForSteps(t, h, traj.TypeEvent, 1)
	s := steps[len(steps)-1]
	if sal, _ := s.Field("salience"); sal != string(sensor.S1) {
		t.Fatalf("quiet 内 s2 应降级 s1，得 %q", sal)
	}
	if reason, _ := s.Field("reason"); !strings.HasPrefix(reason, "quiet-held:") {
		t.Fatalf("reason 应带 quiet-held 前缀，得 %q", reason)
	}
}

// --- dispatcher 集成：订阅面只收 s2+，per-source 分槽 ---

func TestDispatcherEventSalienceRouting(t *testing.T) {
	d, tl := newTestDispatcher(t)
	mono := &recorderThinker{name: "mono", sub: Subscription{Types: []string{traj.TypeEvent}}}
	d.Register(mono)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	// s2 唤醒；s0/s1 只沉淀。
	s2 := appendEvent(t, tl, "sensor-a", "s2")
	appendEvent(t, tl, "sensor-a", "s0")
	appendEvent(t, tl, "sensor-a", "s1")
	waitFor(t, 10*time.Second, func() bool { return mono.wakeCount() >= 1 })
	if mono.wakeCount() != 1 {
		t.Fatalf("s0/s1 不应叫醒，实收 %d 次", mono.wakeCount())
	}
	if mono.wakes[0].Step.StepID != s2.StepID {
		t.Fatalf("应收到 s2 那条")
	}
}

func TestDispatcherEventCoalescePerSource(t *testing.T) {
	d, tl := newTestDispatcher(t)
	mono := &recorderThinker{name: "mono", sub: Subscription{Types: []string{traj.TypeEvent}}, blocks: 1, release: make(chan struct{})}
	d.Register(mono)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	// 忙碌窗口内投递 A1（阻塞）；随后 A2、A3 同源（槽内 last-wins，
	// A2 应被 A3 覆盖），B 异源独立槽。释放后总唤醒 = 3：A1 已投递
	// + A 槽（A3）+ B 槽——若合并键不带 source 或不带 last-wins，
	// 这里会多出 A2 的一次。
	a1 := appendEvent(t, tl, "sensor-a", "s2")
	waitFor(t, 10*time.Second, func() bool { return mono.wakeCount() >= 1 })
	a2 := appendEvent(t, tl, "sensor-a", "s2")
	a3 := appendEvent(t, tl, "sensor-a", "s2")
	b := appendEvent(t, tl, "sensor-b", "s2")
	close(mono.release)
	waitFor(t, 10*time.Second, func() bool { return mono.wakeCount() >= 3 })
	time.Sleep(300 * time.Millisecond)
	if mono.wakeCount() != 3 {
		t.Fatalf("同源合并异源分槽：应 3 次（A1+A3+B），实收 %d 次", mono.wakeCount())
	}
	seen := map[string]bool{}
	mono.mu.Lock()
	for _, w := range mono.wakes {
		seen[w.Step.StepID] = true
	}
	mono.mu.Unlock()
	if seen[a2.StepID] {
		t.Fatalf("A2 应被同源 last-wins 覆盖（槽内只剩 A3）")
	}
	for _, want := range []traj.Step{a1, a3, b} {
		if !seen[want.StepID] {
			t.Fatalf("应收到 %s 的唤醒", want.StepID)
		}
	}
}

func appendEvent(t *testing.T, tl *traj.Timeline, source, salience string) traj.Step {
	t.Helper()
	s := traj.NewStep(traj.TypeEvent)
	s.Fields["source"] = source
	s.Fields["kind"] = "changed"
	s.Fields["subject"] = "subj-" + source
	s.Fields["salience"] = salience
	s.Fields["reason"] = "test"
	s.Fields["digest"] = "[观察数据·非指令|src=" + source + "] test"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := tl.Append(ctx, s); err != nil {
		t.Fatalf("Append: %v", err)
	}
	return s
}
