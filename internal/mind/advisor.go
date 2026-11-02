// advisor.go — 任务方向研判者（D12 顾问型，2026-11-03 裁决落地）：
// 低频独立思考者对照任务书与最近执行轨迹判断「偏航」（明显偏离
// 目标——区别于监督快照的「打转」检测：重复失败聚合），偏航时写
// alert 步骤告警，不打断执行——cancel 权留给操作员（「主动性 ≠
// 自治」：介入 = cancel + 带说明 resubmit，D9a 裁决）。
//
// 设计边界（裁决记录）：
//   - 顾问型：只发 alert 不动手；执行者型（可 cancel）被否——
//     「谁监督监督者」未解，误杀成本 = 重跑 token+时间；
//   - 第三种上下文：读任务书与轨迹但无执行工具——与 monolith
//     （执行者）/responder（对话者）正交；
//   - 无 Running/AwaitingApproval 任务时零模型调用直接返回——
//     空闲部署零成本，默认启用无负担；
//   - 偏航判定保守：输出不可解析或不确定时判 on-track——误报
//     会狼来了，漏报由操作员验收兜底（VerifyDeliverables 收口）。
package mind

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lomehong/mindloop/internal/llm"
	"github.com/lomehong/mindloop/internal/obs"
	"github.com/lomehong/mindloop/internal/task"
	"github.com/lomehong/mindloop/internal/traj"
)

// AdvisorOptions 配置研判者。
type AdvisorOptions struct {
	Timeline *traj.Timeline
	// Thinker 是研判模型（单次调用、无工具）。
	Thinker runnerThinker
	// SelfName 是身份名（alert 署名）。
	SelfName string
	// Interval 是研判周期（Subscription.Watchdog）；0 取默认
	// DefaultAdvisorInterval。
	Interval time.Duration
	// TailSteps 是研判窗口内随任务书携带的步骤数上限。
	TailSteps int
}

// DefaultAdvisorInterval 是缺省研判周期：30 分钟——一个 attempt
// 的中位时长（P0-4 实测 p50=3.3min/p90=10.5min）的 3~10 倍，
// 一次 attempt 至多被研判一两次。
const DefaultAdvisorInterval = 30 * time.Minute

// advisorSystemPrompt 是研判提示（英文面向模型）。
const advisorSystemPrompt = `You are an independent task-direction advisor. You will be given a task brief and the recent execution trace of the agent working on it. Judge whether the execution is still heading toward the task's goal:

- "on-track": the steps visibly advance the task goal (or its necessary groundwork);
- "drift": the steps are clearly solving a different problem, wandering without progress toward the goal, or contradicting the brief.

Reply with ONE JSON object only: {"verdict":"on-track|drift","reason":"one-sentence evidence citing what you saw"}. Be conservative: when uncertain, answer on-track. You advise; a human decides.`

// Advisor 是顾问型研判思考者。
type Advisor struct {
	opts AdvisorOptions
}

// NewAdvisor 构造研判者（Thinker 接口；由调度器按 Watchdog 周期唤醒）。
func NewAdvisor(opts AdvisorOptions) *Advisor {
	if opts.Interval <= 0 {
		opts.Interval = DefaultAdvisorInterval
	}
	if opts.TailSteps <= 0 {
		opts.TailSteps = 40
	}
	return &Advisor{opts: opts}
}

func (a *Advisor) Name() string { return "advisor" }

func (a *Advisor) Subscriptions() Subscription {
	// 只订阅 watchdog 合成唤醒（自发档预算归因），无步骤类型订阅。
	return Subscription{Watchdog: a.opts.Interval}
}

// advisorVerdict 是研判输出的解析形态。
type advisorVerdict struct {
	Verdict string `json:"verdict"`
	Reason  string `json:"reason"`
}

func (a *Advisor) Wake(ctx context.Context, w Wake) Outcome {
	// 归因：自发档（watchdog）+ advisor 阶段（stats 新桶）。
	ctx = llm.WithAttrib(ctx, llm.Attrib{Thinker: a.Name(), Wake: obs.WakeWatchdog, Phase: "advisor"})
	store := task.New(a.opts.Timeline, a.opts.SelfName)
	all, err := store.List(ctx)
	if err != nil {
		return Outcome{Note: "任务清单读取失败: " + err.Error()}
	}
	var active *task.Task
	for i := range all {
		if all[i].State == task.Running || all[i].State == task.AwaitingApproval {
			active = &all[i]
			break
		}
	}
	if active == nil {
		return Outcome{} // 无在途任务：零模型调用。
	}
	trace, err := a.taskTrace(active.ID)
	if err != nil {
		return Outcome{Note: "轨迹读取失败: " + err.Error()}
	}
	user := fmt.Sprintf("任务书:\n%s\n\n最近执行轨迹:\n%s", active.Content, trace)
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	text, err := a.opts.Thinker.Think(ctx, advisorSystemPrompt, []llm.Message{{Role: "user", Content: user}})
	if err != nil {
		return Outcome{Note: "研判调用失败: " + err.Error()}
	}
	var v advisorVerdict
	if err := json.Unmarshal([]byte(trimJSON(text)), &v); err != nil || v.Verdict != "drift" {
		return Outcome{Note: "研判完成: on-track（或输出不可解析，保守判 on-track）"}
	}
	// 偏航：写 alert 告警（代码写入、无 launched_by——与 scheduleAlerter
	// 同款，告警必须叫醒人/心智，不能只躺在日志里）。
	s := traj.NewStep(traj.TypeAlert)
	s.Fields["from"] = a.opts.SelfName
	s.Fields["to"] = "operator"
	s.Fields["source"] = "advisor"
	s.Fields["kind"] = "task-drift"
	s.Fields["task_id"] = active.ID
	s.Fields["reason"] = "偏航研判: " + oneLineAdvisor(v.Reason, 300)
	s.Fields["content"] = fmt.Sprintf("[advisor] 任务 %s 疑似偏航：%s——研判是建议，是否 cancel 由操作员决定（介入=cancel+带说明重新提交）", idsShort(active.ID), oneLineAdvisor(v.Reason, 300))
	wctx, wcancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer wcancel()
	if err := a.opts.Timeline.Append(wctx, s); err != nil {
		return Outcome{Note: "偏航告警落盘失败: " + err.Error()}
	}
	return Outcome{Note: "研判完成: drift——已发告警（顾问型不动手，cancel 权在操作员）"}
}

// taskTrace 渲染任务最近的执行轨迹（盖该 task_id 章的步骤）。
func (a *Advisor) taskTrace(taskID string) (string, error) {
	steps, err := a.opts.Timeline.Tail(160, nil)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	n := 0
	for i := len(steps) - 1; i >= 0 && n < a.opts.TailSteps; i-- {
		s := steps[i]
		if tid, _ := s.Field("task_id"); tid != taskID {
			continue
		}
		content, _ := s.Field("content")
		if content == "" {
			content, _ = s.Field("digest")
		}
		fmt.Fprintf(&b, "  [%s] %s\n", s.Type, oneLineAdvisor(content, 160))
		n++
	}
	if n == 0 {
		return "（尚无盖该任务章的步骤）", nil
	}
	return b.String(), nil
}

// trimJSON 从模型输出里剥出 JSON 对象（容忍围栏与前后闲话）。
func trimJSON(text string) string {
	t := strings.TrimSpace(text)
	if i := strings.Index(t, "{"); i >= 0 {
		if j := strings.LastIndex(t, "}"); j > i {
			return t[i : j+1]
		}
	}
	return t
}

func oneLineAdvisor(s string, limit int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit]) + "…"
}

func idsShort(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
