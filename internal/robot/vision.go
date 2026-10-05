// 视觉装配：把截屏 PNG 整形成能进模型请求的图片字节。与截屏平台
// 解耦（纯 image 包），非 Windows 同样可用——runner 的视觉回路
// 在任何平台都能对既有 PNG 做整形。
package robot

import (
	"bytes"
	"fmt"
	"image/jpeg"
	"image/png"
)

// FitForVision 把 PNG 控制在 maxBytes 内：达标原样返回（image/png）；
// 超限转 JPEG（质量 85，再超降 60）——截图是文字与界面，中质量
// JPEG 的可读性足够视觉回路用。两次都超限则报错，绝不静默发出
// 会被供应商拒绝的请求。
func FitForVision(pngBytes []byte, maxBytes int) ([]byte, string, error) {
	if len(pngBytes) <= maxBytes {
		return pngBytes, "image/png", nil
	}
	img, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		return nil, "", fmt.Errorf("robot: 解码截屏: %w", err)
	}
	for _, q := range []int{85, 60} {
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: q}); err != nil {
			return nil, "", fmt.Errorf("robot: JPEG 转码: %w", err)
		}
		if buf.Len() <= maxBytes {
			return buf.Bytes(), "image/jpeg", nil
		}
	}
	return nil, "", fmt.Errorf("robot: 截图 %d 字节压不进 %d 上限（分辨率过高？）", len(pngBytes), maxBytes)
}
