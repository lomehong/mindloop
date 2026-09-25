package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"mindloop/internal/identity"
	"mindloop/internal/llm"
	"mindloop/internal/mcp"
	"mindloop/internal/mind"
	"mindloop/internal/obs"
	"mindloop/internal/traj"
)

// 本文件是 chat 与 mind run 共享的心智装配：模型客户端、用量观测、
// 双模型分层、扩展能力面（skills/MCP）、调度器与两个思考者——
// 两条命令的差别只剩"客户端从哪来"（chat 允许降级 echo，mind run
// 硬性要求配置）与调度器参数，装配语义必须且只需在这里对齐一次。

// mindStack 聚合一次心智装配的产物。
type mindStack struct {
	dispatcher *mind.Dispatcher
	client     *llm.Client // 思考档（run 命令的横幅要打印它的供应商/模型）
}

// mindStackOpts 是装配参数。零值字段即 chat 的历史行为（无回退
// 策略覆盖、无轮次覆盖、200ms 轮询在 poll 显式给出前为零值——
// 调用方负责传）。
type mindStackOpts struct {
	// clientFactory 产出思考档客户端；返回错误则装配失败。
	clientFactory func() (*llm.Client, error)
	poll          time.Duration
	watchdog      time.Duration
	backoff       *mind.BackoffPolicy // nil = monolith 用内置默认
	maxIterations int                 // 0 = monolith 用内置默认
	logger        func(format string, args ...any)
}

// assembleMindStack 按 chat / mind run 共用的语义装配一个完整心智。
// persona 错误策略统一为拒启：persona 读不出来意味着人格残缺，
// 让它上线对人说的是残缺的话。
func (c *CLI) assembleMindStack(id *identity.Identity, o mindStackOpts) (*mindStack, error) {
	client, err := o.clientFactory()
	if err != nil {
		return nil, err
	}
	// 观测面：用量台账 + 健康标记（<身份目录>/ 下）。
	client.OnDone = obs.UsageRecorder(id.Dir, client.Model, client.Provider, o.logger)
	// 请求档（MINDLOOP_REQUEST_MODEL，未设则与思考档同一）。
	requestClient := requestTierClient(client, id.Dir, o.logger)

	// 扩展能力面：技能库 + MCP 服务器（配置坏则降级为警告，不拦启动）。
	skillsDirs, mcpServers, extraEnv, extErr := identityExtension(id)
	if extErr != nil && o.logger != nil {
		o.logger("身份扩展面加载失败: %v", extErr)
	}

	persona, err := id.Persona()
	if err != nil {
		return nil, fmt.Errorf("mind: 加载 persona（人格残缺时拒绝启动心智）: %w", err)
	}

	dispatcher := mind.NewDispatcher(id.Timeline, o.poll)
	dispatcher.SetLogger(o.logger)
	dispatcher.Register(mind.NewMonolith(mind.MonolithOptions{
		Timeline:       id.Timeline,
		Thinker:        mind.LLMThinker{Client: client},
		RequestThinker: mind.LLMThinker{Client: requestClient},
		Backoff:        o.backoff,
		MaxIterations:  o.maxIterations,
		Watchdog:       o.watchdog,
		Persona:        persona,
		SelfName:       id.Name, // 轮次耗尽的工作摘要以身份名署名投递 operator
		MemDir:         filepath.Join(id.Dir, "memories"),
		EnableRecap:    true,
		SkillsDirs:     skillsDirs,
		MCPServers:     mcpServers,
		ExtraEnv:       extraEnv,
		SetLogger:      o.logger,
	}))
	// 流式回复：MINDLOOP_STREAM=0 整体关闭（无旁路文件、无 replying
	// 状态，回复回退一次性补全）；缺省开启。流式只做 responder；
	// monolith 的行动循环不流式。
	dispatcher.Register(mind.NewResponder(mind.ResponderOptions{
		Timeline:  id.Timeline,
		Thinker:   mind.LLMThinker{Client: client},
		SelfName:  id.Name,
		Persona:   persona,
		Streaming: streamEnabled(),
		StreamFn:  responderStreamFn(client),
	}))
	return &mindStack{dispatcher: dispatcher, client: client}, nil
}

// streamEnabled 折算 MINDLOOP_STREAM kill switch：环境变量非 "0" 即
// 开启（含未设置——流式是默认行为）。
func streamEnabled() bool { return os.Getenv("MINDLOOP_STREAM") != "0" }

// responderStreamFn 把 llm.Client 的 CompleteStream 适配成 responder
// 的 StreamFn 调用面——参数同构，请求收敛成结构化请求。用量观测
// 走 CompleteStream 内部的 OnDone，与非流式路径同一份台账。
func responderStreamFn(client *llm.Client) func(context.Context, string, []llm.Message, func(string)) (string, error) {
	return func(ctx context.Context, system string, msgs []llm.Message, onDelta func(string)) (string, error) {
		res, err := client.CompleteStream(ctx, llm.Request{System: system, Messages: msgs}, onDelta)
		if err != nil {
			return "", err
		}
		return res.Text, nil
	}
}

// clearStopFlag 消费残留的停机标志：上次会话可能带着未消费的
// stop 退出，不清掉的话新调度器第一个心跳就会自杀。持锁者调用
// （mind.ClearStopFlag 的约定）。
func clearStopFlag(id *identity.Identity) error {
	return mind.ClearStopFlag(id.Timeline)
}

// mindRunning 只读探测心智是否在跑：运行锁属主进程是否存活。
// 探测方绝不能有副作用——TryRunLock 会建目录甚至偷走残留锁，
// say/status/stop 这类旁观命令用它会把"看一眼"变成"动一手"。
func mindRunning(tlDir string) bool {
	return traj.LockOwnerAlive(mind.RunLockDir(tlDir))
}

// mindOwnerPID 读取运行锁属主的 pid（0 = 未知：锁不存在、属主
// 文件缺失或损坏）。traj 的属主文件是 owner.json（含 pid/created/exe）。
func mindOwnerPID(tlDir string) int {
	data, err := os.ReadFile(filepath.Join(mind.RunLockDir(tlDir), "owner.json"))
	if err != nil {
		return 0
	}
	var o struct {
		PID int `json:"pid"`
	}
	if json.Unmarshal(data, &o) != nil {
		return 0
	}
	return o.PID
}

// mcpInvalidArgs 把参数解析失败包装为 -32602 语义的错误——
// mcp serve 的 Handler 统一用它回报调用方的协议错误。
func mcpInvalidArgs(err error) error {
	return fmt.Errorf("%w: %v", mcp.ErrInvalidArguments, err)
}
