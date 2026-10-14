# 剧场 v2 二期交接文档

状态：**二期 2.1-2.3 + 2.6 已落地（2026-10-14，接手会话完成）**；
2.4 引库三件按设计"按需启用"缓引（动效契约 CSS 近似可用、命令面板
与 Virtual 待真实需求）；2.5 企微镜像需渠道能力（WeCom 渠道 + 设置页
开关）落地后启用，在场芯片文案当前保持诚实。历史内容见下。
读者：继续推进的会话/操作员。
设计真相源：**docs/designs/theater.md（v2 定稿，状态行已更新）**；
前沿依据在 docs/research/agent-ui-frontier.md。

**已落地记录（2026-10-14）**：
- 2.1 协议三改：scene 步骤 batch_id/exit_reason 字段（uicmd
  --batch/--exit-reason、responder 按回复共享批号、审批钩子撤卡
  done）；web 折叠端批缓冲（150ms 静默定时器兜底）+ patch 帧统一
  batchId（无批号 rev 派生）+ staggerHint 40；scene.md §3.3 附注
  interrupt/resume 语义映射（不引框架）；
- 2.2 TTL 沉淀：ExpiredSceneIDs（作用域仅 ambient）+ SceneTTLSweeper
  （随心智进程，MINDLOOP_SCENE_TTL 缺省 1h）+ 前端 ttl→单行沉淀条；
- 2.3 E1/E3：scene.ts op 级事件（本地镜像取被撤卡内容）；E1 核心
  球象限脉冲 120ms；E3 error 边沿陶土碎粒抛物线 700ms；
- 2.6：记忆式落地（localStorage last section）+ NeedYouBar 全局
  行动条（signalsFor 升格 lib/need-you.ts，home 重导出保测试）；
- 基线红修复：ForegroundTitle 契约收紧（空标题前台一律 ok=false，
  三处调用方语义核实兼容）。
- 验证：Go 五包全绿、前端 233 测全绿、typecheck/build 过、S17
  sceneCards 回归过（stage testids 未变）。

## 0. 现状快照

| 事项 | 状态 |
|---|---|
| 设计定稿 | `284bb07`（决策裁决 + 调研增补八条 + 红线五条，theater.md §15-§17） |
| 一期落地 | `7641710`——分期 A/B/C/D + 审批增补，纯前端（web/static/app 13 文件），CI 3 job 全绿 |
| 原型 | docs/designs/mockups/theater-stage.html；预览：`python -m http.server 18790 --directory docs/designs/mockups` → http://127.0.0.1:18790/theater-stage.html |
| 并行加固线 | 另一会话已开始提交（`0847be6` 全项目审查修复），**接手时主分支有一个已知红**：go/windows 的 `internal/task` `TestProjectionCacheConcurrentReadersAndWriter` 失败（go/ubuntu 被 fail-fast 取消）——来源是加固线的 cache_test.go，**不是剧场线的锅**。开工前先看该测试是否已被加固会话修复（`git log --oneline origin/main` 是否有新提交 + CI 状态）；若仍是红，与剧场二期解耦处理：先修红保基线（修前 `git log -p internal/task/cache_test.go internal/task/projection.go` 看懂意图），再按 §2 顺序推进 |

## 1. 一期已落地（不要重做）

前端（全部在 web/static/app/，纯逻辑在 lib/theater.ts 配 18 项单测）：

- **lib/theater.ts**：五态判定 mindStateOf、分区 partitionCards、变奏
  variationCompactIds、隔夜合成 overnightDigestOf、记住偏好
  load/saveDecision、等待时长 fmtWaiting；
- **components/stage-hud.tsx**：隔夜账卡（>4h 合成/dismiss 当天不再）、
  工作心跳行（awaiting 带已等待时长）、在场芯片（本机诚实读数）；
- **components/scene-layer.tsx**：舞台分区制（confront 遮罩独占、右列
  ≤3 整卡 + 溢出 chip + 变奏单行卡）、ChoiceCard 依据行（按 hash 前缀
  匹配审批队列 risks）+「按上次决定」、E2/E4 特效信号派生；
- **components/talk-strip.tsx + chat-overlay.tsx**：对话单真相源——
  对话 tab 已删，头部「对话 ⤢」全屏浮层 + 舞台底部对话条，共享
  useChat 缓存；/chat 路由保留兜底；
- **components/scene-cosmos.tsx**：五态参数化（MIND_STATE_PARAMS +
  指数阻尼）、等审批自转停+树脂慢闪、E2 巡视/E4 共振环、年轮刻线
  （今日真实收口任务数）、在听辉光、闲置微翻身；
- **routes/review.tsx**：回看——近 7 天任务章节 + 叙事分集章节 +
  本周读数，深链 `?step=`；
- **routes/identity-shell.tsx**：舞台接管态卸载工作区、对话按钮；
- 验证基线：233 前端测全绿、typecheck/build 过、新文件 lint 零增。

## 2. 二期工作项（按开工顺序）

### 2.1 协议轨道三改（theater.md §16.7）——先做，其余两项依赖它

**前置**：加固线已合并、CI 绿。涉及 Go（internal/mind/scene.go、
internal/cli/mindsetup.go、internal/web 场景 SSE 处理），与加固文件集
相邻但不含其文件——开工前 diff 一遍确认无重叠。

1. **choreography 显式化**：场景 patch 帧加 `batchId` + `staggerHint`
   字段；同一 choreography 事务的多个 upsert/remove 共享 batchId。
   目的：多卡同帧落位可被协议断言，不靠网络时序巧合。前端
   lib/scene.ts 的 SceneFrame 加可选字段（未知字段忽略即向后兼容），
   scene-layer 按 batchId 单帧批量落位 + stagger 40ms；
2. **exit_reason 生命周期括号**：remove 携带
   `exit_reason: ttl|superseded|done|dismissed`。前端按 reason 选退场
   动效（蒸发/替换淡切/收工/手动收起）；
3. **interrupt/resume 对齐**：审批等待已由 choice 卡 + policy.Decide
   承载，补齐语义文档与字段命名对齐 AG-UI（读规范即可，不引框架）。

### 2.2 ambient 过期沉淀（theater.md §16.2）

依赖 2.1 的 exit_reason。服务端 TTL 到期的 remove 带 `ttl` → 前端
不撤卡而是降为单行摘要（scene-layer 已有 compact 渲染路径与
partitionCards「单行不占配额」逻辑，接上即可）；隔夜由账卡合并
（overnightDigestOf 已就绪）。

### 2.3 E1/E3 动效（theater.md §11 剩余两条）

- **E1 推卡前兆**：upsert 到达时，宇宙层核心球朝卡片象限单侧脉冲
  +0.1（120ms）。挂点：lib/scene.ts apply() 的 upsert 分支 → 事件
  回调 → scene-layer 转 fx prop（CosmosFx 加 `pulse?: {t0, quadrant}`）；
- **E3 错误碎粒**：mindState 转入 error 的瞬间（非持续），核心近轨
  弹出 3-5 粒陶土色碎粒又坠回（700ms 抛物线）。挂点：scene-cosmos
  帧循环检测 mindState 边沿。

### 2.4 引库三件（theater.md §16.8 定案，按需启用）

- **motion**（motion/react，LazyMotion ~34kb）：卡片动效契约
  （theater.md §12 四 intent 表 + stagger 40ms）当前为 CSS 近似，
  引库后按表实现 enter/morph/exit + layout FLIP；reduced-motion 用
  MotionConfig reducedMotion="user"；
- **cmdk**：命令面板是新组件（当前仪表盘没有）——全局 Ctrl+K、
  路由跳转/身份切换/全文搜索，WAI-ARIA combobox；注意 cmdk 无虚拟
  化，条目控制在 2-3k 内；
- **TanStack Virtual**：回看章节 >200 条时再引（当前 7 天窗口普通
  列表够用，review.tsx 已留「随滚动加载」的 elide 文案）。

### 2.5 企微镜像（theater.md §8，默认关）

后端渠道能力：presence=离开时，inform 及以上事件摘要推企微（审批
已有渠道，inform 走同管道）。挂点：internal/connector 的 wecom 渠道
（连接页已有基础设施）+ 设置页开关（默认 off）。**不要在前端假装
这个能力**——在场芯片的文案已写明「默认关，设置页可开」，设置页
开关落地前该文案保持诚实。

### 2.6 记忆式落地（theater.md §5，可选打磨）

落地页 = 上次停留视图：identity-shell 在 localStorage 记最后
section，进入 /i/:id 时无显式段则跳上次；首次恒轨迹（工作台语义由
home.tsx 承担）。加全局「需要你」行动条（待审批/预算≥80%/stalled
聚合，home.tsx 的 attention 区已有数据源可复用）。

## 3. 验收条款（theater.md §14 摘录）

| 项 | 验收 |
|---|---|
| A 时间感 | 隔夜打开 10 秒扫完昨夜；工作中累积读数可见 |
| B 舞台分区 | 8 卡压测不溢出；对话单入口唯一 |
| C 宇宙五态 | 五态不读文字可辨；reduced-motion 全降级 |
| D 回看 | 与任务板/分集/用量同源数据 |
| 审批增补 | 同类二次审批出现「按上次决定」；沙箱动作零浮层（风险分级后端侧） |

## 4. 工程纪律与已知的坑

- **环境**：Windows + Git Bash。Go 的 CRLF/gofmt 本地噪音——检查用
  git index：`git show :file | gofmt -l`；前端无此问题；
- **CI**：3 job（go ubuntu -race / go windows / viewer 前端 build）。
  viewer **只 build 不 test/lint**——前端验证必须本地跑：
  `cd web/static && npm run typecheck && npm test && npm run lint`；
- **CI flake 规律**（历史教训）：轮询等待预算 ≥10s，交付类 ≥30s；
  时间窗敏感的测试（TTL 类）窗口 ≥2s；连红两轮再怀疑代码，先查时序；
- **gh 未登录**：查 CI 用匿名 API
  `curl -s https://api.github.com/repos/lomehong/mindloop/commits/<sha>/check-runs`（60 次/时限速）；
- **react-compiler lint**（前端新增代码易踩）：渲染期禁 `Date.now()`
  ——用 query 的 `dataUpdatedAt` 或 state 时钟；effect 内禁直接
  setState——用「渲染期调整」模式（比较 prev prop 后 setState）；
- **样式**：Tailwind 4 + 语义 token（resin/clay 已在 app.css 定义），
  舞台新组件勿引入硬编码色值；
- **并发提交**：加固会话可能仍在推码——push 被拒（non-fast-forward）
  就 `git pull --rebase` 再推；**不要动 internal/ 下对方的未提交
  改动，也不要替对方提交**；
- **红线**（theater.md §17，实施时反复对照）：禁装饰性假状态（一切
  读数从真实队列/API 派生）、禁情绪化文案、禁静默卡死、生成式 UI
  不蔓延（卡片禁模型自由布局）、隐喻疲劳监控。

## 5. 交接自检清单

- [ ] `git pull --rebase` 后全量 `go test ./...` 与 `cd web/static && npm test` 基线绿
- [ ] 加固线已合并（internal/ 无未提交改动）→ 开工 2.1 协议三改
- [ ] 每步：本地三件套（typecheck/test/build）+ Go 侧 gofmt index 检查 → 提交（中文信息）→ push → 匿名 API 确认 CI 三 job 绿
- [ ] 2.1-2.3 完成后按 §3 验收条款逐条核验，更新 theater.md 状态行
