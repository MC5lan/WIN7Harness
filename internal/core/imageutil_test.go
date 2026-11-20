package core

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// makeDIB 构造一个 w×h 的 24bpp 裸 DIB（bottom-up，BI_RGB），像素由 fn 提供。
func makeDIB(w, h int, fn func(x, y int) color.RGBA) []byte {
	rowBytes := ((w*3 + 3) / 4) * 4
	buf := make([]byte, 40+rowBytes*h)
	binary.LittleEndian.PutUint32(buf[0:], 40)
	binary.LittleEndian.PutUint32(buf[4:], uint32(w))
	binary.LittleEndian.PutUint32(buf[8:], uint32(h))
	binary.LittleEndian.PutUint16(buf[12:], 1)
	binary.LittleEndian.PutUint16(buf[14:], 24)
	binary.LittleEndian.PutUint32(buf[16:], 0)
	binary.LittleEndian.PutUint32(buf[20:], uint32(rowBytes*h))
	for y := 0; y < h; y++ {
		srcY := h - 1 - y // bottom-up
		row := buf[40+y*rowBytes:]
		for x := 0; x < w; x++ {
			c := fn(x, srcY)
			row[x*3] = c.B
			row[x*3+1] = c.G
			row[x*3+2] = c.R
		}
	}
	return buf
}

func TestDecodeDIB24(t *testing.T) {
	dib := makeDIB(2, 2, func(x, y int) color.RGBA {
		if x == 0 && y == 0 {
			return color.RGBA{R: 255, A: 255}
		}
		return color.RGBA{G: 128, A: 255}
	})
	img, err := DecodeDIB(dib)
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 2 || img.Bounds().Dy() != 2 {
		t.Fatalf("尺寸不符：%v", img.Bounds())
	}
	r, g, b, _ := img.At(0, 0).RGBA()
	if r>>8 != 255 || g>>8 != 0 || b>>8 != 0 {
		t.Fatalf("左上角像素应为红色，实际 r=%d g=%d b=%d", r>>8, g>>8, b>>8)
	}
	r2, g2, _, _ := img.At(1, 1).RGBA()
	if g2>>8 != 128 || r2>>8 != 0 {
		t.Fatalf("右下角像素应为绿色，实际 r=%d g=%d", r2>>8, g2>>8)
	}
}

func TestDecodeDIBEmpty(t *testing.T) {
	if _, err := DecodeDIB(nil); err == nil {
		t.Fatal("空数据应报错")
	}
	if _, err := DecodeDIB(make([]byte, 40)); err == nil {
		t.Fatal("尺寸为 0 应报错")
	}
}

func TestResizeMax(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 3000, 1500))
	got := ResizeMax(src, 1568)
	if got.Bounds().Dx() != 1568 || got.Bounds().Dy() != 784 {
		t.Fatalf("缩放结果不符：%v", got.Bounds())
	}
	// 小图不放大
	small := image.NewNRGBA(image.Rect(0, 0, 100, 50))
	if ResizeMax(small, 1568).Bounds().Dx() != 100 {
		t.Fatal("小图不应被放大")
	}
}

func TestPrepareImage(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 800, 600))
	for i := range src.Pix {
		src.Pix[i] = 200
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, src); err != nil {
		t.Fatal(err)
	}
	out, w, h, err := PrepareImage(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if w != 800 || h != 600 {
		t.Fatalf("尺寸应保持，实际 %dx%d", w, h)
	}
	if len(out) == 0 || len(out) > MaxImageBytes {
		t.Fatalf("输出体积异常：%d", len(out))
	}
	if _, err := png.Decode(bytes.NewReader(out)); err != nil {
		t.Fatalf("输出不是合法 PNG：%v", err)
	}
}

func TestSaveAttachmentDedup(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WIN7HARNESS_HOME", dir)

	src := image.NewNRGBA(image.Rect(0, 0, 10, 10))
	var buf bytes.Buffer
	png.Encode(&buf, src)
	raw := buf.Bytes()

	p1, err := SaveAttachment("conv1", raw)
	if err != nil {
		t.Fatal(err)
	}
	p2, err := SaveAttachment("conv1", raw)
	if err != nil {
		t.Fatal(err)
	}
	if p1 != p2 {
		t.Fatalf("同样内容应命中同一文件：%s / %s", p1, p2)
	}
	if filepath.Base(filepath.Dir(p1)) != "conv1" {
		t.Fatalf("附件应落在会话目录下：%s", p1)
	}
	if _, err := os.Stat(p1); err != nil {
		t.Fatalf("附件未落盘：%v", err)
	}
}

func TestImageDataURLFromFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WIN7HARNESS_HOME", dir)
	src := image.NewNRGBA(image.Rect(0, 0, 20, 10))
	p := filepath.Join(dir, "a.png")
	f, _ := os.Create(p)
	png.Encode(f, src)
	f.Close()

	u, err := ImageDataURL(p, MaxImageSide)
	if err != nil {
		t.Fatal(err)
	}
	if len(u) < 30 || u[:22] != "data:image/png;base64," {
		t.Fatalf("data URL 异常：%.40s", u)
	}
}

func TestIsImagePath(t *testing.T) {
	cases := map[string]bool{
		"a.PNG": true, "b.jpg": true, "c.jpeg": true, "d.bmp": true,
		"e.txt": false, "f": false, "g.webp": true,
	}
	for name, want := range cases {
		if got := IsImagePath(name); got != want {
			t.Fatalf("%s: 期望 %v 实际 %v", name, want, got)
		}
	}
}

// makeDIBV5 构造 32bpp、biSize=124 的 CF_DIBV5 形态（剪贴板截图常见）。
func makeDIBV5(w, h int, rgba func(x, y int) [4]byte) []byte {
	rowBytes := w * 4
	buf := make([]byte, 124+rowBytes*h)
	binary.LittleEndian.PutUint32(buf[0:], 124)
	binary.LittleEndian.PutUint32(buf[4:], uint32(w))
	binary.LittleEndian.PutUint32(buf[8:], uint32(h))
	binary.LittleEndian.PutUint16(buf[12:], 1)
	binary.LittleEndian.PutUint16(buf[14:], 32)
	binary.LittleEndian.PutUint32(buf[16:], 3) // BI_BITFIELDS
	binary.LittleEndian.PutUint32(buf[20:], uint32(rowBytes*h))
	binary.LittleEndian.PutUint32(buf[40:], 0x00FF0000)
	binary.LittleEndian.PutUint32(buf[44:], 0x0000FF00)
	binary.LittleEndian.PutUint32(buf[48:], 0x000000FF)
	binary.LittleEndian.PutUint32(buf[52:], 0xFF000000)
	for y := 0; y < h; y++ {
		srcY := h - 1 - y
		row := buf[124+y*rowBytes:]
		for x := 0; x < w; x++ {
			c := rgba(x, srcY)
			row[x*4] = c[2] // B
			row[x*4+1] = c[1]
			row[x*4+2] = c[0]
			row[x*4+3] = 0 // 剪贴板里面 alpha 常是 0，必须当不透明处理
		}
	}
	return buf
}

func TestDecodeDIBV5NoExtraMask(t *testing.T) {
	dib := makeDIBV5(2, 2, func(x, y int) [4]byte {
		if x == 0 && y == 0 {
			return [4]byte{10, 200, 30, 0}
		}
		return [4]byte{0, 0, 255, 0}
	})
	img, err := DecodeDIB(dib)
	if err != nil {
		t.Fatalf("V5 头解码失败：%v", err)
	}
	r, g, b, a := img.At(0, 0).RGBA()
	if r>>8 != 10 || g>>8 != 200 || b>>8 != 30 {
		t.Fatalf("像素错位（多半是掩码偏移算错）：r=%d g=%d b=%d", r>>8, g>>8, b>>8)
	}
	if a>>8 != 255 {
		t.Fatalf("alpha=0 的剪贴板位图应按不透明处理，实际 a=%d", a>>8)
	}
}

func TestPrepareImageJPEGAndGIF(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 64, 48))
	for i := range src.Pix {
		src.Pix[i] = 120
	}
	var jb bytes.Buffer
	if err := jpeg.Encode(&jb, src, nil); err != nil {
		t.Fatal(err)
	}
	out, w, h, err := PrepareImage(jb.Bytes())
	if err != nil {
		t.Fatalf("JPEG 解码失败：%v", err)
	}
	if w != 64 || h != 48 {
		t.Fatalf("尺寸不符：%dx%d", w, h)
	}
	if _, err := png.Decode(bytes.NewReader(out)); err != nil {
		t.Fatalf("JPEG 转 PNG 失败：%v", err)
	}

	var gb bytes.Buffer
	if err := gif.Encode(&gb, src, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := PrepareImage(gb.Bytes()); err != nil {
		t.Fatalf("GIF 解码失败：%v", err)
	}
}
