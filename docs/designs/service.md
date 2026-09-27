# 设计：服务化（Windows 任务计划程序宿主）

状态：v1（2026-09-27）。目标平台 Windows 10/11（项目的一等公民）；本设计
即 connectors.md §6.1 预留的"Windows 服务化立项"，也是
proactive-reporting 全链路（schedule → 心智 → 企微）从"挂在开发 shell
下"变成"无人值守"的前置。
依赖：无新核心概念；只动 CLI 装配层（internal/cli/servicecmd.go +
internal/service）。
关联：`internal/cli/mindcmd.go`（mind stop 优雅停机）、
`internal/cli/connectorcmd.go`（run-all 零配置干净退出）、
`internal/cli/webcmd.go`（仪表盘）、`internal/mind/runlock.go`（运行锁）。

## 1. 目标与非目标

**目标**：三个常驻进程——`mind run`、`connector run-all`、`web`——
脱离开发终端：登录自启、崩溃自动重启（带间隔退避）、刻意停止不被
复活、一条命令安装/卸载/查看状态。全部 OS 原生机制，零第三方依赖。

**非目标**：
- 不为服务化改动 mind/connector/web 任何一行核心逻辑——服务化是
  装配层的事；
- 不自研监督进程——监督、重启、自启全部委托 OS 宿主（§2 的形态
  决策）；
- 不做登录前 boot-start（需要服务账户/凭据决策，§8 开放问题）；
- 不做"死睡外部告警"（headlong thinkers-silence 的对应物，§8）；
- 不做 Linux/macOS（systemd user units 是后续小立项，§8）。

## 2. 形态决策：任务计划程序，每组件一个任务

对标 headlong `deploy/`：每组件一个 systemd unit
（thinkers@<identity> / web / telegram-bridge…），Restart=on-failure +
RestartSec、刻意停止走干净退出码让 systemd 放手、StartLimit 窗口 +
OnFailure 告警、cgroup 隔离、journald 日志。没有自研监督者。

Windows 上的两个候选：

| 候选 | 问题 |
|---|---|
| SCM 服务（`golang.org/x/sys/windows/svc` 或手写 SCM 协议） | ① 引第三方依赖或手写一套回调协议；② 服务账户与 `MINDLOOP_HOME`（用户目录下）错位——SYSTEM 账户看不到用户的 .env/凭据，改用本账户又要存密码；③ 安装/卸载必须管理员 |
| **任务计划程序（Task Scheduler）** | ✅ OS 原生；InteractiveToken 任务免管理员、直接以安装者身份运行（.env、目录、网络全对）；RestartOnFailure 内建；`schtasks` CLI 齐全 |

**决策：任务计划程序，一组件一任务。** headlong 的单元形态照搬，
cgroup 隔离的对应物是"每组件独立任务 + IgnoreNew 单实例"；journald
的对应物是 `cmd /c` 重定向的每组件日志文件（§5）。

**实证钉死的前提**（2026-09-27 本机验证）：免管理员注册任务
**必须**在 XML 里显式写 `<Principals>`（UserId + LogonType=
InteractiveToken + RunLevel=LeastPrivilege）**且** LogonTrigger 里带
`<UserId>`——省略 Principal 的裸 XML 一律 Access denied（默认语义
变成"任何用户"，需要特权）。`schtasks` 不支持 `/tn "Folder\Task"`
自动建文件夹——任务名用平铺（§3）。

## 3. 任务命名与动作

```
Mindloop-<identity>-mind       →  <exe> mind run <identity>
Mindloop-<identity>-connector  →  <exe> connector run-all <identity>
Mindloop-<identity>-web        →  <exe> web --no-build --no-open
```

- `<exe>` 是 install 时 `os.Executable()` 的绝对路径——**任务绑定
  exe 位置**：换位置/重命名 exe 后任务会失效（status 可见
  LastResult 非 0），重装即修复。
- `--no-build`：启动路径上不允许出现 npm 子进程（开机即构建是
  事故源）；构建产物缺失时 web 照旧降级为 API-only 并在日志里
  提示，install 时预检并警告。
- `--no-open`：web 新增旗标——服务模式下不自动开浏览器（每次登录
  触发弹一个浏览器窗口不可接受）。
- connector 零渠道时干净退出（exit 0，connectorcmd.go:229）——
  没配渠道的任务空转不重启；RestartOnFailure 只在非零退出时触发。
- WorkingDirectory = `MINDLOOP_HOME`（`traj.Home()`，install 时
  固化为绝对路径）——日志相对落点稳定。

## 4. 任务设置：headlong unit 语义的逐项移植

| 任务计划程序 | headlong unit | 理由 |
|---|---|---|
| `RestartOnFailure Interval=PT1M Count=999` | `Restart=on-failure` + `RestartSec=60` | 崩溃循环的每次重启都是一轮 LLM 成本——1 分钟退避是 headlong 同款数字。Count 是**总次数**而非滑动窗口（与 systemd StartLimit 15min×3 的差异如实接受：耗尽后停在原地，`service start` 手动复活；告警属 §8 后续）。**既有限制（MS 文档化）**：只对触发器拉起的实例生效——登录自启的进程崩溃会自动重启；手动 `/run`（`service start`）拉起的实例失败不自动重启，需再跑一次 start |
| `ExecutionTimeLimit=PT0S` | （systemd 默认无时限） | 默认 72h 时限会腰斩长驻进程——必须显式关掉 |
| `MultipleInstancesPolicy=IgnoreNew` | 单实例语义 | web/connector 无运行锁，IgnoreNew 是唯一防双开；mind 另有运行锁双保险 |
| `DisallowStartIfOnBatteries=false` `StopIfGoingOnBatteries=false` | — | 笔记本场景：电池/省电不停心智 |
| `StartWhenAvailable=false` | 冷启动不重放 | 错过的登录触发**不补跑**——宿主侧同构："重放历史从来不是任何人想要的" |
| `AllowHardTerminate=true` | KillMode | `/end` 兜底可用 |
| LogonTrigger（UserId=安装者） | WantedBy + 用户会话 | 覆盖"登录即在线"；boot-start 是 §8 开放问题 |

## 5. 包装批处理与日志

任务动作直接调用 install 生成的包装批处理
`<MINDLOOP_HOME>/run/<component>.cmd`：

```bat
@echo off
chcp 65001 >nul
rem mindloop mind wrapper（service install 生成；手工改动会被下次 install 覆盖）
set "MINDLOOP_HOME=C:\Users\lome\.mindloop"
"C:\...\mindloop.exe" mind run ada >> "C:\...\logs\mind.log" 2>&1
```

- **环境自包含（实证教训，2026-09-27 探针任务抓到）**：计划任务
  进程看不到安装 shell 的环境变量——首版把动作写成 `cmd /c` 内联
  重定向，任务进程直接漂到默认 HOME 找身份而失败。MINDLOOP_HOME
  必须钉死在批处理里，任务定义才能脱离安装会话独立存在。
- 批处理就是 Windows 原生的 unit 文件：可读、可手工临时改（下次
  install 覆盖），并整体避开了 `cmd /c` 嵌套引号的转义地狱。
- `chcp 65001` 让后续行按 UTF-8 解析（中文路径不乱码）；前两行
  保持 ASCII。% 在 cmd 里是变量展开符，路径含 % 视为畸形不处理。
- journald 的对应物 = 追加日志文件（stderr+stdout 全捕获），不
  轮转（与轨迹同哲学；手工清理）。

**install 预检**（只警告不阻止）：viewer 构建产物缺失 → web 降级
API-only；渠道未配置 → connector 干净退出（不触发重启）；web 默认
端口 127.0.0.1:8080 被占用 → 失败任务会按 1 分钟退避反复重试刷
日志（真实发生：探针 web 撞上用户在跑的仪表盘）。

## 6. 生命周期：刻意停止的移植

headlong 的关键机制："Restart=on-failure 复活不了刻意停止的心智
——每条受认可的停止路径都标记 deliberate_stop 让进程干净退出，
干净的退出 systemd 放手不管"。任务计划程序的对应语义：

| 命令 | 动作序列 | 对应语义 |
|---|---|---|
| `service start` | `/enable` → `/run` | 复活被刻意停止的任务 |
| `service stop` | ① mind：`mind stop <identity>`（停机标志，调度器 200ms 心跳内收尾，干净退出）；10s 超时或 connector/web：`/end` 硬杀（三组件都按崩溃窗口设计：cursor 三态、追加轨迹、Job Object 收树——硬杀是设计内的输入，不是异常）② `/disable` | **刻意停止标记**：禁用防住 RestartOnFailure 的非零退出复活，也防住下次登录自启——停了就是停了，跨重启持续 |
| `service restart` | stop → start | — |
| `service status` | 见下 | — |
| `service uninstall` | `/end`（若在跑）→ `/delete` ×3 | 幂等：任务不存在不是错误 |

**status** 的信息源刻意避开 `schtasks /query /v` 的本地化输出
（中文系统输出中文标签，不可解析）：

- `powershell -NoProfile Get-ScheduledTask` / `Get-ScheduledTaskInfo`
  → State 枚举（Ready/Running/Disabled，英文稳定）+ LastTaskResult
  （数字）+ LastRunTime，`ConvertTo-Json` 交给 encoding/json；
- mind 另做运行锁探测（进程真实存在与否，query 只反映任务视角）。

## 7. 验收清单

2026-09-27 真机（Windows 11，免管理员）逐项实证：

- [x] install：三个任务注册成功（免管理员），query 可见，
      logs/ 与 run/ 目录建立，包装批处理落盘且 MINDLOOP_HOME 钉死
- [x] install 预检：身份目录校验；渠道未配置警告；端口占用警告
- [x] start：任务转 Running/按动作执行；组件日志捕获 stderr
      （connector 零渠道干净退出 0——不触发重启循环；mind 缺模型
      配置 exit 1；web 撞端口报错退出）
- [x] stop：全员转 Disabled（刻意停止标记），不被复活，下次登录
      也不自启；mind 不在跑时优雅停机路径快速通过
- [x] uninstall：三个任务消失；对不存在任务幂等
- [x] XML 生成 golden 测试（UTF-16LE BOM、Principal、转义）+
      包装批处理测试（参数/日志/环境钉死/CRLF）；生命周期经
      Execer fake 全覆盖，Linux CI 可跑
- [ ] **登录触发拉起后的崩溃自动重启**：RestartOnFailure 机制对
      触发器实例生效（MS 文档），待下次真实登录观察确认；手动
      /run 实例不重启是文档化限制（§4）
- [ ] 沙箱凭据隔离不受影响（服务化不新增环境变量注入——包装
      批处理只设 MINDLOOP_HOME，凭据仍在身份 .env）

## 8. 开放问题

1. **boot-start（登录前在线）**：需要"以哪个身份跑"的决策——S4U
   （免存密码但无网络凭据，出站 TLS 可用）、存密码（schtasks 不
   支持，要 PowerShell `/ru /rp` 或 COM）、SYSTEM（MINDLOOP_HOME
   得迁出用户目录）。个人桌面场景登录触发已够，等真实需求。
2. **死睡告警**（headlong thinkers-silence/timer 的对应物）：外部
   定时任务检测"轨迹 N 小时无新步骤"→ 企微提醒。连接器独立于
   mind 存活，天然可行；唤醒预算与告警去重策略待设计。
3. **重启耗尽告警**（headlong OnFailure）：Count=999 耗尽后任务
   停在原地无人知晓——同死睡告警一起做（都是"外部检查者"角色）。
4. **Linux/macOS**：systemd user units 生成器（`service install`
   同一命令面，写 3 个 .service 到 ~/.config/systemd/user +
   `systemctl --user enable --now`）。
5. **升级路径**：exe 原地替换后任务无需重装（路径不变）；改名/
   挪位置需 `service install` 重跑——要不要 `service status`
   检测 exe 失效并提示重装？
