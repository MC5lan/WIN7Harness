//go:build windows

package ui

import (
	"runtime"
	"sync"
	"unsafe"

	"win7harness/internal/core"
	"win7harness/internal/win32"
)

// 低色深（16bpp 及以下）屏幕的显色补偿。
//
// 背景：远程桌面、老笔记本、虚拟机常常只有 16bpp 色深（High Color，RGB565）。
// 我们的后备位图是 32bpp，BitBlt 交给系统降位时用的是"直接截断"，
// 结果是深色主题的渐变出现明显色带、灰阶偏绿或偏紫。
// 补偿办法：在贴屏之前自己按 565 量化，并叠加 4x4 Bayer 有序抖动，
// 让量化误差在相邻像素间分布，肉眼看到的是平滑过渡而不是一条条色带。

// bayer4 是 4x4 有序抖动矩阵，取值 -8..7，单位是 8 位色阶。
var bayer4 = [4][4]int{
	{-8, 0, -6, 2},
	{4, -4, 6, -2},
	{-5, 3, -7, 1},
	{7, -1, 5, -3},
}

// quantizeLowColor 在色深 <= 16 时对后备位图做 565 量化 + 抖动。
func (c *Canvas) quantizeLowColor() {
	c.quantizeLowColorRect(win32.RECT{Left: 0, Top: 0, Right: c.w, Bottom: c.h})
}

// quantizeLowColorRect 只量化脏区：局部刷新时没必要重算整张 1080p 位图。
// 抖动相位仍用绝对 x/y，保证各次局部量化的图案与全幅一致。
func (c *Canvas) quantizeLowColorRect(r win32.RECT) {
	if c.bits == nil || c.w <= 0 || c.h <= 0 || c.bpp > 16 {
		return
	}
	r = c.clampRect(r)
	x0, y0 := int(r.Left), int(r.Top)
	x1, y1 := int(r.Right), int(r.Bottom)
	if x1 <= x0 || y1 <= y0 {
		return
	}
	w, h := int(c.w), int(c.h)
	stride := w * 4
	buf := unsafe.Slice((*byte)(c.bits), stride*h)

	workers := runtime.NumCPU()
	if workers > 8 {
		workers = 8
	}
	// 小脏区（输入框/光标/滚动条）单线程做，避免每次刷新都起一组 goroutine。
	if (x1-x0)*(y1-y0) < 20000 || y1-y0 < 32 {
		workers = 1
	}
	if workers < 1 {
		workers = 1
	}
	rows := (y1 - y0 + workers - 1) / workers
	var wg sync.WaitGroup
	for k := 0; k < workers; k++ {
		yy0 := y0 + k*rows
		yy1 := yy0 + rows
		if yy1 > y1 {
			yy1 = y1
		}
		if yy0 >= yy1 {
			break
		}
		wg.Add(1)
		core.SafeGo("ui/lowcolor.go:59", func() {
			func(yy0, yy1 int) {
				defer wg.Done()
				for y := yy0; y < yy1; y++ {
					row := buf[y*stride : (y+1)*stride]
					dy := bayer4[y&3]
					for x := x0; x < x1; x++ {
						i := x * 4
						d := dy[x&3]
						// DIB 是 BGRA 顺序：B/G/R 分别 5/6/5 位。
						row[i] = quant565(row[i], 5, d)
						row[i+1] = quant565(row[i+1], 6, d)
						row[i+2] = quant565(row[i+2], 5, d)
					}
				}
			}(yy0, yy1)
		})
	}
	wg.Wait()
}

// quant565 把 8 位分量按 bits 位量化，叠加抖动 d 后还原到 8 位。
func quant565(v byte, bits uint, d int) byte {
	levels := 1<<bits - 1
	x := int(v) + d
	if x < 0 {
		x = 0
	} else if x > 255 {
		x = 255
	}
	q := (x*levels + 127) / 255
	if q > levels {
		q = levels
	}
	return byte((q*255 + levels/2) / levels)
}
