package core

import (
	"encoding/binary"
	"testing"
)

// makeTestDIB 构造 w*h 的 32bpp 自下而上 DIB，像素值编码坐标便于校验。
func makeTestDIB(w, h int) []byte {
	stride := w * 4
	off := 40
	buf := make([]byte, off+stride*h)
	binary.LittleEndian.PutUint32(buf[0:4], 40)
	binary.LittleEndian.PutUint32(buf[4:8], uint32(w))
	binary.LittleEndian.PutUint32(buf[8:12], uint32(h)) // 正数 = 自下而上
	binary.LittleEndian.PutUint16(buf[12:14], 1)
	binary.LittleEndian.PutUint16(buf[14:16], 32)
	binary.LittleEndian.PutUint32(buf[20:24], uint32(stride*h))
	// 自下而上存储：存储行 y 对应视觉行 h-1-y；像素值记视觉坐标。
	for y := 0; y < h; y++ {
		row := off + y*stride
		vis := h - 1 - y
		for x := 0; x < w; x++ {
			buf[row+x*4] = byte(x)
			buf[row+x*4+1] = byte(vis)
			buf[row+x*4+2] = 0x7f
			buf[row+x*4+3] = 0xff
		}
	}
	return buf
}

func TestNormalizeRect(t *testing.T) {
	r := NormalizeRect(90, 80, 10, 20)
	if r != (DIBRect{X: 10, Y: 20, W: 80, H: 60}) {
		t.Fatalf("归一化错误：%+v", r)
	}
	if r2 := NormalizeRect(5, 5, 5, 5); r2.W != 0 || r2.H != 0 {
		t.Fatalf("零面积应保持 0：%+v", r2)
	}
}

func TestClipRect(t *testing.T) {
	if _, ok := ClipRect(DIBRect{X: 200, Y: 200, W: 10, H: 10}, 100, 100); ok {
		t.Fatal("完全越界应返回 false")
	}
	r, ok := ClipRect(DIBRect{X: -10, Y: -10, W: 50, H: 50}, 100, 100)
	if !ok || r.X != 0 || r.Y != 0 || r.W != 40 || r.H != 40 {
		t.Fatalf("越界裁剪错误：%+v ok=%v", r, ok)
	}
}

// 裁剪要按"视觉坐标"取，且输出行序翻转为自上而下。
func TestCropDIB32Pixels(t *testing.T) {
	const w, h = 8, 6
	src := makeTestDIB(w, h)
	out, err := CropDIB32(src, DIBRect{X: 2, Y: 1, W: 3, H: 2})
	if err != nil {
		t.Fatal(err)
	}
	ow, oh, bpp, err := DIBSize(out)
	if err != nil || ow != 3 || oh != 2 || bpp != 32 {
		t.Fatalf("尺寸错误：%d x %d x %d (%v)", ow, oh, bpp, err)
	}
	if !isTopDown(out) {
		t.Fatal("输出应为自上而下")
	}
	// 目标第一行应对应源视觉 y=1：源里存储行 = h-1-1 = 4
	for dx := 0; dx < 3; dx++ {
		px := out[40+dx*4:]
		if px[0] != byte(2+dx) || px[1] != 1 {
			t.Fatalf("第一行像素不对：x=%d got(%d,%d)", dx, px[0], px[1])
		}
	}
	for dx := 0; dx < 3; dx++ {
		px := out[40+3*4+dx*4:]
		if px[0] != byte(2+dx) || px[1] != 2 {
			t.Fatalf("第二行像素不对：x=%d got(%d,%d)", dx, px[0], px[1])
		}
	}
}

// 输入本身就是自上而下时，不能重复翻转。
func TestCropDIB32TopDownInput(t *testing.T) {
	src := makeTestDIB(4, 4)
	// 把自下而上数据翻成自上而下，并把 biHeight 置负
	flipRows(src, 4, 4)
	binary.LittleEndian.PutUint32(src[8:12], uint32(0xFFFFFFFC))
	out, err := CropDIB32(src, DIBRect{X: 0, Y: 0, W: 2, H: 1})
	if err != nil {
		t.Fatal(err)
	}
	if out[40] != 0 || out[41] != 0 {
		t.Fatalf("首像素应为 (0,0)，实际 (%d,%d)", out[40], out[41])
	}
}

// 越界选区应被夹紧而不是崩掉。
func TestCropDIB32ClipsSelection(t *testing.T) {
	src := makeTestDIB(10, 10)
	out, err := CropDIB32(src, DIBRect{X: 8, Y: 8, W: 100, H: 100})
	if err != nil {
		t.Fatal(err)
	}
	ow, oh, _, _ := DIBSize(out)
	if ow != 2 || oh != 2 {
		t.Fatalf("应夹到 2x2，实际 %dx%d", ow, oh)
	}
}

// 非 32bpp / 压缩 DIB 要明确拒绝，避免裁出花屏。
func TestCropDIB32RejectsUnsupported(t *testing.T) {
	src := makeTestDIB(4, 4)
	binary.LittleEndian.PutUint16(src[14:16], 16)
	if _, err := CropDIB32(src, DIBRect{W: 1, H: 1}); err == nil {
		t.Fatal("16bpp 应被拒绝")
	}
	src2 := makeTestDIB(4, 4)
	binary.LittleEndian.PutUint32(src2[16:20], 3)
	if _, err := CropDIB32(src2, DIBRect{W: 1, H: 1}); err == nil {
		t.Fatal("压缩 DIB 应被拒绝")
	}
	if _, err := CropDIB32([]byte{1, 2, 3}, DIBRect{W: 1, H: 1}); err == nil {
		t.Fatal("过短数据应报错")
	}
}

// flipRows 原地翻转 DIB 的像素行序。
func flipRows(dib []byte, w, h int) {
	stride := w * 4
	tmp := make([]byte, stride)
	for i := 0; i < h/2; i++ {
		a := 40 + i*stride
		b := 40 + (h-1-i)*stride
		copy(tmp, dib[a:a+stride])
		copy(dib[a:a+stride], dib[b:b+stride])
		copy(dib[b:b+stride], tmp)
	}
}

func isTopDown(dib []byte) bool {
	return int32(binary.LittleEndian.Uint32(dib[8:12])) < 0
}
