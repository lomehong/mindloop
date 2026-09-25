# 贡献指南

感谢关注 mindloop。开工前请先读 [README.md](README.md) 的「架构」与
「Go 最佳实践清单」两节——本仓库的纪律（依赖单向、库零日志输出、
阻塞操作收 context）是被评审和测试共同守护的约定。

## 开发环境

- **Go 1.24+**。Windows 体验最完整（Job Object 沙箱只有 Windows 能
  完整测试）；Linux/macOS 可编译可运行，沙箱降级为进程级终止。
- **Node 22 + npm**：仅修改仪表盘前端（web/static/app）时需要。

## 常用命令

Go 侧（在仓库根目录）：

```bash
go build ./...   # 编译
go vet ./...     # 静态检查
gofmt -l .       # 格式（应无输出）
go test ./...    # 测试套件
```

前端侧（在 web/static/）：

```bash
npm ci              # 按 package-lock.json 安装（锁文件统一用 npm）
npm run typecheck   # react-router typegen && tsc
npm test            # vitest
npm run build       # 产物在 web/static/build/client，mindloop web 运行时读取
```

改前端不需要重编 Go——产物是运行时从磁盘读取的，重新 build 后刷新
浏览器即可（详见 README「仪表盘：构建与部署」）。

## CI 门禁

push 到 main 与所有 PR 会触发 [.github/workflows/ci.yml](.github/workflows/ci.yml)：

- **go job**（ubuntu + windows 矩阵）：`gofmt -l .`（有输出即失败）、
  `go vet ./...`、`go test ./...`；ubuntu 另跑 `go test -race ./...`。
- **viewer job**：`npm ci` → typecheck → `npm test` → `npm run build`。

PR 若 CI 不绿请勿请求合并。

## 约定

- 分支从 `main` 拉出，PR 保持小步、单一主题；提交信息用祈使句
  （如 `fix: 偷锁前重读 owner.json 防 TOCTOU`）。
- 核心库（internal/ 下除 cli、web 外）不打印日志、不自行起业务
  goroutine、阻塞操作首参 `context.Context`。
- 行为变更必须带测试——本仓库的测试即回归记录，修 bug 请先补一条
  能复现它的用例。
- 用户可见的变更请在 [CHANGELOG.md](CHANGELOG.md) 的 Unreleased 段记一笔。

## 安全问题

不要用公开 issue 报告安全漏洞，见 [SECURITY.md](SECURITY.md)。
