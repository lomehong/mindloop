package web

import "strings"

// isBadIdentSegment 拒掉含路径穿越的段名——`../etc`（字符白名单通
// 过，因为 "." 是允许的）必须在这里被拒。/ 是 URL 分隔符已被 split
// 消化掉，所以单段只可能含子字符级穿越。
func isBadIdentSegment(s string) bool {
	if s == "" || len(s) > 64 {
		return true
	}
	if s == "." || s == ".." {
		return true
	}
	if strings.Contains(s, "..") {
		return true
	}
	return false
}
