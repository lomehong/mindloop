// sensorfact.go — 感官工厂：类型词表 → 感官实例。注册表随感知分期
// 生长（眼 file/git/web → 耳 webhook → 鼻 self）；未注册的类型在
// 装配时返回错误，由运行器按退避重试并告警——运行期升级感官实现
// 不需要动装配代码。
package cli

import (
	"fmt"

	"github.com/lomehong/mindloop/internal/connector/sensor"
	"github.com/lomehong/mindloop/internal/identity"
	"github.com/lomehong/mindloop/internal/mind"
)

// sensorFactory 按身份装配感官工厂。
func sensorFactory(id *identity.Identity) mind.SensorFactory {
	return func(cfg sensor.SensorConfig) (sensor.Sensor, error) {
		switch cfg.Type {
		case "file":
			return sensor.NewFileSensor(cfg)
		case "git":
			return sensor.NewGitSensor(cfg)
		case "web":
			return sensor.NewWebSensor(cfg)
		case "webhook":
			// webhook 的观察者是 service/web 进程的 /hook 端点（HMAC
			// 鉴权后按 event 契约落轨迹，心智经 feeder 看见）——心智
			// 进程里没有 Watch 循环可跑，ErrDisabled = 通道静默。
			return nil, sensor.ErrDisabled
		case "self":
			return mind.NewSelfSensor(cfg, id.Dir)
		default:
			return nil, fmt.Errorf("sensor: 类型 %q 尚未接入（当前支持：file git web webhook self）", cfg.Type)
		}
	}
}
