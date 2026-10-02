// sensorself.go — 内感受感官（鼻的指向内的一支，perception.md §5
// Phase 3）：监听自身健康与预算消耗。同一套事件管线、感官源指向
// 自己——痛觉即既有 S3/alert 语义。
//
// 内感受的两条豁免（评审钉死的自指死锁）：
//   - 预算类告警 Silent=true：alert 步骤带 eval=0，订阅面零模型
//     消化——"用最后的力气谈论没力气"不发生；
//   - S3 走 alert 通道天然不被自发档预算拦截（alert 触发的唤醒
//     归因是 step，非 isSpontaneous）。
package mind

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/lomehong/mindloop/internal/connector/sensor"
	"github.com/lomehong/mindloop/internal/obs"
)

// selfPollEvery 是内感受的采样周期：健康与预算都是低频信号。
const selfPollEvery = 30 * time.Second

// SelfSensor 监听身份自身的健康标记与预算准入状态。
type SelfSensor struct {
	cfg sensor.SensorConfig
	dir string
}

// NewSelfSensor 构造内感受感官（dir = 身份目录）。
func NewSelfSensor(cfg sensor.SensorConfig, identityDir string) (sensor.Sensor, error) {
	if cfg.Type != "self" {
		return nil, fmt.Errorf("sensor: %s 不是 self 类型", cfg.ID)
	}
	return &SelfSensor{cfg: cfg, dir: identityDir}, nil
}

func (s *SelfSensor) ID() string { return s.cfg.ID }

func (s *SelfSensor) Watch(ctx context.Context, onEvent func(sensor.PEvent)) error {
	tick := time.NewTicker(selfPollEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
			s.sample(onEvent, time.Now())
		}
	}
}

func (s *SelfSensor) sample(onEvent func(sensor.PEvent), now time.Time) {
	// 痛觉：模型连续失败（熔断区）。Silent——模型坏时心智无话可说，
	// 通知直达 operator。
	h := obs.LoadHealth(filepath.Join(s.dir, "llm-health.json"))
	if h.ConsecutiveErrors >= obs.CircuitThreshold {
		onEvent(sensor.PEvent{
			Kind: sensor.KindAnomaly, Subject: "self:llm-health",
			Dedup:  "health-" + now.Format("2006-01-02-15"), // 同小时合并
			Digest: fmt.Sprintf("模型连续失败 %d 次，熔断冷却中（最后一次 %s）", h.ConsecutiveErrors, h.LastErrorAt),
			Hint:   sensor.S3, Silent: true,
		})
	}

	// 饥渴：自发档预算水位。80% 预警、100% 到顶——每天各至多一条
	//（dedup 绑 UTC 日期），Silent 直达。
	ad := obs.LoadAdmission(s.dir)
	if ad.SelfLimit <= 0 {
		return
	}
	for _, th := range []struct {
		pct  int
		note string
	}{
		{100, "自发档预算已用完——watchdog/定时/感知唤醒已全部暂停，对话与任务不受影响"},
		{80, "自发档预算消耗已过 80%——感知唤醒即将受限"},
	} {
		if ad.SelfUsedToday*100 < th.pct*ad.SelfLimit {
			continue
		}
		onEvent(sensor.PEvent{
			Kind: sensor.KindThreshold, Subject: "self:budget",
			Dedup:  fmt.Sprintf("self-budget-%d-%s", th.pct, now.UTC().Format("2006-01-02")),
			Digest: fmt.Sprintf("%s（%d/%d tokens）", th.note, ad.SelfUsedToday, ad.SelfLimit),
			Hint:   sensor.S3, Silent: true,
		})
	}
}
