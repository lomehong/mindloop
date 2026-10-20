// robotd 是 mindloop "身"的独立 MCP server：屏幕的看与触，stdio
// 传输。独立成二进制是设计红线（动作面与心智进程隔离——即便心智
// 被攻破，能碰到桌面动作的进程面也最小）；与主二进制共享
// internal/robot 与 internal/robotd 的全部实现，无 cgo。
//
// Claude Desktop / Cursor 接入示例（claude_desktop_config.json）：
//
//	{ "mcpServers": { "robotd": {
//	    "command": "robotd",
//	    "args": ["--window-allow", "记事本"] } } }
//
// 不给 --window-allow 即观察模式：截屏可用，动作一律拒绝。
//
// 青鸟（Nuwa 采集 SDK v0.2）挂载五挂点中的两类：panic 捕获（同步
// 上报后原样重抛——采集不改变进程的失败语义）与启动失败（同步上报
// 后 flush 退出）。NUWA_INGEST_URL / NUWA_INGEST_TOKEN 指向 Nuwa
// 服务端。
package main

import (
	"context"

	"github.com/lomehong/qingniao-go"

	"github.com/lomehong/mindloop/internal/robotd"
)

const version = "0.15.0"

func main() {
	q := qingniao.NewFromEnv(qingniao.Meta{
		Repo:    "github.com/lomehong/mindloop",
		Service: "robotd",
	})
	defer q.Close(context.Background())

	defer q.Capture(func() {
		if err := robotd.RunStandalone(context.Background(), "robotd", version); err != nil {
			// 启动失败（配置/端口/权限）不是 panic——走 Fatal：同步
			// 上报、flush 队列后退出，失败语义不丢。
			q.Fatal("robotd exited with error", err)
		}
	})
}
