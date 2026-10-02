//go:build release

// release 构建的嵌入实现。编译前必须先构建前端
// （cd web/static && npm run build）——产物缺失时本文件无法编译，
// 这正是 release 构建的硬门槛：二进制里的前端永远与构建时点一致。
package viewer

import (
	"embed"
	"io/fs"
)

//go:embed all:build/client
var embedded embed.FS

func init() {
	sub, err := fs.Sub(embedded, "build/client")
	if err != nil {
		// all: 嵌入成功的前提下 Sub 不会失败；真发生了就保持
		// FS 为 nil，让上层回退磁盘查找，而不是拒绝启动。
		return
	}
	FS = sub
}
