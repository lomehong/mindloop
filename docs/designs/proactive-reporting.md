# 设计：主动汇报（Proactive Reporting）

状态：v2 已按评审修订（2026-09-27）——TriggerSelf 修正为 false（零代码
守卫）、quiet 重写为有效触发时刻单一函数模型、every+task 类型模型与
幂等键钉死、coalesced 描述与机制对齐。待实施。
依赖：connectors.md（外发通道）；本文件可先行实施其本地部分
关联：`internal/schedule`（at+task/every+exec）、`internal/mind/dispatcher.go`
（WantWake/订阅面）、`internal/obs`（用量台账）、`internal/recap`

## 1. 目标与非目标

**目标**：把"主动性"从一个调度内核能力变成人的体感——心智在正确的
时机、把正确粒度的内容、送到人正在看的通道，且**绝不变成噪音**。

**非目标**：
- 不新增调度原语的运行时机制——WantWake、watchdog、at+task、every+exec
  已覆盖全部时机需求，本设计只做组合、词表扩展与时间判定函数化；
- 不做代码强制的自动汇报——汇报义务走教学（提示注入与技能纪律），
  代码只保证通道存在、收据可查（见 §6 的理由）。

## 2. 现状盘点：六成已建成

| 体感要素 | 现状 | 缺口 |
|---|---|---|
| 定时主动（每天晚报） | `at+task` 到点提交任务（幂等键 `sched-<id>-<date>`），daily-activity-report 技能已跑通"采样→报告→对话流汇报" | 出站通道（→ connectors.md）；周报/聚合源 |
| 周期检查（每 N 分钟看一眼） | `every+exec` 零模型成本跑命令，但**结果不触发心智** | `every+task` 组合（Parse 目前显式拒绝） |
| 异常告警 | exec 失败记 schedule.log + 状态投影，**无人被叫醒** | alert 步骤类型 + monolith 订阅 |
| 自主自查 | WantWake 预约式唤醒已可用 | 运行手册教学（长任务中途报进展） |
| 汇报到对话流 | `traj append message --from ada --to operator` | 地址路由规范（本地 vs 渠道） |
| 安静时段 | 无 | quiet 窗口（Tick/Recover 的时间判定需函数化，§3.3） |

结论：**缺的不是机制，是四个小件**——every+task、alert 类型、quiet
窗口、汇报路由规范——加一张把组合讲清楚的运行手册。

## 3. 触发源矩阵（时机）

| 触发源 | 机制 | 谁判断"值不值得报" | 成本 |
|---|---|---|---|
| ① 定时任务 | at+task（已有） | 模型（任务模板自带汇报要求） | 每次 1 思考档调用 |
| ② 周期巡检 | **every+task（新增组合）** | 模型：无事则空唤醒结束，零输出 | 高频低耗（REQUEST_MODEL 档） |
| ③ 异常告警 | **exec 失败钩子写 alert 步骤（新类型）** | 代码保证叫醒，模型判断升级与否 | 仅失败时 |
| ④ 自主自查 | WantWake（已有） | 模型 | 教学层面收编 |

### 3.1 every+task：周期巡检任务

放开 `schedule.Parse` 中"every 只能与 exec 组合"的限制（schedule.go:115），
允许 `every + task`：

```json
{"id": "ci-watch", "every": "30m", "task":
  "检查本地 CI 与仓库通知。无新事项则直接 FINAL，不汇报；有值得注意的变更，向 {{report_to}} 发 ≤3 句摘要。"}
```

**类型模型**：`Kind` 现为二值（`KindExec`/`KindTask`，schedule.go:58-63），
Tick 按 Kind 分发（schedule.go:326-337）。放开后校验矩阵从"两个对角
组合"变为"三格"：

| | exec | task |
|---|---|---|
| **every** | ✅ KindExec（既有） | ✅ **KindPatrol（新增）** |
| **at** | ❌ 维持拒绝 | ✅ KindTask（既有） |

`at+exec` 维持拒绝并给理由：周期跑命令不需要模型，是 exec 的本职；
"命令输出触发心智"的语义由巡检（every+task）承担——at+exec 没有不被
这两格覆盖的用例，多一格只多一种要测试的状态。提交路径上 KindTask
与 KindPatrol 完全同构（提交→幂等→心智领取），仅触发器不同。

**幂等键钉死为计划触发时刻导出**：v1 曾设想"当天第 N 个周期"（slot N）
——不可行，N 依赖内存态 `next`（schedule.go:349-353 首次注册即开始、
不持久化），重启/热加载后漂移，幂等键不稳定。统一改为：

```
DateKey(id, planned) = "sched-" + id + "-" + planned.Format("2006-01-02-1504")
```

- 键由**计划触发时刻**导出（at 条目的当天 at 点；Patrol 条目的触发
  网格点），与顺延、重启、热加载全部无关——与 at 的 DateKey 同源同构；
- `SubmitNow`（schedule.go:441-447）手动键同步：取 now 所在槽的计划
  网格点（at 条目取当天 at 点，与自动触发同键，当天手动+自动不双发）；
- **升级注记**：at 键从 `sched-<id>-<date>` 变为 `sched-<id>-<date>-<HHMM>`，
  升级当天的旧键去重失效可能双提交一次——影响一行日志量级，可接受，
  CHANGELOG 注明。

### 3.2 exec 失败 → alert 步骤（代码保证叫醒）

失败不能只落在 schedule.log 里等人发现。`recordExec` 失败分支追加
写 alert 步骤：

```go
traj.NewStep(traj.TypeAlert)  // "alert"，步骤类型词表新增
Fields: from=<self>, to=operator, source=system, kind=exec-failure,
        content="[<id>] 执行失败: <err> <detail 截断>"
```

**为什么 alert 必须是新类型而不是 message 加 message_kind**：
dispatcher 的背压二分法按 `step.Type == TypeMessage` 进人类 FIFO
（dispatcher.go:109-117）。alert 若伪装成 message 会挤占人类消息的保序
队列；而 coalesced 合并槽才是告警的正确背压。responder 的订阅面
`Types: []string{TypeMessage}`（responder.go:105-109）天然不收 alert，
零改动。

**订阅与防回路（v2 修正）**：monolith 订阅面加 `TypeAlert`（coalesced，
**`TriggerSelf` 保持 false，零代码守卫**）。机制上两向都对：

- schedule 写入的 alert 不在任何 runner 运行内、**无 launched_by 章**，
  dispatcher 的守卫 `if by, ok := step.Field("launched_by"); ok && by == 自名`
  （dispatcher.go:435-438）对 `ok=false` 不拦——**照常触发，告警叫醒达成**；
- monolith 自产步骤带 `launched_by=monolith`（monolith.go:202 + runner
  correlate 盖章），false 下**被既有守卫自动拦截**——防回路在代码层闭环。

v1 曾论证 TriggerSelf=true 并配"只允许产出 message"的教学约束——方向
反了：那是把 alert 的载荷字段 `from=<self>` 与作者章 `launched_by` 混为
一谈（守卫只看后者）。正确解不需要任何新守卫或纪律补丁。

**coalesced 的真实语义（v2 修正）**：coalesced 是 last-wins
（dispatcher.go:119-122，同名槽只留最后一条）——连续十条失败，模型
只见**第十条**，前九条详情只在 schedule.log。降噪收益依然成立（把
"忙/未及投递"窗口内的堆积压成一次叫醒），但不是"一次评估一批"；
串行慢速失败会各自叫醒。要全貌需写入侧聚合（alert 内容带"自上次
告警以来失败 N 次：条目列表"）——Phase 1 不做，先跑真实数据（§8）。

**写入通道（v2 新增）**：`schedule.Options` 没有轨迹写入位
（schedule.go:204-222），新增：

```go
// Alert 是 exec 失败告警的写出口（nil = 现状只落日志）。参数为纯
// 数据（schedule 包保持不 import traj）；装配层闭包持有 Timeline 与
// 身份名，负责构造 alert 步骤落盘。
Alert func(kind, entryID, content string)
```

装配点在 mindsetup 层与 `Submit` 同处接线，不随手 new Timeline。
prompt 渲染器新增 alert 段时遵守敏感区既有约定：读无锁、渲染即投影、
不写轨迹；quiet 的时间判定同样不得引入锁内长操作（§3.3 的评估函数
是纯函数，天然满足）。

### 3.3 quiet 窗口：有效触发时刻的单一函数（v2 重写）

v1 的"到点后过一遍归一化、幂等键用原定日期"三处与代码对不上：
`submitAt` 的键与渲染都用 `now`（schedule.go:412-413，没有"计划日期"
参数入口）；Tick 的 fired 判定只看"今天的 at 点"（schedule.go:330-331），
看不见被顺延到明天的时刻；Recover 只检查"今天 at 已过"（schedule.go:300），
跨午夜顺延的条目在重启窗口静默丢失。以最典型的 at=23:30 +
quiet=23:00-08:00 为例：v1 方案下该条目**永不触发**。

重写为**单一评估函数，Tick 与 Recover 共用**：

```go
// effectiveFires 返回 (lastSeen, now] 内已越过的有效触发时刻集合。
// 每个 fire 携带 plannedAt（产生它的计划时刻），幂等键由 plannedAt 导出
// （§3.1），与顺延无关。纯函数，无锁无 IO。
func effectiveFires(p Parsed, lastSeen, now time.Time) []Fire
```

- **有效触发时刻** = at 点落在 quiet 窗口内则顺延到窗口终点，否则
  原时刻。跨午夜是正常结果：9/27 23:30 + quiet 23:00-08:00 → 有效
  时刻 9/28 08:00，plannedAt=9/27 23:30，键 `sched-<id>-2026-09-27-2330`；
- **Tick**：`for fire := range effectiveFires(p, r.lastSeen, now) { submit(fire) }`
  ——内存 lastSeen 是既有字段（schedule.go:316/341）。不配 quiet 的
  条目退化为现有行为（当天 at 点越过即触发），向后兼容；
- **Recover**：共用同一函数，回看窗口 **48h**——覆盖"昨天 at 被顺延到
  今天早上"的最近一次可顺延窗口；更早的错过保持错过（与"一个月前的
  你在吗不需要回答"同向）。重启补提交仍靠幂等键去重，重复调用无害：
  9/28 08:05 重启 → 评估出 9/28 08:00 的 fire（plannedAt 9/27 23:30）
  → 键未提交 → 补提交。现状 Recover 的"今天 at 已过"判定整体替换；
- **幂等键用 plannedAt**（不是有效时刻）：顺延提交与当天直接触发共享
  一键；`submitAt` 需增加 plannedAt 参数（现状签名只有 now，schedule.go:407）；
- **替代形态不取**（fired 照常、提交挂起到窗口结束，需持久化挂起状态）：
  多一份要解释重启行为的持久状态，复杂度更高，评估函数形态让"什么
  时候触发"只有一个答案；
- **exec 条目不受 quiet 限制**——巡检与采集本来就安静，报告才安静。

## 4. 内容分级与降噪纪律

| 级别 | 内容 | 模型档 | 长度纪律 |
|---|---|---|---|
| L1 回执 | 任务完成一句话 | REQUEST 档 | ≤100 字 |
| L2 进展 | 长任务里程碑（WantWake 自查产出） | REQUEST 档 | ≤200 字 |
| L3 报告 | 晚报/周报（报告落盘 + 摘要） | 思考档 | 落盘全文 + ≤500 字摘要 |
| L4 告警 | exec 失败升级、健康异常 | 思考档（评估后） | ≤150 字 |

降噪纪律（全部是教学与配置，不是代码）：
1. **无事不报是一等公民**：every+task 模板首句永远是"无新事项则直接
   FINAL"；空唤醒走 REQUEST 档，成本有界（双模型分层保证）。
2. **报告先落盘后摘要**：全文在 `activity/reports/`（或推广的
   `reports/`），对话流只进摘要——仪表盘与渠道都干净。
3. **quiet 窗口管报告不管对话**：23 点后的巡检发现留给早上 8 点
   （§3.3 的顺延语义）；对话回复即时，channel 没有安静的义务。
4. **告警合并（v2 措辞修正）**：coalesced last-wins 把堆积告警压成
   一次叫醒，模型只见最新一条——需要全貌时由写入侧在 alert 内容里
   带"自上次告警以来失败 N 次：条目列表"（Phase 2 可选，§3.2）。

## 5. 通道路由（依赖 connectors.md）

路由即数据（connectors.md §3 不变式 1）：**汇报去哪，由写入方的
`to` 字段表达，无隐式规则**。

- 默认 `to=operator` → 对话流（仪表盘/CLI 可见）——现状已工作；
- `to=wecom:<uid>` → bridge 出站泵投递（connectors.md §5.2/§5.4）；
- 地址从哪来：**系统提示连接器披露段**（connectors.md Phase 3）列出
  本身份可投递地址；任务模板用 `{{report_to}}` 占位符——schedule
  渲染时替换为身份配置的默认外发地址（`REPORT_TO` 环境变量，缺省
  `operator`）。一行配置实现"晚报自动上企微"。
  渲染职责**已决**：放 schedule 的 Submit 渲染点（与 `{{date}}` 同处，
  schedule.go:171-173/412）——任务文本自包含，仪表盘所见即最终文本。

## 6. 任务完成汇报：编排层注入，不是代码强制

任务系统已有完整事实链（`Submission{From, ClientMessageID, SourceStepID}`
→ 状态机 → `Result` 落盘）。缺的是"完成后向发起方说一声"的义务传达。

**为什么不代码强制**（succeeded 状态自动写汇报 message）：自动写的
汇报没有模型总结（Result 是机械字段），且绕过了"这次要不要说人话"
的判断——日报和 CI 巡检的汇报语气粒度完全不同，模板化代码写不出。
**采用教学式**：编排层把任务交付给 runner 时，在任务文本尾部注入一行
（task 来源非交互时）：

```
（本任务由 schedule 触发；完成后向 {{report_to}} 发一条 ≤100 字的
完成回执，含结果要点与产物路径。）
```

注入点在 Submit 渲染（schedule.Options.Submit 装配处）与 monolith
领取任务的提示组装处——两处都只是拼字符串。第一版可以先不做注入，
靠任务模板手写汇报要求（daily-activity-report 技能已验证此路径
可行）；注入做的是"忘了写汇报要求的模板也默认有礼貌"。

## 7. 验收与分期

**Phase 1（本地闭环，不依赖连接器）**

前置改动（按 §3 各节的规格）：
- `KindPatrol` + 校验矩阵三格 + 计划时刻幂等键（含 `SubmitNow` 同步
  与升级注记，§3.1）；
- `traj.TypeAlert` 词表 + schedule.Options.Alert + monolith 订阅
  （TriggerSelf=false）+ prompt alert 渲染段（§3.2）；
- `effectiveFires` 纯函数 + Tick/Recover 共用 + 48h 回看 + submitAt
  的 plannedAt 参数（§3.3）。

验收：配置一条每 30m 的 CI 巡检 + 一条 21:00 晚报 + 一条必失败的
exec——巡检无事时零汇报、有事时报、晚报进对话流、失败被叫醒且
当晚晚报里提及；**追加两个修订专属用例**：at=23:30 + quiet=23:00-08:00
的条目在次日 08:00 恰好触发一次（幂等键为前日计划时刻）；触发的
alert 步骤不产生 responder 回复、monolith 重启后自产步骤不自我叫醒。

**Phase 2（外发，依赖 connectors Phase 1）**
- `{{report_to}}` / `REPORT_TO`、汇报注入行、系统提示披露段；
- 写入侧告警聚合（"自上次告警以来失败 N 次"，视真实数据决定）；
- 验收：晚报与告警出现在手机企业微信。

**Phase 3（报告族推广）——已实施（2026-09-27）**
- daily-activity-report 技能推广为报告技能族：周报（聚合 7 天
  activity + usage 台账）、健康周报（llm-health 快照 + 一周调用
  趋势）；落盘规范统一到 `<identity>/reports/<kind>/<date>.md`；
  技能源文件维护在 `docs/skills/<name>/SKILL.md`（安装副本在身份
  skills 目录）。schedule 新增 `days` 星期触发器（at+days 组成
  "每周一 HH:MM"；非触发日直接跳过，48h 回看同样尊重星期）。
  实测：周报/健康周报生成落盘并推送企微成功；执行策略教训——
  聚合类任务必须"一轮写完整脚本"（8 轮预算下逐轮试探会耗尽），
  窗口措辞必须钉死"最近 7 天含今天"（"上周"在周日有歧义）。

**Phase 4（长任务进展教学）**
- 运行手册（skills）：长任务开始时 `WantWake(25m)` 自查约定、
  超时汇报格式；配合 sandbox 已有三类超时与 watchdog 活性，无新代码。

## 8. 开放问题

1. ~~`{{report_to}}` 的渲染职责~~ **已决**：schedule 渲染（§5，评审
   同意：与 `{{date}}` 同处替换、任务文本自包含）。
2. alert 去重窗口：同意先跑真实数据——但描述已按 last-wins 修正
   （§3.2），实验要回答的实际问题是"串行慢速失败各自叫醒的频率
   是否可忍"，写入侧聚合是候选解而非现状。
3. 周报聚合源（recap 分集 vs usage 台账 vs activity 采样）的配比
   需要真实使用一轮再定权重，Phase 3 前置一次手工周报实验。
