//go:build windows

package ui

// 位图绘制：用 StretchDIBits 把内存里的 BGRA 位图贴到画布。
// 走 GDI，Win7 原生支持，无需 GDI+ / Direct2D。

import (
	"image"
	"image/draw"
	"sync"
	"unsafe"

	"win7harness/internal/win32"
)

var (
	pStretchDIBits = win32.Gdi32Proc("StretchDIBits")
	pSetStretchBlt = win32.Gdi32Proc("SetStretchBltMode")
)

// ImageCache 缓存已转成 BGRA 的图片，避免每帧重复转换（滚动时帧率高）。
type ImageCache struct {
	mu sync.Mutex
	m  map[string]*DibImage
}

// DibImage 是已转成 BGRA 32bpp 的位图，可直接贴到画布。
type DibImage struct {
	w, h int32
	bits []byte
	info []byte // BITMAPINFOHEADER(40) + 调色板占位

	// rgba 是 canvas 后端用的 RGBA 视图（惰性生成，只读共享）。
	rgbaOnce sync.Once
	rgba     *image.RGBA
}

// RGBA 返回该位图的 RGBA 视图，供矢量光栅器绘制。
func (d *DibImage) RGBA() *image.RGBA {
	d.rgbaOnce.Do(func() {
		img := image.NewRGBA(image.Rect(0, 0, int(d.w), int(d.h)))
		for y := 0; y < int(d.h); y++ {
			src := d.bits[y*int(d.w)*4:]
			dst := img.Pix[y*img.Stride:]
			for x := 0; x < int(d.w); x++ {
				s := src[x*4 : x*4+4]
				t := dst[x*4 : x*4+4]
				t[0], t[1], t[2], t[3] = s[2], s[1], s[0], 0xFF
			}
		}
		d.rgba = img
	})
	return d.rgba
}

// W 返回像素宽度。
func (d *DibImage) W() int32 { return d.w }

// H 返回像素高度。
func (d *DibImage) H() int32 { return d.h }

// NewImageCache 创建图片缓存。
func NewImageCache() *ImageCache {
	return &ImageCache{m: map[string]*DibImage{}}
}

// Get 按 key 取缓存，未命中则用 loader 加载并转换。
func (ic *ImageCache) Get(key string, loader func() (image.Image, error)) *DibImage {
	ic.mu.Lock()
	if d, ok := ic.m[key]; ok {
		ic.mu.Unlock()
		return d
	}
	ic.mu.Unlock()
	img, err := loader()
	if err != nil {
		// 失败也记一笔，避免每帧重试读盘
		ic.mu.Lock()
		ic.m[key] = nil
		ic.mu.Unlock()
		return nil
	}
	d := toDIB(img)
	ic.mu.Lock()
	if len(ic.m) > 64 { // 简易上限，防止一直增长
		ic.m = map[string]*DibImage{}
	}
	ic.m[key] = d
	ic.mu.Unlock()
	return d
}

// Clear 清空缓存。
func (ic *ImageCache) Clear() {
	ic.mu.Lock()
	ic.m = map[string]*DibImage{}
	ic.mu.Unlock()
}

// toDIB 把 image.Image 转成 32bpp top-down BGRA + BITMAPINFOHEADER。
func toDIB(img image.Image) *DibImage {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return nil
	}
	nrgba := image.NewNRGBA(image.Rect(0, 0, w, h))
	draw.Draw(nrgba, nrgba.Bounds(), img, b.Min, draw.Src)

	bits := make([]byte, w*h*4)
	for y := 0; y < h; y++ {
		src := nrgba.Pix[y*nrgba.Stride : y*nrgba.Stride+w*4]
		dst := bits[y*w*4:]
		for x := 0; x < w; x++ {
			s := src[x*4:]
			d := dst[x*4:]
			// BGRA，直接用（NRGBA 已是非预乘）
			d[0] = s[2]
			d[1] = s[1]
			d[2] = s[0]
			d[3] = 255
		}
	}

	info := make([]byte, 40+256*4)
	le32 := func(off int, v uint32) {
		info[off] = byte(v)
		info[off+1] = byte(v >> 8)
		info[off+2] = byte(v >> 16)
		info[off+3] = byte(v >> 24)
	}
	le16 := func(off int, v uint16) {
		info[off] = byte(v)
		info[off+1] = byte(v >> 8)
	}
	le32(0, 40)         // biSize
	le32(4, uint32(w))  // biWidth
	le32(8, uint32(-h)) // biHeight 负数 = top-down
	le16(12, 1)         // biPlanes
	le16(14, 32)        // biBitCount
	le32(16, 0)         // BI_RGB
	le32(20, uint32(len(bits)))
	le32(24, 2835) // 72 DPI
	le32(28, 2835)
	return &DibImage{w: int32(w), h: int32(h), bits: bits, info: info}
}

// DrawImg 把位图绘制到 r（会拉伸），保留宽高比由调用方负责。
func (c *Canvas) DrawImg(d *DibImage, r Rect) {
	if d == nil || c.memDC == 0 {
		return
	}
	pr := c.phys(r)
	if c.render() {
		if img := d.RGBA(); img != nil {
			c.rec(rxCmd{kind: rxImage, x: float64(pr.Left), y: float64(pr.Top),
				w: float64(pr.Right - pr.Left), h: float64(pr.Bottom - pr.Top), img: img})
		}
		return
	}
	if pr.Width() <= 0 || pr.Height() <= 0 {
		return
	}
	// HALFTONE 让缩放更平滑（Win7 支持）
	win32.SafeCall(pSetStretchBlt, uintptr(c.memDC), 4)
	win32.SafeCall(pStretchDIBits,
		uintptr(c.memDC),
		uintptr(pr.Left), uintptr(pr.Top), uintptr(pr.Width()), uintptr(pr.Height()),
		0, 0, uintptr(d.w), uintptr(d.h),
		uintptr(unsafe.Pointer(&d.bits[0])),
		uintptr(unsafe.Pointer(&d.info[0])),
		0,          // DIB_RGB_COLORS
		0x00CC0020, // SRCCOPY
	)
}
