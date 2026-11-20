//go:build windows

package ui

// Windows 侧的 canvas 渲染后端：把 Canvas 的几何绘制调用记录成命令流，
// 每帧只把"脏区"重放给 tdewolff/canvas 的矢量光栅器，合成到全帧缓存后
// 写回 DIB，再由 GDI 在顶层重放文本。
//
// 分工理由（见 docs/canvas-render.md）：
//   - 几何（圆角、圆环、线条、渐变）交给矢量光栅器，获得抗锯齿与层次感；
//   - 文本仍由 GDI 绘制，字体度量、换行、省略与旧版完全一致，不会因换后端
//     出现行高抖动或字距跳变；
//   - 每帧命令流与前帧做前缀比对，静止或小变化帧只重放一小块区域，
//     稳态成本接近一次 memcpy。

import (
	"image"
	"os"
	"strings"
	"sync"
	"time"
	"unsafe"

	"win7harness/internal/core"
	"win7harness/internal/win32"
)

// renderMode 决定 Canvas 走哪条绘制路径。
type renderMode uint8

const (
	renderGDI    renderMode = iota // 纯 GDI（显式回退路径）
	renderCanvas                   // 几何走 canvas 矢量光栅（默认），文本走 GDI 重放
)

// String 返回后端名，用于日志。
func (m renderMode) String() string {
	if m == renderCanvas {
		return "canvas"
	}
	return "gdi"
}

// canvasRenderEnv 是后端选择环境变量：canvas（默认）| gdi（回退）。
const canvasRenderEnv = "WIN7H_RENDER"

// pickRenderMode 读取环境变量决定后端。
// 默认走 canvas 矢量光栅；显式写 gdi / off / 0 / false 一键回退到旧 GDI 路径，
// 老机器或显色异常时无需重编译就能现场切回。
// v1.29.1 修复了 FillRect 绕过命令流导致聊天页空白的问题，见 docs/canvas-render.md 第 8 节。
func pickRenderMode() renderMode {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(canvasRenderEnv))) {
	case "gdi", "off", "0", "false":
		return renderGDI
	}
	return renderCanvas
}

// rxState 是 canvas 后端的一帧状态。
type rxState struct {
	scene   rxScene     // 本帧命令流
	prev    rxScene     // 上帧命令流
	frame   *image.RGBA // 全帧合成缓存（物理像素，不透明）
	hasPrev bool        // 是否已有可比较的上帧
	curClip [4]int32    // 当前裁剪（物理像素），clipNone 表示无裁剪
	textBuf []rxCmd     // 预留：文本命令快查
	prof    rxProf      // 帧成本统计（WIN7H_PROFILE=1 时才累加）
	// lastDirty 是本帧实际需要写回 DIB/贴屏的区域；Empty 表示画面未变化。
	lastDirty image.Rectangle
}

// rxProfWindow 是统计窗口：120 帧打一行，约等于 4 秒的静止画面或 3.6 秒动画。
const rxProfWindow = 120

// rxProfEvery 是统计窗口的时间上限：静止画面帧数攒不够时按时间出报告。
const rxProfEvery = 3 * time.Second

// rxProf 累计帧成本。开启后每帧都很便宜（几个整数加法 + 一次 Clock），
// 关闭时连 Clock 都不取，热路径零成本。
type rxProf struct {
	frames  int     // 窗口内帧数
	skipped int     // 命令流完全一致、整帧跳过的次数
	full    int     // 脏区等于全窗口的次数
	dirtyPc float64 // 脏区面积占比累加（%）
	msSum   float64 // 单帧重放+合成耗时累加
	msPeak  float64
	texts   int       // 文本重放条数累加
	t0      time.Time // 本窗口第一帧的时刻（按时间兜底输出）
}

// rxProfOn 读取性能探针开关。默认关闭：日志每 120 帧一行，长时间开着
// 会把日志刷满，所以只在排查帧成本时用 WIN7H_PROFILE=1 显式打开。
var rxProfOn = strings.ToLower(strings.TrimSpace(os.Getenv("WIN7H_PROFILE"))) == "1"

// reset 按新尺寸重建全帧缓存，并强制下一帧全量重绘。
func (r *rxState) reset(w, h int32) {
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	r.frame = image.NewRGBA(image.Rect(0, 0, int(w), int(h)))
	r.scene.reset()
	r.prev.reset()
	r.hasPrev = false
	r.curClip = clipNone
	r.lastDirty = image.Rectangle{}
}

// render 判断当前是否为 canvas 后端。
func (c *Canvas) render() bool { return c.rx != nil }

// rec 往本帧命令流追加一条命令，自动带上当前裁剪。
func (c *Canvas) rec(cm rxCmd) {
	cm.clip = c.rx.curClip
	c.rx.scene.add(cm)
}

// flushCanvas 是 canvas 后端的一帧出口：算脏区、重放几何、写回 DIB、补画文本。
func (c *Canvas) flushCanvas() {
	rx := c.rx
	if rx.frame == nil {
		rx.reset(c.w, c.h)
	}

	var t0 time.Time
	if rxProfOn {
		t0 = time.Now()
	}

	spans := layerSpans(rx.scene.cmds)
	dirty := rxDirty(&rx.scene, &rx.prev, int(c.w), int(c.h), rx.hasPrev, len(spans) <= 1)
	rx.lastDirty = dirty
	if dirty.Empty() {
		// 命令流完全一致：几何与文本都不动，只把现在的位图贴回屏幕。
		// 仍要收掉本帧命令，否则下一帧会继续往同一条流里追加。
		rx.scene.reset()
		if rxProfOn {
			rx.prof.observe(true, 0, time.Since(t0), 0, c.w, c.h)
		}
		return
	}

	// 按图层分段重放：每段先几何（光栅+合成+写回 DIB）再文本（GDI 补画）。
	// 不分段的话文本统统画在最后，浮层底色压不住下层正文的文字。
	texts := 0
	dump := rxDumpOn && rxDumpFrames < 2
	if dump {
		rxDumpFrames++
		rxDumpOnce("本帧 %d 层 脏区 %v 帧 %dx%d", len(spans), dirty, c.w, c.h)
	}
	// 预扫描遮罩命令，记录绝对位置与所在 span 结束位置。
	// 每个 scrim 的 spanEnd 之前是它压的全部底层文本的命令区间。
	type scrimEnt struct {
		idx, spanEnd int
		rect         image.Rectangle
		color        uint32
		alpha        float64
	}
	var scrims []scrimEnt
	for _, sp := range spans {
		for j := sp.A; j < sp.B; j++ {
			if cm := rx.scene.cmds[j]; cm.kind == rxScrim {
				scrims = append(scrims, scrimEnt{j, sp.B,
					image.Rect(int(cm.x), int(cm.y), int(cm.x+cm.w), int(cm.y+cm.h)),
					cm.color, cm.alpha})
			}
		}
	}
	// 遮罩必须是幂等的：从 rx.frame（未压暗的基底）整块重新推导，而不是在 DIB 上原地叠加压暗。
	// 否则底层不重绘时，每次浮层重放都重复压暗，遮罩区几秒内衰减成纯黑（抽屉下输入区消失就是这么来的）。
	applyScrim := func(s scrimEnt) {
		c.blitFrameToDIB(s.rect) // 1. DIB := 未压暗基底（含已重放的底层几何）
		// 2. 重画该区域内所有底层 GDI 文本（帧缓存只含几何，文本必须重放；
		//     spanEnd 之前的文本全部补回，否则第 1 步会擦掉它们）
		below := make([]rxCmd, 0, 64)
		for j := 0; j < s.spanEnd; j++ {
			if cm := rx.scene.cmds[j]; cm.kind == rxText || cm.kind == rxTextRect {
				below = append(below, cm)
			}
		}
		c.replayText(below, s.rect)
		// 3. 统一压暗一次（幂等：输入来自 rx.frame + 刚重放的文本）
		c.scrimDIB(s.rect, s.color, s.alpha)
	}

	for li, sp := range spans {
		cmds := rx.scene.cmds[sp.A:sp.B]
		pb := paintBounds(cmds)
		region := dirty
		bb := pb.Intersect(dirty)
		if li > 0 {
			// 浮层（抽屉/菜单/弹窗）：整块重放，而不是只补脏区。
			//
			// 浮层是不透明面，"盖上"比"改了什么"更接近语义：只补脏区的话，
			// 上一层（正文）的文字会留在浮层没被重放到的像素里，表现为面板里
			// 浮出一列残缺的字。整块重放的成本是一个面板大小的光栅，可接受。
			if pb.Empty() || !pb.Overlaps(dirty) {
				continue
			}
			region = rxUnion(dirty, pb)
			bb = pb
		}
		if bb.Empty() {
			continue
		}
		// 1. 遮罩：先压到后备位图上（连上一层已画好的文字一起压）。
		//    按整块矩形处理，不做脏区求交：遮罩是"覆盖"语义，只压脏区的话，
		//    上一次遗留的像素会在没被压到的地方翻出来（浮层开合时见过这种碎片）。
		for _, s := range scrims {
			if s.idx >= sp.A && s.idx < sp.B {
				applyScrim(s)
			}
		}
		// 2. 本层几何：只把本层画到的矩形写回 DIB。
		//    整块脏区回写会把上一层刚补画的文字擦掉（帧缓存里只有几何）。
		if hasGeometry(cmds) {
			if sub := renderRegion(cmds, region); sub != nil {
				compositeOver(rx.frame, region.Min, sub)
				c.blitFrameToDIB(bb)
			}
		}
		// 3. 本层文本
		texts += c.replayText(cmds, region)
		if dump {
			rxDumpOnce("  层[%d,%d) 命令 %d 回写 %v 文本 %d", sp.A, sp.B, len(cmds), bb, texts)
		}
	}
	rx.prev.cmds = append(rx.prev.cmds[:0], rx.scene.cmds...)
	rx.scene.reset()
	rx.hasPrev = true

	if rxProfOn {
		rx.prof.observe(false, dirtyPct(dirty, c.w, c.h), time.Since(t0), texts, c.w, c.h)
	}
}

// dirtyPct 返回脏区占全窗口的百分比（用于判断"这一帧其实接近全屏"）。
func dirtyPct(r image.Rectangle, w, h int32) float64 {
	total := float64(w) * float64(h)
	if total <= 0 {
		return 100
	}
	return float64(r.Dx()) * float64(r.Dy()) * 100 / total
}

// observe 记录一帧的成本；满一窗（120 帧）打一行日志并清零。
// skip 为 true 表示整帧跳过（命令流一字未变），此时 pct 无意义。
func (p *rxProf) observe(skip bool, pct float64, d time.Duration, texts int, w, h int32) {
	ms := float64(d.Microseconds()) / 1000
	if p.frames == 0 {
		p.t0 = time.Now()
	}
	p.frames++
	if skip {
		p.skipped++
	} else {
		p.dirtyPc += pct
		p.texts += texts
		if pct >= 99.5 {
			p.full++
		}
	}
	p.msSum += ms
	if ms > p.msPeak {
		p.msPeak = ms
	}
	// 两种收口：攒够帧数，或者攒够时间。静止画面几乎不重绘，
	// 只按帧数收口的话探针会长时间不出声，看起来像没生效。
	if p.frames < rxProfWindow && time.Since(p.t0) < rxProfEvery {
		return
	}
	n := float64(p.frames)
	drawn := p.frames - p.skipped
	dp := 0.0
	tp := 0.0
	if drawn > 0 {
		dp = p.dirtyPc / float64(drawn)
		tp = float64(p.texts) / float64(drawn)
	}
	core.Logf("帧成本: %d 帧 跳过 %d 脏区均值 %.1f%% 重放耗时 均值 %.2fms 峰值 %.2fms 全文重绘 %d 文本重放 %.0f 条/帧（%dx%d）",
		p.frames, p.skipped, dp, p.msSum/n, p.msPeak, p.full, tp, w, h)
	*p = rxProf{}
}

// blitFrameToDIB 把全帧缓存的指定区域写成 32bpp top-down BGRA 到后备位图。
// DIB 无 alpha 通道，第 4 字节补 0xFF 即可。
func (c *Canvas) blitFrameToDIB(r image.Rectangle) {
	f := c.rx.frame
	if f == nil || c.bits == nil {
		return
	}
	bw := int(c.w)
	bh := int(c.h)
	if bw <= 0 || bh <= 0 {
		return
	}
	r = r.Intersect(image.Rect(0, 0, bw, bh))
	if r.Empty() {
		return
	}
	pix := unsafe.Slice((*byte)(c.bits), bw*bh*4)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		frow := f.Pix[y*f.Stride : y*f.Stride+bw*4]
		drow := pix[y*bw*4 : (y+1)*bw*4]
		for x := r.Min.X; x < r.Max.X; x++ {
			s := frow[x*4 : x*4+4]
			d := drow[x*4 : x*4+4]
			d[0], d[1], d[2], d[3] = s[2], s[1], s[0], 0xFF
		}
	}
}

// replayText 用 GDI 把落在脏区内的文本命令补画到后备位图上，返回重放的条数。
// cmds 通常是"一个图层"的命令，保证文本压在本层几何之上、又不越过更上层。
// 文本位置、字体、对齐全部复用原来的 GDI 调用，保证与旧版逐字节一致。
func (c *Canvas) replayText(cmds []rxCmd, dirty image.Rectangle) int {
	hit := false
	for i := 0; i < len(cmds); i++ {
		k := cmds[i].kind
		if k != rxText && k != rxTextRect {
			continue
		}
		if cmds[i].bounds().Overlaps(dirty) {
			hit = true
			break
		}
	}
	if !hit {
		return 0
	}

	fullRegion := win32.CreateRectRgn(0, 0, c.w+1, c.h+1)
	defer win32.DeleteObject(fullRegion)

	// 文本重放是滚动/翻页时最大的开销：一次滚动要重画 150~220 条文本，
	// 而每条原本都要做"设裁剪区 + 选字体 + 设背景模式 + 设颜色 + 画 + 还原"
	// 六次 GDI 调用，220 条就是一千三百多次。这些状态绝大多数是重复的，
	// 只在真正变化时切换，调用次数能降一个数量级。
	//
	// 背景模式全程透明，循环外设一次即可。
	win32.SetBkMode(c.memDC, win32.TRANSPARENT)

	n := 0
	var curClip [4]int32
	clipSet := false
	var curFont uintptr
	var curColor uint32
	colorSet := false
	for i := 0; i < len(cmds); i++ {
		cm := cmds[i]
		if cm.kind != rxText && cm.kind != rxTextRect {
			continue
		}
		if !cm.bounds().Overlaps(dirty) {
			continue
		}
		n++
		var clip [4]int32
		if !cm.clipEmpty() {
			clip = [4]int32{cm.clip[0], cm.clip[1], cm.clip[2], cm.clip[3]}
		}
		if !clipSet || clip != curClip {
			if cm.clipEmpty() {
				win32.SelectClipRgn(c.memDC, fullRegion)
			} else {
				rgn := win32.CreateRectRgn(clip[0], clip[1], clip[2], clip[3])
				win32.SelectClipRgn(c.memDC, rgn)
				win32.DeleteObject(rgn)
			}
			curClip, clipSet = clip, true
		}
		f := uintptr(c.font(cm.size, cm.weight, false, cm.face))
		if f != curFont {
			win32.SelectObject(c.memDC, f)
			curFont = f
		}
		if !colorSet || cm.color != curColor {
			win32.SetTextColor(c.memDC, cm.color)
			curColor, colorSet = cm.color, true
		}
		if cm.kind == rxText {
			win32.TextOut(c.memDC, int32(cm.x), int32(cm.y), cm.text)
		} else {
			pr := win32.RECT{
				Left: int32(cm.x), Top: int32(cm.y),
				Right: int32(cm.x + cm.w), Bottom: int32(cm.y + cm.h),
			}
			win32.DrawText(c.memDC, cm.text, &pr, cm.flags|win32.DT_NOPREFIX)
		}
	}
	if curFont != 0 {
		// 还原成默认字体，避免影响后续别的绘制
		win32.SelectObject(c.memDC, uintptr(c.font(12, 400, false, FaceUI)))
	}
	win32.SelectClipRgn(c.memDC, fullRegion)
	return n
}

// rxDumpOn 打开绘制指令转储（WIN7H_RXDUMP=1）。只在排查"某个面颜色/透明度
// 不对"时用：把关键绘制调用的原始参数写进日志，避免靠截图猜参数。
var rxDumpOn = strings.ToLower(strings.TrimSpace(os.Getenv("WIN7H_RXDUMP"))) == "1"

var (
	rxDumpMu     sync.Mutex
	rxDumpN      int
	rxDumpFrames int
)

// rxDumpOnce 写一条转储（每类只写前 8 条，防止刷屏）。
func rxDumpOnce(format string, args ...any) {
	rxDumpMu.Lock()
	defer rxDumpMu.Unlock()
	if rxDumpN >= 200 {
		return
	}
	rxDumpN++
	core.Logf("RXDUMP "+format, args...)
}

// scrimDIB 把一块矩形按 alpha 压暗，直接写后备位图的像素。
//
// 为什么绕过帧缓存：遮罩要压在"已经画好的 GDI 文本"之上，而文本只存在于
// 后备位图里、不在几何帧缓存里。直接改 DIB 是唯一能让遮罩同时盖住几何与文本
// 的做法，且每帧只处理 dirty 与遮罩矩形的交集，静止时零成本。
func (c *Canvas) scrimDIB(r image.Rectangle, color uint32, alpha float64) {
	if c.bits == nil || alpha <= 0 {
		return
	}
	w, h := int(c.w), int(c.h)
	r = r.Intersect(image.Rect(0, 0, w, h))
	if r.Empty() || w <= 0 || h <= 0 {
		return
	}
	a := uint32(alpha*255 + 0.5)
	if a == 0 {
		return
	}
	if a > 255 {
		a = 255
	}
	ia := 255 - a
	cr := uint32(color & 0xFF)
	cg := uint32((color >> 8) & 0xFF)
	cb := uint32((color >> 16) & 0xFF)
	pix := unsafe.Slice((*byte)(c.bits), w*h*4)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		row := pix[y*w*4:]
		for x := r.Min.X; x < r.Max.X; x++ {
			p := row[x*4 : x*4+4] // DIB 是 BGRA
			p[0] = byte(cb*a/255 + uint32(p[0])*ia/255)
			p[1] = byte(cg*a/255 + uint32(p[1])*ia/255)
			p[2] = byte(cr*a/255 + uint32(p[2])*ia/255)
		}
	}
}
