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
	// 起草是分钟级推理：glm-5.3-flash 起草一份完整任务书实测 ~60s
	//（推理 token 远多于正文），30s 必超时；取 2 倍余量。
	taskAssistTimeout = 120 * time.Second
)

// taskAssistPrompt —— 起草人系统提示：把粗糙意图**直接写成终稿**——
// 结构即目标格式，缺信息处默认值直填 + 统一占位标记，操作员的编辑
// 动作收敛为"把【待定】替换成确定值"。禁止把问题列成章节甩回给
// 操作员（那是半成品+问卷，不是终稿）。
const taskAssistPrompt = `你是任务书起草人。把操作员的粗糙意图直接写成一份"完整、可执行"的显式委托任务书**终稿**，交给指定心智独立执行。

要求：
- 结构即终稿结构：目标 / 范围与边界 / 涉及路径与工作目录 / 交付物 / 约束 / 验收标准，六节齐全（Markdown）；
- 可判定：验收标准必须有一条可检验的完成线；
- 交付物：单独一节，列出全部交付文件的相对路径（相对任务工作区），一行一个——这是执行方收口时被机械核验的清单，必须与验收标准里的完成线一一对应；没有文件交付时写明"无文件交付物，结论以收口文本回传"；
- 缺信息不许提问：缺关键信息（路径、范围、时限、格式等）时，禁止把问题列成章节、清单或任何"待确认"形式问操作员——选出最合理的默认值或候选，直接写入正文对应位置，并在该值后面紧跟统一占位标记：【待定｜建议：<默认值或候选>】。操作员后续只做一件事：把【待定】替换成确定值（或保留建议值）；
- 占位标记全文统一【待定｜…】格式，只出现在值的位置上，禁止集中成节；
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
	// 流式形态（阶段性输出）：task_assist_stream.go。
	if len(rest) == 1 && rest[0] == "stream" {
		s.handleTaskAssistStream(w, r, id)
		return
	}
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

	// 起草耗时超过服务器级 WriteTimeout（30s）——不在截止前延长写期限，
	// 超时/失败的 504/502 会在连接被掐后送不出去，浏览器只看到
	// ERR_FAILED。与 SSE 同一先例：本 handler 自管写期限。
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Now().Add(taskAssistTimeout + 15*time.Second))

	ctx, cancel := context.WithTimeout(r.Context(), taskAssistTimeout)
	defer cancel()
	task, err := client.Complete(ctx, taskAssistPrompt,
		[]llm.Message{{Role: "user", Content: "操作员草稿：\n" + draft}})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			writeError(w, 504, "起草超时（120s）——模型较慢时可重试，或手写任务书")
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
