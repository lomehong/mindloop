package mind

import (
	"context"

	"mindloop/internal/traj"
)

// 内置思考者的注册名。Name() 与 ThinkerNames 引用同一组常量——
// 名单与实际注册名在编译期不可能漂移（测试再钉一道，见
// message_test.go 的 TestThinkerNamesMatchesBundled）。
const (
	monolithName  = "monolith"
	responderName = "responder"
)

// ThinkerNames 返回内置思考者的权威名单（注册顺序）。web 仪表盘的
// thinker 控制面与 CLI 提示消费它；新增思考者必须同步此清单——
// TestThinkerNamesMatchesBundled 钉住它与 NewMonolith/NewResponder
// 实际注册名的一致性。
func ThinkerNames() []string {
	return []string{monolithName, responderName}
}

// PostMessage 统一"外部对心智说话"的消息构造：message 步骤的字段
// 形态（from/to/source/content）只有这一处权威定义。chat、mind say、
// web、MCP serve 曾各写一份手拼——字段名漂移会让 responder 的定向
// 过滤（to == SelfName）与调度器的 FIFO 分类静默失效。
//
// 语义（以 chat 的原手写构造为准）：from 是说话者（人类侧惯例
// "operator"），to 是目标身份名，source 是入口标记（chat/cli/web），
// content 是正文。不加 ctx 参数：Append 的锁等待受
// Timeline.LockTimeout（默认 5s）兜底，入口级便利函数不把取消传播
// 的复杂性转嫁给每个调用点。
func PostMessage(tl *traj.Timeline, from, to, source, content string) error {
	s := traj.NewStep(traj.TypeMessage)
	s.Fields["from"] = from
	s.Fields["to"] = to
	s.Fields["source"] = source
	s.Fields["content"] = content
	return tl.Append(context.Background(), s)
}
