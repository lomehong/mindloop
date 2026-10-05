# 设计：演进路线图（2026-10 头脑风暴收敛）

状态：v3（2026-10-02 六角色头脑风暴收敛 v2；同日感知系统立项追加
v3——用户拍板"眼睛里有事"愿景，见 §0.4 与 `perception.md`）。
关联：`connectors.md`、`proactive-reporting.md`、`perception.md`、
`service.md`、`../security/mimosa-baseline.md`（存量 finding 清偿
台账）。

## 0. 战略决策（2026-10-02 用户拍板）

1. **连接器优先，RPA 后置**：先深化 connectors（本文中期主线）；
   robotgo/robotd 桌面自动化现阶段不是必须的，仅作后续可选方向
   （技术方案已存档：独立 robotd MCP server、cgo 不进主二进制）。
2. **继续做框架**：mindloop 是持久化 AI Agent 框架，用户是开发者，
   CLI/库/仪表盘/桌面壳都是框架的开发者接入面。人格化叙事、产品
   rebranding 类提案不再采纳。
3. **零依赖戒律放宽**：不再追求"核心 internal 库零第三方依赖"。
   分层执行——认知域核心（traj/mind/prompt/recap/mem 等纯逻辑包）
   保持零/薄依赖求稳定；协议适配层（connector、provider、传输、
   检索等 I/O 边界）可自由引入成熟第三方库，不再为戒律手写协议
   实现。已写进 README「依赖策略」与 CONTRIBUTING「约定」。
4. **感知系统立项（同日追加）**：给心智装"五感"（眼耳鼻舌身）——
   世界本身成为输入流，感知闭环到动作。愿景原话"眼睛里有事"；
   同日质询"是不是应该是五感"后定稿六根框架——第六根"意"即
   思考者，mindloop 已拥有，本项补全其余五根（手是效应器不是
   感官，归入身/触觉）。总设计见 `perception.md`（已过六角色
   Agent 评审团，v1.2）。本项对决策 1 的实质修订是**解除 RPA
   后置禁令**：身（robotd）排入感知 Phase 5，启动准入 = Phase
   1-3 信噪比数据成熟——不是提前做 RPA，是为它备齐感官前提。

排序逻辑：**先敢用（信任与分发），再有用（连接器深化），最后
用得上（智能与扩张）。**

## 1. 近期——地基

### 1.1 Mimosa 门禁治理

- 存量 16 个高危 finding 建立清偿台账（`../security/mimosa-baseline.md`），
  规矩：**每 PR 至少清偿 1 个，目标一个季度归零**；归零前提交走
  用户终端 warn 模式（已裁定）。
- 插件无基线机制（policy 白名单与 `mimosa-ignore` 注释对门禁均无效，
  已实证），台账即人工基线；新增 finding 零容忍——出现即修，不入台账。

### 1.2 信任补强：从"整脚本审批"到"分级审批 + 可撤销"

**现状（0.7.0 已有）**：`internal/policy` ask（缺省）/trusted/deny
三档执行授权，`runner.BeforeExecute` 统一控制点；审批走文件控制面 +
`mindloop approve` CLI + 企微审批卡（手机上允许/拒绝闭环）。

真实缺口不再是"有没有刹车"，而是"刹车会不会被摘掉"：

- **风险分级（✅ 2026-10-02 已落地第一档）**：`internal/policy/risk.go`
  只读白名单判定器 + ask 门自动放行档（`MINDLOOP_EXEC_POLICY_AUTO`
  开关，豁免记 `auto-readonly` 审计）。落地形态是二值判定
  （可证明只读 → 免审批；其余 → 照旧审批），比路线草稿的四级更
  保守——判定走"逐命令白名单"而非"危险词黑名单"，漏判只读只是
  回到审批（fail-closed），黑名单漏一条就是事故。后续可选：把
  审批卡的风险提示从 RiskNotes 启发式升级为白名单分类的写/网络/
  删除归类。**分级让 ask 模式可持续，是信任体系里杠杆最大的一件事。**
- **undo/快照**：写操作前快照目标文件 + `mind undo <run_id>`。
  "可信 = 出事可挽回，不是不出事"。
- **undo/快照（✅ 2026-10-02 已落地）**：`internal/snapshot` 运行级
  差分快照 + `mindloop undo`（含 --identity）。已知边界如实声明：
  只覆盖工作目录之内。
- **预算控制台（✅ 2026-10-02 已落地）**：分级预算——总预算
  `MINDLOOP_DAILY_TOKENS` 全员受限（既有），新增自发档
  `MINDLOOP_SPONTANEOUS_TOKENS`（watchdog/定时唤醒先停，对话/任务
  保到最后）；判定用调用 ctx 的溯源章，执行在守卫（llm.Client.Gate）
  ——请求发出前的准入拒绝，不靠 prompt 约束。stats/doctor 可见。
- **静态门（✅ 2026-10-02 已落地）**：`internal/policy/tripwire.go`
  不可逆操作守卫——极窄灾难模式（管道给 shell、删根、dd 写盘、
  mkfs、读 SSH 私钥、覆写 .env），trusted 无人值守档命中即拒绝并
  记审计，ask 档并进待批风险提示；`MINDLOOP_EXEC_TRIPWIRE=0` 可关。
  覆盖"注入内容 → 无人拦截的灾难命令"链的最后一环。
- **预算控制台**：每日花费上限、超限自动降档/停自发档；在
  `$MINDLOOP_EXE` 代理层强制执行，不靠 prompt 约束。
- 能力隔离（沙箱 TrustLevel 分级：低权本地用户 → AppContainer →
  WSL2）留远期；`sandbox.Request` 已是唯一执行入口，届时加字段即可。

### 1.3 分发与首里体验（框架的入口）

- **go:embed 双模式（✅ 2026-10-02 已落地）**：release 构建用
  build tag 走 embed.FS（`web/static/viewer_release.go`，产物缺失
  则编译失败），开发态维持磁盘读热刷新；来源优先级 显式
  flag/env > 嵌入 > 自动探测。
- **`mindloop init`（✅ 2026-10-02 已落地）**：交互向导 +
  `--demo`（echo 零 key）+ 非交互 flags + .env 原位合并 + 连通
  探测 + 建/复用身份。
- **`mindloop doctor`（✅ 2026-10-02 已落地）**：七项体检 ✓✗ 表、
  退出码可脚本化。
- **eval 基线（✅ 2026-10-02 已落地）**：`mindloop stats` +
  `internal/obs/stats.go`——分账/空转唤醒率/任务完成率，全部从
  既有台账与任务系统派生；空转判定如实标注台账级代理，待计划
  步骤（§2.4）落地后升级为"有效 FINAL"精确锚点。剩余：发布渠道
  （GoReleaser/Tauri 安装包/winget/scoop，待用户确认发布意愿后
  启动）。
- **发布渠道**：GoReleaser 管 Go 产物；Tauri bundler 出安装包；
  winget manifest（Windows 一等公民先占 winget）+ scoop bucket；
  自动更新用 Tauri updater 不自研。
- **`mindloop init`**：交互式向导（选模型、写 key、生成 .env、建
  演示身份）+ echo 零 key 演示。北极星指标：新克隆到 ada 回复
  ≤ 2 分钟。
- **`mindloop doctor`**：模型连通性/前端产物命中级/技能索引/MCP
  可达性一键体检，✓✗ 表输出；`bugreport` 打包脱敏日志。

### 1.4 评估基线（智能层优化的前提）

从既有 JSONL 派生三个指标进 obs：**空转率**（watchdog 合成唤醒中
产出有效 FINAL 的比例）、**任务完成率**、**token 效率**（按模型档位
分账）。符合"视图皆派生"；先有基线再谈优化。

## 2. 中期——连接器深化

**现状（0.7.0 已有）**：企微智能机器人 WS 长连接桥（出站泵三态游标
起步、rewound 防护、白名单 fail-closed、入站幂等键）、流式回复
（打字机）、审批卡、桥配置热加载、渠道配置页、凭据不下传沙箱；
主动汇报 Phase 1–3（巡检/alert/quiet 窗口/reporting 路由/报告族
周报，已实测推送企微成功）；服务化 Phase 4（schtasks 全套生命周期）。

### 2.1 性能前置：幂等查重换派生索引

`PostMessageOnce` 全文件 O(n) 扫描随轨迹增长单调恶化——bridge 在
0.7.0 已常驻，入站已是多写者热路径，这是现实恶化不是预期风险。
改为 `client_message_id→offset` 持久化派生索引（与 index.go 同
哲学：持久化字节偏移、收缩即重建），全扫描降级为损坏回退。

### 2.2 控制面文件协议收口

runlock、stream/ 旁路、working/replying 状态文件、cursor、policy
待批目录已是 mind/web/connector/service 四类消费者共用的隐性公共
API，无版本化无收口。收口到单一包并盖 schema 版本章，再新增
进程形态。

### 2.3 扩渠道

按 connectors.md v3 适配层清单扩钉钉/飞书/微信客服——内核（游标
三态、rewound、投递语义分档）不动，只写传输与适配器。依赖政策
放宽后，WS 类协议可直接引成熟库，不再手写 RFC 实现（现有
internal/connector/ws 保留为认知锚点）。

**状态（2026-10-05 裁决）**：均未实施，本轮不做、仅文档标注——
现有唯一渠道仍是 Phase 1 企微智能机器人。启动条件：真实需求 +
凭据齐备时按 connectors.md §7 规格落地（表内 ✅/⏸ 是设计期结论，
不是交付状态）。

### 2.4 智能层并行小步走（不占主线资源）

- **计划即轨迹（口径收敛 2026-10-05）**：跨天任务的执行事实由任务
  系统承担（任务步骤 + 状态投影 + `task` CLI）；本轮起，任务队列的
  状态投影（未结/最近结束，只含状态/id/时间）注入自主唤醒上下文
  ——"计划"可见且可追踪，解决跨天任务目标漂移无从检测。独立的
  `plan set/progress` 任务树接口不新增：自主上下文不得携带任务内容
  （未授权要求不回流为执行线索），内容只在领取后的执行上下文出现。
- **记忆代谢**：BM25 保底 + 向量重排混合检索（依赖放宽后可直接引库）；
  frontmatter 加 `confidence/last_accessed/hit_count`，命中回写统计；
  空唤醒定期跑"睡眠整理"（相似合并/矛盾裁决/降权），过程落轨迹可审计。
  与安全侧同批机制：记忆条目补 provenance 章，外部内容衍生的记忆打
  "未核实"标签、注入时降权，支持 diff 审查与时间点回滚（防投毒）。
- **故障卡**：llm-health、三类沙箱超时、退出码翻译成人话
  （"模型端 429，第 3 次重试，约 40s 恢复"），对话页内联 + 托盘
  三色。通知分级（L1 静默入托盘角标、L4 才弹系统通知）在此一并做。

### 2.5 感知系统 Phase 1–2：眼与耳（2026-10-02 立项，同夜实施完成）

总设计见 `perception.md`（已过六角色评审团 v1.2）。**2026-10-02
夜间一次性连续实施完成**：神经系统（event 步骤/判定层/SensorRunner
/订阅面 s2+ 分级路由/唤醒归因短词表——顺带修复自发档预算与空转率
统计在生产归因下从未生效的潜伏 bug）+ 眼（fs/git/web 感官 + doctor
第八项 + sensors CLI）+ 耳（/hook HMAC 端点 + wecom 桥降档）+ 鼻
（sensor/self 内感受 + 缺席检测）+ 舌（taste 步骤 + undo --because
归因 + 派生投影 + 漏报指标）+ 注意力四闸（学习期/quiet/令牌桶/
预算归因）。身的 robotd 按设计准入条件（Phase 1-3 信噪比数据成熟）
另行启动——不是提前做 RPA，是为它备齐感官前提。感官是连接器深化
的自然延伸——感知连接器就是 connector 家族的新成员。

## 3. 远期——扩张（按需启动，无时间表）

- **跨身份协作**：`to=<peer>` 寻址已就绪，补对端 cursor 监听
  （谓词 `to=me && launched_by!=me` 命中即投递，与出站泵同构，
  零新概念）；共享记忆走显式发布 + 作者章，不做隐式共享。
- **critic 第三思考者**：低频复盘最近轨迹（哪步错/哪浪费/该沉淀
  什么记忆与技能），结论经 mem/plan 回流；复用 Thinker 接口与
  WantWake 预约。配套 `skills draft` 自我技能沉淀（重复成功序列
  生成 SKILL.md 草稿，验证转正）+ 失败反思（FINAL 缺失/超时归因）。
- **沙箱能力隔离分级**：见 §1.2，TrustLevel 三档。
- **服务化补强**：Phase 4 已落地，剩 web 单例化（一份列全部身份，
  现状每身份一端口）、身份目录 schema_version 迁移器、多设备迁移
  语义（轨迹整体迁移 + cursor EOF 重启）。
- **轨迹哈希链审计**：JSONL 每条加 prev-hash、日锚定外部（改动极小，
  可签核溯源）。可提前到中期做。
- **RPA/robotd（→ 感知系统 Phase 5 身）**：2026-10-02 感知立项后
  不再"决策后置"，移入 `perception.md` Phase 5（身：触觉与行为
  闭环）——独立 robotd MCP server、cgo 不进主二进制、前置给
  llm 客户端补图片消息，均按存档方案执行；新增硬规矩"动作必须
  自带验证感知，禁止麻醉式自动化"。
- **生态**：技能目录先于市场（官方 mindloop-skills 仓库 + index.json
  + `skills search`）；`pkg/` 稳定 API 层（1.0 前冻结签名）+ webhook
  事件出口；JSONL 撕裂语义统一 + `mindloop gc` 轮转归档；故障注入
  进 CI（含 steal 锁残余双赢家窗口的追查，见 0.7.0 已知问题）。

## 4. 明确不做 / 反对项（评审结论存档）- **不用 JSON mode/结构化输出替代 bash+FINAL**：副作用协议是对格式
  漂移的免疫设计，是资产不是债。
- **不做分布式调度器**：背压二分、watchdog、冷启动不重放的正确性
  全部建立在"单目录锁 + 单 EOF"上；多设备 = 轨迹整体迁移，不拆脑。
- **不做个人微信**（无官方开放协议）与多人聊天机器人产品。
- **不先做技能市场/评分**：冷启动期目录是必需品，市场是奢侈品。
- **不做移动端 App**：移动触达已由 connectors 承担（企微审批卡
  已是"移动端"）。
- ~~**RPA 现阶段不做**~~（2026-10-02 感知立项修订）：身是感知系统
  Phase 5（`perception.md`），仍受"主动性 ≠ 自治"约束——审批门
  /tripwire/undo 全链不豁免。

## 5. 治理规则

- **依赖政策**：分层执行（见 §0.3），新依赖在 PR 描述给一句理由。
- **Mimosa 清偿**：每 PR ≥ 1 个，台账见
  `../security/mimosa-baseline.md`；合规写法遵循已实证的引擎模型
  （结构体字面量 + 白名单校验函数、流式 sha1、`str.match` 等）。
- **评审纪律**：涉 exec/fetch/凭据形态的改动，先按引擎模型选写法
  再动手；门禁残余用"换形状"手段，不用抑制注释（无效）。
- **现状校准**：扩充本路线前先对照 CHANGELOG——README 的进度
  描述可能滞后于 0.7.0 实际落地内容。

## 6. 已知问题（2026-10-02 记录）

- **working 投影测试间歇挂起（✅ 同日根因确认并修复）**：全量并行
  测试负载下偶发"思考者释放后 working 文件永不回收"。根因：
  `workingMark` 的投影删除 `_ = os.Remove(...)` 静默吞错——Windows
  高负载下新写文件被并发读者（杀软实时扫描等）短暂锁住，Remove
  报 sharing violation 即永久残留"幻影工作态"；内存忙集是对的，
  文件投影错了。修复：删除短重试（10×50ms，traj 锁释放同款药方）
  + 确定性钉子测试（持句柄模拟并发读者）。诊断过程沉淀：间歇失败
  靠"失败现场自诊断"（Cleanup 导出全场栈）锁定——失败时仅剩测试
  与主 goroutine，证明唤醒路径已正常完成、只剩投影删除失败。
- ~~**d.step 瞬时错误即调度器永久退出**~~（✅ 同日修复）：
  `Dispatcher.Run` 的 tick 分支对 `step()` 的任何错误直接 `return`
  ——Windows 文件争用的一次瞬时读失败就让调度器死掉不再自愈。
  修复：心跳连败退避容忍——step 错误源全是轨迹/待办/控制面的文件
  读取（瞬时争用类），单拍成功清零，连续 10 次（递增退避 ≈11s）
  才按结构性故障退出（服务模式 RestartOnFailure 兜底）。钉子测试：
  轨迹文件破坏→恢复→投递照常。
- ~~**steal 锁双赢家残余窗口**~~（✅ 0.7.0 遗留，同日闭合）：根因
  确认为 POSIX rename(2) 可替换空目录——挑战者的裸 mkdir 落在
  无主空窗内时，偷取者的 rename-back 整体替换它的目录，挑战者把
  属主写进偷取者的 inode（Windows 的 MoveFileEx 不能替换目录，
  结构免疫）。修复：目录文件身份守卫（POSIX dev:ino，平台分文件
  `lockid_*.go`）在 mkdir/scratch 隔离时记录、写属主前后各验一次，
  被替换者放弃且绝不清理（清理会删掉赢家的锁）；stealLock 回位后
  终验属主字节。压测（MINDLOOP_STEAL_STRESS=1）通过。
- ~~**CI `-race` 时序预算棘轮**~~（✅ 根因缓解）：2 核 runner 上
  全量并行包 + `-race` 的资源竞争把时序敏感测试饿到超预算
  （connector 10s→30s→90s、working 30s 的棘轮只是止血）。ubuntu
  race job 加 `-p 2` 限制包级并行，串行包换真红/假红的可判性。
- **未处置（非代码问题）**：api.ts 参数化 fetch 的 SSRF 检出为
  引擎对浏览器 fetch 的误报，门禁层无合规拼写，2026-10-02 用户
  裁定 warn 模式接受（见 docs/security/mimosa-baseline.md）。
