package traj

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"mindloop/internal/ids"
)

// Home 返回状态根目录：设置了 $MINDLOOP_HOME 则用它，否则用
// ~/.mindloop（Windows 上是 %USERPROFILE%\.mindloop）。环境变量覆盖
// 是从 Headlong 继承的替换点：状态的每个消费者都能被指到别处，
// 而无需改动工具本身。
func Home() string {
	if h := os.Getenv("MINDLOOP_HOME"); h != "" {
		return h
	}
	if uhd, err := os.UserHomeDir(); err == nil {
		return filepath.Join(uhd, ".mindloop")
	}
	return ".mindloop"
}

// TrajRoot 是轨迹的存放处：<home>/trajectories。
func TrajRoot() string { return filepath.Join(Home(), "trajectories") }

// Slugify 转小写并只保留 [a-z0-9._-]，把其他连续字符折叠为单个
// 连字符，并修剪首尾连字符，上限 max 个字符。slug 用于命名目录；
// 绝不让无界或对路径不友好的文本进入目录名。
func Slugify(s string, max int) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_':
			if dash {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(r)
		default:
			dash = b.Len() > 0
		}
	}
	out := strings.Trim(b.String(), "-")
	if max > 0 {
		if r := []rune(out); len(r) > max {
			out = string(r[:max])
		}
	}
	return out
}

// DirName 是轨迹在磁盘上的目录名："<hex8>-<slug>"。8 位十六进制
// 前缀是稳定标识；slug 只是给人浏览目录时看的便利。
func DirName(id, slug string) string { return ids.Short(id, 8) + "-" + slug }

// rel 计算两个绝对目录之间的相对路径。所有跨轨迹引用都以
// "UUID + 相对路径"的形式存储（Headlong 的 id、step id、_ref 三元组），
// 这样整个状态根目录被移动或复制时图不会断。
func rel(from, to string) (string, error) {
	r, err := filepath.Rel(from, to)
	if err != nil {
		return "", fmt.Errorf("mindloop: 相对路径 %s -> %s: %w", from, to, err)
	}
	return r, nil
}
