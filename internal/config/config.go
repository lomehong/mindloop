// Package config 提供 .env 持久配置：MINDLOOP_HOME/.env 在 CLI 启动
// 时读入进程环境，显式设置的环境变量永远优先——Headlong 的
// activate 脚本两段加载（应用 .env + 状态 .env）的单文件简化版。
// 格式是 dotenv 子集：KEY=VALUE、# 注释、空白行；值可带引号。
package config

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// LoadEnv 读入 path 指向的 dotenv 文件。文件不存在不是错误。
// 已存在于进程环境的键不会被覆盖——命令行前缀 `MINDLOOP_API_KEY=x
// mindloop ...` 的优先级高于配置文件，这是可预期的最不意外行为。
func LoadEnv(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	first := true
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		// Windows 编辑器常写入 UTF-8 BOM——不清掉会让第一行的键
		// 名带上不可见前缀而静默失效。
		if first {
			line = strings.TrimPrefix(line, "\ufeff")
			first = false
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if k == "" {
			continue
		}
		// 去掉成对引号；行内 # 注释只在裸值时剥离。
		if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
			v = v[1 : len(v)-1]
		} else if idx := strings.Index(v, " #"); idx >= 0 {
			v = strings.TrimSpace(v[:idx])
		}
		if _, exists := os.LookupEnv(k); !exists {
			if err := os.Setenv(k, v); err != nil {
				return err
			}
		}
	}
	return sc.Err()
}

// LoadHome 读入状态根目录下的 .env。
func LoadHome(home string) error {
	return LoadEnv(filepath.Join(home, ".env"))
}
