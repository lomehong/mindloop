# 设计：桌面宠物（pet）——有机体的小生物形态

状态：**v2.1（2026-10-05）**。沿革：v1 立项当日两度迭代（极简光点 →
圆润小生物，用户实机拍板）；壳侧补 capabilities 远程源授权与 --pet
启动参数；**v2.1 生命感打磨**（见 §1.1）——机制纪律不变：数据流/
菜单/拖动/穿透零改，全部改动限于表现层与互动质感。
关联：`perception.md`（六根框架）、`roadmap.md` §0.2（人格化决策的
边界澄清，见下）、`proactive-reporting.md`（内容分级）、
`../../desktop/README.md`（壳与宠物窗体）。

## 0.0 v2.1 生命感打磨清单（2026-10-05）

- **目光追随**：瞳孔朝光标方向微偏（±2px 限幅，rAF 节流 + 0.18s
  过渡）——CSS 变量 --pet-look-x/y 挂根元素，三层眼睛结构的最外层；
- **摸头**：按住本体 700ms 不动不拖 = 摸头——开心眯眼（闭眼弧线）
  + 三颗小心心漂浮 + 该次松手不弹菜单；与拖动（>6px 即转原生拖）
  互斥；
- **心跳**：呼吸之上叠加 4.2s 一次的低幅双搏（scale 1.02/1.015）；
- **随机小动作**：9s 周期的小跳跃；眨眼已周期化；
- **心情渐变**：体色/苗/瞳的 fill/stroke 0.6s 过渡（此前瞬跳）；
- **气泡尾巴**：语音气泡带指向本体的小尾巴 + 入场滑入；
- 实现全部在表现层（creature.tsx 结构 + app.css pet 段 +
  pet-app 两处交互 handler），机制零改。
关联：`perception.md`（六根框架）、`roadmap.md` §0.2（人格化决策的
边界澄清，见下）、`proactive-reporting.md`（内容分级）、
`../../desktop/README.md`（壳与宠物窗体）。

## 0. 定位与决策记录

**用户 2026-10-04 拍板：主动陪伴型 + 圆润 SVG 小生物**（初版极简
光点上机后不符预期，同日重做；心情/播报/菜单/拖动等机制全部保留，
只换视觉皮肤）。

- 宠物是**壳面新成员**——与托盘同族的"有机体状态可视化面/开发者
  接入面"，不是产品 rebranding。roadmap §0.2"人格化叙事、产品
  rebranding 类提案不再采纳"针对的是营销叙事与品牌改造；用户明确
  要求的桌面形态不在此列，本设计文档即为边界澄清的备查记录。
- **播报是纯显示层**：只消费已经落盘的事实（轨迹步骤、审批目录、
  预算投影），不新增任何预算/审批旁路。说话走既有 `POST /chat`，
  戳醒走既有 `POST /thinkers/{n}/step`（自发档预算门照常生效）。
- **「看见≠打扰」的落地 = 勿扰**：手动开关 + 夜段（默认
  22:00–08:00）双保险；勿扰期间播报不弹泡，只累积为光点上的琥珀
  微点，用户主动查看才展开。

## 1. 形态：圆润 SVG 小生物

果冻质感的圆润身体（脚底为轴的挤压拉伸呼吸）+ 大眼睛（周期眨眼、
高光）+ 微笑/平直/张合三态嘴 + 头顶一株小苗（摇曳=活着，垂头=病/
盹/滞/离线）+ 腮红 + 影子。心情表达：体色（.pet-mood-* 变量）+
眼神（working 眯细、alert 圆睁、hungry 耷拉）+ 口型（speaking 张
合）+ 苗的朝向；打盹冒 Zzz。轨道粒子 = 忙碌思考者（一颗一个），
说话 = 涟漪外扩 + 流式气泡。零美术资产（纯手写 SVG + CSS 帧动画），
跟随仪表盘亮暗主题。离线 = 灰体闭眼无嘴。

## 2. 心情：派生投影，不是新状态机

系统里没有 mood/emotion 状态机（全库零命中）——宠物也不发明一个。
心情是既有可观测事实的纯派生（`web/static/app/lib/pet-state.ts`
的 `deriveMood`，优先级从高到低）：

| 优先级 | 心情 | 判据（数据源） | 光点表现 |
|---|---|---|---|
| 1 | offline | web 后端不可达 | 灰、静止 |
| 2 | sick | 预算熔断冷却中（`/usage` admission.cooling_until） | 灰紫、慢闪 |
| 3 | hungry | 预算水位 ≥80%（admission.used_today/daily_limit） | 琥珀 |
| 4 | speaking | responder 在回复（SSE `status`） | 涟漪 + 流式气泡 |
| 5 | alert | 近 90s 内有 alert/error 步骤（SSE `step`） | 红脉冲 |
| 6 | working | SSE `working.busy` 或 `/activity`=working | 粒子加速、转青 |
| 7 | stalled | `/activity`=stalled | 暗琥珀、极慢 |
| 8 | dozing | `/activity`=asleep 或 >30min 无步骤 | 暗、极慢呼吸 |
| 9 | idle | 默认 | 主题蓝慢呼吸 |

微反应叠加（1.6s，不改变心情）：message→专注波纹、event/taste→
涟漪、screen→光环偏移、final→绽放、alert/error→颤动。
reasoning/action/shell-output 等高频工作步骤**不加戏**——working
心情已经在表达，逐帧抖动只会吵。

## 3. 数据面（后端 Go 零改动）

- **实时**：`GET /api/identities/{id}/replies/stream`（SSE）——
  `working`（忙闲+思考者）、`step`（一举一动）、`status`/`delta`/
  `done`（流式回复）。Last-Event-ID 断线续传，指数退避重连
  （3s 起步封顶 30s，常量在 `lib/polling.ts`）。
- **轮询兜底（缓变信号）**：`/activity` 30s（dozing/stalled）、
  `/usage` 5min（预算水位）、`/api/llm-health` 60s、
  `/approvals` 2min（审批待办）。查询静默失败——后端暂停时逐项
  退化为"无读数"，不让全局错误 toast 刷屏。

## 4. 主动陪伴：播报规则

边沿触发 + 会话内去重（台账超限丢非当日旧键）：

- **必播**：alert 步骤、error 步骤、任务完成（final 带 task_id）、
  新审批待办、模型熔断边沿；
- **每日一次**：预算跨 80% / 100%；
- **开场摘要**：连接后每个身份一次（身份 · 心情 · 今日 token
  水位 · 待审批数）；
- **节流**：队列串行，相邻播报 ≥5s，单条展示 ≤12s；
- **勿扰**：见 §0；未读上限 20 条，超出丢最旧。

S0/S1 沉淀类 event 步骤**不是播报素材**（只有摘要价值没有时效
价值）——播报面与 proactive-reporting.md 的内容分级同构，只是
搬运工不是裁判。

## 5. 宿主：一处渲染，两种形态

- **页面形态**：SPA 路由 `/pet`（浏览器直接打开即是）——与仪表盘
  同源同凭据流（`?token=` 吸收 / localStorage），无导航栏无留白
  （root.tsx petMode，/talk 先例），不注册 Service Worker（窗体要
  即时新鲜状态，不要离线缓存）。
- **窗体形态**：Tauri 壳的第二个 WebviewWindow
  （`desktop/src-tauri/src/pet.rs`）——260×300、透明、无边框、
  置顶、不进任务栏、不抢焦点、**无阴影**（Windows 上透明窗体默认
  边框阴影必须显式关掉）。后端未运行时托盘项禁用；`--pet` 启动
  参数可直接开窗（单实例转发，对已运行的壳也生效）。
  **capabilities 是命门**（`capabilities/default.json`，main+pet 联合
  能力）：页面远程加载
  自 127.0.0.1，Tauri 2 对远程源 IPC 默认全拒——漏了它，拖动/穿透/
  位置记忆/隐藏会整体静默失灵（v1 当日踩实，pet 页启动时显式探测
  IPC 并在菜单暴露 `data-ipc` 诊断位）。合并缘由：pet webview 存在时
  wry 的 IPC ACL 会错拿最后创建 webview 的标签判定（实测 main 的
  窗口命令被按 pet 上下文拒绝），联合能力使任一标签下都可放行。
- 窗体行为全在页面 JS：`data-tauri-drag-region` 拖动 + localStorage
  位置记忆；**点击穿透**用光标位置轮询做命中测试（穿透后 DOM 收不到
  鼠标事件，事件驱动会失联，150ms 轮询 `cursorPosition` 对照
  `[data-pet-solid]` 矩形）；托盘「宠物」是统一逃生门——每次显示
  前强制解除穿透。

## 6. 交互

- **拖光点/状态行本体 = 移动窗体**（JS 判定移动阈值后调原生
  `startDragging`，位置记忆；注意空白区**不可拖**——它被点击穿透
  放行了鼠标事件，这是穿透与拖拽区互斥的必然取舍，桌面宠物的惯例
  本来就是拖本体）；
- 单击光点 = 快捷菜单：说话输入框（`POST /chat`，from=operator）、
  戳醒（`stepThinker`，标注消耗自发档）、勿扰开关、身份选择
  （多身份时）、未读查看、打开仪表盘、隐藏；
- 双击光点 = 打开仪表盘；Escape 收菜单；
- 关窗 = 缩起（沿用壳的全局 CloseRequested=hide），托盘唤回。

## 7. 测试与验收

- 单元：`pet-state.test.ts`（23 例：心情优先级、反应映射、播报
  去重/队列/勿扰、构造器边界、clipText 的 CJK rune 语义）；
- E2E：`uitest/dashboard.mjs` 三场景（`petPage`/`petMenu`/
  `petChatSend`）= scenarios.md **S15**；petChatSend 是真模型
  场景，验证 POST /chat → responder → SSE → 说话态整条管线；
- 窗体形态：壳不在无头测试射程内，文档级验收 + 人工确认。

**已知观测边界（记录在案，非缺陷）**：服务端对 `replying` 旁路与
流式文本的观察是 200ms 轮询（`internal/web/watcher.go`）；单字快
回复的旁路文件窗口可能短于一个轮询周期，此时 status/delta 双双
漏采，光点的说话态/流式气泡不闪现（概率约等于窗口/周期比）。轨迹
步骤不受影响（必定落盘、必定广播）。E2E 场景据此设四路成功信号：
说话态 / 流式气泡 / 裸 status / 非回显的回复 message 步骤。仪表盘
对话页的同源流式打字机对超快回复同样存在此边界（它靠日志轮询兜
底）。根治需 replying 文件宽限期（Go 侧改动），暂记候选。

场景辅助信号：`pet-root` 暴露 `data-sse="on|off"`（裸 SSE 订阅
状态，区别于 mood 的 offline——后者在身份未选出时会被"身份列表
可读"顶替）。

## 8. 本期明确不做（远期候选，非遗漏）

多身份聚合视图（宠物同时表达所有身份）、语音、宠物驱动 robotd
动作的具象化（"身"之表：宠物伸手替心智点鼠标的动画）、Linux/macOS
宠物窗口（壳本身仅 Windows）、独立宠物设置页（设置全在菜单内）。
