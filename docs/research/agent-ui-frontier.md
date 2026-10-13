# Agent UI 前沿调研（2026-10）

状态：三视角 Agent 团队（产品扫描 / 设计趋势 / 实现技术）调研汇总，
作为「剧场」v2（docs/designs/theater.md）的外部对照与打磨输入。
结论先行：**剧场 v2 的方向与业界前沿高度同构，且在时间感与氛围层
两个维度上没有先例（差异化机会）；三组调研共提出 14 条可采纳的打
磨建议与 5 条必须守住的红线，汇总于 §4。**

---

## 1. 产品扫描：六维模式清单与收敛趋势

### 1.1 布局隐喻——全部以「对话流为脊柱」分化出三种

- **对话+侧舞台**：ChatGPT Canvas（聊天旁独立编辑窗）、Claude
  Artifacts / Cowork Live Artifacts（左聊天右实时渲染）、v0（chat
  侧栏 + code/preview 双 tab + generations 版本切换）；
- **对话+虚拟机直播**：ChatGPT agent（内嵌浏览器+叙述流）、Manus
  （chat + live computer view + 进度条）、Devin（Shell/Browser/
  Editor/Planner 四 tab 工作区）、智谱 AutoGLM（云设备远程画面）；
- **Mission Control 看板**：Cursor 3 Agents Window（多 agent 并行
  总览，local/cloud/SSH/手机）、GitHub Mission Control（按 PR 派
  发/转向/追踪）、扣子空间（左任务列表+右对话）。

来源：openai.com/index/introducing-canvas · support.claude.com
(Artifacts) · v0.app · openai.com/index/introducing-chatgpt-agent ·
arxiv.org/html/2509.14528v2 (Manus) · ppaolo.substack.com (Devin
分析) · autoglm.zhipuai.cn · digitalapplied.com (Cursor 3) ·
github.com/orgs/community/discussions/177791 · 36kr.com (扣子空间)

### 1.2 「正在干活」呈现——三层分化，累积读数是空白

a) 逐步叙述+思考摘录（ChatGPT agent；NN/g 实测肯定了搜索次数/来
   源数/截图这类具体读数）；b) 屏幕直播（Operator/Manus/AutoGLM）；
c) **计划树常驻**：Manus 不断重写 todo.md 把全局计划钉在 UI 上、
   Replit ordered task list+checkpoint、Deep Research 先出大纲确认
   再执行。**「23 min · 第 47 步」式累积读数业界反而弱**，只有
   Devin session 时间线接近。Genspark 反向走极端：「Less Control,
   More Tools」几乎不展示过程。

来源：nngroup.com/articles/impressions-chatgpt-agent ·
manus.im/blog/Context-Engineering-for-AI-Agents ·
docs.replit.com/features/agent/overview · gemini.google/overview/
deep-research · openai.com/index/genspark

### 1.3 审批与危险动作——三档光谱

- **事前计划批准**：Claude Code Plan Mode（计划=markdown 文件，批
  了才动文件）、Devin planner approve、Replit plan mode；
- **逐动作门禁**：Operator watch mode（敏感站点强制人在环盯屏）、
  ChatGPT agent 对登录/购买等 consequential action 先问再动+一键
  接管、Comet 对敏感动作暂停且**记住偏好减少重复询问**；
- **沙箱替代追问**：Claude Cowork 限定单文件夹沙箱降低逐条批准成
  本；Anthropic 数据：permission prompt 批准率约 97%，故推 auto
  mode 代批。

来源：lucumr.pocoo.org (Plan Mode) · openai.com/index/introducing-
operator · openai.com/index/introducing-chatgpt-agent ·
perplexity.ai/hub/blog/the-new-comet-assistant ·
simonwillison.net/2026/Jan/12/claude-cowork · anthropic.com/news/
enabling-claude-code-to-work-more-autonomously

### 1.4 主动打扰与通知——后台跑+完成推+多端取

ChatGPT scheduled tasks（独立任务面板 + Push/Email 通知 + 暂停/恢
复）、Devin 住 Slack + 手机端、Comet background assistants + 定时
任务、Cursor/Devin 手机 App 只看进度。**没有任何产品做「分级打
扰」——inform 级与 confront 级通知扁平**（我们的 intent 四级 +
触达纪律是超前设计）。

### 1.5 记忆/上下文可见性——设置页里的静态清单

ChatGPT memory 可逐条查看/编辑/删除（Simon Willison 批评为
"memory dossier"）、Manus 把 memory/knowledge 放进 Projects 工作
区。共同点：**记忆从不常驻主视图**——我们的画像蒸馏层常驻注入 +
召回台账是不同路线。

### 1.6 多任务并行——Mission Control 模式胜出

Cursor Agents Window、GitHub 按 PR 管理、Devin 并行云端 session、
Kimi OK Computer Swarm 集群、MiniMax Agent Desktop 定时任务。
回看类唯一代表：**Manus replayable sessions（逐帧回放它怎么干完
一单）**。

### 1.7 收敛趋势（5 条）

1. **对话流是唯一脊柱**：画布/浏览器/看板全是侧舞台；2026-09
   Anthropic 把 Cowork 合并回单一 Claude——独立工作区 tab 是过渡
   形态。→ 印证对话单真相源（§3）裁决正确。
2. **计划先于执行成为第一审批面**：Plan Mode 全线普及——批准一次
   计划，之后只门禁危险动作。→ mindloop 远期候选（任务收口契约
   已是雏形）。
3. **审批预算守恒**：沙箱给自由、危险动作设门禁、记住偏好降重复。
4. **异步化**：从「盯屏看它干活」转向「定时任务+推送+手机查看」，
   盯屏只留给敏感动作（watch mode 是盯屏的最后据点）。
5. **回放代替黑盒**：replay/session timeline/PR diff——事后可审计
   比事中可窥视更被认真对待。

## 2. 设计趋势与方法论

### 2.1 五条趋势

- **T1 生成式 UI**：共识收敛为「光谱论」——生成 = 从可信组件库中
  选择与组合（structured UI intent），而非运行时自由造 UI；
  InfoWorld 称 "The application still owns the truth"；设置/支付/
  审计等稳定流程保持确定性 UI。来源：vercel.com/blog/ai-sdk-3-
  generative-ui · copilotkit.ai/generative-ui · infoworld.com
  (a-better-approach-to-generative-ui) · puckeditor.com (AI slop)
- **T2 Ambient agents**：LangChain 定义文献——事件驱动、后台常
  驻，人机交互收敛为 **Notify / Question / Review** 三模式；因
  Slack 通知堆积的失败经验提出 **Agent Inbox**（邮箱+工单混合体，
  聚合全部未决线程）。calm technology 复兴：Amber Case 直指当前
  AI 产品「把注意力设计做反了」。来源：langchain.com/blog/
  introducing-ambient-agents · calmtech.com · workday.com (zero-UI)
- **T3 混合主动权与信任校准**：Horvitz CHI'99（打断成本/信息价值
  决定谁主动）、Lee & See 2004（overtrust→滥用，undertrust→弃
  用，设计目标是校准）；实践警报：审批疲劳让 HITL 变橡皮图章，
  比不审批更危险。来源：erichorvitz.com/chi99horvitz.pdf ·
  journals.sagepub.com (Trust in Automation) · arxiv.org/html/
  2608.23642v1
- **T4 状态可视化**：从「加载圈」到「可审计的工作时间线」；
  ShapeofAI 把「思考流」列为标准模式；Devin 计划树+时间线、
  Claude Code Agent View 看板——行业收敛为指挥中心混合形态。
  来源：shapeof.ai/patterns/stream-of-thought · assistant-ui.com ·
  devin.ai/agents101
- **T5 持久关系与时间感**：Claude 的记忆是可见可编辑的摘要
  （透明派更利校准），ChatGPT 静默后台抽取；Replika 侧研究：持久
  记忆创造情感连续性，但**人格漂移与记忆失效是最大翻车点**。
  **没有任何主流产品做出真正的「时间线回看」——开放设计空间。**
  来源：simonwillison.net/2025/Sep/12/claude-memory ·
  journals.sagepub.com/doi/10.1177/20438869261470446

### 2.2 方法论要点（对舞台隐喻最相关）

1. **打断必须过「价值闸门」**：Horvitz 打断成本/信息价值 + Gloria
   Mark 23 分钟专注恢复成本 → 通知应延迟投递而非即时洪峰；
2. **审批是稀缺资源**：Notify/Question/Review 三分 + 审批疲劳研
   究 → 高摩擦 Review 只留给高风险，审批 UI 必须展示「依据与后
   果」维持信任分辨率；
3. **边缘到中心的平滑移动**（calm tech）：环境信息低分辨率常驻外
   围，关键事件升入中心——五态宇宙不读文字可辨正是这条原则的图解；
4. **时间线透明 = 信任校准器**：让「它一直在干」可查证、可回放，
   而非宣称；
5. **生成不夺权**：应用拥有真相，模型只在白名单组件内选择。

## 3. 实现技术选型与性能红线

### 3.1 协议与事件设计

四家方案（AG-UI / Vercel AI SDK / assistant-ui / Thesys C1）共同
模式 = **快照 + 增量 + 括号对生命周期 + 声明式 kind→组件映射**。
mindloop 已有 scene 快照 + upsert/remove patch + rev，骨架与
AG-UI STATE_* 同构，方向正确。改进三条：

1. **batch 升为协议字段**：choreography 目前靠「同帧到达」的隐式
   假设。给 patch 帧加 `batchId` + `staggerHint`，规定服务端节流/
   合并规则——多卡同帧落位应可被协议断言，而非网络时序巧合；
2. **intent 生命周期括号化**：surface 增加退场原因
   （`exit_reason: ttl|superseded|done|dismissed`）与进场时机提示
   ——动效契约要可机械执行，协议必须携带「为什么变」而不只是
   「变成什么」；
3. **confront 对齐 interrupt/resume 语义 + 未知 kind fallback**：
   审批决定回流为结构化 resume 事件；未知 kind 渲染摘要 fallback。

来源：docs.ag-ui.com/concepts/events · ai-sdk.dev/docs/ai-sdk-ui/
generative-user-interfaces · assistant-ui.com · thesys.dev

### 3.2 技术选型表

| 项 | 推荐 | 代价 |
|---|---|---|
| 卡片动效/FLIP | **motion**（motion/react v12+） | layout prop 内建 FLIP 且可中断，MotionConfig reducedMotion、staggerChildren 直配动效契约；LazyMotion ~34kb |
| 命令面板 | **cmdk** | WAI-ARIA combobox，Vercel 同款；无虚拟化，2-3k 条上限够用 |
| 回看虚拟滚动 | **TanStack Virtual v3** | headless 双轴 useVirtualizer；动态高度需实测 |
| 宇宙参数 lerp | **手写**（或 pmndrs/maath easing.damp <2kb） | 1−exp(−λ·dt) 指数阻尼，帧率无关 |
| 不值得引库 | 全局快捷键 tauri-plugin-global-shortcut、通知 tauri-plugin-notification、SSE 手写（已有且正确）、心跳行/隔夜账卡纯 CSS+React、单卡 FLIP 手写 | 零依赖 |

View Transitions API（2025-10 进 Baseline）只用于「对话展开态」视
图级切换，不适合多卡 choreography（拍平嵌套层级）。
**结论：不引入 agent UI 框架**（AG-UI/assistant-ui/C1 均与「core
无像素控制」的投影式协议不匹配），动效引 motion 一个库就够。

### 3.3 性能红线（Tauri WebView2）

- 帧预算：宇宙层 60fps，draw calls <100、顶点 <100k；星空/轨道弧
  InstancedMesh/Points 单 draw call；动态光 ≤3；
- pixelRatio ≤2，弱 GPU 降 1.5；bloom 后处理可半分辨率渲染再放大；
- visibilitychange/Tauri 最小化停 rAF；卸载全链 dispose（geometry
  →纹理→material→renderer→render target），监控 renderer.info.memory
  （WebView2 长驻进程泄漏尤其致命）；
- `webglcontextlost` preventDefault + restored 重建（Tauri 双显卡
  切换会触发），最终降级 CSS 静态氛围层；
- DOM 动效只动 transform/opacity；batch 帧先读后写；reduced-motion
  全退化瞬时 opacity，监听 matchMedia change。

来源：motion.dev/docs/react-layout-animations · web.dev/blog/
same-document-view-transitions · github.com/pacocoursey/cmdk ·
tanstack.com/virtual · github.com/pmndrs/maath · rorydriscoll.com
(帧率无关阻尼) · v2.tauri.app/plugin/* · github.com/tauri-apps/
tauri/issues/6559 · utsubo.com/blog/threejs-best-practices-100-tips

值得逐行读：ag-ui-protocol/ag-ui · pmndrs/maath · dip/cmdk ·
TanStack/virtual · Utsubo three.js 性能清单。

## 4. 对剧场 v2 的净影响

### 4.1 被验证的设计（不需要改）

| 设计 | 外部印证 |
|---|---|
| 对话单真相源（删对话 tab） | 趋势 1：对话流唯一脊柱，独立工作区 tab 是过渡形态 |
| 分区制 + 3 张上限 + 折叠 chip | arXiv N=31 实证：均匀日志流淹没里程碑 |
| 五态宇宙参数化 | calm tech 边缘-中心原则的图解；业界全是 spinner/进度条 |
| 心跳行累积读数 | 行业空白（只有 Devin 时间线接近）；NN/g 肯定具体读数 |
| 隔夜账卡 | ≈ LangChain Agent Inbox 的桌面化，ChatGPT tasks 页是雏形 |
| 回看复用周报管道（决策点 5） | Replika 教训：叙事与账本不一致 = 人格漂移感 |
| 企微镜像默认关（决策点 3） | Amber Case「注意力做反了」是最大反模式 |
| Scene Protocol manifest 白名单 | T1「应用拥有真相」的正确落法 |

### 4.2 采纳的打磨建议（确认后并入 theater.md）

1. **等审批态加已等待时长微读数**——等待时长是「值不值得打断你」
   的决策输入（assistant-ui elapsed-time 模式）；
2. **ambient 过期第三条路**：专注期延迟合并进心跳行/账卡，而非
   丢 vs 洪峰二选一；
3. **审批预算控制**：confront 数量由 policy 预算约束——同类动作批
   量确认、低风险转 inform、**记住偏好**（Comet approve-and-remember：
   同类审批二次出现 prefill「按上次决定」），防遮罩独占变打断工厂；
4. **confront 按风险分级**而非仅按意图（Operator watch mode 启发：
   支付/删除/外发强制盯屏，沙箱内动作永不 confront）；
5. **动效变奏阈值**：同类事件第 N 次自动降为单行呈现，防 E1-E4
   退化为注意力税；
6. **审批卡附一句「依据」**，维持 Lee & See 信任分辨率；动词写清
   「谁做什么」（takeover 类语义歧义教训）；
7. **协议三改**（§3.1）：batchId/staggerHint 显式化、exit_reason
   生命周期括号、interrupt/resume 语义对齐——并入 Scene Protocol
   Phase 3；
8. **技术栈定案**（§3.2）：motion + cmdk + TanStack Virtual，其余
   手写/官方插件——并入分期 B/D 的实施说明。

### 4.3 必须守住的红线（新增强化）

1. **禁止装饰性假状态**：宇宙显示 working 而任务实际 stalled 时，
   信任崩塌比纯文本报错更快——五态参数永远从真实队列派生；
2. **禁情绪化文案**：不给五态起「难过/着急」类名字——可爱化诱发
   overtrust（盲信），「思考者」抽象雕塑路线保持；
3. **静默卡死禁令**：任何停顿必须有可见状态（心跳行停摆本身要是
   可读信号）；
4. **生成式 UI 不蔓延**：卡片禁止模型自由布局，分区制/reduced-
   motion 契约即失效点；
5. **隐喻疲劳监控**：动效变奏阈值（§4.2 第 5 条）上线后观察退出
   率，必要时加「动效强度」设置档。

### 4.4 差异化定位

全行业用 spinner/进度条/屏幕直播表达「活着」；分级打扰（intent
四级 + 触达纪律）、累积心跳读数、隔夜账卡、氛围级状态可视化、
真·时间线回看——五处皆无先例或仅有雏形。剧场的赌注（把 Agent
状态做成可远观可细查的舞台）站在 calm tech 与信任校准理论的正确
一边，且不与任何主流产品的路线撞车。

### 4.5 远期候选（不进 v2 分期）

- **Plan Mode**：任务执行前先批一次计划（业界全线普及，mindloop
  任务收口契约已是雏形）；
- **Manus 式 replay**：逐帧回放一次 run 的舞台重现（引擎室步级账
  本的可视化升级）；
- **记忆 dossier 常驻化**：画像/记忆从设置页升入舞台边缘读数。
