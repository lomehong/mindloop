// sensorrun.go — 感官运行器：感知系统在 mind 主进程内的宿主
// （perception.md §4.2 评审钉死：感官不跨进程——文件观察本就是进程
// 内能力，跨进程只多 IPC 面与故障域）。
//
// 职责与铁律：
//   - 每感官独立 goroutine + panic recover + 指数退避重启（心跳
//     连败退避同款药方）——感官的 panic 绝不带走心智进程；
//   - onEvent 走有界队列：下游写轨迹的 Windows 瞬时争用不拖垮
//     观察循环；超限丢弃计数折叠进同源下一条事件（丢弃留痕）；
//   - 准入令牌桶在判定之前（闸门封唤醒也封写入——预算耗干 DoS
//     的对策是准入不是事后熔断）；
//   - 判定后的三道闸：学习期封顶（新感官前 N 天 ≤S1）→ quiet
//     降级（S2/S3 → S1，quiet-held 留痕）→ 写步骤（S3 走 alert、
//     其余走 event；S0/S1 由 dispatcher.route 按 salience 跳过，
//     订阅面只收 S2+）；
//   - 热加载按 sensor-id diff：未变的保留反射状态（"每次保存配置
//     就失忆"必然重演重复刷屏）；坏配置整包拒绝（fail-closed）。
package mind

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lomehong/mindloop/internal/connector/sensor"
	"github.com/lomehong/mindloop/internal/traj"
)

// SensorFactory 按配置构造一个感官实例；未知类型/配置错误返回错误
// （该感官按退避重试——运行期修好配置无需重启心智）。
type SensorFactory func(sensor.SensorConfig) (sensor.Sensor, error)

// SensorRunnerOptions 是运行器装配参数。
type SensorRunnerOptions struct {
	// Timeline 是身份轨迹（event/alert 步骤的写位与状态重建源）。
	Timeline *traj.Timeline
	// IdentityDir 是身份目录（sensors.json / sensors-state.json）。
	IdentityDir string
	// SelfName 是身份名（alert 步骤的 from 字段）。
	SelfName string
	// Factory 按配置构造感官；nil = 无感官可跑（启动日志说明）。
	Factory SensorFactory
	// ReloadEvery 是配置热加载的检查间隔；0 = 5s。
	ReloadEvery time.Duration
	// LearnDays 覆盖全局学习期天数；0 = sensor 包缺省（3 天）。
	LearnDays int
	Logger    func(format string, args ...any)
}

// NewSensorRunner 构造感官运行器。
func NewSensorRunner(opts SensorRunnerOptions) *SensorRunner {
	return &SensorRunner{opts: opts}
}

// SensorRunner 是感官的宿主与神经系统装配层。
type SensorRunner struct {
	opts SensorRunnerOptions

	mu        sync.Mutex
	running   map[string]*runningSensor
	states    map[string]*sensor.State
	buckets   map[string]*sensor.Bucket
	firstSeen map[string]time.Time // 学习期起点（sensors-state.json 持久化）
	cancel    context.CancelFunc
	ctx       context.Context
}

type runningSensor struct {
	cfg      sensor.SensorConfig
	cancel   context.CancelFunc
	queue    chan sensor.PEvent
	dropped  atomic.Int64 // 队列溢出丢弃计数（折叠进下一条事件）
	lastSeen time.Time    // 最近事件（缺席检测基准）
	lastAbs  time.Time    // 最近一次缺席告警（防重复）
}

// Start 装载配置并启动全部启用的感官。没有 sensors.json = 静默
// 不启动（感知系统整体缺位是合法状态）；配置坏 = 拒绝启动并返回
// 错误（fail-closed：宁可不跑也不能按半份配置跑）。
func (r *SensorRunner) Start(ctx context.Context) error {
	file, err := sensor.Load(r.cfgPath())
	if err != nil {
		return err
	}
	enabled := map[string]sensor.SensorConfig{}
	if file != nil {
		for _, c := range file.Sensors {
			if c.IsEnabled() {
				enabled[c.ID] = c
			}
		}
	}
	if len(enabled) == 0 {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ctx != nil && r.ctx.Err() == nil {
		return nil // 已在运行
	}
	if err := r.loadLearnStateLocked(); err != nil && r.opts.Logger != nil {
		r.opts.Logger("sensor: 学习期状态读不出来（按首次启动处理）: %v", err)
	}
	r.registerFirstSeenLocked(enabled)
	r.ctx, r.cancel = context.WithCancel(ctx)
	r.running = map[string]*runningSensor{}
	r.states = map[string]*sensor.State{}
	r.buckets = map[string]*sensor.Bucket{}

	// 状态重建：回读冷却窗口内的 event/alert 步骤（视图皆派生——
	// 重启不重复唤醒，不掉进"重启后恰逢预算紧张窗口"的复合故障）。
	records := r.rebuildRecords()
	for _, cfg := range enabled {
		r.startOneLocked(cfg, records)
	}
	go r.manageLoop()
	if r.opts.Logger != nil {
		r.opts.Logger("sensor: 感知系统启动，%d 个感官（学习期 %d 天）", len(enabled), r.learnDaysLocked())
	}
	return nil
}

// Stop 取消全部感官通道（感官 goroutine 只写轨迹，无任务状态需要
// 收敛；停机收敛由调用方的 WaitIdle/进程生命周期兜底）。
func (r *SensorRunner) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancel != nil {
		r.cancel()
	}
}

func (r *SensorRunner) logf(format string, args ...any) {
	if r.opts.Logger != nil {
		r.opts.Logger("sensor: "+format, args...)
	}
}

func (r *SensorRunner) cfgPath() string { return filepath.Join(r.opts.IdentityDir, "sensors.json") }
func (r *SensorRunner) statePath() string {
	return filepath.Join(r.opts.IdentityDir, "sensors-state.json")
}

// learnDaysLocked 折算全局学习期天数。调用方须持 r.mu。
func (r *SensorRunner) learnDaysLocked() int {
	if r.opts.LearnDays > 0 {
		return r.opts.LearnDays
	}
	return sensor.DefaultLearningDays
}

// rebuildRecords 从轨迹读回事件记录（Tail 有界——重建只关心冷却
// 窗口内的近史，全量重放是投影重建的代价模型，不在这里重演）。
func (r *SensorRunner) rebuildRecords() []sensor.EventRecord {
	steps, err := r.opts.Timeline.Tail(500, []string{traj.TypeEvent, traj.TypeAlert})
	if err != nil {
		r.logf("状态重建读轨迹失败（按空状态启动）: %v", err)
		return nil
	}
	var out []sensor.EventRecord
	for _, s := range steps {
		ts, err := time.Parse(traj.TimeFormat, s.TS)
		if err != nil {
			continue
		}
		src, _ := s.Field("source")
		kind, _ := s.Field("kind")
		subject, _ := s.Field("subject")
		dedup, _ := s.Field("dedup")
		sal, _ := s.Field("salience")
		reason, _ := s.Field("reason")
		out = append(out, sensor.EventRecord{
			TS: ts, Source: src, Kind: kind, Subject: subject,
			Dedup: dedup, Salience: sal, Reason: reason,
		})
	}
	return out
}

// startOneLocked 启动单感官：反射状态（重建或 Clone 随迁）→ 有界
// 队列 → 观察 goroutine（panic 防护 + 退避重启）→ 消费 goroutine。
// 调用方须持 r.mu（本方法改 r.running/r.states）。
func (r *SensorRunner) startOneLocked(cfg sensor.SensorConfig, records []sensor.EventRecord) {
	st := sensor.NewState()
	if old := r.states[cfg.ID]; old != nil {
		st = old.Clone() // 热加载重建：去重/冷却记忆随迁，不失忆
	} else {
		st.Rebuild(filterRecords(records, cfg.ID), time.Now().Add(-2*time.Hour))
	}
	r.states[cfg.ID] = st

	ctx, cancel := context.WithCancel(r.ctx)
	rs := &runningSensor{
		cfg:      cfg,
		cancel:   cancel,
		queue:    make(chan sensor.PEvent, 16),
		lastSeen: time.Now(),
	}
	r.running[cfg.ID] = rs
	go r.consumeLoop(ctx, rs, st)
	go r.watchLoop(ctx, rs)
}

func filterRecords(records []sensor.EventRecord, id string) []sensor.EventRecord {
	var out []sensor.EventRecord
	for _, rec := range records {
		if rec.Source == id {
			out = append(out, rec)
		}
	}
	return out
}

// watchLoop 是观察 goroutine：Watch 错误退避重启，panic recover。
// 感官死循环不重演"调度器死=心智死"。
func (r *SensorRunner) watchLoop(ctx context.Context, rs *runningSensor) {
	defer func() {
		if p := recover(); p != nil {
			r.logf("!! 感官 %s panic（已恢复）: %v", rs.cfg.ID, p)
		}
	}()
	factory := r.opts.Factory
	fails := 0
	for {
		if ctx.Err() != nil {
			return
		}
		if factory == nil {
			return
		}
		s, err := factory(rs.cfg)
		if err != nil {
			r.logf("感官 %s 构造失败（%v）——10 分钟后重试", rs.cfg.ID, err)
			if sleepCtx(ctx, 10*time.Minute) {
				return
			}
			continue
		}
		watchErr := s.Watch(ctx, func(e sensor.PEvent) {
			// 有界队列：满了丢弃计数（留痕——折叠进后续事件摘要），
			// 观察循环绝不阻塞在下游写轨迹上。
			select {
			case rs.queue <- e:
			default:
				rs.dropped.Add(1)
			}
		})
		if ctx.Err() != nil {
			return
		}
		if errors.Is(watchErr, sensor.ErrDisabled) {
			r.logf("感官 %s 已禁用，通道静默", rs.cfg.ID)
			return
		}
		fails++
		if fails > 10 {
			fails = 10 // 退避上限 2s
		}
		wait := time.Duration(fails) * 200 * time.Millisecond
		r.logf("感官 %s 退出（%v），%v 后重启（连续 %d 次）", rs.cfg.ID, watchErr, wait, fails)
		if sleepCtx(ctx, wait) {
			return
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return true
	case <-t.C:
		return false
	}
}

// consumeLoop 是判定消费 goroutine：准入桶 → 判定 → 学习期/quiet
// 闸 → 写步骤；空闲档位做关联快照刷新与缺席检测。
func (r *SensorRunner) consumeLoop(ctx context.Context, rs *runningSensor, st *sensor.State) {
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	view := r.currentView()
	for {
		select {
		case <-ctx.Done():
			return
		case e := <-rs.queue:
			r.handle(rs, st, view, e)
		case <-tick.C:
			view = r.currentView()
			r.absenceCheck(rs)
		}
	}
}

func (r *SensorRunner) currentView() sensor.ReflexView {
	r.mu.Lock()
	defer r.mu.Unlock()
	var cfgs []sensor.SensorConfig
	for _, rs := range r.running {
		cfgs = append(cfgs, rs.cfg)
	}
	return sensor.ViewFromConfigs(cfgs)
}

// handle 是单事件的处理管线（串行——判定状态无锁的前提）。
func (r *SensorRunner) handle(rs *runningSensor, st *sensor.State, view sensor.ReflexView, e sensor.PEvent) {
	now := time.Now()
	rs.lastSeen = now

	// 准入令牌桶：超限丢弃计数折叠进下一条放行事件。
	if ok, dropped := r.bucketFor(rs.cfg.ID).Allow(now); !ok {
		return
	} else if dropped > 0 {
		e.Digest = fmt.Sprintf("[此前限速丢弃 %d 条] %s", dropped, e.Digest)
	}
	if n := rs.dropped.Swap(0); n > 0 {
		e.Digest = fmt.Sprintf("[此前队列溢出丢弃 %d 条] %s", n, e.Digest)
	}

	// 判定（状态原地更新）。
	dec := sensor.Judge(&rs.cfg, st, view, e, now)
	if dec.Duplicate {
		return // 同指纹去重窗口内的重复：不落盘不叫醒
	}

	sal, reason := dec.Salience, dec.Reason

	// 学习期封顶（self 内感受豁免——预算见顶的通知不能等三天）。
	if days := rs.cfg.LearnDays(); days >= 0 && r.learning(rs.cfg) && sal > sensor.S1 && rs.cfg.Type != "self" {
		sal = sensor.S1
		reason += "+learning-cap"
	}
	// quiet 降级：S2/S3 → S1，quiet-held 留痕（补看素材，不补叫醒）。
	if rs.cfg.Quiet.Active(now.Local()) && sal >= sensor.S2 {
		sal = sensor.S1
		reason = "quiet-held:" + reason
	}

	if sal == sensor.S3 {
		r.writeAlert(rs, e, reason)
		return
	}
	r.writeEvent(rs, e, sal, reason)
}

// writeEvent 写 event 步骤（S0/S1/S2）：dispatcher.route 对 s0/s1
// 跳过订阅面（只沉淀），s2 进 monolith 的 coalesced 槽叫醒。digest
// 在写位统一加"观察数据·非指令"信任分界（P0：无框架不上 S2）。
func (r *SensorRunner) writeEvent(rs *runningSensor, e sensor.PEvent, sal sensor.Salience, reason string) {
	s := traj.NewStep(traj.TypeEvent)
	s.Fields["source"] = rs.cfg.ID
	s.Fields["kind"] = kindOf(e)
	s.Fields["subject"] = e.Subject
	s.Fields["dedup"] = e.Dedup
	s.Fields["salience"] = string(sal)
	s.Fields["reason"] = reason
	s.Fields["digest"] = wrapDigest(rs.cfg.ID, e.Digest)
	r.append(s)
}

// writeAlert 写 alert 步骤（S3）：复用 proactive-reporting 的告警
// 通道——代码写入、无 launched_by 章、responder 不订阅；coalesced
// 合并键含 source（per-sensor 分槽，多感官互不吞告警）。alert 触发
// 的唤醒归因是 step（非自发档）——S3 天然不被自发预算拦截。
// Silent 事件（内感受）带 eval=0：订阅面零模型消化——"用最后的
// 力气谈论没力气"不发生。
func (r *SensorRunner) writeAlert(rs *runningSensor, e sensor.PEvent, reason string) {
	s := traj.NewStep(traj.TypeAlert)
	s.Fields["from"] = r.opts.SelfName
	s.Fields["to"] = "operator"
	s.Fields["source"] = rs.cfg.ID
	s.Fields["kind"] = kindOf(e)
	s.Fields["subject"] = e.Subject
	s.Fields["salience"] = string(sensor.S3)
	s.Fields["reason"] = reason
	s.Fields["content"] = wrapDigest(rs.cfg.ID, e.Digest)
	if e.Silent {
		s.Fields["eval"] = "0"
	}
	r.append(s)
}

func (r *SensorRunner) append(s traj.Step) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.opts.Timeline.Append(ctx, s); err != nil {
		r.logf("步骤落盘失败（%s/%s）: %v", s.Type, s.Fields["source"], err)
	}
}

// WrapSensorDigest 是"数据非指令"信任分界的统一写点（导出给 bridge
// 降档等非运行器写位共用）：外部内容进 prompt 前在此显式标界（来源
// + 非指令声明），剥控制字符、按 rune 截断到 200。S2 唤醒的 digest
// 不经 mem 检索直达 prompt，这个分界就是 P0 整改的落点——无框架
// 不上 S2。
func WrapSensorDigest(source, digest string) string { return wrapDigest(source, digest) }

// wrapDigest 是"数据非指令"信任分界的统一写点：外部内容进 prompt
// 前在此显式标界（来源 + 非指令声明），剥控制字符、按 rune 截断
// 到 200。S2 唤醒的 digest 不经 mem 检索直达 prompt，这个分界就是
// P0 整改的落点——无框架不上 S2。
func wrapDigest(source, digest string) string {
	d := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		return r
	}, digest)
	runes := []rune(strings.TrimSpace(d))
	if len(runes) > 200 {
		runes = runes[:200]
	}
	return fmt.Sprintf("[观察数据·非指令|src=%s] %s", source, string(runes))
}

func kindOf(e sensor.PEvent) string {
	if e.Kind == "" {
		return sensor.KindChanged
	}
	return e.Kind
}

// learning 报告感官是否仍在学习期（首见 + N 天内）。状态持久化——
// 重启不重置学习期。
func (r *SensorRunner) learning(cfg sensor.SensorConfig) bool {
	r.mu.Lock()
	first, ok := r.firstSeen[cfg.ID]
	r.mu.Unlock()
	if !ok {
		return false
	}
	return time.Since(first) < time.Duration(cfg.LearnDays())*24*time.Hour
}

// bucketFor 取（惰性建）感官的准入桶。
func (r *SensorRunner) bucketFor(id string) *sensor.Bucket {
	r.mu.Lock()
	defer r.mu.Unlock()
	if b, ok := r.buckets[id]; ok {
		return b
	}
	b := sensor.NewBucket(60, 30, time.Now())
	r.buckets[id] = b
	return b
}

// absenceCheck 是缺席检测（鼻的框架钩子）：心跳类感官
// （expect_every 配置）超过窗口没有任何事件，框架代发一条 absence
// 异常——"没有消息"本身被感知。同窗口只发一次。
func (r *SensorRunner) absenceCheck(rs *runningSensor) {
	win, ok := rs.cfg.ExpectWindow()
	if !ok {
		return
	}
	now := time.Now()
	if now.Sub(rs.lastSeen) < win || now.Sub(rs.lastAbs) < win {
		return
	}
	rs.lastAbs = now
	e := sensor.PEvent{
		Kind:    sensor.KindAbsence,
		Subject: rs.cfg.ID,
		Dedup:   "absence-" + now.Format("2006-01-02-1504"),
		Digest:  fmt.Sprintf("感官 %s 超过 %s 无任何事件（心跳缺席）", rs.cfg.ID, rs.cfg.ExpectEvery),
	}
	// 缺席是异常，S2 起，学习期不封顶——它正是"没来"的哨兵。
	sal, reason := sensor.S2, "absence-detected"
	if rs.cfg.Quiet.Active(now.Local()) {
		sal, reason = sensor.S1, "quiet-held:"+reason
	}
	if sal == sensor.S3 {
		r.writeAlert(rs, e, reason)
		return
	}
	r.writeEvent(rs, e, sal, reason)
}

// manageLoop 是管理 goroutine：配置热加载（sensor-id diff）。
// 坏配置整包拒绝并保留旧运行（fail-closed）。
func (r *SensorRunner) manageLoop() {
	every := r.opts.ReloadEvery
	if every <= 0 {
		every = 5 * time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	var lastMod time.Time
	if fi, err := os.Stat(r.cfgPath()); err == nil {
		lastMod = fi.ModTime()
	}
	for {
		select {
		case <-r.ctx.Done():
			return
		case <-t.C:
		}
		fi, err := os.Stat(r.cfgPath())
		if err != nil || fi.ModTime().Equal(lastMod) {
			continue
		}
		lastMod = fi.ModTime()
		file, err := sensor.Load(r.cfgPath())
		if err != nil {
			r.logf("配置热加载拒绝（保留旧运行）: %v", err)
			continue
		}
		r.reload(file)
	}
}

// reload 按 sensor-id diff 应用新配置：删停、增起、改重建（状态
// Clone 随迁——去重/冷却记忆不因改配置失忆）。
func (r *SensorRunner) reload(file *sensor.File) {
	r.mu.Lock()
	defer r.mu.Unlock()
	want := map[string]sensor.SensorConfig{}
	for _, c := range file.Sensors {
		if c.IsEnabled() {
			want[c.ID] = c
		}
	}
	records := r.rebuildRecords()
	// 删停与改重建。
	for id, rs := range r.running {
		next, keep := want[id]
		if keep && sameSensorCfg(rs.cfg, next) {
			continue
		}
		rs.cancel()
		delete(r.running, id)
		if keep {
			r.startOneLocked(next, records)
		}
	}
	// 新增。
	for id, cfg := range want {
		if _, ok := r.running[id]; !ok {
			r.startOneLocked(cfg, records)
		}
	}
	r.registerFirstSeenLocked(want)
}

func sameSensorCfg(a, b sensor.SensorConfig) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(ab) == string(bb)
}

// loadLearnStateLocked 读学习期起点。调用方须持 r.mu。
func (r *SensorRunner) loadLearnStateLocked() error {
	data, err := os.ReadFile(r.statePath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var st struct {
		FirstSeen map[string]string `json:"first_seen"`
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return err
	}
	r.firstSeen = map[string]time.Time{}
	for id, v := range st.FirstSeen {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			r.firstSeen[id] = t
		}
	}
	return nil
}

// registerFirstSeenLocked 把无首见记录的感官记为现在并落盘（学习
// 期从配置出现那一刻起算）。调用方须持 r.mu。
func (r *SensorRunner) registerFirstSeenLocked(want map[string]sensor.SensorConfig) {
	if r.firstSeen == nil {
		r.firstSeen = map[string]time.Time{}
	}
	changed := false
	for id := range want {
		if _, ok := r.firstSeen[id]; !ok {
			r.firstSeen[id] = time.Now()
			changed = true
		}
	}
	st := struct {
		FirstSeen map[string]string `json:"first_seen"`
	}{FirstSeen: map[string]string{}}
	for id, t := range r.firstSeen {
		st.FirstSeen[id] = t.Format(time.RFC3339)
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return
	}
	path := r.statePath()
	tmp := fmt.Sprintf("%s.%d.tmp", path, os.Getpid())
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return
	}
	if err := os.Rename(tmp, path); err != nil && changed {
		r.logf("学习期状态落盘失败: %v", err)
	}
}
