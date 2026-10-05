# mindloop Desktop（桌面壳）

Rust/Tauri 2.x 桌面壳：无边框窗口承载 mindloop 仪表盘 + 托盘常驻 +
sidecar 进程托管。**Go 后端零改动**——WebView 内直连
`http://127.0.0.1:<port>`，同源访问 Go 服务的 /api，凭据沿用仪表盘
自带的「访问凭据」控件（WebView2 localStorage 持久化）。

## 进程模型（探测优先）

1. 启动时依次探测 8080、8081–8089：命中 mindloop web（HTTP 200 且
   正文含 mindloop 特征）→ **直连已有实例**（含计划任务服务化的
   web），托盘不提供「停止」项——计划任务的 RestartOnFailure 会立刻
   复活它，壳不去碰。
2. 没有现成实例 → 从内嵌 sidecar 拉起 `mindloop web --no-build
   --no-open --port <空闲口> --viewer-dir <内置资源>`。
   - 子进程纳入 Windows Job Object（KillOnJobClose）：壳无论正常退出
     还是崩溃，sidecar 随之终止，不遗留孤儿。
   - 意外退出自动重启，至多 3 次、每次退避 2 秒。
3. 关闭窗口 = 缩入托盘（mindloop 继续运行）；托盘「退出」会弹确认
   （可勾选记住），确认后停止壳拉起的 sidecar 并退出壳。
   mind/connector 不归壳管——它们由计划任务或仪表盘控制，web 停止
   不影响在途心智。

## 构建与运行

依赖：Go、bun、Rust（MSVC）、WebView2 Runtime（Win11 自带，Win10 缺
失时 NSIS 安装包会自动下载引导）。

```powershell
cd desktop
powershell -ExecutionPolicy Bypass -File .\build.ps1          # 完整打包（NSIS）
powershell ... -File .\build.ps1 -SkipBundle                  # 只构建，快速验证
powershell ... -File .\build.ps1 -Dev                         # 开发模式（tauri dev）
```

build.ps1 做三件事：bun 构建 viewer → `go build` sidecar 到
`src-tauri/binaries/mindloop-x86_64-pc-windows-msvc.exe` → 同步
viewer 静态资源到 `src-tauri/viewer-static/` → `tauri build`。

**改了 Go 代码后**：重跑 build.ps1（或只跑其中 go build 一段）刷新
sidecar，再重启壳——壳永远使用打包时的 sidecar 快照。

## 产物

- NSIS 安装包：`src-tauri/target/release/bundle/nsis/`
- 便携形态：`src-tauri/target/release/` 下 `mindloop.exe`（壳）+
  `mindloop.exe`… 见 bundle 输出说明——安装目录里的文件整体拷走即
  是便携版（壳、sidecar、viewer 资源同目录）。

## 配置

`%APPDATA%/com.mindloop.desktop/config.json`：

```json
{ "preferred_port": 8080, "confirm_quit": true }
```

## 结构

```
desktop/
  src/                  chrome 页（vanilla：标题栏/状态点/boot 遮罩/退出对话框）
  src-tauri/
    src/main.rs         入口：单实例、窗口事件、命令、监督循环
    src/proc.rs         sidecar 解析/拉起/停止
    src/health.rs       探测与特征判定（/ 或 /sw.js 200 且含 mindloop）
    src/job.rs          Job Object（KillOnJobClose）
    src/tray.rs         托盘图标与菜单
    src/pet.rs          宠物窗口（透明置顶小窗，装载 /pet 页面）
    src/config.rs       用户设置
```

## 宠物窗口

托盘菜单「宠物」（后端运行中才可点）或启动参数 `--pet` 开/关一个
260×300 的透明置顶悬浮窗，装载 web 端 `/pet` 页面——圆润 SVG 小
生物：果冻身体、大眼睛、头顶小苗；轨道粒子 = 忙碌思考者，涟漪 =
说话；心情是忙闲/预算/健康的派生投影，主动播报警情/任务完成/审批
待办（设计见 `docs/designs/pet.md`，E2E 场景 S15）。

交互约定：**拖小生物本体 = 移动窗体**（位置记忆；空白区是点击穿透
的，拖不动它），单击本体 = 快捷菜单（说话/戳醒/勿扰/隐藏），双击 =
打开仪表盘，光标移出本体与菜单后整窗点击穿透（不挡桌面操作）。
窗体的窗体控制权限集中在 `capabilities/default.json`（main + pet 联合
能力；页面远程加载自本地后端，Tauri 2 对远程源 IPC 默认全拒——缺权限
拖动/穿透/隐藏会整体哑掉。合并缘由：pet webview 存在时 wry 的 IPC ACL
会错拿其标签判定，联合能力使任一标签下都可放行）。两条兜底：关窗 = 缩起（托盘唤回）；页面进入点击穿透
后收不到鼠标事件，托盘「宠物」每次显示前都会强制解除穿透，永远是
摸得着的退路。后端中途停止时窗体自行退化为「未连接」灰团。

## 已知限制（v1）

- 仪表盘内 `target="_blank"` 外链（如「在 Slack 中打开」）在 WebView2
  内的弹出行为未做拦截处理，可能以弹窗形式出现；系统浏览器打开留待
  v1.1 接 wry 新窗口处理。
- 系统通知依赖 WebView2 对 iframe 的权限策略，可能降级为应用内铃铛。
- v1 仅 Windows 10/11 x64。
