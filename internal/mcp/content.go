package mcp

import (
	"encoding/base64"
	"fmt"
)

// ImageContent 是 MCP 标准图片内容块（{"type":"image","mimeType",
// "data"}）：Data 是 base64 编码的图片字节。
type ImageContent struct {
	MIMEType string `json:"mimeType"`
	Data     string `json:"data"`
}

// Validate 检查图片块可装配（MIME 白名单 + base64 合法 + 大小上限）。
func (ic ImageContent) Validate() error {
	switch ic.MIMEType {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
	default:
		return fmt.Errorf("mcp: 不支持的图片类型 %q", ic.MIMEType)
	}
	raw, err := base64.StdEncoding.DecodeString(ic.Data)
	if err != nil {
		return fmt.Errorf("mcp: 图片数据不是合法 base64: %w", err)
	}
	if len(raw) == 0 {
		return fmt.Errorf("mcp: 图片数据为空")
	}
	const maxImageBytes = 8 << 20
	if len(raw) > maxImageBytes {
		return fmt.Errorf("mcp: 图片 %d 字节超过上限 %d", len(raw), maxImageBytes)
	}
	return nil
}

// ToolResult 是工具的富结果：文本之外可携带图片内容块（robotd 的
// 触觉回读截屏走这里）。返回 ToolResult{Text: s} 即等价于旧的一行
// 文本形态。
type ToolResult struct {
	Text   string
	Images []ImageContent
}

// NewTextResult 是单行文本结果的便捷构造。
func NewTextResult(s string) ToolResult { return ToolResult{Text: s} }

// contentBlocks 把富结果转成 MCP content 数组。图片块在验证失败时
// 降级为文本占位——协议响应里绝不能出现坏块（客户端会整条丢弃）。
func (tr ToolResult) contentBlocks() []map[string]any {
	blocks := make([]map[string]any, 0, 1+len(tr.Images))
	if tr.Text != "" || len(tr.Images) == 0 {
		blocks = append(blocks, map[string]any{"type": "text", "text": tr.Text})
	}
	for _, img := range tr.Images {
		if err := img.Validate(); err != nil {
			blocks = append(blocks, map[string]any{"type": "text", "text": "[图片内容块被丢弃: " + err.Error() + "]"})
			continue
		}
		blocks = append(blocks, map[string]any{
			"type":     "image",
			"mimeType": img.MIMEType,
			"data":     img.Data,
		})
	}
	return blocks
}
