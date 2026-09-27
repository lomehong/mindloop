// Package connector 是连接器（bridge）的渠道无关内核：一个独立的
// 轨迹读写进程，把外部渠道与心智的对话流接起来。两条不变式：
//
//   - 路由即数据：消息归哪个渠道只由轨迹上的 to 字段表达，出站泵
//     的过滤谓词是纯日志谓词，没有隐式规则或旁路通道。
//   - bridge 与 mind 无进程内依赖：只共享轨迹文件（多写者由目录锁
//     保证，读者各自持 cursor）。
//
// 出站泵的三条安全前提是设计文档（docs/designs/connectors.md §5.1）
// 钉死的前置改动：三态游标（LoadCursorAtEnd：缺失/损坏一律 EOF，
// 绝不重放历史）、rewound 感知读取（轨迹被替换/截断即弃批跳 EOF，
// 宁可漏发不可重投）、投递语义分三档（重复窗口/漏发/崩溃窗口），
// 不笼统称 at-least-once。
package connector

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mindloop/internal/traj"
)

// ErrNoAddresses 是出站泵配置错误的哨兵：认领地址集为空。
var ErrNoAddresses = errors.New("connector: 出站泵没有认领地址")

// Delivery 是渠道出站适配器：把一条认领地址上的文本投递出去。to
// 已由出站泵的过滤谓词保证属于本适配器；实现自行完成地址映射
// （如 wecom:<userid> → chatid）与渠道协议调用。
type Delivery interface {
	Send(ctx context.Context, to, text string) error
}

// OutboundOptions 配置出站泵。
type OutboundOptions struct {
	// Path 是根轨迹文件路径。
	Path string
	// CursorPath 是游标持久化文件（三态语义见包注释）。
	CursorPath string
	// Self 是身份名：只投递 from == Self 的 message 步骤。
	Self string
	// Addresses 是本适配器认领的投递地址（to 精确匹配，如
	// "wecom:zhangsan"）。空集合拒绝启动——没有认领地址的出站泵
	// 是配置错误，静默空转只会让人以为"接好了"。
	Addresses []string
	// Deliver 是渠道投递适配器。
	Deliver Delivery
	// Skip 是可选的投递跳过谓词（纯日志谓词的补充）：命中的步骤
	// 不投递、游标照常推进——渠道已用别的通道送达的回复（如已
	// 流式推送的终稿）据此避免重复。
	Skip func(traj.Step) bool
	// Retry 是投递失败的重试上限（默认 3）：指数退避（500ms 起步
	// 翻倍）。耗尽即丢弃该条（漏发档）——一条发不出去的消息绝不能
	// 卡住出站泵；轨迹上它依然存在，仪表盘可见。
	Retry int
	// Poll 是轨迹轮询间隔（默认 500ms）。
	Poll time.Duration
	// Logger 注入诊断（首启/续读、rewound、投递失败）。
	Logger func(format string, args ...any)
}

// Outbound 是出站泵：tail 根轨迹，把 self 发往认领地址的 message
// 步骤投递到渠道。Run 阻塞到 ctx 取消或轨迹不可读。
type Outbound struct {
	opts   OutboundOptions
	claims map[string]bool
}

// NewOutbound 构造出站泵；认领地址为空报错（fail-closed）。
func NewOutbound(opts OutboundOptions) (*Outbound, error) {
	if len(opts.Addresses) == 0 {
		return nil, ErrNoAddresses
	}
	if opts.Retry <= 0 {
		opts.Retry = 3
	}
	if opts.Poll <= 0 {
		opts.Poll = 500 * time.Millisecond
	}
	claims := make(map[string]bool, len(opts.Addresses))
	for _, a := range opts.Addresses {
		claims[a] = true
	}
	return &Outbound{opts: opts, claims: claims}, nil
}

// Run 运行出站泵直到 ctx 取消。游标三态起步；每个轮询周期用
// rewound 感知读取，命中过滤谓词的步骤按三档投递语义处理。
func (o *Outbound) Run(ctx context.Context) error {
	// 游标目录先行创建：Save 用 os.WriteFile，不建父目录——目录
	// 缺失会让每次落盘静默失败，重启即漏发停机期间的主动汇报。
	_ = os.MkdirAll(filepath.Dir(o.opts.CursorPath), 0o755)
	cur, resumed, err := traj.LoadCursorAtEnd(o.opts.CursorPath, o.opts.Path)
	if err != nil {
		return err
	}
	if resumed {
		o.logf("出站泵续读（offset %d）", cur.Offset())
	} else {
		o.logf("出站泵从 EOF 起步（首启或游标损坏，不重放历史）")
	}
	tick := time.NewTicker(o.opts.Poll)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = cur.Save(o.opts.CursorPath)
			return ctx.Err()
		case <-tick.C:
			steps, rewound, err := cur.ReadNewWithRewind()
			if err != nil {
				return err
			}
			if rewound {
				// 轨迹被替换/截断：本批是重放，弃批——readNew 已把
				// offset 推进到 EOF，落盘即"跳到 EOF"。没有任何
				// cursor 损坏迹象的重投比损坏更危险（bridge 没有
				// responder 那样的下游守卫吸收重放）。
				o.logf("检测到轨迹被替换/截断，弃批 %d 条跳到 EOF", len(steps))
				_ = cur.Save(o.opts.CursorPath)
				continue
			}
			delivered := false
			for _, s := range steps {
				if !o.claimed(s) {
					continue
				}
				to, _ := s.Field("to")
				content, _ := s.Field("content")
				if err := o.deliver(ctx, to, content); err != nil {
					o.logf("投递 %s 失败（重试耗尽，丢弃）: %v", to, err)
				} else {
					delivered = true
				}
				// 无论成败，投递过的步骤都要推进游标——成败即三档
				// 里的"漏发"或"完成"，都不是"留在原地重试"。
				_ = cur.Save(o.opts.CursorPath)
			}
			if !delivered && len(steps) > 0 {
				_ = cur.Save(o.opts.CursorPath) // 未命中的批次也推进，避免重启重扫
			}
		}
	}
}

// claimed 是过滤谓词（设计 §5.2）：type=message && from=<self> &&
// to ∈ 认领地址集。launched_by != bridge 的防御条件在谓词里永真
// （bridge 从不写 message 步骤），保留在类型层面：入站泵与出站泵
// 不共享写句柄。
func (o *Outbound) claimed(s traj.Step) bool {
	if s.Type != traj.TypeMessage {
		return false
	}
	if o.opts.Skip != nil && o.opts.Skip(s) {
		return false
	}
	if from, _ := s.Field("from"); from != o.opts.Self {
		return false
	}
	to, _ := s.Field("to")
	return o.claims[to]
}

// deliver 是三档投递语义的"正常路径"：成功即返回；指数退避重试
// Retry 次后放弃（调用方按漏发档处理）。
func (o *Outbound) deliver(ctx context.Context, to, text string) error {
	var err error
	delay := 500 * time.Millisecond
	for attempt := 0; attempt <= o.opts.Retry; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
				delay *= 2
			}
		}
		if err = o.opts.Deliver.Send(ctx, to, text); err == nil {
			return nil
		}
	}
	return err
}

func (o *Outbound) logf(format string, args ...any) {
	if o.opts.Logger != nil {
		o.opts.Logger(format, args...)
	}
}

// Segment 把长文本按段落边界折叠分段：段落边界处若 fence 深度为零
// 则优先落刀（干净切）；单段自身超限时按 rune 硬切兜底——硬切点
// 可能落在代码块内部，这是保底路径（有界性优先于整洁性）。limit
// 按 rune 计（渠道上限多为字符数）；适配器各自传渠道阈值的保守
// 折扣。模型与 responder 对分段无感知——这是投递层职责。
func Segment(text string, limit int) []string {
	if limit <= 0 || len([]rune(text)) <= limit {
		return []string{text}
	}
	paras := strings.Split(text, "\n\n")
	var segs []string
	var cur strings.Builder
	depth := 0 // 未闭合 ``` 计数：奇数时是代码块内部，不落刀
	flush := func() {
		if cur.Len() > 0 {
			segs = append(segs, cur.String())
			cur.Reset()
		}
	}
	for _, para := range paras {
		depth += strings.Count(para, "```")
		// 追加后超限且当前不在代码块内：先收束本段。
		if cur.Len() > 0 && len([]rune(cur.String()+para)) > limit && depth%2 == 0 {
			flush()
		}
		if cur.Len() > 0 {
			cur.WriteString("\n\n")
		}
		cur.WriteString(para)
		// 单段自身超限：按 rune 硬切（fence 状态随段推进，硬切点
		// 可能落在代码块内——这是保底路径，正常文本走不到）。
		for len([]rune(cur.String())) > limit {
			runes := []rune(cur.String())
			cur.Reset()
			cur.WriteString(string(runes[limit:]))
			segs = append(segs, string(runes[:limit]))
		}
	}
	flush()
	if len(segs) == 0 {
		return []string{text}
	}
	return segs
}
