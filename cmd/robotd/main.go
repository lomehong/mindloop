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
// 青鸟（Nuwa 采集 SDK）已挂载：panic 被捕获后上报自愈流水线
// （NUWA_INGEST_URL / NUWA_INGEST_TOKEN），随后原样重抛——采集
// 不改变进程的失败语义。
package main

import (
	"context"
	"os"

	"github.com/lomehong/qingniao-go"

	"github.com/lomehong/mindloop/internal/robotd"
)

const version = "0.1.0"

func main() {
	sink := qingniao.HTTPSink{
		Endpoint: envOr("NUWA_INGEST_URL", ""),
		Token:    os.Getenv("NUWA_INGEST_TOKEN"),
	}
	qingniao.Capture(sink, qingniao.Meta{
		Repo:    "github.com/lomehong/mindloop",
		Service: "robotd",
	}, func() {
		// 临时演示种子：验证 Nuwa 自愈闭环（修复 PR 合并即移除本行）。
		var windows []string
		_ = windows[len(windows)] // index out of range
		robotd.RunStandalone(context.Background(), "robotd", version)
	})
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
