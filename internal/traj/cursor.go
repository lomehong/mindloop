package traj

import (
	"bytes"
	"fmt"
	"io"
	"os"
)

// Cursor 只读取文件自上次读取以来追加的字节——Headlong 每个派生
// 视图（桥接、chat 索引、仪表盘缓存）都建立在这个偏移量 cursor
// 原语之上。三条不变量：
//
//   - 只交付完整行。 torn tail（残缺的半行）保持未读，等写者写完
//     后的下一次读取交付。
//   - 文件收缩或被替换时，cursor 归零，让派生视图重建而不是静默
//     错位。
//   - 归零意味着重放：不允许重投旧步骤的 feeder 应按 step id 或
//     ts 过滤——Headlong 的调度器用 rewind 守卫做的正是这件事。
type Cursor struct {
	path   string
	offset int64
	fileID string
}

// NewCursor 返回一个从头开始的 path 的 cursor。
func NewCursor(path string) *Cursor { return &Cursor{path: path} }

// NewCursorAtEnd 返回一个从文件当前末尾开始的 cursor——feeder 的
// 正确起点。Headlong 的教训写在它的桥接代码里：重放历史意味着
// 把 130 条旧消息重投给真实的人，"从来不是任何人想要的"。
// 从 EOF 起步，只看启动之后发生的事。
func NewCursorAtEnd(path string) *Cursor {
	c := NewCursor(path)
	f, err := os.Open(path)
	if err != nil {
		return c
	}
	defer f.Close()
	if id := fileIdentity(f); id != "" {
		c.fileID = id
	}
	if fi, err := f.Stat(); err == nil {
		c.offset = fi.Size()
	}
	return c
}

// LoadCursor 读取保存在 cursorPath 的、针对文件 path 的状态。
// 状态缺失或不可读都意味着"从头开始"；保存的身份与活文件不再
// 匹配时会由下一次 ReadNew 丢弃，绝不会跨文件套用。
func LoadCursor(cursorPath, path string) (*Cursor, error) {
	data, err := os.ReadFile(cursorPath)
	if err != nil {
		if os.IsNotExist(err) {
			return NewCursor(path), nil
		}
		return nil, err
	}
	var offset int64
	var fileID string
	if _, err := fmt.Sscanf(string(data), "%d %s", &offset, &fileID); err != nil {
		return NewCursor(path), nil
	}
	return &Cursor{path: path, offset: offset, fileID: fileID}, nil
}

// Save 把 cursor 状态持久化为一行："<offset> <fileid>"——与
// Headlong 的桥接保存的三元组记录（少一个 size）相同。
func (c *Cursor) Save(cursorPath string) error {
	return os.WriteFile(cursorPath, []byte(fmt.Sprintf("%d %s\n", c.offset, c.fileID)), 0o644)
}

// Offset 返回当前字节偏移量。
func (c *Cursor) Offset() int64 { return c.offset }

// ReadNew 返回自上次调用以来追加的行对应的步骤。nil 切片加 nil
// 错误表示没有新内容（或残缺的半行还没写完）。
func (c *Cursor) ReadNew() ([]Step, error) {
	f, err := os.Open(c.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	id := fileIdentity(f)
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if c.fileID != "" && (id != c.fileID || fi.Size() < c.offset) {
		c.offset = 0 // 被重写或截断：从头重建
	}
	if id != "" {
		c.fileID = id
	}
	if fi.Size() == c.offset {
		return nil, nil
	}

	if _, err := f.Seek(c.offset, io.SeekStart); err != nil {
		return nil, err
	}
	buf, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	idx := bytes.LastIndexByte(buf, '\n')
	if idx < 0 {
		return nil, nil // 目前只有残缺的半行
	}
	complete := buf[:idx+1]
	c.offset += int64(len(complete))

	var steps []Step
	for _, ln := range bytes.Split(complete, []byte{'\n'}) {
		ln = bytes.TrimSpace(ln)
		if len(ln) == 0 {
			continue
		}
		s, err := ParseStep(ln)
		if err != nil {
			continue // 与 Steps 相同的容错
		}
		steps = append(steps, s)
	}
	return steps, nil
}
