# 基准研究：BaiLongma 对 mindloop 的可借鉴点

> 调研对象：https://github.com/xiaoyuanda666-ship-it/BaiLongma（克隆快照，b20f4ad）
> 结论日期：2026-09-28。本文是研究笔记，非设计承诺；采纳需另行立项。

## 1. BaiLongma 是什么

Electron 桌面 AI Agent（Node.js 单进程 + SQLite 单库），持续主循环驱动：
有用户消息优先处理，空闲按心跳整理记忆、检查任务、刷新上下文。组成：
Electron 壳、本地 HTTP/SSE/WS 服务、LLM 调用层、记忆系统、工具执行器、
语音全链路（云端 ASR + 多家 TTS）、社交连接器（Discord/微信 ClawBot/飞书）、
Brain UI（聊天/思考流/记忆图/焦点线程/热点面板/ACUI 卡片）。

文档质量高且理念先行：ACI-理念文档、Agent-Time-Sense-Theory、
SCENE-PROTOCOL（RFC2119 风格规范）、Agent-驱动UI-设计方案、38KB 功能总说明。
实现真实（非 PPT 项目）：注入器/预取缓存/线索模型均有对应代码
（src/injectors ~1250 行、src/prefetch/runner、src/memory 30+ 模块）。

## 2. 与 mindloop 的哲学对照

| | mindloop | BaiLongma |
|---|---|---|
| 事实源 | 追加式 JSONL 轨迹 + 目录锁，多进程文件协议 | SQLite 单库，单进程内嵌 |
| 活性 | dispatcher：背压二分/watchdog/backoff 驻留/WantWake | 主循环 + TICK 心跳 + 看门狗 + set_tick_interval |
| 沙箱 | Job Object 整树管辖 + FINAL 协议 | 路径检查 + 风险分级 + 确认流 |
| 记忆 | markdown+frontmatter + BM25 + recap 分集 | SQLite 记忆节点 + FTS5 trigram + 本地 embedding + 线索/焦点栈/画像 |
| UI | 仪表盘 = 被动日志查看器（React SPA） | Brain UI = Agent 驱动的场景投影（Scene Protocol） |
| 计划 | schedule at/every/days + quiet + 幂等补提交 | reminder + prefetch task |
| 成本 | 双模型分层（思考档/请求档） | 无（单模型 + 温度） |

重合部分（已做得相当或更好，无需学）：心跳/活性、提醒/计划、任务持久化、
时间戳事件、社交连接器架构（统一主循环 + 路由即数据）。mindloop 领先：
调度器活性理论、双模型分层、进程隔离、零依赖纪律、Windows 原生沙箱。

## 3. 可借鉴点（按建议优先级）

### 3.1 ACI 预判注入（最高价值）
> "在模型开口之前，系统主动预判它需要什么，提前查好，直接注入。"

三类场景：
1. **语义记忆预判**：消息到达即并行检索记忆，注入 prompt——模型不再需要
   自己调 search_memory。零风险（只读）。
2. **工具链模式预判**：学习"写早报→天气+新闻+日历"的历史调用序列，
   命中意图后并行预执行，结果注入 prompt，省掉数轮工具往返。
3. **定时预热缓存**：cron 周期预取天气/新闻/日历存 prefetch_cache（带
   TTL），注入时直接用。确定性最高（时间触发）。

关键约束与机制：
- 只允许**只读/幂等/低副作用/结果有时效**的工具进入预判范围
- 置信度分级：>0.85 直接注入 / 0.5–0.85 轻提示 / <0.5 不注入
- **注入器 deadline 1.5s**：预判工具慢了就不等，宁可少注入不拖慢响应
- 预判只是提前执行，不是替代决策——错了最多浪费一次查询

mindloop 落地路径：`every+exec`（周期跑只读命令不叫醒模型）已经等价于
"定时预热的执行器"，缺的只是**产物进 prompt 渲染**这一步——约定一个
预取产物目录/步骤类型，prompt 渲染器把有效期内缓存注入 monolith 上下文。
模式预判可后置（先做记忆与定时两类）。

### 3.2 Scene Protocol / Agent 驱动 UI（第二价值）
> "core 持有一份 scene 作为唯一真相源；UI 是 scene 的纯投影；UI = f(state)"

- Agent 工具面只有一个动词：幂等 `ui.set(id, {kind, data, intent, focus})`，
  set null 即移除——show/update/hide 合而为一
- **intent 表达语义重要性而非像素**：ambient（环境氛围）/ inform（一般）/
  confront（必须停下决策），渲染端自行映射呈现
- rev/base 间隙检测 + resync：断线重连 = 重发快照，状态漂移在数学上不可能
- **manifest 回灌**：把"舞台上有什么"（id+kind+一行摘要+intent）紧凑回注
  Agent 上下文，绝不回注 data/像素——Agent 能按 id 引用更新、主动清场、
  感知画面密度
- 明确禁止 agent 注入 HTML/JS：长尾内容用排版原语（text/metric/row/col）
  组合，动画是渲染端手工打磨的 enter/exit/morph
- 生命周期信号（停留/关闭）只作上下文绝不当触发器

对 mindloop 的意义：仪表盘从"被动日志查看器"升级为"Agent 可驱动的投影面"。
企微审批卡（button_interaction 卡）本质就是 confront 类 surface 的渠道特例——
Scene 协议是它的通用化。scene 状态可落轨迹（事实源一致），仪表盘经 SSE 投影。

### 3.3 按需工具注入 + find_tool 自发现
不每轮注入全量工具 schema，按消息正文/是否心跳/活跃任务/最近工具日志裁剪；
模型需要未注入工具时调 `find_tool` 搜索工具目录动态追加。省 token、减少
误调用。mindloop 的技能已有渐进披露（系统提示索引段），同一手法可推广到
工具 schema 面。

### 3.4 行动日志防重复
把"最近动作"（工具/参数摘要/结果摘要/耗时/风险级）作为独立段注入 prompt，
让模型知道"刚读过这个文件、刚搜过这个词"，显著减少重复行为。mindloop
轨迹里有原始数据，缺一个**面向模型的紧凑动作摘要段**。

### 3.5 记忆系统的具体机制
- **线索模型 threads/commitments**：多条开放线索并发 + 前台指针 + 指代解析
  （"那个网页""进度怎么样"）+ 承诺钉住线索温度——比 recap 分集更细的
  并发话题管理，解决话题漂移与代词指错
- **焦点栈**：话题深化 push / 回旧题 pop / 出栈压缩为结论回填记忆
- **记忆审计**：每轮记录查询文本、命中 id、注入数、耗时、跳过原因——
  记忆质量可观测（Brain UI 有近一小时统计图）
- **用户画像**：角色/领域/偏好/沟通风格 + 证据与置信度，相关对话注入
- FTS5 trigram 中文子串检索 + 本地 embedding 融合排序；记忆生命周期
  （visibility 软隐藏、merged_into 合并链、降权、consolidation loop）

### 3.6 时间感的增量细节
mindloop 已有心跳/背压/WantWake（有限自我调速=论文级的 set_tick_interval），
增量只有两点：**birth time / 存在时长**注入自我叙事（"我已存在 N 天"）；
"对话窗口 = 短时意识、记忆 = 长期知识"的显式分层表述（prompt 渲染文档化）。

### 3.7 局域网安全访问（移动端仪表盘）
本地根证书 + 按局域网地址签发的 HTTPS 证书 + 配对口令（URL fragment 传递、
页面保存后立即从地址栏移除）+ 根证书二维码。比 mindloop 现在的 `--token`
完整——做手机/iPad 访问仪表盘时直接照抄这套。

### 3.8 本机资源感知与本地 Agent 委托
启动扫描系统/电池/桌面文件/已装软件/SSH/Git/地理天气/本地已装 AI Agent
（Claude Code、Codex…）入上下文；`delegate_to_agent` 把子任务委托给本机
其他 Agent。mindloop 是 Windows 一等公民，扫描 + 委托是 agent 间协作的
低成本入口。

### 3.9 工程实践
- 冒烟测试脚本族（smoke:tools / smoke:brain-ui / smoke:social）与
  记忆修复脚本（repair:memories 带 dry-run）
- 打包后跑**真实产物 smoke test**；正式发布强制 Authenticode 签名
- 内容寻址持久化聊天图片（原始截图删除后聊天仍可显示）
- 浏览器会话管理（persistent profile 按 task/origin 隔离、TTL 回收、
  崩溃恢复语义诚实声明"只复用已落盘状态"）——未来加浏览器工具时照抄

## 4. 不建议学的
- 单进程 Electron 内嵌一切：mindloop 多进程文件协议的故障隔离更强
- SQLite 单库存一切：与轨迹事实源哲学冲突
- 记忆节点关系图/向量全量上：先有审计与画像，再谈向量
- 语音/媒体/音乐库大而全：与 mindloop "本人专属工作 Agent" 定位偏离

## 5. 落地路线建议
1. **ACI-定时预热**：every+exec 产物 → prompt 渲染注入（最小改动，最大体感）
2. **行动日志摘要段**：prompt 渲染器加"最近动作"段
3. **Scene 卡片协议**：仪表盘 SSE 投影 + ui.set 工具（审批卡通用化）
4. **按需工具注入 + find_tool**
5. **记忆审计 + 画像**（配合仪表盘记忆页）
6. **线索模型**（中期，替代/增强 recap 分集）
7. **局域网安全访问**（移动端需求出现时）
