package tools

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func makeShrinkTestImage(w, h int, checkerAlpha bool) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			a := uint8(0xff)
			if checkerAlpha && x < w/2 {
				a = 0 // 左半边整块透明，用于验证白底铺平（棋盘格缩放后会混色，不适合做该断言）
			}
			img.SetRGBA(x, y, color.RGBA{
				R: uint8(x * 255 / w),
				G: 0x40,
				B: uint8(y * 255 / h),
				A: a,
			})
		}
	}
	return img
}

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestShrinkImageResizeAndEncode(t *testing.T) {
	data := encodePNG(t, makeShrinkTestImage(400, 300, false))
	out, mime, ow, oh, nw, nh, ok, err := shrinkImage(data, "image/png", 100)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("大图应触发缩放与重编码")
	}
	if mime != "image/jpeg" {
		t.Fatalf("输出 mime: got %s want image/jpeg", mime)
	}
	if ow != 400 || oh != 300 || nw != 100 || nh != 75 {
		t.Fatalf("尺寸不符: %dx%d -> %dx%d", ow, oh, nw, nh)
	}
	decoded, err := jpeg.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("输出不是合法 JPEG: %v", err)
	}
	if d := decoded.Bounds(); d.Dx() != 100 || d.Dy() != 75 {
		t.Fatalf("JPEG 实际尺寸不符: %v", d)
	}
}

func TestShrinkImageSmallNoop(t *testing.T) {
	data := encodePNG(t, makeShrinkTestImage(50, 50, false))
	_, _, _, _, _, _, ok, err := shrinkImage(data, "image/png", 100)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("未超边长的小 PNG 不应触发重编码")
	}
}

func TestShrinkImageTransparentToWhite(t *testing.T) {
	data := encodePNG(t, makeShrinkTestImage(200, 200, true))
	out, _, _, _, _, _, ok, err := shrinkImage(data, "image/png", 50)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("大图应触发缩放")
	}
	decoded, err := jpeg.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	// (0,0) 原本是透明像素，JPEG 无 alpha → 应被铺成白底（而非黑色）
	r, g, b, _ := decoded.At(0, 0).RGBA()
	if r < 0xd000 || g < 0xd000 || b < 0xd000 {
		t.Fatalf("透明像素未铺白底: #%02x%02x%02x", r>>8, g>>8, b>>8)
	}
}
