package mind

import (
	"context"

	"mindloop/internal/llm"
)

// LLMThinker 把 llm.Client 适配成 runner.Thinker——运行循环与
// responder 只认 Think 接口，供应商不关心循环协议，接缝在这里。
// CLI（run/chat/mind/recap）与 web 仪表盘共用这一个适配器，
// 不再各写一份私有桥接。
type LLMThinker struct{ Client *llm.Client }

func (a LLMThinker) Think(ctx context.Context, system string, msgs []llm.Message) (string, error) {
	return a.Client.Complete(ctx, system, msgs)
}
