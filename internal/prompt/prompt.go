// Package prompt 把轨迹渲染为 LLM 消息序列——Headlong 的 context
// 工具的对应物。三条核心设计：
//
//   - 选择是 O(head+tail+pin) 的：只渲染头部、尾窗和被点名的步骤，
//     中段以省略标记代替，原文永远可以用 traj show 取回。
//   - 截断档位是行号的纯函数（见 Options.windowStart / bandFor）：
//     尾窗起点向下对齐到块边界，于是日志每追加一批步骤，被改写的
//     顶多是一个块——provider 的 prompt cache 在两次调用之间最大
//     化命中。这不是优化点缀，是本包存在的理由。
//   - 预算按字节计（token 的近似），超出时先二分收缩档位上限，
//     最后才硬切，硬切处留下可追查的标记。
package prompt

import (
	"fmt"
	"sort"
	"strings"

	"mindloop/internal/ids"
	"mindloop/internal/traj"
)

// Role 是 LLM 消息角色。
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message 是一条标准的 chat 消息——未来 llm 工具的输入形态。
type Message struct {
	Role    Role   `json:"role"`
	Content string `json:"content"`
}

// 档位与窗口的默认值。 Defaults 吸收自 Headlong 的 bin/context。
const (
	DefaultHead            = 1
	DefaultTail            = 50
	DefaultBlock           = 20
	DefaultFieldLimit      = 2048
	DefaultBlockFieldLimit = 8192
	// MinLimit 是预算二分的下限：低于它的档位只会产出残句。
	MinLimit = 256
)

// Options 控制渲染。零值字段取 Defaults。
type Options struct {
	// Head 是开头原样保留的步骤数（越旧越压缩的反面：第一步骤
	// 常常定义了任务本身，永远不省略）。
	Head int
	// Tail 是结尾尾窗的步骤数。
	Tail int
	// Block 是"新近整块"的大小：最新 Block 个步骤以更宽的
	// BlockFieldLimit 渲染，且尾窗起点向下对齐到它的边界。
	// 0 表示不分块。
	Block int
	// Pin 是额外保留的 step id 前缀（中段被点名的步骤照常渲染）。
	Pin []string

	// FieldLimit 是常规区域的单字段字节上限。
	FieldLimit int
	// BlockFieldLimit 是新近整块区域的单字段字节上限。
	BlockFieldLimit int
	// MaxBytes 是渲染内容的总字节预算；0 表示不限。
	MaxBytes int

	// AssistantTypes 渲染为 assistant 角色的步骤类型；其余一律
	// user（与 Headlong 的 --assistant-types/--user-types 同源）。
	AssistantTypes []string
	// ExcludeFields 是额外从渲染中剔除的字段名。
	ExcludeFields []string
}

func (o Options) withDefaults() Options {
	if o.Head < 0 {
		o.Head = 0
	}
	if o.Tail <= 0 {
		o.Tail = DefaultTail
	}
	if o.Block < 0 {
		o.Block = 0
	}
	if o.FieldLimit <= 0 {
		o.FieldLimit = DefaultFieldLimit
	}
	if o.BlockFieldLimit <= 0 {
		o.BlockFieldLimit = DefaultBlockFieldLimit
	}
	return o
}

// windowStart 返回尾窗的起始下标。关键细节：起点向下对齐到 Block
// 的倍数——于是日志在同一个块内追加时，所有行的档位都不变；唯一
// 的改写发生在块边界跨越的那一次调用里。稳定性即省钱。
func (o Options) windowStart(n int) int {
	start := n - o.Tail
	if start < 0 {
		start = 0
	}
	if o.Block > 0 && start > 0 {
		start -= start % o.Block
	}
	return start
}

type band struct {
	limit  int
	elided bool
}

// bandFor 返回第 i 行的档位——纯粹是 (i, n, 选项) 的函数，这是
// 网格稳定性的形式化表达。
func (o Options) bandFor(i, n int) band {
	head := o.Head
	if head > n {
		head = n
	}
	start := o.windowStart(n)
	if start <= head {
		// 日志不大：整段都在窗口里，无省略区。
		return band{limit: o.limitFor(i, n)}
	}
	switch {
	case i < head:
		return band{limit: o.FieldLimit}
	case i < start:
		return band{elided: true}
	default:
		return band{limit: o.limitFor(i, n)}
	}
}

// limitFor 返回第 i 行的字段字节上限。新近整块的定义是块对齐的：
// blockStart(n) = ⌊(n-1)/Block⌋×Block——于是档位只在日志跨越块边界
// 的那次追加时变化（每 Block 步一次），块内追加不触碰任何既存行。
// 若用朴素的 i >= n-Block，边界会随每次追加漂移，缓存网格就失效了。
func (o Options) limitFor(i, n int) int {
	if o.Block > 0 && n > 0 {
		blockStart := ((n - 1) / o.Block) * o.Block
		if i >= blockStart {
			return o.BlockFieldLimit
		}
	}
	return o.FieldLimit
}

// Render 把步骤渲染为消息序列。角色映射之后，相邻同角色消息以
// 空行合并，满足 provider 对 user/assistant 交替的要求。
func Render(steps []traj.Step, opts Options) []Message {
	opts = opts.withDefaults()
	n := len(steps)
	if n == 0 {
		return nil
	}

	assistant := make(map[string]bool, len(opts.AssistantTypes))
	for _, t := range opts.AssistantTypes {
		assistant[t] = true
	}
	pins := resolvePins(steps, opts.Pin)

	// renderAt 是唯一的渲染出口：档位一律取自 bandFor——生产路径
	// 与测试钉死的"档位纯函数"是同一份实现，不会再各自漂移。
	renderAt := func(i int, b band) string {
		if b.elided {
			// 被点名的省略区步骤照常渲染（用其本档位上限）。
			return renderStep(steps[i], opts.limitFor(i, n), opts)
		}
		return renderStep(steps[i], b.limit, opts)
	}
	var msgs []Message
	emit := func(r Role, text string) {
		if len(msgs) > 0 && msgs[len(msgs)-1].Role == r {
			msgs[len(msgs)-1].Content += "\n\n" + text
			return
		}
		msgs = append(msgs, Message{Role: r, Content: text})
	}
	roleFor := func(i int) Role {
		if assistant[steps[i].Type] {
			return RoleAssistant
		}
		return RoleUser
	}

	head := opts.Head
	if head > n {
		head = n
	}
	start := opts.windowStart(n)

	for i := 0; i < head; i++ {
		emit(roleFor(i), renderAt(i, opts.bandFor(i, n)))
	}
	elidedCount := 0
	if start > head {
		for i := head; i < start; i++ {
			if pins[i] {
				emit(roleFor(i), renderAt(i, opts.bandFor(i, n)))
			} else {
				elidedCount++
			}
		}
		emit(RoleUser, fmt.Sprintf("……（此处省略 %d 步；原文用 mindloop traj show <step_id> --full 查看）……", elidedCount))
	}
	from := start
	if from < head {
		from = head
	}
	for i := from; i < n; i++ {
		emit(roleFor(i), renderAt(i, opts.bandFor(i, n)))
	}

	if opts.MaxBytes > 0 && bytesOf(msgs) > opts.MaxBytes {
		msgs = fitBudget(steps, opts, msgs)
	}
	return msgs
}

// resolvePins 把 id 前缀解析为步骤下标（每个前缀取首个命中）。
func resolvePins(steps []traj.Step, pins []string) map[int]bool {
	out := make(map[int]bool, len(pins))
	for _, p := range pins {
		for i, s := range steps {
			if strings.HasPrefix(s.StepID, p) {
				out[i] = true
				break
			}
		}
	}
	return out
}

func bytesOf(msgs []Message) int {
	total := 0
	for _, m := range msgs {
		total += len(m.Content)
	}
	return total
}

// fitBudget 先以二分收缩档位上限重渲（保留能放下的最大上限），
// 仍超预算才硬切——与 Headlong 的 --max-bytes 二分同一思路。
func fitBudget(steps []traj.Step, opts Options, msgs []Message) []Message {
	hi := float64(1)
	lo := float64(0)
	var best []Message
	for iter := 0; iter < 12; iter++ {
		mid := (lo + hi) / 2
		try := opts
		try.MaxBytes = 0
		try.FieldLimit = scaled(opts.FieldLimit, mid)
		try.BlockFieldLimit = scaled(opts.BlockFieldLimit, mid)
		candidate := Render(steps, try)
		if bytesOf(candidate) <= opts.MaxBytes {
			best = candidate
			lo = mid
		} else {
			hi = mid
		}
	}
	if best == nil {
		// 即便下限档位也放不下：硬切兜底。
		try := opts
		try.MaxBytes = 0
		try.FieldLimit = MinLimit
		try.BlockFieldLimit = MinLimit
		best = Render(steps, try)
	}
	return hardCut(best, opts.MaxBytes)
}

func scaled(limit int, m float64) int {
	v := int(float64(limit) * m)
	if v < MinLimit {
		v = MinLimit
	}
	return v
}

// hardCut 顺序保留消息，越界的那条就地截断，其余丢弃；留下的
// 痕迹让"模型没看到尾部"成为日志里可追查的事实。
func hardCut(msgs []Message, budget int) []Message {
	out := make([]Message, 0, len(msgs))
	used := 0
	for _, m := range msgs {
		if used+len(m.Content) <= budget {
			out = append(out, m)
			used += len(m.Content)
			continue
		}
		remain := budget - used
		const marker = "\n《预算超出：轨迹过密，尾部内容已丢弃》\n《预算超出：轨迹过密》"
		if remain > 2*len(marker) {
			text, _ := cutRunes(m.Content, remain-len(marker))
			out = append(out, Message{Role: m.Role, Content: text + marker})
		}
		break
	}
	return out
}

// renderStep 把一个步骤渲染为文本块：
//
//	[id8] type k=v k=v:
//	content……
//
// 每个字段都按档位上限截断，截断处留下可执行的取回命令——
// 分级是索引而不是证词，agent 永远能走回原文。
func renderStep(s traj.Step, limit int, opts Options) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[%s] %s", ids.Short(s.StepID, 8), s.Type)
	for _, k := range sortedKeys(s.Fields) {
		if k == "content" || isMeta(k, opts) {
			continue
		}
		v, _ := s.Field(k)
		b.WriteString(" " + k + "=" + cutMarked(v, minInt(limit, 256), s.StepID, true))
	}
	if content, ok := s.Field("content"); ok {
		b.WriteString(":\n")
		b.WriteString(cutMarked(content, limit, s.StepID, false))
	}
	return b.String()
}

func isMeta(k string, opts Options) bool {
	switch k {
	case "run_id":
		return true
	}
	if strings.HasSuffix(k, "_ref") || strings.HasSuffix(k, "_bytes") {
		return true
	}
	for _, x := range opts.ExcludeFields {
		if k == x {
			return true
		}
	}
	return false
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
