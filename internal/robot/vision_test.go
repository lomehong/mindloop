package robot

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math/rand"
	"testing"
)

// noisePNG 生成噪声 PNG（不可压缩，体积确定地超阈值）。
func noisePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < w*h; i++ {
		img.Pix[i*4+0] = byte(rng.Intn(256))
		img.Pix[i*4+1] = byte(rng.Intn(256))
		img.Pix[i*4+2] = byte(rng.Intn(256))
		img.Pix[i*4+3] = 255
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestFitForVisionPassthrough(t *testing.T) {
	png := tinyPNGTest(t)
	got, mime, err := FitForVision(png, 1<<20)
	if err != nil {
		t.Fatalf("FitForVision: %v", err)
	}
	if mime != "image/png" || !bytes.Equal(got, png) {
		t.Fatalf("达标图片应原样返回: mime=%s 同=%v", mime, bytes.Equal(got, png))
	}
}

func TestFitForVisionShrinksToJPEG(t *testing.T) {
	big := noisePNG(t, 500, 400) // 噪声 PNG ~600KB；JPEG 对噪声也能压过 200KB
	if len(big) < 256*1024 {
		t.Skipf("噪声 PNG 不够大（%d 字节），换阈值意义不大", len(big))
	}
	got, mime, err := FitForVision(big, 200*1024)
	if err != nil {
		t.Fatalf("FitForVision: %v", err)
	}
	if mime != "image/jpeg" {
		t.Fatalf("超限应转 JPEG: %s", mime)
	}
	if len(got) > 200*1024 {
		t.Fatalf("转码后仍超限: %d", len(got))
	}
	if _, err := jpeg.Decode(bytes.NewReader(got)); err != nil {
		t.Fatalf("产出不是合法 JPEG: %v", err)
	}
}

func TestFitForVisionImpossible(t *testing.T) {
	big := noisePNG(t, 900, 900)
	if _, _, err := FitForVision(big, 16); err == nil {
		t.Fatal("16 字节上限应报错而不是静默失败")
	}
}

func tinyPNGTest(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 1, G: 2, B: 3, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
