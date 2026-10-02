// limit.go — 感官级准入令牌桶与缺席检测（perception.md §4.3：闸门
// 封唤醒也封写入——预算耗干 DoS 的对策是准入在判定之前，不是事后
// 熔断）。令牌桶按感官一桶：超限事件丢弃计数、折叠进同源下一条
// 事件（丢弃本身留痕，不静默）。
package sensor

import (
	"sync"
	"time"
)

// Bucket 是一个感官的准入桶。缺省 60 事件/分钟、突发 30——远高于
// 任何健康通道的产量，只为故障态（fsnotify 洪泛、webhook 风暴）
// 兜底。
type Bucket struct {
	mu       sync.Mutex
	rate     float64 // 每秒补充令牌
	burst    float64 // 桶容量
	tokens   float64
	last     time.Time
	dropped  int // 超限丢弃计数（折叠进下一条放行事件）
	disabled bool
}

// NewBucket 构造令牌桶；perMinute <=0 = 不限速（disabled）。
func NewBucket(perMinute, burst int, now time.Time) *Bucket {
	if perMinute <= 0 {
		return &Bucket{disabled: true, tokens: 1, burst: 1, last: now}
	}
	if burst <= 0 || burst > perMinute {
		burst = perMinute
	}
	return &Bucket{
		rate:   float64(perMinute) / 60.0,
		burst:  float64(burst),
		tokens: float64(burst),
		last:   now,
	}
}

// Allow 消费一个令牌；返回本次是否放行与累计丢弃数（>0 时调用方
// 应把它折叠进本次事件的摘要）。
func (b *Bucket) Allow(now time.Time) (ok bool, dropped int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.disabled {
		return true, 0
	}
	elapsed := now.Sub(b.last).Seconds()
	b.last = now
	b.tokens += elapsed * b.rate
	if b.tokens > b.burst {
		b.tokens = b.burst
	}
	if b.tokens >= 1 {
		b.tokens--
		dropped = b.dropped
		b.dropped = 0
		return true, dropped
	}
	b.dropped++
	return false, 0
}
