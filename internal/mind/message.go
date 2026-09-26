package mind

import (
	"context"
	"errors"

	"mindloop/internal/task"
	"mindloop/internal/traj"
)

// ErrMessageConflict：同一 (from, client_message_id) 已用于不同内容的
// 消息——客户端载荷不一致必须可见，不静默采纳任一版本。
var ErrMessageConflict = errors.New("mind: client_message_id 已用于不同内容的消息")

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

// “外部对心智说话”的消息构造入口：message 步骤的字段形态
// （from/to/source/content）只有 PostMessageOnce 这一处权威定义。
// chat、mind say、web、MCP serve 曾各写一份手拼——字段名漂移会让
// responder 的定向过滤（to == SelfName）与调度器的 FIFO 分类静默失效。
//
// 消息同时盖 protocol_version 章：盖章即“新协议”的日志事实——
// 恢复窗口（Recover/Pending）只处理盖章消息，旧历史永远不被补答。
//
// 语义（以 chat 的原手写构造为准）：from 是说话者（人类侧惯例
// "operator"），to 是目标身份名，source 是入口标记（chat/cli/web），
// content 是正文。不加 ctx 参数：Append 的锁等待受
// Timeline.LockTimeout（默认 5s）兜底，入口级便利函数不把取消传播
// 的复杂性转嫁给每个调用点。
//
// clientMessageID 非空时支持幂等重发：按 (from, client_message_id)
// 查重——同键同载荷返回原步骤（响应丢失后的安全重发），同键不同
// 内容返回 ErrMessageConflict。查重域与 task.Store 的提交幂等键一致。
//
// 查重与追加之间存在竞态窗口：同一 (from, cid) 的并发重发可能双双
// 未命中而重复落盘。重发场景是串行的（客户端收到响应/超时后才重发），
// 此处不为此引入跨进程锁；需要严格幂等的执行委托走 task.Store。
func PostMessageOnce(tl *traj.Timeline, from, to, source, content, clientMessageID string) (traj.Step, error) {
	if clientMessageID != "" {
		steps, err := tl.Steps()
		if err != nil {
			return traj.Step{}, err
		}
		for _, st := range steps {
			if st.Type != traj.TypeMessage {
				continue
			}
			cid, _ := st.Field("client_message_id")
			if cid != clientMessageID {
				continue
			}
			if f, _ := st.Field("from"); f != from {
				continue
			}
			if c, _ := st.Field("content"); c != content {
				return traj.Step{}, ErrMessageConflict
			}
			return st, nil
		}
	}
	s := traj.NewStep(traj.TypeMessage)
	s.Fields["protocol_version"] = task.ProtocolVersion
	s.Fields["from"] = from
	s.Fields["to"] = to
	s.Fields["source"] = source
	s.Fields["content"] = content
	if clientMessageID != "" {
		s.Fields["client_message_id"] = clientMessageID
	}
	if err := tl.Append(context.Background(), s); err != nil {
		return traj.Step{}, err
	}
	return s, nil
}

// PostMessage 是 PostMessageOnce 的无键封装（普通聊天路径）。
func PostMessage(tl *traj.Timeline, from, to, source, content string) error {
	_, err := PostMessageOnce(tl, from, to, source, content, "")
	return err
}
