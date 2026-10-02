// Package viewer 提供仪表盘前端构建产物的嵌入视图。
//
// 默认构建（无 release 标签）FS 为 nil——开发态维持磁盘读取热刷新
// （npm run build 后刷新浏览器即生效，无需重编 Go）；`-tags release`
// 构建经 viewer_release.go 把 build/client 全量嵌入二进制，
// go install / 换机分发后仪表盘开箱即用，不再依赖编译期源码路径。
//
// 运行时查找优先级（CLI 侧解析）：显式 --viewer-dir / 环境变量 >
// 本包 FS（release 构建）> 磁盘自动探测链。
package viewer

import "io/fs"

// FS 是嵌入的前端产物根（index.html 在顶层）；nil 表示本次构建未
// 启用嵌入。测试可临时替换。
var FS fs.FS
