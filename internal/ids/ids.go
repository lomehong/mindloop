// Package ids 生成日志层所需的标识符。刻意保持零依赖——整个框架
// 只用 Go 标准库构建，正如 Headlong 只需要 bash、curl 和 jq。
package ids

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
)

// NewUUID 返回一个随机的 RFC 4122 版本 4 UUID。步骤 id 与轨迹 id
// 共用同一个命名空间：轨迹的 id 就是它第一行的 step_id。
func NewUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("ids: crypto/rand 失败: %v", err))
	}
	b[6] = (b[6] & 0x0f) | 0x40 // 版本 4
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 变体位
	dst := make([]byte, 36)
	hex.Encode(dst[0:8], b[0:4])
	dst[8] = '-'
	hex.Encode(dst[9:13], b[4:6])
	dst[13] = '-'
	hex.Encode(dst[14:18], b[6:8])
	dst[18] = '-'
	hex.Encode(dst[19:23], b[8:10])
	dst[23] = '-'
	hex.Encode(dst[24:36], b[10:16])
	return string(dst)
}

// Short 返回 id 去掉连字符后的前 n 个十六进制字符——即轨迹目录的
// 命名约定 "<hex8>-<slug>"。
func Short(id string, n int) string {
	s := strings.ReplaceAll(id, "-", "")
	if len(s) < n {
		return s
	}
	return s[:n]
}
