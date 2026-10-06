package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/lomehong/mindloop/internal/childenv"
	"github.com/lomehong/mindloop/internal/connector/sensor"
	"github.com/lomehong/mindloop/internal/identity"
	"github.com/lomehong/mindloop/internal/llm"
	"github.com/lomehong/mindloop/internal/mcp"
	"github.com/lomehong/mindloop/internal/mind"
	"github.com/lomehong/mindloop/internal/obs"
	"github.com/lomehong/mindloop/internal/policy"
	"github.com/lomehong/mindloop/internal/runner"
	"github.com/lomehong/mindloop/internal/schedule"
	"github.com/lomehong/mindloop/internal/task"
	"github.com/lomehong/mindloop/internal/traj"
)

// 本文件是 chat 与 mind run 共享的心智装配：模型客户端、用量观测、
// 双模型分层、扩展能力面（skills/MCP）、调度器与两个思考者——
// 两条命令的差别只剩"客户端从哪来"（chat 允许降级 echo，mind run
// 硬性要求配置）与调度器参数，装配语义必须且只需在这里对齐一次。

// mindStack 聚合一次心智装配的产物。
type mindStack struct {
	dispatcher *mind.Dispatcher
	client     *llm.Client // 思考档（run 命令的横幅要打印它的供应商/模型）
	request    *llm.Client // 请求档（分层未设时与思考档同一指针）
	summary    *llm.Client // 摘要档（分层未设时与思考档同一指针）
}

// mindStackOpts 是装配参数。零值字段即 chat 的历史行为（无回退
// 策略覆盖、无轮次覆盖、200ms 轮询在 poll 显式给出前为零值——
// 调用方负责传）。
type mindStackOpts struct {
	// clientFactory 产出思考档客户端；返回错误则装配失败。
	clientFactory     func() (*llm.Client, error)
	poll              time.Duration
	watchdog          time.Duration
	backoff           *mind.BackoffPolicy // nil = monolith 用内置默认
	maxIterations     int                 // 0 = monolith 用内置默认（唤醒 8 轮）
	taskMaxIterations int                 // 0 = monolith 用内置默认（任务 16 轮）
	taskCallBudget    int                 // 0 = monolith 用内置默认（任务 24 次调用）
	logger            func(format string, args ...any)
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
	// 准入守卫：熔断（连续失败冷却 + 单次探测）与身份每日 token
	// 预算（MINDLOOP_DAILY_TOKENS，未设即不启用）。三个档位的
	// 客户端共用一份守卫——同一身份的健康与用量是一条线。
	guard := obs.NewGuard(id.Dir, o.logger)
	guard.Attach(client)
	// 请求档（MINDLOOP_REQUEST_MODEL 或 providers.json 的 request
	// 绑定，皆无则与思考档同一）。
	requestClient := requestTierClient(id.Dir, client, o.logger)
	guard.Attach(requestClient)
	// 摘要档（MINDLOOP_SUMMARY_MODEL 或 providers.json 的 summary
	// 绑定，皆无则与思考档同一）——recap 的摘要调用与唤醒主循环
	// 档位解耦。
	summaryClient := summaryTierClient(id.Dir, client, o.logger)
	guard.Attach(summaryClient)

	// 扩展能力面：技能库 + MCP 服务器（配置坏则降级为警告，不拦启动）。
	skillsDirs, mcpServers, extraEnv, extErr := identityExtension(id)
	if extErr != nil && o.logger != nil {
		o.logger("身份扩展面加载失败: %v", extErr)
	}

	persona, err := id.Persona()
	if err != nil {
		return nil, fmt.Errorf("mind: 加载 persona（人格残缺时拒绝启动心智）: %w", err)
	}

	// 上下文字节预算：总量 + 两个分段上限（未配置取默认语义）。
	ctxBytes, memBytes, sumBytes := contextBudgets()

	// 执行授权：自主行动与显式任务共用同一门（policy.Gate 实现
	// runner.BeforeExecute）；等待审批时经 WaitHook 同步任务
	// awaiting_approval 状态，chat 场景的审批提示经 o.logger 进终端。
	gate := policy.NewGate(policy.Dir(id.Timeline.Dir), policy.ModeFromEnv())
	gate.AutoReadOnly = policy.AutoReadOnlyFromEnv()
	gate.Tripwire = policy.TripwireFromEnv()
	gate.Logger = o.logger
	gate.WaitHook = taskApprovalHook(id.Timeline, id.Name)

	dispatcher := mind.NewDispatcher(id.Timeline, o.poll)
	dispatcher.SetLogger(o.logger)
	dispatcher.Register(mind.NewMonolith(mind.MonolithOptions{
		Timeline:           id.Timeline,
		Thinker:            mind.LLMThinker{Client: client},
		RequestThinker:     mind.LLMThinker{Client: requestClient},
		SummaryThinker:     mind.LLMThinker{Client: summaryClient},
		Backoff:            o.backoff,
		MaxIterations:      o.maxIterations,
		TaskMaxIterations:  o.taskMaxIterations,
		TaskCallBudget:     o.taskCallBudget,
		Watchdog:           o.watchdog,
		Persona:            persona,
		SelfName:           id.Name, // 轮次耗尽的工作摘要以身份名署名投递 operator
		MemDir:             filepath.Join(id.Dir, "memories"),
		ContextBudget:      ctxBytes,
		MemoryBytes:        memBytes,
		SummaryBytes:       sumBytes,
		EnableRecap:        true,
		Snapshots:          snapshotsFromEnv(),
		SkillsDirs:         skillsDirs,
		MCPServers:         mcpServers,
		ConnectorAddresses: connectorAddresses(),
		ExtraEnv:           extraEnv,
		SetLogger:          o.logger,
		BeforeExecute:      gate.Authorize,
	}))
	// 流式回复：MINDLOOP_STREAM=0 整体关闭（无旁路文件、无 replying
	// 状态，回复回退一次性补全）；缺省开启。流式只做 responder；
	// monolith 的行动循环不流式。responder 是对话面（人说一句、
	// 立刻回一句），走请求档——与外部步骤触发的反应式唤醒同一档。
	dispatcher.Register(mind.NewResponder(mind.ResponderOptions{
		Timeline:      id.Timeline,
		Thinker:       mind.LLMThinker{Client: requestClient},
		SelfName:      id.Name,
		Persona:       persona,
		ContextBudget: ctxBytes,
		SummaryBudget: sumBytes,
		// 记忆与 monolith 同源：对话里被问到做过的事时，
		// BM25 召回相关记忆而不凭空否认。
		MemDir:      filepath.Join(id.Dir, "memories"),
		MemoryBytes: memBytes,
		Streaming:   streamEnabled(),
		StreamFn:    responderStreamFn(client),
	}))
	// 身份日程挂上调度器心跳：Run 启动装载、每拍检查到点。
	dispatcher.SetSchedule(assembleSchedule(id, extraEnv, o.logger))
	// 感官宿主挂上调度器生命周期（感知系统宿主在 mind 主进程，
	// perception.md §4.2）：sensors.json 缺席 = 静默；配置坏 =
	// 调度器降级告警，不杀心智。
	dispatcher.SetSensors(mind.NewSensorRunner(mind.SensorRunnerOptions{
		Timeline:    id.Timeline,
		IdentityDir: id.Dir,
		SelfName:    id.Name,
		Factory:     sensorFactory(id),
		LearnDays:   sensor.EnvLearnDays(),
		Logger:      o.logger,
	}))
	return &mindStack{dispatcher: dispatcher, client: client, request: requestClient, summary: summaryClient}, nil
}

// contextBudgets 读取上下文预算环境变量：MINDLOOP_CONTEXT_BYTES
// （单次请求输入总量）、MINDLOOP_MEMORY_BYTES（相关记忆段）、
// MINDLOOP_SUMMARY_BYTES（摘要/分集段）。未设置或非法返回 0——
// 默认语义，不因配置错误拦启动。
func contextBudgets() (total, memory, summary int) {
	return envBytes("MINDLOOP_CONTEXT_BYTES"), envBytes("MINDLOOP_MEMORY_BYTES"), envBytes("MINDLOOP_SUMMARY_BYTES")
}

// envBytes 解析正整数字节配置（空/非法/非正值均返回 0）。
func envBytes(name string) int {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0
	}
	return n
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

// reportTo 折算任务完成回执/主动汇报的默认外发地址：REPORT_TO
// （显式环境变量 > 身份 .env，均已在此前加载）> 缺省 operator。
func reportTo() string {
	if v := strings.TrimSpace(os.Getenv("REPORT_TO")); v != "" {
		return v
	}
	return "operator"
}

// connectorAddresses 折算已就绪渠道桥的可投递地址（仅进 monolith
// 系统提示的披露段）。当前渠道清单与 connectorcmd 同步推进：
// wecom 三项齐备即认领 wecom:<uid> 地址。
func connectorAddresses() []string {
	if os.Getenv("WECOM_BOT_ID") == "" || os.Getenv("WECOM_BOT_SECRET") == "" {
		return nil
	}
	allow := childenv.List(os.Getenv("WECOM_ALLOW"))
	if len(allow) == 0 {
		return nil
	}
	out := make([]string, 0, len(allow))
	for _, uid := range allow {
		out = append(out, "wecom:"+uid)
	}
	return out
}

// schedulePaths 汇总身份日程的三处文件（装配与 CLI 子命令同源）。
// 路径语义已下沉到 schedule.Paths（web 消费面共用同一形态）。
func schedulePaths(id *identity.Identity) (schedulePath, logPath, statePath string) {
	return schedule.Paths(id.Dir, id.Timeline.Dir)
}

// assembleSchedule 装配身份日程：at/巡检任务经任务层幂等提交，exec
// 条目经沙箱执行（不叫醒模型、零 API 成本），exec 失败经 Alert 写
// alert 步骤叫醒心智。
func assembleSchedule(id *identity.Identity, extraEnv []string, logger func(string, ...any)) *schedule.Runtime {
	schedulePath, logPath, statePath := schedulePaths(id)
	return schedule.New(schedule.Options{
		Path:      schedulePath,
		Dir:       id.Dir,
		LogPath:   logPath,
		StatePath: statePath,
		Env:       extraEnv,
		ReportTo:  reportTo(),
		Logger:    logger,
		Submit:    scheduleSubmitter(id),
		Alert:     scheduleAlerter(id, logger),
	})
}

// scheduleAlerter 返回 exec 失败告警的写出口：alert 步骤是代码写入
// 的系统事实（from=身份、to=operator、source=system），不带
// launched_by 章——dispatcher 的作者章守卫不拦它，monolith 的订阅
// 面照常触发；responder 不订阅 alert，告警不引发寒暄。落盘失败只
// 进日志：告警是增强，不能反过来打断日程节拍。
func scheduleAlerter(id *identity.Identity, logger func(string, ...any)) func(kind, entryID, content string) {
	tl, self := id.Timeline, id.Name
	return func(kind, entryID, content string) {
		s := traj.NewStep(traj.TypeAlert)
		s.Fields["from"] = self
		s.Fields["to"] = "operator"
		s.Fields["source"] = "system"
		s.Fields["kind"] = kind
		s.Fields["entry"] = entryID
		s.Fields["content"] = content
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := tl.Append(ctx, s); err != nil && logger != nil {
			logger("alert 步骤落盘失败（%s/%s）: %v", kind, entryID, err)
		}
	}
}

// scheduleSubmitter 返回日程任务提交通道：From=schedule、幂等键 =
// sched-<id>-<date>；同键异载荷（ErrConflict：当天键已被用于不同
// 内容）视为"当天已有任务"的良性跳过。
func scheduleSubmitter(id *identity.Identity) func(ctx context.Context, content, clientMessageID string) (string, error) {
	store := task.New(id.Timeline, id.Name)
	return func(ctx context.Context, content, clientMessageID string) (string, error) {
		item, err := store.Submit(ctx, task.Submission{
			From:            schedule.SourceName,
			ClientMessageID: clientMessageID,
			Content:         content,
		})
		if err != nil {
			if errors.Is(err, task.ErrConflict) {
				return "当天已有任务", nil
			}
			return "", err
		}
		return "任务 " + item.ID, nil
	}
}

// taskApprovalHook 把审批等待的进入/离开同步到任务状态机：等待 →
// awaiting_approval（取消与续跑因此有据可依），离开 → running。
// 无任务归属的执行（run 命令场景）不碰任务；转换被取消等竞态拒绝
// 时保持已有事实——钩子是同步面，不是权威。
func taskApprovalHook(tl *traj.Timeline, owner string) func(runner.Execution, bool) {
	store := task.New(tl, owner)
	return func(ex runner.Execution, waiting bool) {
		if ex.TaskID == "" {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		state := task.Running
		if waiting {
			state = task.AwaitingApproval
		}
		_, _ = store.Advance(ctx, ex.TaskID, task.Event{State: state, Attempt: ex.Attempt, RunID: ex.RunID})
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
