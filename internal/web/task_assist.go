package web

// POST /api/identities/{name}/task-assist —— AI 辅助创建任务：把操作员
// 的粗糙草稿起草为一份"完整、可执行"的任务书（新任务对话框的 AI
// 辅助按钮）。这是 Web 侧首个 LLM 调用点，隔离在本文件：
//
//   - 复用身份 providers 档位（request 档，回落环境变量）；
//   - 单次调用、不落轨迹、不触任务管线——AI 只起草，提交与否、
//     内容终稿仍由操作员决定；
//   - 调用计费走身份用量台账（与对话同级暴露，无新增泄露面）。

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/lomehong/mindloop/internal/config"
	"github.com/lomehong/mindloop/internal/identity"
	"github.com/lomehong/mindloop/internal/llm"
	"github.com/lomehong/mindloop/internal/traj"
)

const (
	taskAssistMaxDraft = 4000 // 草稿字符上限（rune）——面向意图草稿，不是全文粘贴
	taskAssistTimeout  = 30 * time.Second
)

// taskAssistPrompt —— 起草人系统提示：把粗糙意图改写为完整、可执行
// 的显式委托任务书。质量标准即"新任务"门槛本身：完整、可执行、
// 可判定，且是任务书不是聊天。
const taskAssistPrompt = `你是任务书起草人。把操作员的粗糙意图改写为一份"完整、可执行"的显式委托任务书，交给指定心智独立执行。

要求：
- 结构：目标 / 范围与边界 / 涉及路径与工作目录 / 约束 / 验收标准；
- 可判定：验收标准必须有一条可检验的完成线；
- 不编造：缺关键信息（路径、期望结果、时限）时，在"待确认"一节列出需要操作员补充的问题，禁止虚构；
- 不可逆操作（删除/覆盖/对外发送）必须显式声明；
- 语气：任务书体，祈使 + 事实陈述，无寒暄无对话腔；
- 语言跟随操作员草稿。

输出：仅任务书正文（Markdown），不加任何解释或前后缀。`

// taskAssistClientFor 依身份档位构造起草客户端；测试中可整体替换。
var taskAssistClientFor = func(id *identity.Identity) (*llm.Client, error) {
	providers, err := config.LoadProviders(traj.Home(), id.Dir)
	if err != nil {
		return nil, err
	}
	choice, err := config.ResolveTier(providers, "request", os.Getenv)
	if err != nil {
		return nil, err
	}
	if choice.Source != "providers.json" {
		return llm.FromEnv()
	}
	return llm.New(llm.Spec{
		Provider: choice.Profile.Provider,
		BaseURL:  choice.Profile.BaseURL,
		APIKey:   choice.APIKey,
		Model:    choice.Binding.Model,
	})
}

func (s *Server) handleTaskAssist(w http.ResponseWriter, r *http.Request, id *identity.Identity, rest []string) {
	if len(rest) != 0 {
		writeError(w, 404, "未知子路径: task-assist/"+strings.Join(rest, "/"))
		return
	}
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var body struct {
		Draft string `json:"draft"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		writeError(w, 400, "请求体应为 JSON")
		return
	}
	draft := strings.TrimSpace(body.Draft)
	if draft == "" {
		writeError(w, 400, "草稿为空——先写下要它做的事，再让 AI 帮你写清楚")
		return
	}
	if runes := []rune(draft); len(runes) > taskAssistMaxDraft {
		writeError(w, 400, "草稿过长（上限 4000 字符）——AI 辅助面向意图草稿，不是全文粘贴")
		return
	}

	client, err := taskAssistClientFor(id)
	if err != nil {
		writeError(w, 409, "该身份未配置 LLM（providers.json / .env）——AI 辅助不可用，可手动写任务书")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), taskAssistTimeout)
	defer cancel()
	task, err := client.Complete(ctx, taskAssistPrompt,
		[]llm.Message{{Role: "user", Content: "操作员草稿：\n" + draft}})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			writeError(w, 504, "起草超时（30s）——可重试，或手写任务书")
			return
		}
		writeError(w, 502, "起草失败："+err.Error())
		return
	}
	task = strings.TrimSpace(task)
	if task == "" {
		writeError(w, 502, "起草返回为空——请重试，或手写任务书")
		return
	}
	writeJSON(w, 200, map[string]string{"task": task})
}
