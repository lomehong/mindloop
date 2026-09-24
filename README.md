# mindloop

跨平台的持久化 AI Agent 框架，用 Go 实现。设计上遵循
[Headlong](https://github.com/laude-institute/headlong) 的思路——以
追加式 JSONL 轨迹日志为唯一事实源，其上是小工具组合，加上一个在外部
交互之间持续思考的心智循环。**Windows 是一等公民**：进程隔离用原生
Job Object 而非 Docker，锁与文件身份按 NTFS 语义设计；Linux/macOS
同样可跑（沙箱降级为进程级终止，其余能力不变）。

当前进度：路线图 1–8 全部完成（日志、上下文渲染、运行循环、持久
心智、responder 回复、记忆、recap 分层上下文、观测面）。依赖策略：
**核心 internal 库零第三方依赖**（Windows 沙箱直接用 syscall 声明
内核调用），仅 CLI 层引入
[spf13/cobra](https://github.com/spf13/cobra)。

## 架构

```
cmd/mindloop/          入口薄壳（~20 行）：Ctrl+C → ctx，其余交给 cli
internal/cli/          命令行层：子命令调度、旗标解析、输出格式化
internal/mind/         持久心智：调度器（feeder/背压/watchdog）+ monolith
internal/identity/     身份目录布局：persona + 元数据 + 根轨迹
internal/runner/       运行循环：渲染 → 思考 → 提取 → 执行 → 记录 → FINAL
internal/llm/          模型客户端：openai-compatible / anthropic / echo
internal/sandbox/      Job Object 沙箱：整树管辖、三类超时、FINAL 协议
internal/prompt/       上下文渲染器：轨迹 → LLM 消息序列
internal/traj/         日志层：追加式 JSONL 轨迹、目录锁、cursor、查询
internal/ids/          UUID v4 与短前缀
```

依赖方向单向：`cmd → cli → {mind → runner, identity} → {llm, prompt,
sandbox} → traj → ids`。库不打印日志、不读环境变量（`traj.Home()`、
`llm.FromEnv()`、`identity.Home()` 除外）、不启动 goroutine；阻塞
操作都接受 `context.Context`。`runner.Thinker` 与 `mind.Thinker`
两个接口把"模型是谁""思考者是谁"与调度解耦——测试用脚本化假实现。

## 持久心智（第 4 步的核心设计）

- **背压二分法**：人类消息（`message`）FIFO 保序投递（上限 16、
  丢最旧）；其余类型 last-wins 合并——自循环思考者永远不在积压的
  过期自我唤醒里打转，而空闲槽位永远先给人。
- **活性由调度器保证**：watchdog 周期性合成唤醒治愈任何断链；
  自发性是思考者向调度器**预约**的（`Outcome.WantWake`），不是
  自己起定时器——定时器会随思考者一起死。
- **回退策略纯函数化**：可见工作或外部触发归零；闲置每级 ×2 封顶
  5 分钟；思考型封顶 1 分钟。升级曲线被穷举测试。
- **双重防回路**：订阅面排除自己会产出的全部类型（测试钉死）+
  `launched_by` 作者章守卫。
- **冷启动不重放**：调度器的 cursor 从 EOF 起步——Headlong 的
  桥接曾把 130 条旧消息重投给真实的人，此后"重放历史从来不是
  任何人想要的"成为铁律。
- **调度主循环绝不阻塞**：思考者在独立 goroutine 里跑，全部占满
  时 feeder/watchdog/调度照常心跳（Headlong 在并发上限处阻塞主
  循环是其自认的事故）。

## 从 Headlong 继承的设计支柱

- **日志即 API。** 每个消费者都是同一批 JSONL 文件的读者或写者。
- **视图皆派生。** 一切索引基于持久化字节偏移 cursor，收缩即重建。
- **一个 id 命名空间。** 轨迹本身是一个步骤；`run` 步骤的 id 即
  run_id，运行内所有步骤盖 `run_id` 章（结构性类型除外）。
- **上下文是投影。** 每轮从日志重渲，绝不在内存私藏一份真相；
  截断处留下 `traj show <id> --full` 取回命令。
- **缓存网格。** 截断档位是行号的纯函数，尾窗对齐块边界——块内
  追加不改写既存行，prompt cache 前缀稳定。
- **FINAL 靠副作用不靠解析。** 完成信号是沙箱里的哨兵文件（内容
  来自环境变量 FINAL），对模型输出格式漂移天然免疫；`set -e` 之下
  失败的脚本不产生 FINAL——失败即未完成。
- **教学式纠偏。** 模型忘写代码块 / 写了多个块：第一次照常执行并
  把提示回灌，不罚但教会。

## Windows 原语对照

| Headlong (POSIX) | mindloop (Windows) |
|---|---|
| `set -m` 进程组 + 遍历 /proc 杀树 | **Job Object**：整树管辖、一次终止、KILL_ON_JOB_CLOSE 兜底 |
| `setsid` 会话 | Job Object 挂载即隔离 |
| mktemp + $TMPDIR 隔离 | 每轮脚本临时文件落在工作目录并即时清理 |
| 三类超时（输出/空闲/嵌套） | sandbox 同款三类：`KillOutput`/`KillIdle`/`KillTimeout` |
| 咨询式 mkdir 锁 | 同款，但属主文件带 pid、偷锁前查存活（`OpenProcess(SYNCHRONIZE)`） |
| bin/llm 唯一调用点 | internal/llm 唯一调用点 |

## Go 最佳实践清单

- `cmd/` 薄壳（~20 行）、逻辑全在 `internal/`；包名短小、类型名
  不与包名口吃（`traj.Timeline`、`sandbox.Request`）；依赖单向。
- 阻塞操作首参 `context.Context`；CLI 用 `signal.NotifyContext`；
  stdout/stderr 注入（`cli.Run(ctx, args, stdout, stderr) int`），
  整层可不经子进程测试。
- 哨兵错误（`ErrNotFound`/`ErrLockTimeout`/`ErrStalled`/
  `ErrMaxIterations`/`ErrNoBash`/`ErrNoProvider`…）+ `%w` +
  `errors.Is`；库返回错误、CLI 打印错误。
- 端口接口在消费侧定义（`runner.Thinker`）；平台差异收敛到
  `//go:build` 文件；`syscall` 的 LazyDLL 直接声明内核调用——核心库
  零第三方依赖，cobra 只出现在 CLI 层。
- CLI 基于 cobra：帮助/示例自动生成、未知命令 did-you-mean 建议、
  shell 补全（`mindloop completion`）、旗标与位置参数自由混排；
  退出码语义统一（0 成功 / 1 运行失败 / 2 用法错误 / 3 run 失速）。
- 测试即回归记录：并发交错、锁偷取/宽限/竞争、cursor 撕裂尾/
  收缩/重放、解析器值拷贝陷阱、Job Object 杀树（心跳文件验证
  孤儿已死）、fence 的 heredoc 陷阱——全部有用例钉死。

## 用法

```bash
go build -o mindloop.exe ./cmd/mindloop

# 日志层
mindloop traj new --slug demo
mindloop traj append <id> message --from operator --content "hello"
mindloop traj tail <id> -n 5 --pretty
mindloop traj merge <child> --to <parent>
mindloop traj check <id>

# 上下文渲染（缓存网格 + 字节预算）
mindloop prompt <id> --format messages --max-bytes 60000

# 运行循环：模型写 bash → Job Object 沙箱执行 → 记录 → FINAL
MINDLOOP_PROVIDER=echo mindloop run <id> "清点当前目录"    # 冒烟
MINDLOOP_PROVIDER=openai-compatible \
MINDLOOP_BASE_URL=https://api.openai.com/v1 \
MINDLOOP_MODEL=gpt-4.1-mini MINDLOOP_API_KEY=sk-... \
mindloop run <id> "统计本目录下 go 文件的总行数"

# 持久心智：身份 + 调度器 + monolith + responder
mindloop identity create ada
MINDLOOP_PROVIDER=echo mindloop mind run ada --watchdog 5m   # 前台运行
mindloop mind say ada "你好，看看你的工作目录"               # 注入人类消息
# 与 ada 对话（推荐入口）：自动启动心智，实时看回复与行动
mindloop chat ada
#   你> 介绍一下你自己
#   ada> 我是 ada……

# 其他心智操作
mindloop mind say ada "你好，看看你的工作目录"   # 说一句，等回复（--no-wait 可不等）
mindloop mind status ada                         # 运行状态 + 最近活动
mindloop mind history ada -n 10                  # 对话记录
mindloop mind run ada                            # 前台守护模式（无交互）
mindloop mind stop ada                           # 优雅停机

# 记忆库：agent 在沙箱里用 $MINDLOOP_EXE mem add 自己写，
# 人用同一套命令读写——工具同时是它的和人的
mindloop mem add --identity ada --type fact "操作员偏好简短回复"
mindloop mem search --identity ada "操作员偏好"
mindloop mem list --identity ada -n 5
mindloop mem forget --identity ada <记忆id>

mindloop tailf <id>                            # 实时跟踪
```

### 持久配置（<home>/.env，完整模板见 `.env.example`）

CLI 启动时自动读取 `MINDLOOP_HOME/.env`（显式环境变量优先）：

```ini
MINDLOOP_MODEL=glm-5
MINDLOOP_BASE_URL=https://open.bigmodel.cn/api/paas/v4
MINDLOOP_API_KEY=你的key
# 思考型模型默认预算 32768；不够时调大
MINDLOOP_MAX_TOKENS=65536
```

LLM 配置以 `MINDLOOP_MODEL` 为核心开关——**provider 一般不用设**，
按模型名自动推断：`claude-*` → anthropic、`echo` → 本地占位、其余 →
openai-compatible（`glm-*` 自动落到智谱端点并套用思考型预算分层）。
`MINDLOOP_PROVIDER` 仅在需要强制覆盖时使用。`MINDLOOP_BASE_URL`、
`MINDLOOP_API_KEY`、`MINDLOOP_MAX_TOKENS` 见 `.env.example`；key 依次
回退 `ANTHROPIC_API_KEY`、`OPENAI_API_KEY`。瞬时错误（网络 / 408 /
429 / 5xx）线性退避重试；网关把错误装进 HTTP 200 的行为会被拆包识别。

## 路线图

1. **[x] 日志层**——属主检查锁、fork/merge、cursor、查询 CLI。
2. **[x] 上下文渲染器**——块对齐截断网格、UTF-8 安全、预算二分。
3. **[x] 运行循环**——Thinker 接口、fence 提取（heredoc 感知）、
   Job Object 沙箱三类超时、FINAL 副作用协议、失速/同命令守卫。
4. **[x] 持久心智**——调度器（feeder/背压二分/watchdog 合成唤醒/
   自发性预约）、monolith（唤醒分类、工作探测、回退策略纯函数）、
   身份目录布局、`mind say` 人类入口、冷启动不重放。
5. **[x] responder 回复**——单次调用低延迟回复、对话历史组装、
   answered 日志事实守卫、陈旧消息守卫、persona 注入。
6. **[x] 记忆**——markdown + frontmatter 存储、写序号单调排序、
   BM25 检索（ASCII 词元 + CJK 二元组）、monolith 唤醒注入相关
   记忆、沙箱内经 `$MINDLOOP_EXE mem add` 自写记忆。
7. **[x] recap 分层上下文**——确定性三条件切窗（间隔/步数/字节）、
   摘要缓存带 provenance 章（model + prompt_version）、增量补齐
   零重算、尾窗不摘要（成本阀）、monolith 上下文 = 人生分集
   （粗层）+ 原文尾窗（细层）。
8. **[x] 观测面**——用量台账（usage/llm-usage.jsonl）、健康标记
   （llm-health.json，连续错误计数）、`mind chat` 对话视图。
9. [ ] Windows 服务 + 仪表盘——mind run 的服务化形态（暂缓）。

## Windows 说明（踩过的坑，测试钉死）

- **删除中的目录**：同名 `mkdir` 返回 `ACCESS_DENIED` 而非
  `AlreadyExists`——锁竞争把它当暂时状态重试。
- **释放锁要短重试**：并发读者握着 `owner.json` 句柄时目录处于
  delete-pending，静默放弃会永久泄漏锁。
- **Job Object 分配窗口**：不在 CREATE_SUSPENDED 下预分配，理论
  上有子进程逃逸的微秒窗口；杀树另有兜底，测试用心跳文件验证
  孤儿确实死了。
- **追加**用单次 `O_APPEND` 写（NTFS `FILE_APPEND_DATA`，对文件末尾
  原子）。
- **bash**：执行语言是 Git Bash（PATH 中的 `bash`），找不到时报
  `ErrNoBash` 并提示安装。

## 开发

```bash
go build ./...   # 编译
go vet ./...     # 静态检查
gofmt -l .       # 格式（应无输出）
go test ./...    # 测试套件
```
