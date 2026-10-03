# 设计：系统宿主（system host）——声明式能力面

状态：v1 实施（2026-10-03 用户拍板"配置期望状态，系统负责兑现和
维持"）。依赖：internal/service（schtasks 装机）、web（多身份
API）、三套配置热加载（sensors/schedule/bridge）。
关联：`roadmap.md` §3（服务化补强）、`perception.md`（sensors.json）

## 1. 问题：命令动物园

能力面 = 命令集 + 进程集：`mind run`（心智）、`web`（仪表盘）、
`connector run`（渠道桥）各自启动、各自生命周期；service 按
身份×组件装 3 个 schtasks（装机时固化，改什么跑 = 重装）。
使用成本随能力数线性上涨——用户要的是"声明我要什么，系统负责
兑现与维持"。

## 2. 模型：一个宿主 + 一份总配置

**`mindloop system run`** 是唯一的常驻宿主：

- 读 `<home>/system.json`（能力总开关，版本化 schema）：
  ```json
  {"version": 1,
   "web":  {"enabled": true, "host": "127.0.0.1", "port": 8080},
   "identities": {"ada": {"enabled": true, "mind": true, "wecom": true}}}
  ```
- 按 config 派生**期望子进程集**并监督：`web` 一份（多身份已支持）、
  每身份 `mind run <名>`、每身份×渠道 `connector run --identity <名>`
  （子进程 = 既有命令，桥与心智的进程隔离不变式保留）。
- 监督语义：子进程退出即指数退避重启（1s→60s，稳定 5 分钟清零）；
  system.json 变更 5 秒内热加载，按 diff 增停启；宿主退出时 Windows
  Job Object（KILL_ON_JOB_CLOSE）连带收割全部子进程——孤儿不会
  占住运行锁饿死重启后的孩子。
- 状态投影：`<home>/system-status.json`（每个孩子 running/pid/
  restarts/lastExit/since，原子写）——仪表盘与 CLI 的 status 都是
  它的视图（视图皆派生）。
- **零配置即用**：system.json 缺失时按现状推导缺省（web 开在
  127.0.0.1:8080、全部身份开 mind、有企微凭据的身份开 wecom）。

配置即数据：sensors.json/schedule.json/.env 等细节配置仍是各能力
的事实源，system.json 只管"什么在跑"。仪表盘与 CLI 都是它的视图。

## 3. 统一配置面（仪表盘系统页）

- `/system` 服务端直出页（SPA 集成后续立项）：能力总览（宿主
  状态投影 + system.json）+ 按身份的开关（mind/wecom）+ 感官管理
  （列表/启停/增删）。
- API：`GET/PUT /api/system`（读/写 system.json，写走既有校验）；
  `GET /api/identities/{名}/sensors`、`PUT .../sensors/{id}/enabled`、
  `POST .../sensors`、`DELETE .../sensors/{id}`（与 CLI 同一份
  sensor 包校验）。
- 敏感红线沿用：channels 页的 env 红线（凭据不回显）。

## 4. 服务化迁移

`mindloop service install --host`：装**一个**任务包装
`system run`（组件常量新增 ComponentSystem）；旧的按身份×组件
安装保留兼容。装机即"系统常驻"，此后一切能力增减走配置。

## 5. CLI 的位置

`system run/status` 是宿主运维（配置面没有独立子命令：直接编辑
system.json，或仪表盘 `/system` 页走 GET/PUT /api/system）；既有命令
全部保留原语义（chat 仍可接管对话、doctor/stats/undo/approve 仍是
诊断与控制面）——但它们不再是"获得功能"的必需品。

## 6. 非目标

- 不做多宿主（一个 home 一个宿主；运行锁仍是单实例权威）；
- 不做跨机器编排；
- 不在宿主内嵌能力逻辑（宿主只监督进程，桥/心智隔离不变式保留）；
- SPA 系统页（React）后续立项，本期服务端直出页先行。
