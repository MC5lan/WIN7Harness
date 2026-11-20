//go:build ignore

// WIN7 Harness 品牌图标生成器（纯 Go 标准库，无 Pillow / ImageMagick）。
//
// 设计与应用内 DrawLogo 完全同源：珊瑚圆角方块 + 白色终端提示符 “>” + 光标条。
// 旧的深色青绿环形图标已经废弃——exe、托盘、窗口和欢迎页统一用这一枚。
//
// 输出：多尺寸 ICO。16/24/32/48/64 用 32bpp BMP（Win7 资源管理器最稳），
// 128/256 用 PNG（Vista+ 支持，体积小）。
//
// 用法：go run ./tools/gen_icon.go
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
)

const (
	// 与应用令牌 AccentBase 一致：#D97757（COLORREF 0x005777D9）
	coralR = 0xD9
	coralG = 0x77
	coralB = 0x57
)

type spec struct {
	bgR, bgG, bgB float64
	stroke        float64 // > 的笔画半径（归一化）
	topY, tipY    float64 // > 的起笔 / 转折点
	botY          float64 // > 的收笔
	barY, barH    float64 // 光标条中心/高度
	barX0, barX1  float64
	barR          float64
}

func styleFor(size int) spec {
	s := spec{
		bgR: coralR / 255.0, bgG: coralG / 255.0, bgB: coralB / 255.0,
		stroke: 0.085,
		topY:   0.245, tipY: 0.485, botY: 0.725,
		barY: 0.868, barH: 0.072,
		barX0: 0.290, barX1: 0.665,
		barR: 0.034,
	}
	// 小尺寸加粗笔画、放大光标，避免任务栏 16px 糊成一团；
	// 同时把 > 稍微上提，保证和光标条之间留出一条珊瑚色空隙。
	if size <= 20 {
		s.stroke = 0.108
		s.topY, s.tipY, s.botY = 0.210, 0.440, 0.665
		s.barY = 0.858
		s.barH = 0.092
		s.barX0, s.barX1 = 0.275, 0.690
		s.barR = 0.042
	} else if size <= 32 {
		s.stroke = 0.096
		s.topY, s.tipY, s.botY = 0.220, 0.455, 0.680
		s.barY = 0.862
		s.barH = 0.082
		s.barX0, s.barX1 = 0.282, 0.678
		s.barR = 0.038
	}
	return s
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func sdRoundRect(px, py, cx, cy, hw, hh, r float64) float64 {
	qx := math.Abs(px-cx) - (hw - r)
	qy := math.Abs(py-cy) - (hh - r)
	ax, ay := math.Max(qx, 0), math.Max(qy, 0)
	return math.Hypot(ax, ay) + math.Min(math.Max(qx, qy), 0) - r
}

func sdCapsule(px, py, ax, ay, bx, by, r float64) float64 {
	pax, pay := px-ax, py-ay
	bax, bay := bx-ax, by-ay
	l2 := bax*bax + bay*bay
	h := 0.0
	if l2 > 0 {
		h = clamp01((pax*bax + pay*bay) / l2)
	}
	return math.Hypot(pax-bax*h, pay-bay*h) - r
}

// shade 返回某点的 RGBA（0..1，直通 alpha）。ss 为超采样步长，用于软边缘。
func shade(x, y, ss float64, st spec) (float64, float64, float64, float64) {
	// 圆角方块：留 3% 透明边，任务栏/桌面才有“图标”而不是贴边色块。
	d := sdRoundRect(x, y, 0.5, 0.5, 0.470, 0.470, 0.215)
	bgCov := clamp01(0.5 - d/ss)
	if bgCov <= 0 {
		return 0, 0, 0, 0
	}
	r, g, b := st.bgR, st.bgG, st.bgB
	// 轻微纵向明暗：上亮下暗，纯色块在 256px 下过于死板。
	r *= 1.035 - 0.075*y
	g *= 1.035 - 0.075*y
	b *= 1.035 - 0.075*y
	r, g, b = clamp01(r), clamp01(g), clamp01(b)

	// “>” 两段圆头笔画。
	d1 := sdCapsule(x, y, 0.295, st.topY, 0.635, st.tipY, st.stroke)
	d2 := sdCapsule(x, y, 0.635, st.tipY, 0.295, st.botY, st.stroke)
	glyphCov := clamp01(0.5 - math.Min(d1, d2)/ss)

	// 下方光标条。
	d3 := sdRoundRect(x, y, (st.barX0+st.barX1)/2, st.barY,
		(st.barX1-st.barX0)/2, st.barH/2, st.barR)
	barCov := clamp01(0.5 - d3/ss)
	if barCov > glyphCov {
		glyphCov = barCov
	}
	if glyphCov > 0 {
		r = r*(1-glyphCov) + 1.0*glyphCov
		g = g*(1-glyphCov) + 1.0*glyphCov
		b = b*(1-glyphCov) + 1.0*glyphCov
	}
	return r, g, b, bgCov
}

// render 返回 size×size 的 NRGBA（straight alpha），4×4 超采样。
func render(size int) *image.NRGBA {
	const ss = 4
	st := styleFor(size)
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	step := 1.0 / float64(size*ss)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var ar, ag, ab, aa float64
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					fx := (float64(x*ss+sx) + 0.5) * step
					fy := (float64(y*ss+sy) + 0.5) * step
					r, g, b, a := shade(fx, fy, step, st)
					ar += r * a
					ag += g * a
					ab += b * a
					aa += a
				}
			}
			n := float64(ss * ss)
			if aa <= 0 {
				img.SetNRGBA(x, y, color.NRGBA{})
				continue
			}
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(clamp01(ar/aa)*255 + 0.5),
				G: uint8(clamp01(ag/aa)*255 + 0.5),
				B: uint8(clamp01(ab/aa)*255 + 0.5),
				A: uint8(clamp01(aa/n)*255 + 0.5),
			})
		}
	}
	return img
}

func pngBytes(img image.Image) []byte {
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// bmpBytes 生成 ICO 内嵌的 32bpp DIB（BITMAPINFOHEADER + BGRA 自下而上 + AND mask）。
func bmpBytes(img *image.NRGBA) []byte {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	var buf bytes.Buffer
	// BITMAPINFOHEADER：biHeight 为双倍（XOR + AND）。
	binary.Write(&buf, binary.LittleEndian, uint32(40))
	binary.Write(&buf, binary.LittleEndian, int32(w))
	binary.Write(&buf, binary.LittleEndian, int32(h*2))
	binary.Write(&buf, binary.LittleEndian, uint16(1))
	binary.Write(&buf, binary.LittleEndian, uint16(32))
	binary.Write(&buf, binary.LittleEndian, uint32(0))
	binary.Write(&buf, binary.LittleEndian, uint32(w*h*4))
	binary.Write(&buf, binary.LittleEndian, int32(0))
	binary.Write(&buf, binary.LittleEndian, int32(0))
	binary.Write(&buf, binary.LittleEndian, uint32(0))
	binary.Write(&buf, binary.LittleEndian, uint32(0))

	for y := h - 1; y >= 0; y-- {
		for x := 0; x < w; x++ {
			c := img.NRGBAAt(x, y)
			buf.Write([]byte{c.B, c.G, c.R, c.A})
		}
	}
	maskStride := ((w + 31) / 32) * 4
	mask := make([]byte, maskStride*h)
	for y := 0; y < h; y++ {
		row := (h - 1 - y) * maskStride
		for x := 0; x < w; x++ {
			if img.NRGBAAt(x, y).A < 128 {
				mask[row+x/8] |= 0x80 >> uint(x%8)
			}
		}
	}
	buf.Write(mask)
	return buf.Bytes()
}

type entry struct {
	size int
	data []byte
}

func buildICO(entries []entry) []byte {
	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, uint16(0))
	binary.Write(&buf, binary.LittleEndian, uint16(1))
	binary.Write(&buf, binary.LittleEndian, uint16(len(entries)))
	offset := 6 + 16*len(entries)
	for _, e := range entries {
		dim := byte(e.size)
		if e.size >= 256 {
			dim = 0
		}
		buf.Write([]byte{dim, dim, 0, 0})
		binary.Write(&buf, binary.LittleEndian, uint16(1))
		binary.Write(&buf, binary.LittleEndian, uint16(32))
		binary.Write(&buf, binary.LittleEndian, uint32(len(e.data)))
		binary.Write(&buf, binary.LittleEndian, uint32(offset))
		offset += len(e.data)
	}
	for _, e := range entries {
		buf.Write(e.data)
	}
	return buf.Bytes()
}

func main() {
	root, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	// 允许从仓库根目录或 tools/ 目录运行。
	if _, err := os.Stat(filepath.Join(root, "internal", "ui")); err != nil {
		if _, err2 := os.Stat(filepath.Join(root, "..", "internal", "ui")); err2 == nil {
			root = filepath.Clean(filepath.Join(root, ".."))
		}
	}
	buildDir := filepath.Join(root, "build")
	uiDir := filepath.Join(root, "internal", "ui")
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		panic(err)
	}
	if err := os.MkdirAll(uiDir, 0o755); err != nil {
		panic(err)
	}

	sizes := []int{16, 24, 32, 48, 64, 128, 256}
	entries := make([]entry, 0, len(sizes))
	for _, s := range sizes {
		fmt.Printf("render %dx%d ...\n", s, s)
		img := render(s)
		var data []byte
		if s >= 128 {
			data = pngBytes(img)
		} else {
			data = bmpBytes(img)
		}
		entries = append(entries, entry{size: s, data: data})
		if s == 256 {
			if err := os.WriteFile(filepath.Join(buildDir, "icon_256.png"), pngBytes(img), 0o644); err != nil {
				panic(err)
			}
		}
		if s == 32 {
			if err := os.WriteFile(filepath.Join(buildDir, "icon_32.png"), pngBytes(img), 0o644); err != nil {
				panic(err)
			}
		}
	}
	ico := buildICO(entries)
	for _, p := range []string{
		filepath.Join(buildDir, "win7harness.ico"),
		filepath.Join(uiDir, "icon.ico"),
	} {
		if err := os.WriteFile(p, ico, 0o644); err != nil {
			panic(err)
		}
		fmt.Printf("wrote %s (%d bytes)\n", p, len(ico))
	}
}
