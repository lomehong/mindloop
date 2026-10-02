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
		default:
			return nil, fmt.Errorf("sensor: 类型 %q 尚未实现（当前支持：%s）",
				cfg.Type, "file git web webhook self")
		}
	}
}
