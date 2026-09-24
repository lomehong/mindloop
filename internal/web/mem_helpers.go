package web

import (
	"os"
	"path/filepath"
	"strings"
)

// memoryFilename 从 Memory 对象的 Path 字段取 basename（如
// "000025_eeb0820f.md"）。
func memoryFilename(m memoryRow) string {
	i := strings.LastIndex(m.Path, "/")
	if i < 0 {
		i = strings.LastIndex(m.Path, "\\")
	}
	if i < 0 {
		return m.Path
	}
	return m.Path[i+1:]
}

// memorySlug 把 "000025_eeb0820f.md" 截成 "eeb0820f"。
func memorySlug(name string) string {
	n := strings.TrimSuffix(name, ".md")
	if i := strings.LastIndex(n, "/"); i >= 0 {
		n = n[i+1:]
	}
	if i := strings.LastIndex(n, "_"); i >= 0 {
		return n[i+1:]
	}
	return n
}

// readMemoryFile 防路径穿越读记忆文件内容。
func readMemoryFile(identityDir, name string) ([]byte, error) {
	if strings.Contains(name, "..") || strings.Contains(name, "/") || strings.Contains(name, "\\") {
		return nil, os.ErrInvalid
	}
	return os.ReadFile(filepath.Join(identityDir, "memories", name))
}
