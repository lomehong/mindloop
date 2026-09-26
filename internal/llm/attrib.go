// 调用归因：模型调用在业务上属于哪个任务/运行/尝试/思考者/阶段。
// 经 ctx 随每次调用传递——Usage 是调用局部值，绝不借共享 client 的
// 回调状态"记住"是谁在调用（那会让并发调用串账）。
package llm

import "context"

// Attrib 描述一次模型调用的业务归属。零值字段表示"未设"，合并
// 时不覆盖已有值。
type Attrib struct {
	Task    string // 显式任务 ID
	Run     string // 运行代次（runner 的 run_id）
	Attempt int    // 任务 attempt（0 = 非任务运行）
	Thinker string // 发起调用的思考者（monolith/responder/recap）
	Wake    string // 唤醒原因（scheduled spontaneity 等）
	Phase   string // 阶段（task/wake/chat/recap）
}

// attribKey 是 ctx 里归因的私有键类型。
type attribKey struct{}

// WithAttrib 把非空字段合并进 ctx 上的归因：内层调用点更具体，
// 其非空字段覆盖外层同名字段；未设字段保留外层值。
func WithAttrib(ctx context.Context, a Attrib) context.Context {
	cur, _ := ctx.Value(attribKey{}).(Attrib)
	if a.Task != "" {
		cur.Task = a.Task
	}
	if a.Run != "" {
		cur.Run = a.Run
	}
	if a.Attempt > 0 {
		cur.Attempt = a.Attempt
	}
	if a.Thinker != "" {
		cur.Thinker = a.Thinker
	}
	if a.Wake != "" {
		cur.Wake = a.Wake
	}
	if a.Phase != "" {
		cur.Phase = a.Phase
	}
	return context.WithValue(ctx, attribKey{}, cur)
}

// AttribFrom 取 ctx 上的归因；无人挂载时返回零值。
func AttribFrom(ctx context.Context) Attrib {
	a, _ := ctx.Value(attribKey{}).(Attrib)
	return a
}

// applyAttrib 把归因写进 Usage（Complete 收尾时调用）。
func (u *Usage) applyAttrib(a Attrib) {
	u.Task = a.Task
	u.Run = a.Run
	u.Attempt = a.Attempt
	u.Thinker = a.Thinker
	u.Wake = a.Wake
	u.Phase = a.Phase
}
