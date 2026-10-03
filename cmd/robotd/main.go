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
package main

import (
	"context"

	"github.com/lomehong/mindloop/internal/robotd"
)

const version = "0.1.0"

func main() {
	robotd.RunStandalone(context.Background(), "robotd", version)
}
