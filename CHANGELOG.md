# 更新日志

本项目遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/) 的条目
组织方式；版本号采用语义化版本——0.x 阶段接口仍可能调整，1.0 起承诺兼容。

## [0.7.0]

### 新增

- **端到端流式响应**：对话回复在生成期间经 SSE 实时推送——仪表盘
  对话页逐字渲染 ada 正在写的回复，不再等整条回复落盘。
  端点 `GET /api/identities/{id}/replies/stream`（`text/event-stream`）：
  `status` → `delta`（累积全文，幂等整段替换）→ `done` 三类事件，
  15s `: ping` 保活；走既有同源守卫与 Bearer Token（前端 fetch-stream
  携带 Authorization 头，兼容非回环 Token 部署）。
- **LLM 流式 API**：`llm.Client.CompleteStream` 覆盖 openai-compatible /
  anthropic / echo 三 provider——SSE 解析（`data:` 行、`[DONE]` 哨兵、
  delta 累积）、UTF-8 多字节安全分片；首个增量之前保留既有线性退避
  重试，之后出错即返回（已发出的增量无法撤回）；GLM「HTTP 200 装
  错误」拆包识别与思考型预算分层在流式路径同样生效。
- **瞬态旁路文件与 GC**：回复生成期间写轨迹目录
  `stream/<reply_to>.txt`（累积全文），最终 message 步骤照旧一次性落
  轨迹后删除；旁路文件不进轨迹查询，调度器启动时自动清理崩溃残留；
  回复状态文件 `<RunLockDir>/replying` 同生命周期。
- **流式开关**：`MINDLOOP_STREAM=0` 整体关闭（回复回退为落盘后一次
  性可见）。流式仅作用于 responder 回复；monolith 自主行动与 `mind say`
  等 CLI 等待路径语义不变。
- **对话实时活动反馈**：同一 SSE 端点扩展 `working` 与 `step` 两类
  事件——`working`（思考者在途状态，来自运行锁目录 `working` 状态
  文件，busy 数组含 thinker/wake/since，文件存在 ⇔ 有思考者工作，
  调度器启动 GC 一并清理）；`step`（轨迹新增步骤实时尾随：
  step_id/type/ts/excerpt，excerpt 为 content 首行裁 120 rune，
  trajectory/run/prompt 簿记类型跳过，历史步骤不重放）。对话页在
  monolith 干活期间显示实时活动卡片（按步骤类型映射图标与中文标签，
  最近 6 条，working 归零即移除）。

## [0.6.0]

路线图 1–10 全部完成的首个完整版本。

### 新增

- **日志层**：追加式 JSONL 轨迹、属主检查目录锁（Windows 按 NTFS 语义设计，
  delete-pending 短重试）、fork/merge、字节偏移 cursor、`traj` 查询 CLI。
- **上下文渲染器**：块对齐截断网格（prompt cache 前缀稳定）、UTF-8 安全、
  字节预算二分、`mindloop prompt` 命令。
- **运行循环**：`runner.Thinker` 接口、fence 提取（heredoc 感知）、Job Object
  沙箱三类超时（KillOutput/KillIdle/KillTimeout）、FINAL 副作用协议、失速/
  同命令守卫、`mindloop run` 命令。
- **持久心智**：调度器（feeder/背压二分/watchdog 合成唤醒/自发性预约）、
  monolith/responder 思考者、运行锁、`mind say`/`mind chat` 人类入口、
  冷启动不重放（cursor 从 EOF 起步）。
- **记忆**：markdown + frontmatter 存储、BM25 检索（ASCII 词元 + CJK 二元组）、
  monolith 唤醒注入相关记忆、沙箱内经 `$MINDLOOP_EXE mem add` 自写。
- **recap 分层上下文**：确定性三条件切窗（间隔/步数/字节）、摘要缓存带
  provenance 章（model + prompt_version）、增量补齐零重算、尾窗成本阀、
  `mindloop recap` 命令。
- **观测面**：用量台账（usage/llm-usage.jsonl）、健康标记（llm-health.json）、
  `mind chat` 对话视图。
- **仪表盘**：`mindloop web` 15 个页面（身份/时间线/思考者控制/记忆/对话/
  recap/用量/配置等）；写端点方法路由 + 同源守卫 + 非回环绑定强制 Token。
- **技能与工具扩展面**：Agent Skills 开放标准（SKILL.md 解析校验、身份/全局
  两层技能库、系统提示渐进披露、GitHub 安装）；MCP 标准客户端（mcpServers
  配置互通、stdio + streamable HTTP 传输）与 `mcp serve` 反向接入。
- **模型接入**：provider 按模型名自动推断（claude-* → anthropic、echo 占位、
  其余 openai-compatible）、glm-* 智谱端点自动回退与思考型预算分层、
  双模型分层（`MINDLOOP_REQUEST_MODEL`）、瞬时错误线性退避重试、
  网关 HTTP 200 装错误的拆包识别。
- **配置**：`MINDLOOP_HOME/.env` 持久配置（显式环境变量优先）+ 身份级 `.env`
  三层覆盖。
