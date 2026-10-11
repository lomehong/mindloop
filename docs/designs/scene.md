# 设计：Scene Protocol（Agent 驱动界面）

状态：v1 草案（2026-09-28）。参考 BaiLongma 的 SCENE-PROTOCOL.md 与
Agent-驱动UI-设计方案（docs/research/bailongma-benchmark.md §3.2），
转换到 mindloop 的轨迹事实源哲学。待评审后分期实施。
依赖：无新核心概念；复用 web streamHub（SSE 投影）与 policy gate
（choice 卡的第一个真实生产者）。
关联：`internal/traj/step.go`（步骤词表）、`internal/web/watcher.go`
（streamHub）、`internal/policy`（审批闭环）、`internal/mind/monolith.go`
（wakeTask 分段账本）、企微审批卡（wecom/approvals.go——confront 的
渠道特例）。

## 1. 目标与非目标

**目标**：让心智（模型）能以"声明状态"的方式驱动仪表盘的卡片面——
不碰像素、不写 HTML，只描述"此刻舞台上应该有什么"；界面是状态的纯
投影，用户交互以 intent 回流。第一个真实用例：**审批卡桌面化**
（现在审批只在企微有卡，仪表盘是列表条目）。

**非目标**：
- 不做电影级动画——v1 只做功能正确（React diff 的默认过渡），
  enter/morph 打磨是后续；
- 不做通用表单/媒体卡——kind 词汇表按需扩展（§4）；
- 不做"卡片聊天"——intent 上行 v1 只开 choice→审批一条闭环，
  通用 intent 只落轨迹作上下文，不叫醒心智；
- 不改企微审批卡——它是渠道特例，与桌面卡共存（同一待批队列，
  两个投影端）。

## 2. 核心判断：轨迹即场景

BaiLongma 的 scene 真相源在 core 进程内存（SceneStore），WebSocket
双向。mindloop 是多进程（mind 写、web 读、壳旁观）——真相源必须跨
进程可见。**决策：scene 状态落轨迹**，与"路由即数据"同一哲学的
再一次应用（→ 场景即数据）：

```
mind 进程                          web 进程               前端
ui set ──▶ SceneStore(内存,判重) ──▶ type=scene 步骤 ──▶ watcher tail ──▶ 折叠 ──▶ SSE 投影
              ▲                                                                                │
              └────────────── 重启时从轨迹 replay 重建 ◀──────────────────────────────────────┘
```

由此白送的四个性质（命令式架构拿不到的）：

1. **断线重连 = 从轨迹折叠全量**——rev 就是轨迹序号，resync 语义
   天然成立，状态漂移在数学上不可能；
2. **多端投影免费**——web / 手机 PWA / 桌面壳都是折叠器，同一份
   scene 步骤流；
3. **审计免费**——卡片生命周期全在轨迹上（谁在几点发了什么卡）；
4. **零进程内依赖**——mind 不认识 web，web 不持有 mind 对象。

**不变式（钉死）**：
- scene 步骤是唯一事实源；内存 Store 只是缓存（判重 + manifest
  派生），重启可从轨迹完整重建；
- 前端不存业务状态——`UI = f(scene)`，收到全量帧必须令屏幕完全
  等于该 scene；
- core（mind）搜不到任何 CSS/布局字眼；UI 里搜不到业务决策。

## 3. 步骤形态与协议

### 3.1 轨迹步骤（core → 投影端）

新步骤类型 `scene`（进 reservedTypes——结构性类型，不继承 run id）：

```jsonc
{"type":"scene","step_id":"…","ts":"…",
 "op":"upsert",            // upsert | remove
 "id":"weather-bj",        // surface 稳定身份（生命周期内不复用）
 "kind":"weather",         // 渲染端词汇表（§4）
 "intent":"inform",        // ambient | inform | confront（语义重要性）
 "focus":false,            // 同一时刻至多一张 confront/focus 卡
 "summary":"北京 18° 晴",   // 一行摘要——manifest 回注直接用（模型写）
 "data":{…}}               // kind 定义的纯数据（可 JSON 序列化，无样式）
```

remove 形态：`{"op":"remove","id":"…"}`（其余字段省略）。

**幂等**：mind 进程的 SceneStore 在 set 前比对——同 id 同 (kind,
data, intent, focus) 的重复 set 不落步骤（无变化不广播）。data 用
canonical JSON 序列化后比对。

**rev**：投影端的 scene 版本 = 轨迹序号（step 序）。折叠器天然
单调；SSE 的 Last-Event-ID 续接（streamHub 现成机制）承担"补帧"，
不在协议层再造 rev/base——**复用而非重造**。

### 3.2 SSE 投影（web → 前端）

`GET /api/identities/{name}/scene/stream`（streamHub 同款）：

- 订阅时先发一帧全量 `{"type":"scene","rev":<步骤序>,"surfaces":[…]}`（从
  折叠状态序列化）；
- 之后每条 scene 步骤转一帧增量 `{"type":"scene.patch","rev":N,"ops":[…]}`；
- 断线重连（Last-Event-ID 越界）→ 重发全量。语义与 BaiLongma §3.2
  等价，实现走 streamHub 既有缓冲环。

### 3.3 intent 上行（投影端 → core）

`POST /api/identities/{name}/scene/intent`：

```jsonc
{"surface":"approve-a1b2","name":"select","data":{"value":"approve"}}
```

落 `intent` 步骤（进 reservedTypes）。v1 消费规则：

- **choice 卡且 data 关联审批**（surface id 内嵌审批 hash 前缀）→
  `policy.Decide(hash, value=="approve")`——审批桌面化闭环（§6）；
- 其余 intent 只落轨迹（历史与上下文材料），**不叫醒心智、不触发
  任何执行**——生命周期信号（dismiss 等）同理，只作上下文。

## 4. kind 词汇表 v1

每个 kind：用途 + data schema + 上行 intent。渲染端不认识的 kind
降级为 `text` 占位（title=kind + summary），不得崩溃。

| kind | 用途 | data | 上行 |
|---|---|---|---|
| `text` | 信息卡（标题+正文） | `{title?, body, footnote?}` | dismiss |
| `progress` | 长任务进度（原地 morph） | `{label, value, max?, status?, note?, indeterminate?}` | — |
| `choice` | 决策/审批（confront 本职） | `{prompt, options:[{value,label,tone?}], context?}` | `select {value}` |
| `metric` | 单个关键数值 | `{label, value, unit?, trend?}` | — |

**明确禁止**：agent 注入 HTML/JS/CSS（inline-template/script 是把
core 绑死在浏览器上的脏耦合）；长尾内容未来靠排版原语（row/col/stack）
扩展词汇表，不让模型写代码。form/image/media 等按真实需求再立项。

## 5. 工具面与 manifest 回注

### 5.1 ui set（CLI 形态，$MINDLOOP_EXE 调用）

```
mindloop ui set <身份名> <id> --kind text --intent inform \
  --summary "北京 18° 晴" --data '{"title":"天气","body":"…"}'
mindloop ui set <身份名> weather-bj --remove
```

- 幂等 upsert：第一次是"显示"，再调是"更新"，`--remove` 即移除
  ——show/update/hide 合一（BaiLongma §三）；
- **密度上限 8 张**：超限返回错误（stderr 可见）——模型经 manifest
  感知画面密度自我克制，core 硬上限兜底；
- data 上限 16KB、summary 上限 80 字节（写盘与回注双保护）；
- 落轨迹前经 SceneStore 判重（§3.1）；`ui list` 投影当前场景（调试）。

### 5.2 manifest 回注（wakeTask 新段）

> 注入"舞台上现在有什么"，绝不注入"它们长什么样"。

```
当前界面卡片（id 供更新/撤卡引用；用户看得到这些，说话要一致）：
- weather-bj (text) 「北京 18° 晴」 ambient
- approve-a1b2 (choice) 「允许执行 rm -rf /tmp/x？」 confront ←焦点
```

从 SceneStore 派生（id + kind + summary + intent），预算
`MINDLOOP_SCENE_BYTES`（默认 2KB）。**渲染排除**：scene 步骤原文
不进 AutonomousSteps 渲染（data 全文回放是浪费）——scene 进
reservedTypes 后由渲染层排除，与 manifest 段互为表里。

它解决三件模型没法自己推断的事：按 id 引用更新、主动清场、画面
密度感。intent 上行回答"用户刚做了什么"（触发器），manifest 回答
"现在有什么"（背景）——两者都进上下文，语义不同。

## 6. 审批卡桌面化（choice 的第一个真实闭环）

现状：approval 模式下 gate 阻塞等待批准，企微桥轮询 ListPending
发企微卡；仪表盘只有列表条目（home 轮询 /approvals）。

Scene 化：**gate 装配层的 WaitHook 在阻塞前发 choice 卡**——

```
gate.Authorize 待批 ──▶ WaitHook ──▶ ui set approve-<hash8> (choice, confront, focus)
     ▲                                        │
     │ 轮询决策文件（现有机制）                  ▼
     └────────── policy.Decide ◀── intent select ◀── 用户点击
```

- surface id：`approve-<hash 前 8 位>`——intent 上行按前缀换回
  完整 hash（复用 policy.ResolveHash 的前缀语义）；
- 与企微卡共存：同一待批队列，先点的生效，后到的点击落决策被
  gate"决定晚到不残留"语义安全忽略（与企微桥同一交互）；
- 卡片终态：决策落盘后由 **web 投影端**把 confront 卡 morph 为
  "已允许/已拒绝（点击无副作用）"？——不。**决策事实只落轨迹
  （decision 文件消费即删 + taste/日志），卡片撤销由 WaitHook 返回
  后的 remove 或 10 分钟 TTL 清扫**。v1 从简：Decision 落盘即
  remove 卡片（WaitHook 收到放行/拒绝后 remove）。
- 企微桥不改——它有独立的 pending 轮询与白名单。

## 7. 渲染端（前端）

- `SurfaceView` 组件注册表：kind → React 组件（首批 4 个）；
- SSE 订阅 /scene/stream：全量帧 → 替换整个 surfaces 数组；patch 帧
  → upsert/remove；断线 → 重连拿全量（不本地猜测）；
- 呈现：卡片面挂在身份页（workspace 侧栏或浮层，实施时定）；
  intent=confront 的卡居中聚焦，ambient 卡角落弱化——CSS 是渲染端
  的艺术决定，core 不知道；
- 未知 kind → text 占位降级。

## 8. 安全与预算

- ui set 是模型动作但**不过 policy gate**：卡片是呈现不是执行；
  副作用只发生在 choice 的 intent 回流 → policy.Decide（那里已有
  白名单/决策文件语义）；
- data 16KB / summary 80 字节 / 8 张上限 / scene 步骤含 data 全文
  的轨迹膨胀与 message 同量级，recap 不收 scene 步骤（粗层不需要
  卡片史）；
- intent 端点鉴权同 /api/*（现有 token/同源守卫）；
- 前端数据不可信边界：data 是纯展示数据，组件不 eval、不注入
  DOM（React 默认转义即达标）。

## 9. 分期与验收

**Phase 1（协议骨架 + 只读卡，全链路最小）**
- traj：TypeScene + TypeIntent 词表（reservedTypes）；
- mind：SceneStore（replay 重建 + 判重 + 密度上限）+ `ui` CLI
  （set/remove/list）+ wakeTask manifest 段；
- prompt：渲染排除 scene 步骤；
- web：/scene/stream（streamHub 折叠投影）；
- 前端：SurfaceView（text/progress）+ SSE 订阅 + 降级占位。
- 验收：模型 `ui set` 天气卡 → 仪表盘出现；同 data 重 set 不落
  步骤；改 data 原地更新；remove 消失；web 重启重连拿全量；
  manifest 段出现在唤醒提示；scene 原文不进渲染。

**Phase 2（choice + 审批桌面化，confront 闭环）**
- web：/scene/intent 端点（choice→policy.Decide）；
- 装配：gate WaitHook 发审批卡 + 决策后 remove；
- 前端：choice 卡（confront 聚焦呈现）。
- 验收：MINDLOOP_EXEC_POLICY=approval 下脚本待批 → 仪表盘 confront
  卡 → 点击允许 → 脚本继续执行；拒绝 → gate 拒绝路径；企微卡
  不受影响（先点先生效）。

**Phase 3（词汇表扩展与打磨，按需立项）**
- metric/image kind；ambient 卡的呼吸感与 morph 动画；手机 PWA
  投影端（同一 scene 流的第二个折叠器）。

## 10. 开放问题

1. 卡片归属：scene 步骤落身份根轨迹（全局场景）还是独立 scene
   轨迹？根轨迹简单（watcher 现成），但对话流 readers 会扫到
   scene 步骤（已由渲染排除缓解）。倾向根轨迹。
2. 多身份：scene 按身份隔离（watcher 已 per-identity）——无跨
   身份场景需求，v1 不做。
3. choice 卡的 context 字段要不要带脚本全文（confront 决策需要
   看清内容）？倾向带（审批正当知情），受 data 16KB 上限约束。
4. 壳（desktop）要不要独立投影端（tauri 窗口原生渲染）？——web
  视图已嵌壳，v1 不重复。
