# AGENTS.md — mindloop 仓库 agent 操作规程

给在本仓库工作的 AI coding agent（包括 Nuwa 修复 runner）。

## 仓库是什么

mindloop：Windows 常驻机器人（robotd，stdio MCP server + web 面板）。Go 后端 + React Router 前端（web/static）。本仓库是 Nuwa 自愈流水线的第一个试点目标仓库。

## 提交前必跑（CI 门禁同款，缺一不可）

```bash
go build ./...
go vet ./...
gofmt -l .          # 有输出即失败——先 gofmt -w 修再提交（CI 红过一次：手写代码未过 gofmt 就推送）
go test ./...
```

注意：本机 `go test ./...` 会扫到 `web/static/node_modules` 里的第三方 Go 包（FAIL/噪音可忽略，git 忽略该目录，CI 不存在）；真失败看 `./cmd/... ./internal/...`。

前端改动追加（在 web/static 下）：

```bash
npm ci && npx tsc --noEmit && npm run lint && npm test && npm run build
```

## 硬规则

1. **gofmt 零容差**：`.gitattributes` 钉了 LF；提交前 `gofmt -l .` 必须无输出。
2. **Windows 是一等公民**：Job Object 沙箱只有 Windows 能测到，跨平台改动必须双端自检。
3. **seed panic 纪律**：`cmd/robotd/main.go` 的演示种子（若存在）仅用于 Nuwa 闭环验证，修复 PR 合并即移除；不得新增其他故意缺陷。
4. **nuwa.repo.json 是 runner 指令文件**：`failing_check` 命令是 runner 的验证门与 CI 的对齐基准；改门禁命令时两处同步。
5. 一次提交一个完整变更，message 写清 what + why。
