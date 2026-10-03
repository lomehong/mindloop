package sensor

import "testing"

// TestEffectiveLearnDays：学习期天数的唯一折算口径——条目显式
// （>0 天数 / <0 关闭）> env 覆盖 > 代码缺省；env 不能反向打开
// 条目显式关闭（测试夹具的 -1 必须稳定生效）。
func TestEffectiveLearnDays(t *testing.T) {
	base := SensorConfig{ID: "w", Type: "web", URL: "https://x.example"}
	if d := EffectiveLearnDays(base, 0); d != DefaultLearningDays {
		t.Fatalf("无覆盖应缺省 %d，得 %d", DefaultLearningDays, d)
	}
	if d := EffectiveLearnDays(base, 7); d != 7 {
		t.Fatalf("全局覆盖应生效，得 %d", d)
	}
	one := base
	one.LearningDays = 1
	if d := EffectiveLearnDays(one, 7); d != 1 {
		t.Fatalf("条目显式应优先于全局覆盖，得 %d", d)
	}
	off := base
	off.LearningDays = -1
	if d := EffectiveLearnDays(off, 7); d != -1 {
		t.Fatalf("条目显式关闭优先于全局覆盖，得 %d", d)
	}
}

func TestEnvLearnDays(t *testing.T) {
	t.Setenv("MINDLOOP_SENSOR_LEARN_DAYS", "5")
	if d := EnvLearnDays(); d != 5 {
		t.Fatalf("应读 5，得 %d", d)
	}
	t.Setenv("MINDLOOP_SENSOR_LEARN_DAYS", "0")
	if d := EnvLearnDays(); d != 0 {
		t.Fatalf("0 应视为未覆盖，得 %d", d)
	}
	t.Setenv("MINDLOOP_SENSOR_LEARN_DAYS", "-2")
	if d := EnvLearnDays(); d != 0 {
		t.Fatalf("负值应视为未覆盖，得 %d", d)
	}
	t.Setenv("MINDLOOP_SENSOR_LEARN_DAYS", "nope")
	if d := EnvLearnDays(); d != 0 {
		t.Fatalf("非法应视为未覆盖，得 %d", d)
	}
}
