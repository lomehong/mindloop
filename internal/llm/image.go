// 图片消息：Message.Images 携带 base64 图片，装配时按供应商转成
// 各自的 wire 形状（openai-compatible 的 image_url data URL，
// anthropic 的 base64 source 块）。无图消息的序列化与历史行为
// 完全一致——纯文本路径零风险，这是"触觉回读"的字面通道前提。
package llm

import (
	"encoding/base64"
	"fmt"
)

// Image 是消息携带的一张图片：Data 是 base64 编码的图片字节（不带
// data: 前缀——两家协议的包裹形状由本包在装配时添加）。
type Image struct {
	MIMEType string `json:"mime_type"`
	Data     string `json:"data"`
}

// MaxImageBytes 是单张图片的原始字节上限：超限在装配时报错而不是
// 发出注定被供应商拒绝的巨型请求（anthropic 单图 5MB，openai 系
// 同量级）。
const MaxImageBytes = 5 << 20

// NewImage 从原始字节构造图片，base64 编码在此完成。
func NewImage(mimeType string, raw []byte) Image {
	return Image{MIMEType: mimeType, Data: base64.StdEncoding.EncodeToString(raw)}
}

// imageMIMETypes 是两家供应商共同接受的图片 MIME 白名单——装配时
// 校验；白名单之外的类型开到供应商面前只会收获一句含混的 400。
var imageMIMETypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
	"image/webp": true,
}

// validate 拒绝装配不了的图片：MIME 不在白名单、base64 非法或
// 解码后超过 MaxImageBytes。
func (img Image) validate() error {
	if !imageMIMETypes[img.MIMEType] {
		return fmt.Errorf("llm: 不支持的图片类型 %q（接受 image/png、image/jpeg、image/gif、image/webp）", img.MIMEType)
	}
	raw, err := base64.StdEncoding.DecodeString(img.Data)
	if err != nil {
		return fmt.Errorf("llm: 图片数据不是合法 base64: %w", err)
	}
	if len(raw) == 0 {
		return fmt.Errorf("llm: 图片数据为空")
	}
	if len(raw) > MaxImageBytes {
		return fmt.Errorf("llm: 图片 %d 字节超过单图上限 %d——截屏请先降分辨率或转 JPEG", len(raw), MaxImageBytes)
	}
	return nil
}

// wireMessage 把一条消息转成 provider 的 wire 形状。无图消息两家
// 协议同形（content 是纯字符串，与历史请求逐字节一致）；带图消息
// 的 content 变内容块数组，文本块在前（有图消息几乎总是"看这个 +
// 问题"的顺序）。
func wireMessage(provider string, m Message) (map[string]any, error) {
	if len(m.Images) == 0 {
		return map[string]any{"role": m.Role, "content": m.Content}, nil
	}
	parts := []map[string]any{}
	if m.Content != "" {
		parts = append(parts, map[string]any{"type": "text", "text": m.Content})
	}
	for _, img := range m.Images {
		if err := img.validate(); err != nil {
			return nil, err
		}
		switch provider {
		case ProviderOpenAICompat:
			parts = append(parts, map[string]any{
				"type":      "image_url",
				"image_url": map[string]any{"url": "data:" + img.MIMEType + ";base64," + img.Data},
			})
		case ProviderAnthropic:
			parts = append(parts, map[string]any{
				"type": "image",
				"source": map[string]any{
					"type":       "base64",
					"media_type": img.MIMEType,
					"data":       img.Data,
				},
			})
		default:
			return nil, fmt.Errorf("llm: 供应商 %s 不支持图片消息", provider)
		}
	}
	return map[string]any{"role": m.Role, "content": parts}, nil
}

// wireMessages 转换整批消息；任一图片非法则整体报错——请求不发
// 出半个字节（半成功的多模态请求是最难诊断的失败形态）。
func wireMessages(provider string, msgs []Message) ([]map[string]any, error) {
	out := make([]map[string]any, 0, len(msgs))
	for _, m := range msgs {
		wm, err := wireMessage(provider, m)
		if err != nil {
			return nil, err
		}
		out = append(out, wm)
	}
	return out, nil
}
