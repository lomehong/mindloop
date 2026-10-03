// config.go — sensors.json 的解析与校验。形态与 schedule 条目同风格
// （身份目录下一个自描述 JSON 文件，热加载整包拒绝坏配置——fail-
// closed 同 bridge 白名单）。校验失败返回可执行的人话错误：doctor
// 第八项与 CLI 都消费它，配置错误绝不静默（感知没醒要能诊断）。
package sensor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// 全局缺省：学习期天数可用环境变量覆盖（装配层读
// MINDLOOP_SENSOR_LEARN_DAYS），这里是代码缺省。
const DefaultLearningDays = 3

// SalienceRule 是一条显著性规则：词面命中即升档（判定层 MVP 只做
// 便宜可解释的匹配，语义相关性留给记忆代谢的检索升级——错档是
// 常态，写进契约）。
type SalienceRule struct {
	// Name 是规则名（reason 字段引用它，校准归因靠它对上味觉）。
	Name string `json:"name"`
	// Keywords 任一命中（subject 或 digest 的词面包含，大小写不
	// 敏感）即触发。
	Keywords []string `json:"keywords"`
	// Salience 命中后的档位（s1-s3；s0 无意义，校验拒绝）。
	Salience Salience `json:"salience"`
	// Min 是阈值越界门槛（>0 启用，鼻的阈值检测）：关键词命中的
	// 前提下，digest 中存在 ≥ Min 的数值才算命中——"错误数 500"
	// "磁盘 95%" 这类数值型 digest 的阈值规则。0 = 纯词面。
	Min float64 `json:"min,omitempty"`
}

// QuietWindow 是安静时段：S2/S3 在窗口内自动降级 S1（kind 附
// quiet-held），窗口结束后的下一次定时评估折叠补看——顺延是
// "补看素材"，不是"补叫醒"（perception.md §4.3：sensor 事件没有
// plannedAt/lastSeen，不套 effectiveFires 的顺延模型）。
type QuietWindow struct {
	Start string `json:"start"` // "HH:MM" 本地时区
	End   string `json:"end"`   // 同上；跨午夜合法（23:00-08:00）
}

// SensorConfig 是一个感官通道的配置。
type SensorConfig struct {
	ID   string `json:"id"`   // 身份内唯一；event/alert 步骤的 source 字段
	Type string `json:"type"` // file | git | web | webhook | self

	// 观察目标（按类型取用）：
	Path   string `json:"path,omitempty"`   // file/git：目录或仓库
	URL    string `json:"url,omitempty"`    // web：http(s) URL 或 feed
	Secret string `json:"secret,omitempty"` // webhook：HMAC 共享密钥（装配层生成，不进 git）

	// Interval 是 web/git 轮询间隔（"5m" 形态）；下限 5 分钟
	//（web）/ 1 分钟（git），低于下限按缺省处理并告警。
	Interval string `json:"interval,omitempty"`

	// Keywords 进 ReflexView（本感官 + 全局聚合两级都查）。
	Keywords []string `json:"keywords,omitempty"`

	// Salience 覆盖缺省分档。
	Salience struct {
		Default Salience       `json:"default,omitempty"` // 缺省档（未填 = 按类型缺省）
		Rules   []SalienceRule `json:"rules,omitempty"`
	} `json:"salience,omitempty"`

	// Quiet 可选安静窗口。
	Quiet *QuietWindow `json:"quiet,omitempty"`

	// ExpectEvery 是缺席检测窗口（"24h" 形态；鼻 Phase 3）：心跳类
	// 感官超过这么久没有任何事件，框架代发 kind=absence 的异常。
	// 空 = 不启用。
	ExpectEvery string `json:"expect_every,omitempty"`

	// LearningDays 覆盖全局学习期天数（0 = 用全局缺省）：新感官前
	// N 天封顶 S1，stats 出预演报告后由用户显式放行。
	LearningDays int `json:"learning_days,omitempty"`

	// DedupWindow 是同指纹事件的去重窗口（"10m" 形态；缺省 10m）：
	// 窗口内同 (kind, dedup 指纹) 的事件判定为重复——不落盘不叫醒，
	// 抑制计数折叠进同源下一条事件。
	DedupWindow string `json:"dedup_window,omitempty"`

	// RuleCooldown 是显著性规则的冷却（"30m" 形态；缺省 30m）：
	// 规则命中升档后，冷却期内同规则不再升档——防"网页每小时变
	// 五次、次次含关键词"的规则驱动风暴。去重管同内容重复，冷却
	// 管同规则变体。
	RuleCooldown string `json:"rule_cooldown,omitempty"`

	// Enabled 缺省 true；false 即静默停通道（不算故障）。
	Enabled *bool `json:"enabled,omitempty"`
}

// Enabled 报告通道是否启用（nil = true）。
func (c SensorConfig) IsEnabled() bool { return c.Enabled == nil || *c.Enabled }

// LearnDays 折算学习期天数：>0 显式天数；<0 显式关闭学习期（测试
// 与高级用户）；0 = 全局缺省。
func (c SensorConfig) LearnDays() int {
	if c.LearningDays > 0 {
		return c.LearningDays
	}
	if c.LearningDays < 0 {
		return -1
	}
	return DefaultLearningDays
}

// IntervalOf 解析轮询间隔；非法/未填返回 def。
func (c SensorConfig) IntervalOf(def time.Duration) time.Duration {
	if d, err := time.ParseDuration(strings.TrimSpace(c.Interval)); err == nil && d > 0 {
		return d
	}
	return def
}

// Expect window（缺席检测）；未配置返回 false。
func (c SensorConfig) ExpectWindow() (time.Duration, bool) {
	d, err := time.ParseDuration(strings.TrimSpace(c.ExpectEvery))
	if err != nil || d <= 0 {
		return 0, false
	}
	return d, true
}

// Active 报告 quiet 窗口在 t（本地时区）是否生效。跨午夜窗口
// （start > end）覆盖 [start, 24h) ∪ [0, end)。
func (q *QuietWindow) Active(t time.Time) bool {
	if q == nil {
		return false
	}
	sm, ok1 := parseHHMM(q.Start)
	em, ok2 := parseHHMM(q.End)
	if !ok1 || !ok2 {
		return false
	}
	cur := t.Hour()*60 + t.Minute()
	if sm == em {
		return false
	}
	if sm < em {
		return cur >= sm && cur < em
	}
	return cur >= sm || cur < em
}

func parseHHMM(s string) (int, bool) {
	s = strings.TrimSpace(s)
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return 0, false
	}
	var h, m int
	if _, err := fmt.Sscanf(parts[0], "%d", &h); err != nil || h < 0 || h > 23 {
		return 0, false
	}
	if _, err := fmt.Sscanf(parts[1], "%d", &m); err != nil || m < 0 || m > 59 {
		return 0, false
	}
	return h*60 + m, true
}

// File 是 sensors.json 的根形态。
type File struct {
	Version int            `json:"version"`
	Sensors []SensorConfig `json:"sensors"`
}

// Types 是支持的感官类型词表。
var Types = map[string]bool{
	"file":    true,
	"git":     true,
	"web":     true,
	"webhook": true,
	"self":    true,
}

// DefaultSalience 是各类型的缺省档：保守缺省（宁 S0 勿 S2）——
// file 是高噪声通道落 S0；web/git 是显式订阅落 S1；webhook 是显式
// 配置的对端（HMAC 鉴权）落 S2；self 是内感受，S2 起步（豁免见
// 装配层）。
func DefaultSalience(typ string) Salience {
	switch typ {
	case "file":
		return S0
	case "git", "web":
		return S1
	case "webhook", "self":
		return S2
	}
	return S0
}

// Load 读并校验 sensors.json。文件不存在返回 (nil, nil)——未配置
// 感官是合法状态（感知系统整体静默）。
func Load(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("sensor: 读 %s 失败: %w", filepath.Base(path), err)
	}
	return Parse(data)
}

// Parse 解析并校验配置（doctor 与热加载共用同一份校验）。
func Parse(data []byte) (*File, error) {
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("sensor: 配置不是合法 JSON: %w", err)
	}
	if err := f.Validate(); err != nil {
		return nil, err
	}
	return &f, nil
}

// Validate 校验整包配置：id 唯一非空、类型在词表内、观察目标按
// 类型必填、salience 词表合法、quiet/interval 可解析。全部错误一次
// 报完（doctor 一次给全清单）。
func (f *File) Validate() error {
	seen := map[string]bool{}
	var errs []string
	for i := range f.Sensors {
		c := &f.Sensors[i]
		where := fmt.Sprintf("sensors[%d]", i)
		id := strings.TrimSpace(c.ID)
		if id == "" {
			errs = append(errs, where+": id 不能为空")
			continue
		}
		if seen[id] {
			errs = append(errs, where+"("+id+"): id 重复")
		}
		seen[id] = true
		if !Types[c.Type] {
			errs = append(errs, where+"("+id+"): 未知类型 "+c.Type+
				"（支持 file/git/web/webhook/self）")
			continue
		}
		switch c.Type {
		case "file", "git":
			if strings.TrimSpace(c.Path) == "" {
				errs = append(errs, where+"("+id+"): "+c.Type+" 类型需要 path")
			}
		case "web":
			u := strings.TrimSpace(c.URL)
			if u == "" {
				errs = append(errs, where+"("+id+"): web 类型需要 url")
			} else if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
				errs = append(errs, where+"("+id+"): url 必须 http(s):// 开头")
			}
		}
		if c.Salience.Default != "" && (c.Salience.Default < S0 || c.Salience.Default > S3) {
			errs = append(errs, where+"("+id+"): salience.default 非法（s0-s3）")
		}
		for ri, r := range c.Salience.Rules {
			if r.Salience < S1 || r.Salience > S3 {
				errs = append(errs, fmt.Sprintf("%s(%s): salience.rules[%d] 非法（s1-s3）", where, id, ri))
			}
			if len(r.Keywords) == 0 {
				errs = append(errs, fmt.Sprintf("%s(%s): salience.rules[%d] 缺 keywords", where, id, ri))
			}
		}
		if c.Quiet != nil {
			if _, ok1 := parseHHMM(c.Quiet.Start); !ok1 {
				errs = append(errs, where+"("+id+"): quiet.start 非法（HH:MM）")
			}
			if _, ok2 := parseHHMM(c.Quiet.End); !ok2 {
				errs = append(errs, where+"("+id+"): quiet.end 非法（HH:MM）")
			}
		}
		if c.Interval != "" {
			if _, err := time.ParseDuration(c.Interval); err != nil {
				errs = append(errs, where+"("+id+"): interval 非法（如 5m）")
			}
		}
		if c.ExpectEvery != "" {
			if _, err := time.ParseDuration(c.ExpectEvery); err != nil {
				errs = append(errs, where+"("+id+"): expect_every 非法（如 24h）")
			}
		}
		if c.DedupWindow != "" {
			if _, err := time.ParseDuration(c.DedupWindow); err != nil {
				errs = append(errs, where+"("+id+"): dedup_window 非法（如 10m）")
			}
		}
		if c.RuleCooldown != "" {
			if _, err := time.ParseDuration(c.RuleCooldown); err != nil {
				errs = append(errs, where+"("+id+"): rule_cooldown 非法（如 30m）")
			}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("sensor: 配置校验失败:%s", "\n  - "+strings.Join(errs, "\n  - "))
	}
	return nil
}

// Get 按 id 取配置；没有返回 nil。
func (f *File) Get(id string) *SensorConfig {
	for i := range f.Sensors {
		if f.Sensors[i].ID == id {
			return &f.Sensors[i]
		}
	}
	return nil
}
