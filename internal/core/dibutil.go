package core

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// DIB 裁剪：把屏幕抓下来的整幅 32bpp DIB 按选区裁成小图。
//
// 为什么不走 image.Image 中转：截图是热路径，一张 4K 屏是 3300 万像素，
// 转成 image.NRGBA 再编码会带来两次全幅拷贝与几十 MB 临时内存。
// 这里直接在原始字节上做行级选取，顺便把结果统一成"自上而下"的 DIB，
// 这样后续无论写剪贴板还是存 PNG 都不用再关心行序。

// DIBRect 是截图选区（左上角 + 宽高，像素坐标）。
type DIBRect struct {
	X, Y, W, H int
}

// NormalizeRect 把拖拽产生的两个点整理成规范矩形（支持反向拖拽）。
func NormalizeRect(x1, y1, x2, y2 int) DIBRect {
	if x2 < x1 {
		x1, x2 = x2, x1
	}
	if y2 < y1 {
		y1, y2 = y2, y1
	}
	return DIBRect{X: x1, Y: y1, W: x2 - x1, H: y2 - y1}
}

// ClipRect 把选区限制在图像范围内，返回是否仍有有效面积。
func ClipRect(r DIBRect, imgW, imgH int) (DIBRect, bool) {
	if r.X < 0 {
		r.W += r.X
		r.X = 0
	}
	if r.Y < 0 {
		r.H += r.Y
		r.Y = 0
	}
	if r.X+r.W > imgW {
		r.W = imgW - r.X
	}
	if r.Y+r.H > imgH {
		r.H = imgH - r.Y
	}
	return r, r.W > 0 && r.H > 0
}

// CropDIB32 从 32bpp 未压缩 DIB 中裁出一块，返回同样以 BITMAPINFOHEADER 起始的 DIB。
// 输出统一为自上而下（biHeight 为负），行按 4 字节对齐（32bpp 天然满足）。
func CropDIB32(dib []byte, r DIBRect) ([]byte, error) {
	if len(dib) < 40 {
		return nil, errors.New("位图信息头不完整")
	}
	hdrSize := int(binary.LittleEndian.Uint32(dib[0:4]))
	if hdrSize < 40 || hdrSize > 200 || len(dib) < hdrSize {
		return nil, fmt.Errorf("不支持的位图头长度 %d", hdrSize)
	}
	w := int(int32(binary.LittleEndian.Uint32(dib[4:8])))
	hSigned := int32(binary.LittleEndian.Uint32(dib[8:12]))
	bpp := int(binary.LittleEndian.Uint16(dib[14:16]))
	comp := binary.LittleEndian.Uint32(dib[16:20])
	if bpp != 32 {
		return nil, fmt.Errorf("只支持 32bpp，收到 %d", bpp)
	}
	if comp != 0 {
		return nil, fmt.Errorf("只支持未压缩 DIB，压缩方式 %d", comp)
	}
	if w <= 0 {
		return nil, errors.New("位图宽度非法")
	}
	topDown := hSigned < 0
	h := int(hSigned)
	if topDown {
		h = -h
	}
	if h <= 0 {
		return nil, errors.New("位图高度非法")
	}
	// 裸 DIB 没有 bfOffBits 字段，像素紧跟在信息头之后；
	// 32bpp 不带调色板，因此偏移就是头长度（这里不能误读 biSizeImage）。
	off := hdrSize
	srcStride := w * 4
	if len(dib) < off+srcStride*h {
		return nil, fmt.Errorf("像素数据不足：需要 %d，实际 %d", off+srcStride*h, len(dib))
	}

	rr, ok := ClipRect(r, w, h)
	if !ok {
		return nil, errors.New("选区为空")
	}

	outStride := rr.W * 4
	out := make([]byte, hdrSize+outStride*rr.H)
	copy(out[:hdrSize], dib[:hdrSize])
	binary.LittleEndian.PutUint32(out[4:8], uint32(rr.W))
	// 统一自上而下，调用方不必再判行序。
	binary.LittleEndian.PutUint32(out[8:12], uint32(int32(-rr.H)))
	binary.LittleEndian.PutUint32(out[20:24], uint32(outStride*rr.H))

	for row := 0; row < rr.H; row++ {
		var srcRow int
		if topDown {
			srcRow = rr.Y + row
		} else {
			// 自下而上存储：目标第 0 行对应源里的最后一行
			srcRow = h - 1 - (rr.Y + row)
		}
		s := off + srcRow*srcStride + rr.X*4
		d := hdrSize + row*outStride
		copy(out[d:d+outStride], dib[s:s+outStride])
	}
	return out, nil
}

// DIBSize 返回 DIB 的宽高与色深，用于校验与自检回显。
func DIBSize(dib []byte) (w, h, bpp int, err error) {
	if len(dib) < 16 {
		return 0, 0, 0, errors.New("位图信息头不完整")
	}
	w = int(int32(binary.LittleEndian.Uint32(dib[4:8])))
	hs := int32(binary.LittleEndian.Uint32(dib[8:12]))
	h = int(hs)
	if h < 0 {
		h = -h
	}
	bpp = int(binary.LittleEndian.Uint16(dib[14:16]))
	return w, h, bpp, nil
}
