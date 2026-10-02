# 更新日志

本项目遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/) 的条目
组织方式；版本号采用语义化版本——0.x 阶段接口仍可能调整，1.0 起承诺兼容。

## [Unreleased]

### 新增

- **前端产物 go:embed 双构建模式**（roadmap §1.3 分发线地基）：
  `go build -tags release` 经 `web/static/viewer_release.go` 把
  build/client 全量嵌入二进制（产物缺失则编译失败——release 的
  硬门槛）；默认构建维持磁盘读取热刷新。运行时来源优先级：
  显式 `--viewer-dir`/`MINDLOOP_VIEWER_DIR` > 嵌入产物 > 磁盘自动
  探测（原链条保留为开发态回退）。web 包静态服务改造为 fs.FS
  统一源（`Config.ViewerFS`，磁盘目录优先于嵌入），`go install`/
  换机分发后仪表盘开箱即用；桌面壳 build.ps1 的 sidecar 构建
  加 `-tags release`。
- **脚本风险分级：ask 策略的只读自动放行档**（roadmap §1.2 信任
  补强第一项）：`internal/policy/risk.go` 只读白名单判定器——脚本
  逐命令全部可证明只读（ls/cat/grep/git 只读子命令/$MINDLOOP_EXE
  只读面等）时免审批直接执行，仍记 approvals/audit.jsonl（决策
  `auto-readonly`）。设计取向与展示用 riskPatterns 相反：**白名单
  而非黑名单**——漏判只读只是回到人工审批（fail-closed），未知
  构造（命令替换、变量赋值前缀、输出重定向除 /dev/null 豁免、
  find -exec 族、循环、解释器）一律照旧审批。开关
  `MINDLOOP_EXEC_POLICY_AUTO=0`（缺省开启，仅 ask 策略生效）。
  目的：防审批疲劳把缺省 ask 逼成 trusted——分级让 ask 可持续。
- **不可逆操作守卫（tripwire）：trusted 策略的最后防线**（roadmap
  §1.2 静态门）：`internal/policy/tripwire.go` 极窄灾难模式清单
  （管道给 shell、删根/家/当前目录、dd 写裸设备、mkfs、fork 炸弹、
  读 SSH 私钥、覆写 .env）——trusted 无人值守时命中即拒绝执行并记
  `tripwire` 审计（防注入内容诱导无人拦截的灾难命令）；ask 档命中
  只并进待批请求的风险提示（与 riskNotes 去重）。守卫刻意向
  "误伤合法命令的代价是把用户逼向关闭守卫"的反方向收窄：
  `rm -rf build/`、`cat .env` 等合法近似命令不在列。开关
  `MINDLOOP_EXEC_TRIPWIRE=0`（缺省开启）。

### 修复

- **Mimosa 存量清偿第一批**（台账 docs/security/mimosa-baseline.md）：
  测试凭据 fixture 从 `"sk-"+strings.Repeat(...)` 运行时拼装改为低熵
  假字面量（web 包三夹具与 llm 包 `testAPIKey`→`fakeProfileKey`，
  脱敏按"键名含敏感子串"判定、与值形态无关，断言语义不变）；
  `skills install` 的 `runGitStep` 参数位新增 `gitArgRe` 收口校验，
  把"字面量选项 + MkdirTemp 产物"契约钉成可证断言。

### 文档

- **演进路线图定稿**（docs/designs/roadmap.md）：六角色头脑风暴交叉
  收敛 + 三项战略拍板（连接器优先/RPA 后置、继续做框架定位、零依赖
  戒律分层放宽），并对照 0.7.0 实际落地内容校准（连接器 Phase 1、
  主动汇报 Phase 1–3、服务化 Phase 4、policy 审批门均为现状，路线只
  规划缺口：信任补强、分发、eval 基线、幂等索引、控制面收口等）。
- **依赖政策分层放宽**：认知域核心保持零/薄第三方依赖，协议适配层
  可引成熟库——同步写进 README「依赖策略」与 CONTRIBUTING「约定」。
- **Mimosa 存量 finding 清偿台账**（docs/security/mimosa-baseline.md）：
  人工基线（插件无基线机制，已实证）+ 清偿规矩（每 PR ≥1 个、新增
  零容忍）+ 已实证的合规写法速查；CONTRIBUTING 增「安全门禁」节。

## [0.7.0]

### 新增

- **连接器生态 Phase 1（设计 docs/designs/connectors.md v3）**：外部
  渠道桥——独立轨迹读写进程，把企微等渠道接入身份对话流，不改调度
  器/responder/任务系统任何一行。内核 `internal/connector`：出站泵
  （游标三态起步、rewound 弃批防护、to 字段纯日志谓词过滤、三档投
  递语义）、分段器（段落边界 + 硬切兜底）；传输层
  `internal/connector/ws`：最小 RFC 6455 客户端（纯标准库：握手
  Accept 校验、客户端掩码、ping 自动 pong、close 握手、分片重组带
  上限，拒绝扩展协商）；适配器 `internal/connector/wecom`：企微智能
  机器人 WS 长连接（订阅、断线指数退避重连、单连接互踢
  ErrKicked 退出、白名单 fail-closed、入站幂等键 `wecom:<msgid>`、
  出站 aibot_send_msg chatid 无状态映射）。CLI `mindloop connector
  run / run-all / list`，凭据走身份 .env（WECOM_BOT_ID/SECRET/ALLOW）。
- **主动汇报 Phase 1（设计 docs/designs/proactive-reporting.md v2）**：
  - `every+task` 周期巡检（KindPatrol）：定期叫醒心智检查一件事，
    无事不报走请求档；quiet 窗口内网格点蒸发（错过的检查不补）。
  - `alert` 步骤类型：exec 失败由代码写 alert 叫醒心智（schedule
    Options.Alert 回调，装配层落盘）——responder 不订阅（告警不
    引发寒暄），monolith 订阅面 TriggerSelf=false 双向天然正确
    （代码写入无 launched_by 章照常触发，自产步骤被作者章守卫拦截），
    告警背压走 coalesced 合并槽；alert 进 monolith 自主上下文。
  - quiet 窗口（"HH:MM-HH:MM"，允许跨午夜）：at 条目到点顺延到
    窗口终点；触发评估收敛为 effectiveFires 纯函数，Tick 与
    Recover（48h 回看补提交）共用同一答案。
- **沙箱凭据隔离钉子**：连接器凭据（WECOM/CONNECTOR 前缀）即使被
  extra 显式点名也不下传沙箱（childenv Sensitive 模式，测试钉死）。
- **企微协议按官方 SDK 校准**（参考 dsh-im-bot 线上实现）：帧形态
  补齐 `headers.req_id`（SDK `前缀_毫秒_随机`）；心跳改为应用层
  `{cmd:"ping"}` 帧、连续 2 次无回执判死重连（SDK maxMissedPong）；
  互踢走 `aibot_event_callback` 的 `event.eventtype=disconnected_event`
  （非独立 cmd）；订阅等待 errcode 回执、连续 5 次认证失败判凭证
  不可用终止（ErrAuthFailed）；出站每段等待 errcode 回执，失败交
  出站泵重试。
- **仪表盘渠道配置页**：connections 页的 bridge 占位槽升级为真实
  渠道配置——`GET/PUT /api/identities/{id}/channels(/wecom)`，表单
  编辑 BotID/Secret/白名单，写入身份 .env 原位合并（保留行序注释、
  0600）；secret 永不回显（只报已配置与否），留空提交 = 保持既有
  值；空白名单拒绝写入（fail-closed 红线）。空 allow 归一为 `[]`
  序列化（nil→null 曾让前端 `.length` 崩进错误边界）。
- **技能 Web 编辑**：仪表盘技能页从"只读列表"升级为可编辑——
  `GET /api/identities/{id}/skills/{name}` 返回 SKILL.md 全文
  （编辑器先读后写；全局层可读，editable=false 前端只读展示），
  `PUT` 全文替换（写盘顺序钉死校验→写盘：frontmatter 校验失败的
  半成品绝不落盘——技能层按目录整读，一个坏 SKILL.md 会被列表
  当坏条目跳过；落盘统一 LF）。只允许编辑身份层，全局层共享资产
  经 CLI 管理（与 DELETE 同一红线）。前端技能卡片加"编辑"（身份
  级）/“查看”（全局层），整页编辑器视图（长文不用弹窗），草稿
  未变时保存禁用，字符数与校验提示就地可见。
- **主动汇报 Phase 3（报告族）**：schedule 新增 `days` 星期触发器
  （逗号分隔 mon/tue/…/sun，与 at 组成"每周一 HH:MM"；非触发日
  直接跳过，48h 回看补提交同样尊重星期，展示态跳到下一个触发日）；
  报告族技能（源在 docs/skills/<name>/SKILL.md，安装于身份 skills
  目录）：weekly-activity-report（7 天活动采样 + usage 台账周合计，
  落盘 reports/weekly/）、weekly-health-report（llm-health 快照 +
  一周调用趋势，落盘 reports/health/）、daily-activity-report 迁移
  到 reports/daily/。实测：周报与健康周报生成落盘并推送企微成功。
  执行策略教训进技能：聚合类任务"一轮写完整脚本"（8 轮预算），
  窗口措辞钉死"最近 7 天含今天"（"上周"在周日有歧义）。
- **主动汇报 Phase 2（reporting 路由）**：
  - `{{report_to}}` 模板占位符渲染为 `REPORT_TO` 配置的默认外发
    地址（显式环境变量 > 身份 .env > 缺省 operator）；
  - at+task 完成回执注入：schedule 定时任务文本尾注入"完成后向
    <report_to> 发 ≤100 字回执"（教学式；巡检条目不注入——无事
    不报的纪律优先）；
  - monolith 系统提示连接器披露段：就绪渠道的可投递地址（如
    `wecom:HongYan`）进 "Delivery channels" 段，模型据此路由
    主动汇报；未配置渠道不出现该段。
- **企微流式回复与审批卡（对齐 dsh-im-bot 参考实现）**：
  - 流式回复：responder 的 stream/ 旁路文件由桥 tail，增量经
    `aibot_respond_msg`（透传原回调 req_id，content 全量替换）推送
    打字机效果；旁路消失 + 终稿落轨迹后发 finish=true。会话跟踪
    （入站 step_id ↔ req_id，StateDir 持久化）丢失即降级主动推送；
    已流式的回复由出站泵 Skip 谓词跳过（标记先行，崩溃宁丢终稿
    不重复；ack 失败撤销标记补发）。
  - 审批卡：policy 待批目录轮询 → button_interaction 卡（允许/
    拒绝按钮，短 token 映射 64 位脚本哈希）→ 点击回调（嵌套
    template_card_event 解析 + 点击者白名单校验）→ `policy.Decide`
    落决策 → `aibot_respond_update_msg` 卡片定稿。手机上完成审批
    闭环；MINDLOOP_EXEC_POLICY=approval 时生效。
- **桥配置热加载**：`connector run` 外置监督循环——每 5s 直读身份
  .env 的渠道键（绕开 LoadEnv 的进程环境不覆盖语义，文件是渠道
  配置真相源），白名单/凭据变更即取消当前桥并以新配置重建（经
  单连接互踢自然让位）；ErrKicked 让位退出、ErrAuthFailed 终止。
- **出站游标持久化修复（实测漏发隐患）**：游标目录从未被创建，
  `os.WriteFile` 不建父目录致每次落盘静默失败、重启即"从 EOF
  起步"——停机窗口的主动汇报全部漏发。修复：Outbound 启动建
  目录；Bridge.Run 返回前等出站泵收尾（落盘完成才交棒下一任，
  含内部 ctx 派生 cancel 防自锁）。实测重建后按 offset 续读。
- **服务化 Phase 4（设计 docs/designs/service.md v1）**：`mindloop
  service install|start|stop|restart|status|uninstall <身份名>`——
  把 mind/connector/web 装配成 Windows 任务计划程序任务（headlong
  deploy/*.service 每组件一 unit 的对应物），监督/重启/自启全部
  委托 OS 宿主，零第三方依赖：登录自启（延迟 15s 错峰）、崩溃
  RestartOnFailure 1 分钟退避 ×999（RestartSec=60 同款）、无时限、
  IgnoreNew 防双开；InteractiveToken 免管理员（XML 必须显式带
  Principal，实证钉死）。任务动作是 install 生成的自包含包装批处理
  `<MINDLOOP_HOME>/run/<组件>.cmd`（钉死 MINDLOOP_HOME——计划任务
  进程看不到安装 shell 的环境变量——并把日志追加重定向到
  `logs/<组件>.log`）；stop 是 headlong
  deliberate-stop 的移植（mind 优雅停机 + 全员 /disable——不被
  重启机制复活、下次登录不自启）；status 走 PowerShell
  Get-ScheduledTask 的英文枚举与数字结果，避开 schtasks 本地化
  输出，另附 mind 运行锁进程探测。install 预检 viewer 构建产物、
  渠道配置与 web 端口占用（只警告）。web 新增 `--no-open`
  （服务化模式不弹浏览器）且 Serve 错误改按命令失败退出（此前撞
  端口会落进"运行 --help 查看用法"分支）。真实 schtasks 注册经
  一次性身份全生命周期验证；已知的任务计划程序限制如实记录：
  RestartOnFailure 只对触发器拉起的实例生效，手动 `service start`
  拉起后崩溃需再手动拉起（登录自启主场景不受影响）。
- **traj 锁偷取结构加固**：stealLock 改为"在独占 scratch 里声明，
  rename CAS 回位原路径"——关掉 rename 走后至 Mkdir 落前的无主
  空窗里 mkdir-claim 与 steal 并发产生的双赢家窗口（2 核压测实证
  后修复）。**已知问题**：极端高压（16 挑战者 × GOMAXPROCS=2）下
  仍存在极低频双赢家残余窗口，待用目录文件身份（NTFS file id /
  POSIX inode）做 claim 终验后彻底关闭；
  `TestConcurrentStealSingleWinner` 随之默认跳过防 CI 假红，
  `MINDLOOP_STEAL_STRESS=1` 选通追查。

### 变更

- **日程幂等键格式升级**：`sched-<id>-<date>` →
  `sched-<id>-<date>-<HHMM>`（计划触发时刻导出，与顺延/重启/热加载
  无关；手动触发经 PlannedFor 与到点触发同键）。升级当天旧键去重
  失效可能双提交一次，量级一行日志。web 日程页 run 端点同步改用
  PlannedFor。
- `LoadCursor` 移除（缺失/损坏返回从头 cursor 是"重放历史给真人"
  级陷阱且无生产调用方）；新增 `LoadCursorAtEnd`（有效续读、缺失/
  损坏一律 EOF）与 `ReadNewWithRewind`（rewound 感知读取）——bridge
  的两条游标安全前提。

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
