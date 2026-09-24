package identity

import "errors"

// ErrNotFound 报告指定名字的身份不存在。
var ErrNotFound = errors.New("identity: 不存在")
