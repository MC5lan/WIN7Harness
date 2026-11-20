//go:build windows

package ui

import (
	"sync"
	"unsafe"

	"win7harness/internal/core"
	"win7harness/internal/win32"
)

// Canvas 是双缓冲绘制表面：所有绘制先落到内存 DIB，再一次性 BitBlt 到窗口，
// 从而彻底避免闪烁。GDI 无 alpha 通道，因此"半透明"通过 Mix 手动混色实现，
// 在纯色/渐变背景上效果与真透明一致，且性能远高于逐像素合成。
type Canvas struct {
	hwnd  win32.HWND
	w, h  int32
	memDC win32.HDC
	bmp   win32.HBITMAP
	bits  unsafe.Pointer

	oldBmp uintptr

	brushes map[uint32]win32.HBRUSH
	pens    map[uint32]win32.HPEN
	pensW   map[penKey]win32.HPEN

	fontMu sync.Mutex
	fonts  map[fontKey]win32.HFONT

	// mcache 缓存文本测量结果。绘制一帧会调用测量几百次（省略号、
	// 居中、换行都要先量宽度），而每量一次就要四次 GDI 调用（选字体、
	// 设背景、取宽度、还原）。同样的标签/消息在帧间反复出现，缓存几乎必中。
	mcache map[measureKey]int32

	clipStack []win32.RECT
	curFont   win32.HFONT
	scale     float32
	// textScale 是内容文字缩放系数（字号缩放的唯一落点）。
	// 只作用于文字本身，不动界面框架布局：框架的行高、按钮高度都是常量，
	// 跟着放大就会把文字顶出行框。聊天区的内容高度全部来自测量，
	// 所以只有它能安全地整体放大。
	textScale float32
	// bpp 是屏幕色深：<=16 时贴屏前要自己做量化抖动，否则渐变色带很难看。
	bpp int32

	// mode 是当前渲染后端；rx 非空表示几何走 canvas 矢量光栅。
	mode renderMode
	rx   *rxState
}

type fontKey struct {
	height int32
	weight int32
	italic bool
	face   string
}

var globalFonts = struct {
	sync.Mutex
	m map[fontKey]win32.HFONT
}{m: map[fontKey]win32.HFONT{}}

// NewCanvas 创建与窗口 DC 兼容的绘制表面。
func NewCanvas(hwnd win32.HWND, scale float32) *Canvas {
	c := &Canvas{
		hwnd:      hwnd,
		brushes:   map[uint32]win32.HBRUSH{},
		pens:      map[uint32]win32.HPEN{},
		scale:     scale,
		textScale: 1,
	}
	screen := win32.GetDC(hwnd)
	c.memDC = win32.CreateCompatibleDC(screen)
	c.bpp = win32.GetDeviceCaps(screen, 12) // BITSPIXEL
	win32.ReleaseDC(hwnd, screen)
	c.mode = pickRenderMode()
	if c.mode == renderCanvas {
		c.rx = &rxState{}
	}
	core.Logf("渲染后端: %s（屏幕色深 %dbpp, 缩放 %.2f）", c.mode.String(), c.bpp, c.scale)
	c.Resize(1, 1)
	return c
}

// Resize 重建后备位图（窗口尺寸变化时调用）。
func (c *Canvas) Resize(w, h int32) {
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	if c.bmp != 0 && c.w == w && c.h == h {
		return
	}
	if c.bmp != 0 {
		win32.SelectObject(c.memDC, c.oldBmp)
		win32.DeleteObject(uintptr(c.bmp))
		c.bmp = 0
	}
	c.w, c.h = w, h
	bmp, bits := win32.CreateDIBSection(c.memDC, w, h)
	c.bmp = bmp
	c.bits = bits
	c.oldBmp = win32.SelectObject(c.memDC, uintptr(bmp))
	if c.rx != nil {
		// 尺寸变了，全帧缓存与脏区基线都作废，下一帧整屏重放。
		c.rx.reset(w, h)
	}
}

// Scale 返回 DPI 缩放系数（96dpi = 1.0）。
func (c *Canvas) Scale() float32 { return c.scale }

// BitsPerPixel 返回屏幕色深，供启动自检与低色深补偿判定。
func (c *Canvas) BitsPerPixel() int32 { return c.bpp }

// SetScale 在 DPI 变化时更新缩放系数。位图尺寸不变，只改逻辑↔物理换算，
// 因此不需要重建后备位图，下一帧重绘即可。
func (c *Canvas) SetScale(s float32) {
	if s < 1.0 {
		s = 1.0
	}
	c.scale = s
}

// W / H 返回画布逻辑尺寸（已除以 scale），布局统一按逻辑像素计算。
func (c *Canvas) W() int32 { return int32(float32(c.w) / c.scale) }
func (c *Canvas) H() int32 { return int32(float32(c.h) / c.scale) }

// Px 逻辑像素转物理像素。
func (c *Canvas) Px(v int32) int32 { return int32(float32(v) * c.scale) }

// Blend 在两个颜色之间线性插值。
func Blend(a, b uint32, t float32) uint32 {
	if t <= 0 {
		return a
	}
	if t >= 1 {
		return b
	}
	ar, ag, ab := a&0xFF, (a>>8)&0xFF, (a>>16)&0xFF
	br, bg, bb := b&0xFF, (b>>8)&0xFF, (b>>16)&0xFF
	r := uint32(float32(ar) + (float32(br)-float32(ar))*t)
	g := uint32(float32(ag) + (float32(bg)-float32(ag))*t)
	bl := uint32(float32(ab) + (float32(bb)-float32(ab))*t)
	return r | g<<8 | bl<<16
}

// Alpha 把 fg 以 alpha 比例混到 bg 上。
func Alpha(bg, fg uint32, alpha float32) uint32 { return Blend(bg, fg, alpha) }

func (c *Canvas) brush(color uint32) win32.HBRUSH {
	if b, ok := c.brushes[color]; ok {
		return b
	}
	b := win32.CreateSolidBrush(color)
	c.brushes[color] = b
	return b
}

func (c *Canvas) pen(color uint32) win32.HPEN {
	if p, ok := c.pens[color]; ok {
		return p
	}
	p := win32.CreatePen(win32.PS_SOLID, 1, color)
	c.pens[color] = p
	return p
}

type penKey struct {
	color uint32
	width int32
}

// penW 按颜色和宽度缓存的粗笔（createPen 比每次创建快得多）
func (c *Canvas) penW(color uint32, width int32) win32.HPEN {
	k := penKey{color, width}
	if p, ok := c.pensW[k]; ok {
		return p
	}
	p := win32.CreatePen(win32.PS_SOLID, width, color)
	if c.pensW == nil {
		c.pensW = map[penKey]win32.HPEN{}
	}
	c.pensW[k] = p
	return p
}

// Clear 用纯色铺满。
func (c *Canvas) Clear(color uint32) {
	c.FillRectP(win32.RECT{Left: 0, Top: 0, Right: c.w, Bottom: c.h}, color)
}

// FillRect 填充矩形（逻辑坐标）。
func (c *Canvas) FillRect(r Rect, color uint32) {
	// 统一走 FillRectP：canvas 后端下所有几何都必须进命令流。
	// 否则这里直接把背景刷到 GDI 位图上，脏区局部回写时整帧会被刷成纯色。
	c.FillRectP(c.phys(r), color)
}

// InvalidateFrame 丢弃与上一帧的比较基线，让下一帧整屏重绘。
//
// 用在"布局拓扑变了"的时机（抽屉开合、面板换位）：局部脏区只覆盖"变了的命令"，
// 但拓扑变化会让整块面板挪位，残留像素会以碎片形式留在新面板里。
// 一帧全量重绘的代价（几毫秒、只在开合抽屉时发生）远小于这种视觉残留。
func (c *Canvas) InvalidateFrame() {
	if c.rx != nil {
		c.rx.hasPrev = false
	}
}

// Layer 声明一个图层边界：此后的绘制视为"更靠上的浮层"。
//
// 为什么需要它：canvas 后端里几何走矢量光栅、文本走 GDI 重放，两趟分开做，
// 结果就是"文本永远压在几何之上"。抽屉、菜单、弹窗这类浮层一旦有自己的底色，
// 下层正文的文字就会从底色上透出来。调用 Layer() 把命令流切成多段，
// 每段各自"先几何后文本"，栈序就还原了。
//
// GDI 后端本身就是按调用顺序画的，这里什么都不用做。
func (c *Canvas) Layer() {
	if c.render() {
		c.rec(rxCmd{kind: rxLayer})
	}
}

// FillRectP 填充矩形（物理坐标，内部用）。
func (c *Canvas) FillRectP(r win32.RECT, color uint32) {
	if c.render() {
		c.rec(rxCmd{kind: rxFill, x: float64(r.Left), y: float64(r.Top),
			w: float64(r.Width()), h: float64(r.Height()), color: color})
		return
	}
	win32.FillRect(c.memDC, r, c.brush(color))
}

// FillRectAlpha 用半透明色填充矩形（alpha 为不透明度 0..1）。
//
// 只给"浮层遮罩"这类必须真透明的面用：
//   - canvas 后端是真混合：命令记为 rxScrim，重放时逐像素混到后备位图上，
//     所以遮罩下面的内容仍然可见（包括已经画好的文字）；
//   - GDI 后端没有 alpha 通道，画实色会把整块内容盖死，比不画更糟，
//     所以 GDI 下直接跳过——抽屉在 GDI 回退态仍然靠描边与阴影分界。
func (c *Canvas) FillRectAlpha(r Rect, color uint32, alpha float32) {
	if alpha <= 0 || r.W <= 0 || r.H <= 0 {
		return
	}
	if !c.render() {
		return
	}
	pr := c.phys(r)
	if rxDumpOn {
		rxDumpOnce("遮罩 %v alpha=%.2f color=%06X", r, alpha, color)
	}
	// 记成 rxScrim：它不参与矢量光栅，而是在重放时直接压到后备位图上。
	// alpha=1 的遮罩就是"直接把这矩形擦成纯色"，浮层底用它保证每帧完整覆盖。
	// 必须这样是因为遮罩要连"已经画好的 GDI 文本"一起压暗——
	// 光栅器只认几何，文本不在几何图层里。
	c.rec(rxCmd{kind: rxScrim, x: float64(pr.Left), y: float64(pr.Top),
		w: float64(pr.Right - pr.Left), h: float64(pr.Bottom - pr.Top),
		color: color, alpha: float64(alpha)})
}

func (c *Canvas) phys(r Rect) win32.RECT {
	return win32.RECT{
		Left:   int32(float32(r.X) * c.scale),
		Top:    int32(float32(r.Y) * c.scale),
		Right:  int32(float32(r.X+r.W) * c.scale),
		Bottom: int32(float32(r.Y+r.H) * c.scale),
	}
}

// Round 绘制圆角矩形（GDI 无抗锯齿，用同色笔刷保证边缘干净）。
func (c *Canvas) Round(r Rect, radius int32, color uint32) {
	pr := c.phys(r)
	if c.render() {
		c.rec(rxCmd{kind: rxRound, x: float64(pr.Left), y: float64(pr.Top),
			w: float64(pr.Right - pr.Left), h: float64(pr.Bottom - pr.Top),
			r: float64(radius) * float64(c.scale), color: color})
		return
	}
	br := c.brush(color)
	pen := c.pen(color)
	oldBr := win32.SelectObject(c.memDC, uintptr(br))
	oldPen := win32.SelectObject(c.memDC, uintptr(pen))
	win32.RoundRect(c.memDC, pr, int32(float32(radius)*c.scale))
	win32.SelectObject(c.memDC, oldBr)
	win32.SelectObject(c.memDC, oldPen)
}

// RoundStroke 圆角描边（空心）。
func (c *Canvas) RoundStroke(p Rect, radius int32, color uint32) {
	q := c.phys(p)
	if c.render() {
		c.rec(rxCmd{kind: rxRoundStroke, x: float64(q.Left), y: float64(q.Top),
			w: float64(q.Right - q.Left), h: float64(q.Bottom - q.Top),
			r: float64(radius) * float64(c.scale), sw: 1, color: color})
		return
	}
	nullBrush := win32.GetStockObject(5) // NULL_BRUSH
	pen := c.pen(color)
	oldBr := win32.SelectObject(c.memDC, nullBrush)
	oldPen := win32.SelectObject(c.memDC, uintptr(pen))
	win32.RoundRect(c.memDC, q, int32(float32(radius)*c.scale))
	win32.SelectObject(c.memDC, oldBr)
	win32.SelectObject(c.memDC, oldPen)
}

// Line 画线段。
func (c *Canvas) Line(x1, y1, x2, y2 int32, color uint32) {
	c.LineW(x1, y1, x2, y2, color, 1)
}

// LineW 是带线宽的版本，供图标等需要更粗笔画的地方使用。
func (c *Canvas) LineW(x1, y1, x2, y2 int32, color uint32, sw int32) {
	if sw < 1 {
		sw = 1
	}
	if c.render() {
		sc := float64(c.scale)
		c.rec(rxCmd{kind: rxLine, x: float64(x1) * sc, y: float64(y1) * sc,
			x2: float64(x2) * sc, y2: float64(y2) * sc, sw: float64(sw), color: color})
		return
	}
	old := win32.SelectObject(c.memDC, uintptr(c.penW(color, sw)))
	win32.Line(c.memDC,
		int32(float32(x1)*c.scale), int32(float32(y1)*c.scale),
		int32(float32(x2)*c.scale), int32(float32(y2)*c.scale))
	win32.SelectObject(c.memDC, old)
}

// HLine / VLine 便捷画线。
func (c *Canvas) HLine(x, y, w int32, color uint32) { c.Line(x, y, x+w, y, color) }
func (c *Canvas) VLine(x, y, h int32, color uint32) { c.Line(x, y, x, y+h, color) }

// Dot 画实心圆。
func (c *Canvas) Dot(cx, cy, r int32, color uint32) {
	if c.render() {
		sc := float64(c.scale)
		c.rec(rxCmd{kind: rxDot, x: float64(cx) * sc, y: float64(cy) * sc,
			r: float64(r) * sc, color: color})
		return
	}
	pr := win32.RECT{
		Left: int32(float32(cx-r) * c.scale), Top: int32(float32(cy-r) * c.scale),
		Right: int32(float32(cx+r) * c.scale), Bottom: int32(float32(cy+r) * c.scale),
	}
	oldBr := win32.SelectObject(c.memDC, uintptr(c.brush(color)))
	oldPen := win32.SelectObject(c.memDC, uintptr(c.pen(color)))
	win32.Ellipse(c.memDC, pr)
	win32.SelectObject(c.memDC, oldBr)
	win32.SelectObject(c.memDC, oldPen)
}

// Ring 画圆环（用于呼吸状态点）。
func (c *Canvas) Ring(cx, cy, radius int32, color uint32) {
	if c.render() {
		sc := float64(c.scale)
		c.rec(rxCmd{kind: rxRing, x: float64(cx) * sc, y: float64(cy) * sc,
			r: float64(radius) * sc, sw: 1, color: color})
		return
	}
	pr := win32.RECT{
		Left: int32(float32(cx-radius) * c.scale), Top: int32(float32(cy-radius) * c.scale),
		Right: int32(float32(cx+radius) * c.scale), Bottom: int32(float32(cy+radius) * c.scale),
	}
	nullBrush := win32.GetStockObject(5)
	oldBr := win32.SelectObject(c.memDC, nullBrush)
	oldPen := win32.SelectObject(c.memDC, uintptr(c.pen(color)))
	win32.Ellipse(c.memDC, pr)
	win32.SelectObject(c.memDC, oldBr)
	win32.SelectObject(c.memDC, oldPen)
}

// Gradient 垂直/水平渐变。
func (c *Canvas) Gradient(r Rect, from, to uint32, vertical bool) {
	if c.render() {
		pr := c.phys(r)
		c.rec(rxCmd{kind: rxGradient, x: float64(pr.Left), y: float64(pr.Top),
			w: float64(pr.Right - pr.Left), h: float64(pr.Bottom - pr.Top),
			color: from, grad: to, vert: vertical})
		return
	}
	win32.GradientFillRect(c.memDC, c.phys(r), from, to, vertical)
}

// Clip 设置裁剪区（配合 PopClip 使用）。
func (c *Canvas) Clip(r Rect) {
	pr := c.phys(r)
	if c.render() {
		// 与 GDI SelectClipRgn 一致：新裁剪直接替换当前裁剪，不做叠加。
		c.rx.curClip = [4]int32{pr.Left, pr.Top, pr.Right, pr.Bottom}
	}
	rgn := win32.CreateRectRgn(pr.Left, pr.Top, pr.Right, pr.Bottom)
	win32.SelectClipRgn(c.memDC, rgn)
	win32.DeleteObject(rgn)
	c.clipStack = append(c.clipStack, pr)
}

// PopClip 恢复到上一层裁剪区（栈空时恢复全窗口）。
//
// 这里必须是"弹出即恢复上层"，而不是"一律恢复全窗口"：
// 聊天页是 Clip(消息区) -> 内部 DrawLines 每行 Clip/PopClip，
// 设置页是 Clip(滚动区) -> 内部每个 TextField 各自动 PopClip。
// 过去出栈后一律画成全窗口，第一条内容/第一个输入框之后裁剪就失效了，
// 于是后续内容直接画到顶栏、输入栏和底部状态栏上（表现为"内容越界、叠字"）。
func (c *Canvas) PopClip() {
	if len(c.clipStack) > 0 {
		c.clipStack = c.clipStack[:len(c.clipStack)-1]
	}
	var pr win32.RECT
	if n := len(c.clipStack); n > 0 {
		pr = c.clipStack[n-1]
	} else {
		pr = win32.RECT{Left: 0, Top: 0, Right: c.w + 1, Bottom: c.h + 1}
	}
	if c.render() {
		c.rx.curClip = [4]int32{pr.Left, pr.Top, pr.Right, pr.Bottom}
	}
	rgn := win32.CreateRectRgn(pr.Left, pr.Top, pr.Right, pr.Bottom)
	win32.SelectClipRgn(c.memDC, rgn)
	win32.DeleteObject(rgn)
}

// font 取（并缓存）字体句柄。
func (c *Canvas) font(size int32, weight int32, italic bool, face string) win32.HFONT {
	ts := c.textScale
	if ts <= 0 {
		ts = 1
	}
	h := int32(float32(size) * c.scale * ts)
	if h < 6 {
		h = 6 // GDI 对极小字号会画成一条线，兜住下限
	}
	key := fontKey{h, weight, italic, face}
	globalFonts.Lock()
	f, ok := globalFonts.m[key]
	if !ok {
		f = win32.CreateFont(h, weight, italic, face)
		globalFonts.m[key] = f
	}
	globalFonts.Unlock()
	return f
}

// SetTextScale 设置内容文字缩放系数，返回旧值供调用方恢复。
// 系数被夹在 0.8~2.0：再小看不清，再大整行只剩几个字。
func (c *Canvas) SetTextScale(v float32) float32 {
	old := c.textScale
	if v < 0.8 {
		v = 0.8
	}
	if v > 2.0 {
		v = 2.0
	}
	c.textScale = v
	return old
}

// Text 在 (x,y) 处绘制文本，y 为文本顶部。
func (c *Canvas) Text(x, y int32, s string, size int32, weight int32, color uint32) {
	c.TextFace(x, y, s, size, weight, color, FaceUI)
}

// TextFace 指定字体族绘制。
func (c *Canvas) TextFace(x, y int32, s string, size int32, weight int32, color uint32, face string) {
	if s == "" {
		return
	}
	if c.render() {
		sc := float64(c.scale)
		c.rec(rxCmd{kind: rxText, x: float64(x) * sc, y: float64(y) * sc,
			size: size, weight: weight, face: face, color: color, text: s})
		return
	}
	f := c.font(size, weight, false, face)
	old := win32.SelectObject(c.memDC, uintptr(f))
	win32.SetBkMode(c.memDC, win32.TRANSPARENT)
	win32.SetTextColor(c.memDC, color)
	win32.TextOut(c.memDC, int32(float32(x)*c.scale), int32(float32(y)*c.scale), s)
	win32.SelectObject(c.memDC, old)
}

// TextRect 区域内绘制（支持对齐、换行、省略）。
func (c *Canvas) TextRect(r Rect, s string, size int32, weight int32, color uint32, flags uint32) {
	c.TextRectFace(r, s, size, weight, color, flags, FaceUI)
}

// TextRectFace 指定字体族的区域绘制。
func (c *Canvas) TextRectFace(r Rect, s string, size int32, weight int32, color uint32, flags uint32, face string) {
	if s == "" {
		return
	}
	if c.render() {
		pr := c.phys(r)
		c.rec(rxCmd{kind: rxTextRect, x: float64(pr.Left), y: float64(pr.Top),
			w: float64(pr.Right - pr.Left), h: float64(pr.Bottom - pr.Top),
			size: size, weight: weight, face: face, color: color, text: s, flags: flags})
		return
	}
	f := c.font(size, weight, false, face)
	old := win32.SelectObject(c.memDC, uintptr(f))
	win32.SetBkMode(c.memDC, win32.TRANSPARENT)
	win32.SetTextColor(c.memDC, color)
	pr := c.phys(r)
	win32.DrawText(c.memDC, s, &pr, flags|win32.DT_NOPREFIX)
	win32.SelectObject(c.memDC, old)
}

// Measure 测量单行文本宽度（逻辑像素）。
func (c *Canvas) Measure(s string, size int32, weight int32) int32 {
	return c.MeasureFace(s, size, weight, FaceUI)
}

// measureKey 是测量缓存的键（字体族 + 字号 + 字重 + 文本）。
type measureKey struct {
	face   string
	size   int32
	weight int32
	text   string
}

// MeasureFace 指定字体族测量。
func (c *Canvas) MeasureFace(s string, size int32, weight int32, face string) int32 {
	if s == "" {
		return 0
	}
	k := measureKey{face: face, size: size, weight: weight, text: s}
	if w, ok := c.mcache[k]; ok {
		return w
	}
	f := c.font(size, weight, false, face)
	old := win32.SelectObject(c.memDC, uintptr(f))
	win32.SetBkMode(c.memDC, win32.TRANSPARENT)
	w := win32.TextWidth(c.memDC, s)
	win32.SelectObject(c.memDC, old)
	res := int32(float32(w) / c.scale)
	if c.mcache == nil || len(c.mcache) > 8192 {
		// 直接清空而不是做 LRU：这里存的是"宽度"，丢了大不了下次再量一次，
		// 不值得为淘汰策略增加复杂度。
		c.mcache = make(map[measureKey]int32, 1024)
	}
	c.mcache[k] = res
	return res
}

// LineHeight 返回该字号的推荐行高（逻辑像素）。
func (c *Canvas) LineHeight(size int32) int32 {
	return size * 3 / 2
}

// Flush 把整个后备位图贴到窗口（全量兜底，供截图/测试使用）。
func (c *Canvas) Flush() {
	if c.rx != nil {
		c.flushCanvas()
	}
	// 16bpp 屏幕上先自己量化 + 抖动，避免系统截断产生色带与偏色。
	c.quantizeLowColor()
	screen := win32.GetDC(c.hwnd)
	win32.BitBlt(screen, 0, 0, c.w, c.h, c.memDC, 0, 0, win32.SRCCOPY|win32.CAPTUREBLT)
	win32.ReleaseDC(c.hwnd, screen)
}

// PrepareFrame 是 canvas 后端的一帧出口：重放命令流并返回本帧真正变化的
// 物理区域。第二个返回值 false 表示 GDI 后端（没有场景差分，调用方应贴
// Windows 给出的整块无效区）。
func (c *Canvas) PrepareFrame() (win32.RECT, bool) {
	if c.rx == nil {
		return win32.RECT{}, false
	}
	c.flushCanvas()
	d := c.rx.lastDirty
	if d.Empty() {
		return win32.RECT{}, true
	}
	return win32.RECT{
		Left:   int32(d.Min.X),
		Top:    int32(d.Min.Y),
		Right:  int32(d.Max.X),
		Bottom: int32(d.Max.Y),
	}, true
}

// FlushPrepared 把 PrepareFrame 算出的区域（或 Windows 的无效区）贴到窗口。
// 调用前必须已执行 PrepareFrame；不会再重放一遍命令流。
func (c *Canvas) FlushPrepared(r win32.RECT) {
	c.flushRect(r)
}

// FlushRect 兼容旧调用：先准备帧，再贴指定物理区域。
func (c *Canvas) FlushRect(r win32.RECT) {
	c.PrepareFrame()
	c.flushRect(r)
}

func (c *Canvas) flushRect(r win32.RECT) {
	r = c.clampRect(r)
	if r.Width() <= 0 || r.Height() <= 0 {
		return
	}
	c.quantizeLowColorRect(r)
	screen := win32.GetDC(c.hwnd)
	win32.BitBlt(screen, r.Left, r.Top, r.Width(), r.Height(),
		c.memDC, r.Left, r.Top, win32.SRCCOPY)
	win32.ReleaseDC(c.hwnd, screen)
}

func (c *Canvas) clampRect(r win32.RECT) win32.RECT {
	if r.Left < 0 {
		r.Left = 0
	}
	if r.Top < 0 {
		r.Top = 0
	}
	if r.Right > c.w {
		r.Right = c.w
	}
	if r.Bottom > c.h {
		r.Bottom = c.h
	}
	if r.Right < r.Left {
		r.Right = r.Left
	}
	if r.Bottom < r.Top {
		r.Bottom = r.Top
	}
	return r
}

// Dispose 释放 GDI 资源。
func (c *Canvas) Dispose() {
	for _, b := range c.brushes {
		win32.DeleteObject(uintptr(b))
	}
	for _, p := range c.pens {
		win32.DeleteObject(uintptr(p))
	}
	for _, p := range c.pensW {
		win32.DeleteObject(uintptr(p))
	}
	c.brushes = map[uint32]win32.HBRUSH{}
	c.pens = map[uint32]win32.HPEN{}
	c.pensW = map[penKey]win32.HPEN{}
	if c.rx != nil {
		c.rx = nil
	}
	if c.bmp != 0 {
		win32.SelectObject(c.memDC, c.oldBmp)
		win32.DeleteObject(uintptr(c.bmp))
		c.bmp = 0
	}
	if c.memDC != 0 {
		win32.DeleteDC(c.memDC)
		c.memDC = 0
	}
}

// Rect 逻辑矩形。
type Rect struct {
	X, Y, W, H int32
}

// R 构造矩形。
func R(x, y, w, h int32) Rect { return Rect{x, y, w, h} }

func (r Rect) Right() int32  { return r.X + r.W }
func (r Rect) Bottom() int32 { return r.Y + r.H }
func (r Rect) CX() int32     { return r.X + r.W/2 }
func (r Rect) CY() int32     { return r.Y + r.H/2 }

// Contains 判断点是否在矩形内。
func (r Rect) Contains(x, y int32) bool {
	return x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H
}

// Inset 内缩。
func (r Rect) Inset(d int32) Rect { return Rect{r.X + d, r.Y + d, r.W - 2*d, r.H - 2*d} }

// Offset 平移。
func (r Rect) Offset(dx, dy int32) Rect { return Rect{r.X + dx, r.Y + dy, r.W, r.H} }

// Shrink 自定义内缩。
func (r Rect) Shrink(l, t, rr, b int32) Rect {
	return Rect{r.X + l, r.Y + t, r.W - l - rr, r.H - t - b}
}

// Intersect 求交。
func (r Rect) Intersect(o Rect) Rect {
	x1, y1 := maxi(r.X, o.X), maxi(r.Y, o.Y)
	x2, y2 := mini(r.Right(), o.Right()), mini(r.Bottom(), o.Bottom())
	if x2 < x1 || y2 < y1 {
		return Rect{}
	}
	return Rect{x1, y1, x2 - x1, y2 - y1}
}

func maxi(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}
func mini(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}
