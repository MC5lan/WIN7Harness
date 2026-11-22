//go:build windows

package ui

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"sync"
)

// 品牌图标位图：引导页等处的大图标直接用 icon.ico 里的 PNG 帧。
//
// 位图路径没有 alpha 合成（GDI 的 StretchDIBits 与 rx 都按不透明处理），
// 且 rx 按位图原始尺寸绘制、不会拉伸，所以这里按目标物理像素缩放，
// 并把透明圆角压到调用方给的底色上（底色应与图标下方的背景一致）。
// 只用标准库，不引入第三方依赖。

type brandKey struct {
	px int32
	bg uint32
}

var (
	brandMu    sync.Mutex
	brandSrc   image.Image
	brandTried bool
	brandCache = map[brandKey]*DibImage{}
)

// AppIcon 返回品牌图标在 px×px 物理像素下、压在 bg 底色上的位图。
// ico 解析失败时返回 nil，调用方退回矢量标识。
func AppIcon(px int32, bg uint32) *DibImage {
	if px <= 0 {
		return nil
	}
	brandMu.Lock()
	defer brandMu.Unlock()
	if !brandTried {
		brandTried = true
		brandSrc = decodeIcoLargestPNG(iconBytes)
	}
	if brandSrc == nil {
		return nil
	}
	k := brandKey{px, bg}
	if d, ok := brandCache[k]; ok {
		return d
	}
	sb := brandSrc.Bounds()
	base := image.NewRGBA(image.Rect(0, 0, sb.Dx(), sb.Dy()))
	fill := &image.Uniform{C: color.RGBA{R: uint8(bg), G: uint8(bg >> 8), B: uint8(bg >> 16), A: 0xFF}}
	draw.Draw(base, base.Bounds(), fill, image.Point{}, draw.Src)
	draw.Draw(base, base.Bounds(), brandSrc, sb.Min, draw.Over)
	d := toDIB(boxScale(base, int(px), int(px)))
	if len(brandCache) >= 16 {
		brandCache = map[brandKey]*DibImage{}
	}
	brandCache[k] = d
	return d
}

// decodeIcoLargestPNG 解码 ico 里尺寸最大的 PNG 帧（Vista 起的 ico 把大图标存成 PNG）。
// 只支持 PNG 帧；没有 PNG 帧或解码失败返回 nil。
func decodeIcoLargestPNG(data []byte) image.Image {
	if len(data) < 6 || binary.LittleEndian.Uint16(data[2:4]) != 1 {
		return nil
	}
	sig := []byte("\x89PNG\r\n\x1a\n")
	var best []byte
	bestW := 0
	n := int(binary.LittleEndian.Uint16(data[4:6]))
	for i := 0; i < n && 6+16*(i+1) <= len(data); i++ {
		e := data[6+16*i : 6+16*(i+1)]
		w := int(e[0])
		if w == 0 {
			w = 256
		}
		size := uint64(binary.LittleEndian.Uint32(e[8:12]))
		off := uint64(binary.LittleEndian.Uint32(e[12:16]))
		if off+size > uint64(len(data)) {
			continue
		}
		frame := data[int(off):int(off+size)]
		if w > bestW && bytes.HasPrefix(frame, sig) {
			best, bestW = frame, w
		}
	}
	if best == nil {
		return nil
	}
	img, err := png.Decode(bytes.NewReader(best))
	if err != nil {
		return nil
	}
	return img
}

// boxScale 用面积平均把不透明位图缩到 dw×dh。图标是大图缩小，
// 最近邻会丢细节并锯齿，这里对每个目标像素取它覆盖的源像素均值。
func boxScale(src *image.RGBA, dw, dh int) *image.RGBA {
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		y0, y1 := y*sh/dh, (y+1)*sh/dh
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for x := 0; x < dw; x++ {
			x0, x1 := x*sw/dw, (x+1)*sw/dw
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var r, g, b, n int
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					i := sy*src.Stride + sx*4
					r += int(src.Pix[i])
					g += int(src.Pix[i+1])
					b += int(src.Pix[i+2])
					n++
				}
			}
			j := y*dst.Stride + x*4
			dst.Pix[j], dst.Pix[j+1], dst.Pix[j+2], dst.Pix[j+3] = uint8(r/n), uint8(g/n), uint8(b/n), 0xFF
		}
	}
	return dst
}
