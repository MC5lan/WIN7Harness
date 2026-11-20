package core

import (
	"bytes"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// 图片输入相关的尺寸与体积红线。
const (
	// MaxImageSide 送模型前允许的最长边，超过则等比缩放。
	// 主流视觉模型（GPT-4o / Qwen-VL / GLM-4V / DeepSeek-VL）在 1568px 内效果最好且不计过多 token。
	MaxImageSide = 1568
	// MaxImageBytes 单张图片编码后的体积上限（约 1.5MB）。
	MaxImageBytes = 1536 * 1024
	// MaxImagesPerMessage 单条消息最多携带的图片数。
	MaxImagesPerMessage = 4
	// ThumbSide UI 缩略图最长边。
	ThumbSide = 320
)

// AttachDir 返回某个会话的附件目录：%APPDATA%\WIN7Harness\attachments\<convID>。
func AttachDir(convID string) string {
	if convID == "" {
		convID = "_tmp"
	}
	d := filepath.Join(LocalDataDir(), "attachments", convID)
	return d
}

// EnsureAttachDir 创建并返回附件目录。
func EnsureAttachDir(convID string) (string, error) {
	d := AttachDir(convID)
	if err := os.MkdirAll(d, 0o755); err != nil {
		return "", err
	}
	return d, nil
}

// bmpInfoHeader 是 BMP 的核心信息头（40 字节版本）。
type bmpInfoHeader struct {
	Size     uint32
	W        int32
	H        int32
	Planes   uint16
	BitCount uint16
	Compress uint32
	ImageSz  uint32
	XPPM     int32
	YPPM     int32
	ClrUsed  uint32
	ClrImp   uint32
}

// DecodeDIB 解码剪贴板里的裸 DIB（以 BITMAPINFOHEADER 起始，没有 BITMAPFILEHEADER）。
// clipboard 上的 CF_DIB / CF_DIBV5 都是这个形态。
func DecodeDIB(b []byte) (image.Image, error) {
	return decodeBMPBody(b, 0)
}

// DecodeBMP 解码完整 BMP 文件（带 "BM" 文件头），也兼容裸 DIB。
func DecodeBMP(b []byte) (image.Image, error) {
	if len(b) < 2 {
		return nil, errors.New("图片数据过短")
	}
	if b[0] == 'B' && b[1] == 'M' {
		if len(b) < 14+4 {
			return nil, errors.New("BMP 头不完整")
		}
		off := int(binary.LittleEndian.Uint32(b[10:14]))
		return decodeBMPBody(b, off)
	}
	return decodeBMPBody(b, 0)
}

func decodeBMPBody(b []byte, pixelOff int) (image.Image, error) {
	// 从 pixelOff 开始是信息头；DIB 情况下 pixelOff=0
	base := pixelOff
	if len(b) < base+40 {
		return nil, errors.New("位图信息头不完整")
	}
	hs := bmpInfoHeader{
		Size:     binary.LittleEndian.Uint32(b[base : base+4]),
		W:        int32(binary.LittleEndian.Uint32(b[base+4 : base+8])),
		H:        int32(binary.LittleEndian.Uint32(b[base+8 : base+12])),
		Planes:   binary.LittleEndian.Uint16(b[base+12 : base+14]),
		BitCount: binary.LittleEndian.Uint16(b[base+14 : base+16]),
		Compress: binary.LittleEndian.Uint32(b[base+16 : base+20]),
		ClrUsed:  binary.LittleEndian.Uint32(b[base+32 : base+36]),
	}
	if hs.Size < 40 || hs.Size > 200 {
		return nil, fmt.Errorf("不支持的位图头长度 %d", hs.Size)
	}
	topDown := hs.H < 0
	h := hs.H
	if topDown {
		h = -h
	}
	w := hs.W
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("位图尺寸非法 %dx%d", w, h)
	}
	if int64(w)*int64(h) > 40_000_000 {
		return nil, errors.New("图片尺寸过大")
	}
	if hs.Compress != 0 && hs.Compress != 3 { // BI_RGB / BI_BITFIELDS
		return nil, fmt.Errorf("不支持的位图压缩方式 %d", hs.Compress)
	}

	// 计算数据起始偏移
	dataOff := int(hs.Size)
	var palette []color.RGBA
	switch hs.BitCount {
	case 1, 4, 8:
		n := int(hs.ClrUsed)
		if n == 0 {
			n = 1 << hs.BitCount
		}
		if n > 256 {
			n = 256
		}
		palOff := base + int(hs.Size)
		if hs.Size == 40 && hs.Compress == 3 {
			palOff += 12 // 3 个颜色掩码
			dataOff += 12
		}
		if palOff+4*n > len(b) {
			return nil, errors.New("调色板数据不完整")
		}
		palette = make([]color.RGBA, n)
		for i := 0; i < n; i++ {
			p := palOff + i*4
			palette[i] = color.RGBA{R: b[p+2], G: b[p+1], B: b[p], A: 255}
		}
	case 16, 24, 32:
		// 仅 V3 头（40 字节）的 BI_BITFIELDS 才在位深后追加 3 个颜色掩码；
		// V4/V5 头（108/124 字节）的掩码字段就在头内，不能重复跳过。
		if hs.Compress == 3 && hs.Size == 40 {
			dataOff += 12
		}
	case 0:
		return nil, errors.New("位图位深为 0")
	default:
		return nil, fmt.Errorf("不支持的位深 %d", hs.BitCount)
	}
	if pixelOff > 0 {
		// BMP 文件：直接用文件头里的像素偏移
		dataOff = pixelOff - base
	}
	pixStart := base + dataOff
	if pixStart >= len(b) {
		return nil, errors.New("像素数据缺失")
	}

	img := image.NewNRGBA(image.Rect(0, 0, int(w), int(h)))
	rowBytes := func() int {
		switch hs.BitCount {
		case 1:
			return ((int(w) + 7) / 8) * 1
		case 4:
			return ((int(w)*4 + 31) / 32) * 4
		case 8:
			return ((int(w) + 3) / 4) * 4
		case 16:
			return ((int(w)*2 + 3) / 4) * 4
		case 24:
			return ((int(w)*3 + 3) / 4) * 4
		default:
			return int(w) * 4
		}
	}()
	if pixStart+rowBytes*int(h) > len(b) {
		return nil, errors.New("像素数据长度不足")
	}

	for y := 0; y < int(h); y++ {
		srcRow := y
		if !topDown {
			srcRow = int(h) - 1 - y
		}
		row := b[pixStart+srcRow*rowBytes:]
		for x := 0; x < int(w); x++ {
			var c color.RGBA
			switch hs.BitCount {
			case 1:
				bit := (row[x/8] >> uint(7-x%8)) & 1
				if int(bit) < len(palette) {
					c = palette[bit]
				}
			case 4:
				v := row[x/2]
				idx := v >> 4
				if x%2 == 1 {
					idx = v & 0x0F
				}
				if int(idx) < len(palette) {
					c = palette[idx]
				}
			case 8:
				idx := row[x]
				if int(idx) < len(palette) {
					c = palette[idx]
				}
			case 16:
				v := binary.LittleEndian.Uint16(row[x*2:])
				r := uint8((v >> 10) & 0x1F)
				g := uint8((v >> 5) & 0x1F)
				bl := uint8(v & 0x1F)
				c = color.RGBA{R: r<<3 | r>>2, G: g<<3 | g>>2, B: bl<<3 | bl>>2, A: 255}
			case 24:
				p := x * 3
				c = color.RGBA{R: row[p+2], G: row[p+1], B: row[p], A: 255}
			case 32:
				p := x * 4
				// BI_RGB 32bpp 的 alpha 通道经常是垃圾值，统一按不透明处理
				c = color.RGBA{R: row[p+2], G: row[p+1], B: row[p], A: 255}
			}
			img.SetNRGBA(x, y, color.NRGBA{R: c.R, G: c.G, B: c.B, A: c.A})
		}
	}
	return img, nil
}

// DecodeImageAuto 自动识别格式解码：PNG / JPEG / GIF / BMP。
func DecodeImageAuto(b []byte) (image.Image, error) {
	if len(b) >= 2 && b[0] == 'B' && b[1] == 'M' {
		return DecodeBMP(b)
	}
	// 裸 DIB：首 4 字节为 40/108/124（信息头长度）
	if len(b) >= 40 {
		sz := binary.LittleEndian.Uint32(b[0:4])
		if sz == 40 || sz == 108 || sz == 124 || sz == 52 || sz == 56 {
			return DecodeDIB(b)
		}
	}
	if img, err := png.Decode(bytes.NewReader(b)); err == nil {
		return img, nil
	}
	if img, err := jpeg.Decode(bytes.NewReader(b)); err == nil {
		return img, nil
	}
	if img, err := gif.Decode(bytes.NewReader(b)); err == nil {
		return img, nil
	}
	return nil, errors.New("无法识别的图片格式（支持 PNG / JPEG / GIF / BMP）")
}

// ResizeMax 等比缩放，使最长边不超过 maxSide。maxSide<=0 或本来就小则原样返回。
// 采用区域平均，缩小时不会出现明显锯齿。
func ResizeMax(src image.Image, maxSide int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if maxSide <= 0 || (w <= maxSide && h <= maxSide) {
		return src
	}
	nw, nh := w, h
	if w >= h {
		nw = maxSide
		nh = int(float64(h) * float64(maxSide) / float64(w))
	} else {
		nh = maxSide
		nw = int(float64(w) * float64(maxSide) / float64(h))
	}
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	dst := image.NewNRGBA(image.Rect(0, 0, nw, nh))
	sx := float64(w) / float64(nw)
	sy := float64(h) / float64(nh)
	for y := 0; y < nh; y++ {
		y0 := int(float64(y) * sy)
		y1 := int(float64(y+1) * sy)
		if y1 <= y0 {
			y1 = y0 + 1
		}
		if y1 > h {
			y1 = h
		}
		for x := 0; x < nw; x++ {
			x0 := int(float64(x) * sx)
			x1 := int(float64(x+1) * sx)
			if x1 <= x0 {
				x1 = x0 + 1
			}
			if x1 > w {
				x1 = w
			}
			var sr, sg, sb, sa, n uint64
			for yy := y0; yy < y1; yy++ {
				for xx := x0; xx < x1; xx++ {
					r, g, bl, a := src.At(b.Min.X+xx, b.Min.Y+yy).RGBA()
					sr += uint64(r >> 8)
					sg += uint64(g >> 8)
					sb += uint64(bl >> 8)
					sa += uint64(a >> 8)
					n++
				}
			}
			if n == 0 {
				n = 1
			}
			dst.SetNRGBA(x, y, color.NRGBA{
				R: uint8(sr / n), G: uint8(sg / n), B: uint8(sb / n), A: uint8(sa / n),
			})
		}
	}
	return dst
}

// EncodePNG 把图片编码为 PNG 字节。
func EncodePNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// PrepareImage 把任意来源的图片字节规整为「最长边≤MaxImageSide、体积≤MaxImageBytes 的 PNG」。
// 返回 PNG 字节与缩放后的尺寸。
func PrepareImage(raw []byte) ([]byte, int, int, error) {
	img, err := DecodeImageAuto(raw)
	if err != nil {
		return nil, 0, 0, err
	}
	img = ResizeMax(img, MaxImageSide)
	out, err := EncodePNG(img)
	if err != nil {
		return nil, 0, 0, err
	}
	// 极端情况下 PNG 仍超限，继续降边长
	side := MaxImageSide
	for len(out) > MaxImageBytes && side > 512 {
		side = side * 3 / 4
		out, err = EncodePNG(ResizeMax(img, side))
		if err != nil {
			return nil, 0, 0, err
		}
	}
	b := img.Bounds()
	return out, b.Dx(), b.Dy(), nil
}

// SaveAttachment 把图片规整后写入会话附件目录，返回绝对路径。
// 文件名用内容哈希，天然去重。
func SaveAttachment(convID string, raw []byte) (string, error) {
	data, _, _, err := PrepareImage(raw)
	if err != nil {
		return "", err
	}
	dir, err := EnsureAttachDir(convID)
	if err != nil {
		return "", err
	}
	sum := sha1.Sum(data)
	name := hex.EncodeToString(sum[:])[:16] + ".png"
	p := filepath.Join(dir, name)
	if st, err := os.Stat(p); err == nil && st.Size() == int64(len(data)) {
		return p, nil
	}
	if err := WriteFileAtomic(p, data, 0o644); err != nil {
		return "", err
	}
	return p, nil
}

// LoadImageFile 读取本地图片文件并解码（用于 UI 显示缩略图）。
func LoadImageFile(path string) (image.Image, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return DecodeImageAuto(b)
}

// ImageDataURL 读取本地图片文件，按需缩放后编码成 data URL，供多模态请求使用。
// dataURLCache 缓存已编码的 data URL，避免每轮请求都重新解码+base64。
// key 含文件大小与修改时间，附件按内容哈希命名且不再改动，命中率高。
var dataURLCache sync.Map

func ImageDataURL(path string, maxSide int) (string, error) {
	if maxSide <= 0 {
		maxSide = MaxImageSide
	}
	key := path
	if st, err := os.Stat(path); err == nil {
		key = fmt.Sprintf("%s|%d|%d|%d", path, st.Size(), st.ModTime().Unix(), maxSide)
		if v, ok := dataURLCache.Load(key); ok {
			return v.(string), nil
		}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	img, err := DecodeImageAuto(b)
	if err != nil {
		return "", err
	}
	if maxSide <= 0 {
		maxSide = MaxImageSide
	}
	out, err := EncodePNG(ResizeMax(img, maxSide))
	if err != nil {
		return "", err
	}
	side := maxSide
	for len(out) > MaxImageBytes && side > 512 {
		side = side * 3 / 4
		out, err = EncodePNG(ResizeMax(img, side))
		if err != nil {
			return "", err
		}
	}
	url := "data:image/png;base64," + base64.StdEncoding.EncodeToString(out)
	dataURLCache.Store(key, url)
	return url, nil
}

// IsImagePath 判断扩展名是否图片。
func IsImagePath(p string) bool {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".bmp", ".webp":
		return true
	}
	return false
}
