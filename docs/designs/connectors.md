# 设计：连接器生态（Connector Ecosystem）

状态：v3 按国内渠道重做（2026-09-27）——渠道无关内核承自 v2 评审（cursor
三态、rewound 防护、写入协议、投递语义分档），适配层按国内渠道（企业
微信/微信客服/钉钉/飞书）重做；Phase 1 定为企业微信智能机器人长连接。
待实施。
依赖：无（被 proactive-reporting.md 依赖）
关联：`internal/mind/message.go`（PostMessageOnce）、`internal/traj/cursor.go`、
`internal/mcp`（双向面）、`internal/childenv`

## 1. 目标与非目标

**目标**：让身份（如 ada）能从国内渠道收发消息——企业微信、微信客服、
钉钉、飞书——且不为此改动调度器、responder、任务系统的任何一行核心逻辑。

**非目标**：
- 不做公网聊天机器人产品——是"本人专属助理"的多入口，不是多人服务；
- 不在核心包实现任何渠道 SDK——渠道适配只允许纯协议实现（HTTP 类走
  `net/http`；WS 类走自研最小客户端，或按 §7 评审的局部破例）；
- 不做个人微信——无官方开放协议；微信侧用户经"微信客服"渠道触达（§7）；
- 不重造能力集成（日历/笔记/文件读取）——那已经是 MCP 客户端的职责。

## 2. 核心判断：日志即 API 已经解决了连接器问题的一半

逐项盘点现有机制，连接器需要的每个部件大都已经存在：

| 连接器需要 | 现有机制 | 位置 |
|---|---|---|
| 入站投递 | `PostMessageOnce`（client_message_id 幂等） | `mind/message.go:52` |
| 入站背压 | message 类 FIFO（上限 16 丢最旧） | `mind/dispatcher.go:109` |
| 回复寻址 | responder 回写 `message` 步骤，`from=<self>` `to=<来访者>` | `mind/responder.go:172` |
| 防重复回复 | `reply_to` 盖章 + answered 守卫（日志事实） | `mind/responder.go:196` |
| 防回路 | `launched_by` 作者章 + TriggerSelf=false | `dispatcher.go:436` |
| 首启不重放 | `NewCursorAtEnd`（EOF 起步，feeder 同款） | `traj/cursor.go:33` |
| 出站游标 | cursor 持久化字节偏移（Save/Load） | `traj/cursor.go:52` |

最后两行有一个必须直面的缺口：`LoadCursor` 对**缺失或损坏一律返回
从头 cursor 且无 ok 位**（cursor.go:52-66），`ReadNew` **丢弃 rewound**
（cursor.go:79-82）。bridge 是三态游标需求，直接照抄现有 API 会重演
Headlong "130 条旧消息重投真人"事故——三态模型与 rewound 防护在
§5.1 作为前置改动钉死。

**结论不变：连接器是一个独立的轨迹读写进程（bridge），不是调度器的
新功能。** bridge 正是 Headlong bridge 的形态，也是"冷启动不重放"铁律
的出处——这条铁律在本设计中升格为可验收的机制（§5.1、§9）。

## 3. 总架构

```
                       ┌─ 写：PostMessageOnce ──▶ 根轨迹 ◀── 写：回复 message 步骤 ─┐
 企微智能机器人 ─WS长连─┤                            ▲                              │
 微信客服       ─拉取───┤                     feeder 路由                             │
 钉钉/飞书      ─WS长连─┘                    （现有，不改）                          │
                                       responder / monolith ◀──────────────────────┘
                                                │
 bridge 进程（每渠道一个 goroutine/进程）        │ 读：cursor tail（§5.1 三态+rewound）
   ├ 入站泵：渠道 API → 轨迹                     │ 过滤 from=<self> && to∈认领地址
   └ 出站泵：轨迹 → 渠道 API ────────────────────┘
```

两条不变式：

1. **路由即数据。** 一条消息归哪个渠道，只由轨迹上的 `to` 字段表达。
   bridge 出站泵的过滤谓词是纯日志谓词：`type=message && from=<self> &&
   to ∈ 认领地址集`。没有任何隐式规则、内存状态或旁路通道。
2. **bridge 与 mind 无进程内依赖。** 只共享轨迹文件（多写者由目录锁保证，
   读者各自持 cursor）——与 `web → mind` 仅经控制面文件协议通信同哲学。

## 4. 入站路径

### 4.1 身份命名

渠道用户在轨迹上的身份：`<channel>:<uid>`（如 `wecom:zhangsan`、
`wxkf:wmAJ2GCAAAme1XQRC-NI-q0_ZM9ukoAw`——微信客服的客户 external_userid）。

- **不做身份合并映射**（曾考虑把白名单用户映射为 `operator`）：
  responder 的回复寻址靠 `to == from` 往返，合并会丢失出站路由信息。
  跨渠道对话连续性已由 history 汇聚解决（responder.go:212 的既有语义：
  所有来访者的双向消息按日志序并入同一对话流）——**记忆统一、路由分离**
  是两个正交问题，现有架构已经解耦，不要重新耦合。
- `from` 的渠道前缀 + `source` 字段让提示渲染天然携带"这句话从哪个
  渠道来"，模型可自然调整语气与长度（企微对话短句、报告类成文）。

### 4.2 写入协议

消息字段形态的唯一权威定义是 `PostMessageOnce`（message.go:31-52 的
注释明言此约束），bridge 不旁路它、不自己拼步骤。渠道信息由既有字段
承载，**不扩展附加字段**：`source=wecom`（入口标记）+ `from=wecom:<uid>`
（发送者）已完整表达渠道归属；曾设想的 `chat_title` 等 nice-to-have
不值得为此给权威接口加扩展位、并引入幂等命中的字段一致性语义，砍掉。

```go
// 入站（bridge 进程内）——6 参签名，幂等键映射渠道消息 id：
mind.PostMessageOnce(tl, "wecom:zhangsan", identityName,
    "wecom", content, "wecom:"+msgid)
```

幂等语义按现实现继承：同 (from, client_message_id) 同内容 → 返回原
步骤（响应丢失后的安全重发）；同键不同内容 → `ErrMessageConflict`——
bridge 收到它**记日志、不重试、丢弃该条**（宁可丢这一条：载荷不一致
说明 bridge 状态与渠道已漂移，重试只会反复冲突；拉取类渠道照常推进
游标）。

### 4.3 白名单（fail-closed）

桥接连接器**必须**配置 allow 列表（渠道用户 id），否则拒绝启动。
这不是功能是红线：轨迹上的心智有沙箱执行权，公网可达的入站口等于
把执行权暴露给陌生人。白名单外的消息**丢弃并落一行 bridge 日志**
（不写轨迹——拒绝的证据不需要进入心智的事实源）。
解析复用 `childenv.List` 的既有分隔习惯（逗号/分号）。

企微智能机器人的 userid 形态：机器人创建者为企业超级管理员时，回调中
为明文 userid；否则为加密 userid。个人自建企业（本设计目标场景）即
超管场景——直接使用管理后台通讯录里的 userid 配置白名单即可。

### 4.4 投递顺序与确认语义

**write-then-ack 是入站路径唯一不可违反的顺序约束，必须钉死**：先
`PostMessageOnce` 落盘成功，**然后**才做渠道侧确认（若有确认通道）。
反向实现（先确认后写）在崩溃窗口会**丢消息**，且渠道侧无从恢复。

国内渠道的确认语义按传输形态分两类，逐渠道落地：

| 渠道 | 入站形态 | 确认/重推语义 | bridge 落地 |
|---|---|---|---|
| 微信客服 | HTTP 拉取 | cursor 回执续拉（`next_cursor`） | 先落盘后保存 cursor——未保存即重拉，天生 at-least-once |
| 企微智能机器人 | WS 推送 | 回调无确认应答（协议未定义 ACK）；msgid 供排重 | 先落盘；重推由 `wecom:<msgid>` 幂等键吸收 |
| 钉钉 Stream | WS 推送 | 机器人消息 fire-and-forget 不重推；事件订阅需回 ACK | 先落盘后回 ACK `{code:200}`（Phase 2 落地时钉死） |
| 飞书长连接 | WS 推送 | 3 秒内不回 ACK 即超时重推 | 先落盘后 ACK（Phase 2 落地时钉死） |

推送类渠道的固有代价如实陈述：回调到达与落盘之间进程崩溃 → 该条丢失
（渠道不重推或无从补拉），窗口极小，MVP 接受；拉取类渠道无此窗口。
企微智能机器人按"可能重推"作保守假设——msgid 是它唯一的去重保险。

其他继承语义：
- 渠道侧按事件序投递（拉取类按消息序、推送类按到达序），bridge 串行
  处理，不并行乱序；
- 调度器对 message 的**派发队列** FIFO 上限 16 丢最旧（dispatcher.go:109-117
  ——注意那是投递队列不是轨迹：被丢的消息仍完整留在轨迹里，只是不再
  触发思考）。bridge 不做本地重试队列，断线重连后从渠道游标/回执续拉，
  积压超过派发容量时丢旧保新——与人类消息同一礼貌；
- 幂等查重是全文件扫描（message.go:54-58，每条入站 O(n)）。低频个人
  场景可接受，注明为已知成本；轨迹膨胀到需要索引时再优化。

## 5. 出站路径

### 5.1 游标三态与 rewound 防护（前置 traj 改动）

出站泵对 cursor 的需求是**三态**：

| 状态 | 判定 | 行为 |
|---|---|---|
| 首启 | cursor 文件不存在 | 从 EOF（只看启动之后） |
| 重启 | 有效 cursor | 续读 |
| 损坏/失配 | 内容不可解析 | 从 EOF（宁可漏发不可重放） |

现有 `LoadCursor` 对缺失与损坏一律返回**从头** cursor 且无 ok 位
（cursor.go:52-66）——首启即全量重放，正是铁律的反面。**前置改动 1**：
traj 包补 `LoadCursorAtEnd(cursorPath, path string) (*Cursor, error)`
（有效则续读，缺失/损坏一律 EOF），bridge 只准用它；本节谓词写死，
实现者无从"照抄 LoadCursor"。

第二个缺口：轨迹文件被替换或截断时，`readNew` 把 offset 归零从头
重读并报告 rewound（cursor.go:101-104），而公开的 `ReadNew` 把 rewound
**丢掉了**（cursor.go:79-82）。对 bridge 的后果：没有任何损坏迹象的
一次静默全量重投——比游标损坏更隐蔽。现有消费方各有防护（chatdurable
用 Offset 前后对比，chatdurable.go:138-147；web 侧索引靠 rewound 整体
重建），bridge 没有 responder 那样的 answered/expired 下游守卫吸收重放，
**必须自己检测**。

**前置改动 2**：traj 暴露 rewound 感知读取（`ReadNewWithRewind`，或
等价的 Offset 前后对比约定）。bridge 检测到 rewound 即**放弃本批、
Save 已推进到 EOF 的 offset**——"宁可漏发不可重放"的同向应用。

### 5.2 过滤与投递

出站泵过滤谓词（纯日志谓词）：

```
type == message
&& from == <self 身份名>
&& to ∈ 认领地址集（wecom:<白名单 userid> 精确匹配）
&& launched_by != <本 bridge 名>   （防御冗余：bridge 从不写 message，
                                    此条件永不命中，保留作纵深）
```

命中即调渠道 API 投递。**投递语义分三档写清，不笼统称 at-least-once**：

1. **正常路径**：投递成功后 cursor 落盘；投递成功与落盘之间崩溃 →
   重启后重投一次（**重复**）。窗口极小，已知限制，MVP 接受；
2. **重试耗尽**：指数退避 3 次仍失败（渠道限流/网络抖动）→ **丢弃
   该条**（**漏发**），bridge 日志审计，cursor 照常推进——一条发不出去
   的消息绝不能卡住出站泵；轨迹上它依然存在，人可在仪表盘看到；
3. **崩溃窗口**：同第 1 档，表现为偶见重复。

两段式 intent/receipt 可消除第 1/3 档的重复，Phase 2 视真实体验决定。

### 5.3 长文分段

国内渠道单条消息上限各不相同（量级如下，实施时按官方文档复核）；出站
泵按**渠道配置的分段阈值**在段落边界折叠分段（markdown 代码块不跨段
切断：分段点避开未闭合 ``` 计数）：

| 渠道 | 单条上限（量级） |
|---|---|
| 企微智能机器人 | ~20480 字节（markdown，主动推送原生支持） |
| 企微自建应用 | ~2048 字节（入站已排除，仅出站备查） |
| 微信客服 | ~2048 字节（text，服务端超出即截断——分段更保守） |
| 钉钉机器人 | ~15000 字节 |
| 飞书文本 | ~150 KB（基本无需分段） |

阈值取表值的保守折扣（如 90%），为标记开销留余量。分段是出站泵职责，
模型与 responder 无感知。

### 5.4 什么消息不该投

- `source=chat` 且带 `reply_to` 且 to 是渠道地址 → 投（这就是对话回复）；
- 主动汇报（`source=task/report`）→ to 写了渠道地址就投——路由决策
  完全在写入方，见 proactive-reporting.md §5；
- `to=operator` / `to=you`（网页与 CLI 入口）→ 不投，那是对话流的
  本地入口；
- `type=alert` → **不直接出站**（出站谓词要求 `type=message`，天然
  排除）。告警上手机由 monolith 评估后以 message 承载
  （proactive-reporting.md §3.2）。

## 6. 进程模型与配置

### 6.1 进程形态

```
mindloop connector run wecom [--identity ada]      # 单渠道前台（开发）
mindloop connector run-all [--identity ada]        # 全部启用渠道（服务化宿主）
mindloop connector list                            # 配置与状态投影
```

- bridge 与 `mindloop mind run` 是两个进程、两把互不相干的锁——
  bridge 不需要运行锁（它不思考，只是读者+写者，多进程 tail 同一
  轨迹已被 web watcher 验证）。
- 企微智能机器人**单连接互踢**：同 BotID 新连接订阅成功即踢掉旧连接
  （旧连接收到 `disconnected_event`）。bridge 重启即自然让位，不做
  双连/主备竞争；收到该事件即退出，等重启拉起。
- Windows 服务化（另一立项）落地后，服务主进程拉起 `connector run-all`
  与 `mind run` 两个子进程——本设计的进程边界为此预留。

### 6.2 配置与凭据：约定优于配置

MVP 零配置文件，全部走既有身份 .env 机制（显式环境变量 > 身份 .env >
全局 .env）：

```bash
# <identity>/.env
WECOM_BOT_ID=ww1234567890
WECOM_BOT_SECRET=xxx          # 长连接专用 Secret（非回调 Token/EncodingAESKey）
WECOM_ALLOW=zhangsan,lisi     # 白名单 userid，缺失则拒绝启动
```

理由：凭据不进任何会被备份/分享的 JSON；与 llm 凭据同一加载路径，
心智零新概念。渠道数多了以后再引入 `connectors.json`（形态预留：
`{"wecom": {"allow": [...], "formatting": "markdown"}}`），届时 .env
里的白名单条目视为 legacy 继续支持。

### 6.3 凭据隔离

bridge 是 mindloop 自身进程（非沙箱子进程），凭据经 .env 直读。
与 childenv 的关系：childenv 白名单管的是**沙箱孙进程**能看见什么，
且实际比本设计 v1 声称的更强——`childenv.go:69` 的 Sensitive 模式使
含 TOKEN/SECRET/PASSWORD 等凭据词的键**即使被显式 extra 点名也不下传**
（childenv.go:99，fail-closed）。`WECOM_BOT_SECRET` 命中 SECRET 模式，
`WECOM_*` 前缀也不在继承白名单内（双重保障）；模型在沙箱里跑的脚本
永远看不见它。仍需一条测试钉死：**沙箱内 `env` 输出不含任何
CONNECTOR/WECOM/WXKF 前缀变量**。

## 7. 渠道适配器规格与依赖红线

红线：核心 internal 库零第三方依赖。国内渠道逐项评估：

| 渠道 | 传输 | 依赖 | 结论 |
|---|---|---|---|
| 企业微信智能机器人 | WS 长连接（openws.work.weixin.qq.com，JSON 订阅/回调） | 需 WS 客户端（自研，见下） | ✅ Phase 1 |
| 微信客服 | HTTP 拉取（sync_msg cursor 增量）+ send_msg 出站 | 纯 net/http | ✅ Phase 2（对话定位，见下） |
| 钉钉 Stream | HTTP 注册端点+ticket → WS；需回 ACK | 复用 WS 客户端 | ⏸ Phase 2 按需 |
| 飞书长连接 | HTTP 注册 → WS；3s ACK | 复用 WS 客户端 | ⏸ Phase 2 按需 |
| 企业微信自建应用 | 入站回调需公网 + AES/XML 加解密 | — | ⏸ 无公网场景不可行 |
| 微信公众号 | 回调需公网 | — | ⏸ 排除 |

（能力面——日历/笔记/文件——不在此表，它们是 MCP，见 §8。）

**为什么 Phase 1 是企业微信智能机器人**（对齐原设计的三条件标准）：
1. **无公网可部署**：长连接为无公网场景设计，出站建连即可，无回调
   地址、无加解密；
2. **主动推送成立**：用户给机器人发过一条消息（解锁）后，
   `aibot_send_msg` 可主动推送且无窗口限制（仅 30 条/分、1000 条/时
   频率限制）——"晚报上手机"场景成立；
3. **协议最简**：JSON 自描述；官方无 Go SDK，反而让"纯协议直连"
   没有选择悬念。
且个人可免费注册企业微信（无需营业执照），智能机器人在管理后台创建——
目标用户（个人自用）准入门槛为零。

**为什么不是微信客服做 Phase 1**：它的纯 HTTP 拉取与内核 write-then-ack
最同构，且能触达个人微信；但出站是**被动窗口**——用户主动发消息后
48 小时内最多 5 条，窗口外无法主动触达，"晚报/告警"场景不成立。它
适合"对话优先"（用户先说话 → ada 在窗口内回复），Phase 2 作为触达
个人微信的补充渠道落地。

**WS 传输层规格**（企微/钉钉/飞书共享，Phase 1 一并交付）：
`internal/connector/ws` —— 最小 RFC 6455 客户端，纯标准库（net +
crypto/tls + crypto/sha1 + encoding/base64）。只实现客户端必需子集：
Upgrade 握手、文本/二进制帧、ping/pong、close、客户端掩码。不做：
扩展协商（拒绝 permessage-deflate）、服务端、超限分片重组（超限拒绝）。
若实施中复杂度失控（成为 bug 温床），升级为**子包局部破例**引入
coder/websocket——与 sandbox 用 syscall 同级论证（红线约束核心
internal 库，渠道适配子包可例外），届时单独评审。

**企微智能机器人适配器规格**（Phase 1 详规）：
- 连接：wss 建连 → `aibot_subscribe{bot_id, secret}`；单连接互踢
  （§6.1）；断线指数退避重连后重新订阅；心跳按官方要求（30s 级）；
- 入站：`aibot_msg_callback` → 取 `body.from.userid`、文本内容、
  `body.msgid`；幂等键 `wecom:<msgid>`；MVP 只处理文本（图片/语音/
  文件不落轨迹，留待需要时评估）；
- 出站：统一 `aibot_send_msg`（chatid=userid、单聊、msgtype=markdown）。
  **不用 req_id 回复通道**——`aibot_respond_msg` 需透传回调 req_id、
  在 bridge 内维护会话状态，破坏出站泵的纯日志谓词；`aibot_send_msg`
  的 `to=wecom:<userid>` → chatid 是无状态映射，与"路由即数据"完全
  吻合。前提：用户已在会话中给机器人发过消息（解锁）——首次使用
  引导写进部署文档；
- 流式（`aibot_respond_msg` + stream.id）不进 MVP：与"回复步骤整段
  写回轨迹"的内核冲突，是渠道增强，Phase 3 视体验评估；
- 频率：出站 30 条/分、1000 条/时（按会话计）；超限走 §5.2 重试退避。

**微信客服适配器规格**（Phase 2 预留）：
- 入站：sync_msg 拉取——`cursor`=上次 `next_cursor`、`limit` 1000、
  以 `has_more` 判定续拉（`msg_list` 为空不能当停止条件）；`token`
  不填则严格限频，bridge 低频拉取可不填；可回溯窗口 3 天；
- 幂等键：`wxkf:<msgid>`；
- 出站：send_msg（被动窗口 48h/5 条；窗口外失败按重试耗尽档处理并
  日志）；出站幂等可指定 `msgid`（客服账号内唯一）；text ≤2048
  字节（服务端截断，分段阈更保守）；
- 权限：自建应用需配置进「微信客服-可调用接口的应用」。

## 8. 能力连接器 = MCP，零新代码

日历、笔记、文件检索这类"心智主动读取外部"的需求不需要 bridge——
它们是工具调用，走既有 MCP 客户端（mcpServers 配置，Claude Desktop
生态直接可用）。本方向对它们只做一件事：**文档与预设**。

- 提供 `mcp.json` 精选预设（日历 MCP、文件系统 MCP、浏览器 MCP…）
  作为 skills/mindsetup 的推荐配置段；
- 系统提示的连接器披露段（Phase 3，与报告通道一起做）同时列出：
  本身份启用的 bridge 渠道与可投递地址（模型需要知道
  "to=wecom:zhangsan 是有效地址"才能正确路由主动汇报）。

## 9. 防回路与安全审查清单

实施验收时逐条核对：

- [ ] bridge 写入的入站消息 `launched_by` 为空（外部来源），responder
      TriggerSelf=false 不受影响；回复步骤 launched_by=responder，
      出站泵过滤条件不含 launched_by=responder 排除项——它就是要投
      responder 的回复（与 monolith 回复共线）。
- [ ] bridge 永不写 `type=message && from=<self>` 的步骤（只读出站、
      只写入站）——用类型系统约束：入站泵与出站泵不共享写句柄。
- [ ] 白名单缺失拒绝启动；白名单外消息不落轨迹。
- [ ] 沙箱环境无连接器凭据（测试钉死，§6.3）。
- [ ] **cursor 缺失/损坏一律 EOF**（依赖 §5.1 前置改动 1，禁止裸用
      `LoadCursor`——它的缺失/损坏语义是从头；三态逐格测试钉死）。
- [ ] **轨迹被替换/截断（rewound）→ 弃批跳 EOF**（§5.1 前置改动 2；
      测试：替换轨迹文件后 bridge 不重投任何旧消息）。
- [ ] bridge 崩溃/重启不产生重复入站（write-then-ack + 渠道游标/幂等
      键 + client_message_id 三保险，§4.4）。
- [ ] 企微回调重推（同 msgid）幂等吸收（`wecom:<msgid>` 键生效）。

## 10. 分期与验收

**Phase 1（骨架 + 企微智能机器人，目标：全链路能跑）**

前置 traj 改动（小而关键，先行交付）：
- `LoadCursorAtEnd`（三态语义，§5.1 前置改动 1）；
- rewound 感知读取（§5.1 前置改动 2）。

bridge 主体：
- internal/connector：出站泵（三态游标 + rewound 防护 + 过滤 + 投递
  接口 + 三档投递语义）、入站泵接口、幂等写入封装（write-then-ack
  按 §4.4 确认矩阵）；
- internal/connector/ws：最小 RFC 6455 客户端（§7 传输层规格）；
- internal/connector/wecom：长连接订阅 + 回调解析 + markdown 出站；
- cli/connectorcmd.go：run / run-all / list；
- 测试：过滤谓词穷举、游标三态逐格、rewound 弃批、幂等重放与
  ErrMessageConflict 路径、白名单 fail-closed、出站分段、WS 帧级
  测试（掩码/分片/ping-pong/close）、沙箱凭据隔离。
- 验收：手机企业微信上给 ada 机器人发"帮我看看今天的日报"→ ada
  回复到企微；`schedule at+task` 的晚报出现在企微；**替换轨迹文件
  后无任何旧消息重投**。

**Phase 2（第二渠道 + 出站强化）**：微信客服（对话窗口内触达个人
微信，纯 HTTP 成本最低）；钉钉/飞书长连接（复用 WS 客户端，按真实
需求排序）；intent/receipt 两段式防重投（视真实体验）。

**Phase 3（披露与预设）**：系统提示连接器段、mcp.json 预设集、
connectors.json 引入（若渠道 ≥3 且配置项确实膨胀）、流式回复评估
（aibot_respond_msg + stream.id）。

**Phase 4（服务化整合）**：并入 Windows 服务立项的进程编排。

## 11. 开放问题

1. 出站投递的内容呈现：responder 长回复在企微上是否默认压缩为摘要 +
   仪表盘链接（MINDLOOP_EXE web 的同源 URL）？与 §5.3 分段正交（分段
   =投递层，摘要=内容层），Phase 2 用真实使用反馈定，两者可并存。
2. 多身份（多个 identity 目录）各自跑 bridge 的资源隔离——MVP 单
   身份；注意企微智能机器人**单连接互踢**是渠道硬约束：多身份必须
   用不同 BotID，不能共用一个。
3. 群聊维度：MVP 只做单聊（个人助理场景）。群聊要引入 chatid 身份
   维度与"群里谁在对话"的归属问题，按需评估，不预先绑定。
4. WS 客户端验收强度：纯标准库手写实现需要帧级边界测试的持续投资——
   若 Phase 1 实测发现协议边缘 case 过多，按 §7 升级路径切换为子包
   局部破例（coder/websocket）。
