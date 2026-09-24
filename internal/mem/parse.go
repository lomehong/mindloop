package mem

import (
	"os"
	"strconv"
	"strings"
	"time"

	"mindloop/internal/traj"
)

const trajTimeFormat = traj.TimeFormat

// parseFile 解析一个记忆文件：YAML frontmatter 子集（key: value 行）
// + 正文。frontmatter 的值如果是 Go 风格引号串则去引号。
func parseFile(path string) (Memory, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Memory{}, err
	}
	m := Memory{Path: path}
	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return Memory{}, errBadFrontmatter
	}
	i := 1
	for ; i < len(lines); i++ {
		ln := strings.TrimRight(lines[i], "\r")
		if strings.TrimSpace(ln) == "---" {
			i++
			break
		}
		k, v, ok := strings.Cut(ln, ":")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if unquoted, err := strconv.Unquote(v); err == nil {
			v = unquoted
		}
		switch k {
		case "id":
			m.ID = v
		case "type":
			m.Type = v
		case "summary":
			m.Summary = v
		case "created":
			if ts, err := time.Parse(trajTimeFormat, v); err == nil {
				m.Created = ts
			}
		}
	}
	if m.ID == "" {
		return Memory{}, errBadFrontmatter
	}
	m.Content = strings.TrimSpace(strings.Join(lines[i:], "\n"))
	return m, nil
}

var errBadFrontmatter = fmtError("mem: frontmatter 缺失或无 id")

func fmtError(s string) error { return &simpleError{s} }

type simpleError struct{ msg string }

func (e *simpleError) Error() string { return e.msg }

// firstLine 取首行作为摘要。
func firstLine(s string) string {
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		s = s[:idx]
	}
	return strings.TrimSpace(s)
}

// quoteYAML 给含特殊字符的值加引号（自产自销：解析端会去引号）。
func quoteYAML(s string) string {
	if strings.ContainsAny(s, ":#\n\"'") || strings.HasPrefix(s, " ") || strings.HasSuffix(s, " ") {
		return strconv.Quote(s)
	}
	return s
}
