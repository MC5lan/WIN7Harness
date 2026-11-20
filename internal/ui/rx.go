package ui

// 与平台无关的绘制命令记录与光栅重放核心。
//
// 设计目标：让 Canvas 的每一次绘制调用先落到命令队列，再按"脏区"重放到
// tdewolff/canvas 的矢量光栅器上，从而获得抗锯齿圆角、真正的高光/描边层次。
// 该文件不带 build tag，可在 Linux 上直接跑单测，验证坐标、圆角、裁剪、渐变
// 等语义，避免只能在 Windows 上盲调。
//
// 坐标约定：命令内的坐标一律是"物理像素、原点左上、y 轴向下"，与 GDI 一致；
// 重放时把区域原点平移到 (0,0)，因此同一批命令既能整屏光栅，也能只光栅脏区。

import (
	"image"
	"image/color"
	"math"

	"github.com/tdewolff/canvas"
	"github.com/tdewolff/canvas/renderers/rasterizer"
)

// rxKind 是绘制命令的种类。
type rxKind uint8

const (
	rxFill        rxKind = iota // 纯色矩形
	rxRound                     // 圆角矩形填充
	rxRoundStroke               // 圆角矩形描边
	rxLine                      // 线段
	rxDot                       // 实心圆
	rxRing                      // 圆环
	rxGradient                  // 线性渐变矩形
	rxImage                     // 位图
	rxText                      // 单点文本（由 GDI 重放）
	rxTextRect                  // 区域内文本（由 GDI 重放）
	rxLayer                     // 图层边界（不绘制，只切分重放批次）
	rxScrim                     // 半透明遮罩（直接压到后备位图上，见 scrimDIB）
)

// clipNone 表示命令不受裁剪限制。
var clipNone = [4]int32{0, 0, -1, -1}

// rxCmd 是一条绘制命令。
type rxCmd struct {
	kind  rxKind
	x, y  float64 // 物理坐标（左上角 / 圆心 / 起点）
	w, h  float64
	x2    float64 // 线段终点
	y2    float64
	r     float64 // 圆角半径 / 圆半径
	sw    float64 // 线宽（描边、线、圆环）
	color uint32  // COLORREF（0x00BBGGRR）
	// alpha 是不透明度，0 与 1 都表示"按 alpha=1 画"（纯色不透明）。
	// 只有抽屉遮罩这类需要真透明的面才会填中间值：光栅器天然带 alpha，
	// 合成到全帧缓存时按预乘公式混合，因此不需要提前和底色混色。
	alpha  float64
	grad   uint32 // 渐变终点色
	vert   bool   // 渐变方向：true 垂直
	text   string
	size   int32
	weight int32
	face   string
	flags  uint32
	img    *image.RGBA
	clip   [4]int32 // 生效裁剪矩形（物理像素），left,top,right,bottom
}

// clipEmpty 判断该命令是否不受裁剪约束。
func (c rxCmd) clipEmpty() bool { return c.clip[2] <= c.clip[0] || c.clip[3] <= c.clip[1] }

// clipRect 返回裁剪矩形。
func (c rxCmd) clipRect() image.Rectangle {
	return image.Rect(int(c.clip[0]), int(c.clip[1]), int(c.clip[2]), int(c.clip[3]))
}

// bounds 返回命令占据的物理像素范围（偏大取值，保证脏区不漏）。
func (c rxCmd) bounds() image.Rectangle {
	switch c.kind {
	case rxFill, rxRound, rxRoundStroke, rxGradient, rxImage, rxTextRect, rxScrim:
		return rxRect(c.x, c.y, c.w, c.h)
	case rxLine:
		pad := c.sw
		if pad < 1 {
			pad = 1
		}
		x0, y0, x1, y1 := c.x, c.y, c.x2, c.y2
		if x1 < x0 {
			x0, x1 = x1, x0
		}
		if y1 < y0 {
			y0, y1 = y1, y0
		}
		return rxRect(x0-pad, y0-pad, x1-x0+2*pad+1, y1-y0+2*pad+1)
	case rxDot:
		return rxRect(c.x-c.r-1, c.y-c.r-1, 2*c.r+2, 2*c.r+2)
	case rxRing:
		return rxRect(c.x-c.r-c.sw-1, c.y-c.r-c.sw-1, 2*(c.r+c.sw)+2, 2*(c.r+c.sw)+2)
	case rxText:
		// 文本宽度按字符数估算（偏大无误）：重放走 GDI，脏区多算一点没关系。
		n := float64(len([]rune(c.text)))
		if c.w > 0 {
			n = c.w
		}
		lineH := float64(c.size) * 1.6
		if c.h > 0 {
			lineH = c.h
		}
		// 字形实际宽度可能比"字符数×字号"更宽（全角、粗体、连字），
		// 这里给 35% 余量：脏区宁大勿小，少一点像素换来旧字不会残留。
		return rxRect(c.x-3, c.y-3, n*float64(c.size)*1.35+6, lineH+6)
	}
	return image.Rectangle{}
}

// rxRect 由左上角与宽高构造矩形（四舍五入到整像素，并向外扩 1px 兜住抗锯齿边缘）。
func rxRect(x, y, w, h float64) image.Rectangle {
	x0 := int(rxFloor(x)) - 1
	y0 := int(rxFloor(y)) - 1
	x1 := int(rxCeil(x+w)) + 1
	y1 := int(rxCeil(y+h)) + 1
	return image.Rect(x0, y0, x1, y1)
}

func rxFloor(v float64) float64 {
	f := float64(int64(v))
	if v < 0 && v != f {
		return f - 1
	}
	return f
}

func rxCeil(v float64) float64 {
	f := float64(int64(v))
	if v > 0 && v != f {
		return f + 1
	}
	return f
}

// rxColor 把 COLORREF 转成 RGBA。
func rxColor(c uint32) color.RGBA {
	return color.RGBA{R: byte(c & 0xFF), G: byte((c >> 8) & 0xFF), B: byte((c >> 16) & 0xFF), A: 0xFF}
}

// rxColorA 把 COLORREF 与不透明度合成 RGBA。a<=0 或 a>=1 都按不透明处理：
// 命令流里 alpha 只用于遮罩，0 值不应当变成"完全看不见"这种隐蔽失效。
func rxColorA(c uint32, a float64) color.RGBA {
	col := rxColor(c)
	if a <= 0 || a >= 1 {
		return col
	}
	col.A = byte(a*255 + 0.5)
	return col
}

// rxScene 持有一帧的命令流。
type rxScene struct {
	cmds []rxCmd
}

// reset 清空命令流，保留已分配的容量。
func (s *rxScene) reset() { s.cmds = s.cmds[:0] }

// add 追加一条命令。
func (s *rxScene) add(c rxCmd) { s.cmds = append(s.cmds, c) }

// sameAs 判断两个命令流是否完全一致（用于整帧跳过）。
func (s *rxScene) sameAs(o *rxScene) bool {
	if len(s.cmds) != len(o.cmds) {
		return false
	}
	for i := range s.cmds {
		if !rxCmdEqual(s.cmds[i], o.cmds[i]) {
			return false
		}
	}
	return true
}

// firstDiff 返回首个差异命令的下标，完全一致时返回 -1。
func (s *rxScene) firstDiff(o *rxScene) int {
	n := len(s.cmds)
	if len(o.cmds) < n {
		n = len(o.cmds)
	}
	for i := 0; i < n; i++ {
		if !rxCmdEqual(s.cmds[i], o.cmds[i]) {
			return i
		}
	}
	if len(s.cmds) == len(o.cmds) {
		return -1
	}
	return n
}

// rxCmdEqual 逐字段比较两条命令。
func rxCmdEqual(a, b rxCmd) bool {
	if a.kind != b.kind || a.x != b.x || a.y != b.y || a.w != b.w || a.h != b.h ||
		a.x2 != b.x2 || a.y2 != b.y2 || a.r != b.r || a.sw != b.sw || a.color != b.color || a.grad != b.grad ||
		a.vert != b.vert || a.size != b.size || a.weight != b.weight || a.alpha != b.alpha ||
		a.face != b.face || a.flags != b.flags ||
		a.text != b.text || a.img != b.img || a.clip != b.clip {
		return false
	}
	return true
}

// unionFrom 返回从下标 i 起所有命令的并集。
func (s *rxScene) unionFrom(i int) image.Rectangle {
	if i < 0 {
		i = 0
	}
	var out image.Rectangle
	for ; i < len(s.cmds); i++ {
		out = rxUnion(out, s.cmds[i].bounds())
	}
	return out
}

// rxLayerSpan 是一个图层在命令流里的下标区间 [A, B)。
type rxLayerSpan struct{ A, B int }

// layerSpans 按 rxLayer 标记把命令流切成若干图层。
//
// 为什么需要分层：几何走矢量光栅（先整块算成位图），文本走 GDI（后画）。
// 两趟分开画意味着"文本永远在几何之上"——底层正文的文字会穿过后画的浮层底色，
// 表现为抽屉/弹窗底下一片花花绿绿的字。按图层分段重放（每层：先几何后文本）
// 就把栈序还原了，代价只是脏区被重放 N 次，而 N 是浮层数量（通常 1~3）。
func layerSpans(cmds []rxCmd) []rxLayerSpan {
	out := []rxLayerSpan{}
	start := 0
	for i := 0; i < len(cmds); i++ {
		if cmds[i].kind != rxLayer {
			continue
		}
		if i > start {
			out = append(out, rxLayerSpan{start, i})
		}
		start = i + 1
	}
	if start < len(cmds) {
		out = append(out, rxLayerSpan{start, len(cmds)})
	}
	return out
}

// bounds 返回这段命令的像素并集，用于跳过落在脏区之外的图层。
func spanBounds(cmds []rxCmd) image.Rectangle {
	var out image.Rectangle
	for _, c := range cmds {
		if c.kind == rxLayer {
			continue
		}
		out = rxUnion(out, c.bounds())
	}
	return out
}

// paintBounds 返回这段命令里"真正进几何帧缓存"的像素并集。
//
// 与 spanBounds 的区别：文本与遮罩都不进帧缓存（文本走 GDI、遮罩直接改 DIB），
// 所以"本层画到了哪里"只能按几何命令算。用 spanBounds 会把遮罩范围也算进来，
// 回写时就会把上层文字一起擦掉。
func paintBounds(cmds []rxCmd) image.Rectangle {
	var out image.Rectangle
	for _, c := range cmds {
		switch c.kind {
		case rxText, rxTextRect, rxLayer, rxScrim:
			continue
		}
		out = rxUnion(out, c.bounds())
	}
	return out
}

// hasGeometry 判断这段里是否有需要光栅的几何命令（没有就只重放文本）。
func hasGeometry(cmds []rxCmd) bool {
	for _, c := range cmds {
		switch c.kind {
		case rxText, rxTextRect, rxLayer, rxScrim:
			continue
		default:
			return true
		}
	}
	return false
}

// rxDirty 计算本帧相对上帧需要重绘的物理区域。
//
// 只用"公共前缀"是不够的：面板里点中一项会往命令流中间插一条高亮命令，
// 后面所有命令的下标整体后移，前缀比对会从插入点一路算到末尾，脏区接近全屏。
// 因此改为同时求公共前缀与公共后缀，只把中间那段差异命令的像素算进脏区。
// 只要中间段的并集覆盖了所有"可能变化的命令"，重放整条命令流就一定正确。
func rxDirty(cur, prev *rxScene, w, h int, hasPrev, fastOK bool) image.Rectangle {
	full := image.Rect(0, 0, w, h)
	if !hasPrev {
		return full
	}
	// 长度相同时逐条比对：只把"真的变了的那几条"算进脏区。
	//
	// 这条快路径是性能关键：呼吸点、光标闪烁这类动画每帧只改一两个小命令，
	// 但它们往往位于命令流靠前的位置（顶栏状态点）。若按"前缀→末尾"取脏区，
	// 一帧就要重绘 80% 的画面；逐条比对能把脏区压到几个小矩形。
	if fastOK {
		// 注意：快路径只允许在"单图层"帧里用。
		// 多图层帧（抽屉/菜单/弹窗）里，浮层是不透明整块重放，
		// 而快路径只把"变了的命令"算进脏区，两者叠加会在帧缓存里留下没写过的空洞，
		// 回写时表现为整块黑。浮层出现次数少，退回"前缀/后缀"路径不影响体验。
		if len(cur.cmds) == len(prev.cmds) {
			var out image.Rectangle
			for i := range cur.cmds {
				if rxCmdEqual(cur.cmds[i], prev.cmds[i]) {
					continue
				}
				out = rxUnion(out, cur.cmds[i].bounds())
				out = rxUnion(out, prev.cmds[i].bounds())
			}
			return out.Intersect(full)
		}
		return rxDiffBounds(cur.cmds, prev.cmds, full)
	}
	nc, np := len(cur.cmds), len(prev.cmds)
	n := nc
	if np < n {
		n = np
	}
	pre := 0
	for pre < n && rxCmdEqual(cur.cmds[pre], prev.cmds[pre]) {
		pre++
	}
	suf := 0
	for suf < n-pre && rxCmdEqual(cur.cmds[nc-1-suf], prev.cmds[np-1-suf]) {
		suf++
	}
	if pre+suf >= nc && pre+suf >= np {
		return image.Rectangle{} // 两帧命令流完全一致
	}
	var out image.Rectangle
	for i := pre; i < nc-suf; i++ {
		out = rxUnion(out, cur.cmds[i].bounds())
	}
	for i := pre; i < np-suf; i++ {
		out = rxUnion(out, prev.cmds[i].bounds())
	}
	return out.Intersect(full)
}

// rxUnion 求并集，空矩形不参与合并。
func rxUnion(a, b image.Rectangle) image.Rectangle {
	if a.Empty() {
		return b
	}
	if b.Empty() {
		return a
	}
	return a.Union(b)
}

// compositeOver 把 sub 合成到 dst 的 origin 处。
// canvas 光栅器输出预乘 alpha，故按 out = src + dst*(1-a) 计算；
// 全透明像素保留底层（脏区里没被任何命令覆盖的地方仍是上一帧的像素）。
func compositeOver(dst *image.RGBA, origin image.Point, sub *image.RGBA) {
	b := sub.Bounds()
	db := dst.Bounds()
	for y := 0; y < b.Dy(); y++ {
		dy := origin.Y + y
		if dy < db.Min.Y || dy >= db.Max.Y {
			continue
		}
		srow := sub.Pix[y*sub.Stride:]
		drow := dst.Pix[dy*dst.Stride:]
		for x := 0; x < b.Dx(); x++ {
			dx := origin.X + x
			if dx < db.Min.X || dx >= db.Max.X {
				continue
			}
			s := srow[x*4 : x*4+4]
			a := uint32(s[3])
			if a == 0 {
				continue
			}
			d := drow[dx*4 : dx*4+4]
			if a == 0xFF {
				d[0], d[1], d[2], d[3] = s[0], s[1], s[2], 0xFF
				continue
			}
			ia := 255 - a
			// 预乘合成：src 已乘过 alpha，直接加底层残留。
			d[0] = s[0] + byte(uint32(d[0])*ia/255)
			d[1] = s[1] + byte(uint32(d[1])*ia/255)
			d[2] = s[2] + byte(uint32(d[2])*ia/255)
			d[3] = 0xFF
		}
	}
}

// renderRegion 把命令流重放成一块 RGBA 区域图像（region 为物理像素区域）。
// 只处理几何命令；文本命令由调用方用 GDI 在贴图后补画。
func renderRegion(cmds []rxCmd, region image.Rectangle) *image.RGBA {
	region = region.Canon()
	w, h := region.Dx(), region.Dy()
	if w <= 0 || h <= 0 {
		return nil
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))

	i := 0
	for i < len(cmds) {
		noclip := cmds[i].clipEmpty()
		clip := cmds[i].clip
		j := i
		for j < len(cmds) && cmds[j].clipEmpty() == noclip && cmds[j].clip == clip {
			j++
		}
		if noclip {
			renderSeg(img, region.Min, cmds[i:j])
		} else {
			bbox := paintBounds(cmds[i:j]).Intersect(region)
			if !bbox.Empty() {
				sub := image.NewRGBA(image.Rect(0, 0, bbox.Dx(), bbox.Dy()))
				renderSeg(sub, bbox.Min, cmds[i:j])
				blitClip(img, region.Min, sub, bbox.Min, cmds[i].clipRect())
			}
		}
		i = j
	}
	return img
}

// renderSeg 把一段"同裁剪"的命令画到 dst 上（dst 左上角对应 origin）。
//
// 半透明填充不进矢量光栅，而是逐像素自己合成：光栅器对填充色 alpha 的处理
// 与"颜色是否预乘"的约定不一致（实测半透明会被当成不透明），而矩形填充本来
// 也不需要抗锯齿。自己算的另一个好处是可以在 Linux 上单测合成结果。
func renderSeg(dst *image.RGBA, origin image.Point, cmds []rxCmd) {
	var batch []rxCmd
	flush := func() {
		if len(batch) > 0 {
			drawRun(dst, origin, batch)
			batch = batch[:0]
		}
	}
	for _, cm := range cmds {
		if cm.kind == rxFill {
			flush()
			if cm.alpha > 0 && cm.alpha < 1 {
				applyAlphaRect(dst, origin, cm)
			} else {
				applySolidRect(dst, origin, cm)
			}
			continue
		}
		if cm.kind == rxRound {
			flush()
			applyRoundRect(dst, origin, cm)
			continue
		}
		batch = append(batch, cm)
	}
	flush()
}

// applyRoundRect 逐行填充圆角矩形，不经过矢量光栅。
//
// 卡片、按钮、弹窗都是圆角矩形，滚动时每帧要重画十几块。走光栅器意味着
// 构建路径 + 抗锯齿边缘扫描，滚动一帧的几何成本里它占大头。圆角半径只有
// 6~10px，这里用圆的方程逐行算起止 x 再 memfill：没有抗锯齿边缘，
// 但在这个半径和 96dpi 下肉眼几乎分不出来，速度却快数倍。
func applyRoundRect(dst *image.RGBA, origin image.Point, cm rxCmd) {
	b := dst.Bounds()
	fw, fh := int(cm.w+0.5), int(cm.h+0.5)
	if fw <= 0 || fh <= 0 {
		return
	}
	rad := int(cm.r + 0.5)
	if rad > fw/2 {
		rad = fw / 2
	}
	if rad > fh/2 {
		rad = fh / 2
	}
	if rad < 0 {
		rad = 0
	}
	x0, y0 := int(cm.x+0.5), int(cm.y+0.5)
	cr := byte(cm.color & 0xFF)
	cg := byte((cm.color >> 8) & 0xFF)
	cb := byte((cm.color >> 16) & 0xFF)
	full := image.Rect(origin.X, origin.Y, origin.X+b.Dx(), origin.Y+b.Dy())
	var clipR image.Rectangle
	hasClip := !cm.clipEmpty()
	if hasClip {
		clipR = cm.clipRect()
	}
	for row := 0; row < fh; row++ {
		lx, rx := 0, fw
		if rad > 0 && (row < rad || row >= fh-rad) {
			// 圆心到这一行中心的垂直距离
			var dy float64
			if row < rad {
				dy = float64(rad) - float64(row) - 0.5
			} else {
				dy = float64(row-(fh-rad)) + 0.5
			}
			if dy < 0 {
				dy = 0
			}
			dx := math.Sqrt(float64(rad*rad) - dy*dy)
			inset := rad - int(dx+0.5)
			if inset < 0 {
				inset = 0
			}
			lx, rx = inset, fw-inset
		}
		if rx <= lx {
			continue
		}
		y := y0 + row
		r := image.Rect(x0+lx, y, x0+rx, y+1).Intersect(full)
		if hasClip {
			r = r.Intersect(clipR)
		}
		if r.Empty() {
			continue
		}
		drow := dst.Pix[(r.Min.Y-origin.Y)*dst.Stride:]
		for x := r.Min.X; x < r.Max.X; x++ {
			pp := drow[(x-origin.X)*4 : (x-origin.X)*4+4]
			pp[0], pp[1], pp[2], pp[3] = cr, cg, cb, 0xFF
		}
	}
}

// applySolidRect 用按行填充代替矢量光栅。
//
// 纯色矩形占了命令流的大头（背景、卡片列、分隔条、选中底色），而矩形本来
// 就没有抗锯齿需求——送进光栅器等于每帧把几十万像素走一遍边扫描再写回。
// 直接 memfill 快一个数量级，老机器上尤其明显（实测重放均值 6ms → 2ms 级）。
func applySolidRect(dst *image.RGBA, origin image.Point, cm rxCmd) {
	r := image.Rect(int(cm.x+0.5), int(cm.y+0.5), int(cm.x+cm.w+0.5), int(cm.y+cm.h+0.5))
	if !cm.clipEmpty() {
		r = r.Intersect(cm.clipRect())
	}
	b := dst.Bounds()
	r = r.Intersect(image.Rect(origin.X, origin.Y, origin.X+b.Dx(), origin.Y+b.Dy()))
	if r.Empty() {
		return
	}
	cr := byte(cm.color & 0xFF)
	cg := byte((cm.color >> 8) & 0xFF)
	cb := byte((cm.color >> 16) & 0xFF)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		row := dst.Pix[(y-origin.Y)*dst.Stride:]
		for x := r.Min.X; x < r.Max.X; x++ {
			p := row[(x-origin.X)*4 : (x-origin.X)*4+4]
			p[0], p[1], p[2], p[3] = cr, cg, cb, 0xFF
		}
	}
}

// applyAlphaRect 把一个半透明矩形按预乘公式混到 dst 上。
func applyAlphaRect(dst *image.RGBA, origin image.Point, cm rxCmd) {
	a := uint32(cm.alpha*255 + 0.5)
	if a == 0 {
		return
	}
	ia := 255 - a
	cr := uint32(cm.color & 0xFF)
	cg := uint32((cm.color >> 8) & 0xFF)
	cb := uint32((cm.color >> 16) & 0xFF)
	r := image.Rect(int(cm.x+0.5), int(cm.y+0.5), int(cm.x+cm.w+0.5), int(cm.y+cm.h+0.5))
	if !cm.clipEmpty() {
		r = r.Intersect(cm.clipRect())
	}
	b := dst.Bounds()
	r = r.Intersect(image.Rect(origin.X, origin.Y, origin.X+b.Dx(), origin.Y+b.Dy()))
	if r.Empty() {
		return
	}
	for y := r.Min.Y; y < r.Max.Y; y++ {
		row := dst.Pix[(y-origin.Y)*dst.Stride:]
		for x := r.Min.X; x < r.Max.X; x++ {
			p := row[(x-origin.X)*4 : (x-origin.X)*4+4]
			p[0] = byte(cr*a/255 + uint32(p[0])*ia/255)
			p[1] = byte(cg*a/255 + uint32(p[1])*ia/255)
			p[2] = byte(cb*a/255 + uint32(p[2])*ia/255)
			p[3] = byte(a + uint32(p[3])*ia/255)
		}
	}
}

// drawRun 把一段命令画到 dst 上，origin 是 dst 左上角对应的物理坐标。
func drawRun(dst *image.RGBA, origin image.Point, cmds []rxCmd) {
	b := dst.Bounds()
	cv := canvas.New(float64(b.Dx()), float64(b.Dy()))
	ctx := canvas.NewContext(cv)
	ctx.SetCoordSystem(canvas.CartesianIV)
	ctx.Translate(-float64(origin.X), -float64(origin.Y))

	for _, cm := range cmds {
		switch cm.kind {
		case rxFill:
			if cm.alpha > 0 && cm.alpha < 1 { // 半透明由 renderSeg 自己合成
				continue
			}
			ctx.SetStroke(canvas.Paint{})
			ctx.SetFillColor(rxColorA(cm.color, cm.alpha))
			ctx.DrawPath(cm.x, cm.y, canvas.Rectangle(cm.w, cm.h))
		case rxRound:
			r := rxRadius(cm.r, cm.w, cm.h)
			ctx.SetStroke(canvas.Paint{})
			ctx.SetFillColor(rxColor(cm.color))
			ctx.DrawPath(cm.x, cm.y, canvas.RoundedRectangle(cm.w, cm.h, r))
		case rxRoundStroke:
			w := cm.sw
			if w < 1 {
				w = 1
			}
			ctx.SetFill(canvas.Paint{})
			ctx.SetStrokeColor(rxColor(cm.color))
			ctx.SetStrokeWidth(w)
			// 描边以路径为中心线，内缩半个线宽才能让外沿落在矩形边界上（与 GDI 一致）。
			ctx.DrawPath(cm.x+w/2, cm.y+w/2, canvas.RoundedRectangle(cm.w-w, cm.h-w, rxRadius(cm.r, cm.w-w, cm.h-w)))
		case rxLine:
			w := cm.sw
			if w < 1 {
				w = 1
			}
			ctx.SetFill(canvas.Paint{})
			ctx.SetStrokeColor(rxColor(cm.color))
			ctx.SetStrokeWidth(w)
			ctx.SetStrokeCapper(canvas.ButtCap)
			p := &canvas.Path{}
			p.MoveTo(cm.x, cm.y)
			p.LineTo(cm.x2, cm.y2)
			ctx.DrawPath(0, 0, p)
		case rxDot:
			ctx.SetStroke(canvas.Paint{})
			ctx.SetFillColor(rxColor(cm.color))
			ctx.DrawPath(cm.x, cm.y, canvas.Circle(cm.r))
		case rxRing:
			w := cm.sw
			if w < 1 {
				w = 1
			}
			ctx.SetFill(canvas.Paint{})
			ctx.SetStrokeColor(rxColor(cm.color))
			ctx.SetStrokeWidth(w)
			ctx.DrawPath(cm.x, cm.y, canvas.Circle(cm.r-w/2))
		case rxGradient:
			ctx.SetStroke(canvas.Paint{})
			var g *canvas.LinearGradient
			if cm.vert {
				g = canvas.NewLinearGradient(
					canvas.Point{X: cm.x, Y: cm.y},
					canvas.Point{X: cm.x, Y: cm.y + cm.h})
			} else {
				g = canvas.NewLinearGradient(
					canvas.Point{X: cm.x, Y: cm.y},
					canvas.Point{X: cm.x + cm.w, Y: cm.y})
			}
			g.Add(0, rxColor(cm.color))
			g.Add(1, rxColor(cm.grad))
			ctx.SetFillGradient(g)
			ctx.DrawPath(cm.x, cm.y, canvas.Rectangle(cm.w, cm.h))
		case rxImage:
			if cm.img != nil {
				ctx.DrawImage(cm.x, cm.y, cm.img, canvas.DPMM(1.0))
			}
		}
	}

	ras := rasterizer.FromImage(dst, canvas.DPMM(1.0), canvas.LinearColorSpace{})
	cv.RenderTo(ras)
	ras.Close()
}

// rxRadius 把圆角半径夹到合法范围，避免退化成异常路径。
func rxRadius(r, w, h float64) float64 {
	m := w
	if h < m {
		m = h
	}
	m /= 2
	if r < 0 {
		r = 0
	}
	if r > m {
		r = m
	}
	if r < 0.5 {
		return 0
	}
	return r
}

// blitClip 把 sub 中落在 clip 内的像素合成到 dst（dst 左上角对应 dstOrigin）。
//
// sub 是光栅器的输出，颜色是**预乘 alpha** 的（R 已乘过 A），所以这里只能做
// out = src + dst*(1-a)，不能再用 (src*a + dst*(1-a))——那会把抗锯齿边缘和
// 半透明遮罩二次乘 alpha，边缘发黑、遮罩变得几乎看不见。
func blitClip(dst *image.RGBA, dstOrigin image.Point, sub *image.RGBA, subOrigin image.Point, clip image.Rectangle) {
	b := dst.Bounds()
	for y := 0; y < sub.Bounds().Dy(); y++ {
		py := subOrigin.Y + y
		if py < clip.Min.Y || py >= clip.Max.Y {
			continue
		}
		dy := py - dstOrigin.Y
		if dy < 0 || dy >= b.Dy() {
			continue
		}
		for x := 0; x < sub.Bounds().Dx(); x++ {
			px := subOrigin.X + x
			if px < clip.Min.X || px >= clip.Max.X {
				continue
			}
			dx := px - dstOrigin.X
			if dx < 0 || dx >= b.Dx() {
				continue
			}
			s := sub.RGBAAt(x, y)
			if s.A == 0 {
				continue
			}
			if s.A == 0xFF {
				dst.SetRGBA(dx, dy, color.RGBA{s.R, s.G, s.B, 0xFF})
				continue
			}
			d := dst.RGBAAt(dx, dy)
			ia := uint32(255 - s.A)
			// alpha 也要一起合成：裁剪段的底多半是刚分配的透明图，
			// 这里若硬写 0xFF，半透明遮罩在"带裁剪"的路径上就会变成实色块。
			dst.SetRGBA(dx, dy, color.RGBA{
				R: addByte(uint32(s.R), uint32(d.R)*ia/255),
				G: addByte(uint32(s.G), uint32(d.G)*ia/255),
				B: addByte(uint32(s.B), uint32(d.B)*ia/255),
				A: addByte(uint32(s.A), uint32(d.A)*ia/255),
			})
		}
	}
}

// addByte 做"饱和加法"：预乘合成里 src + dst*(1-a) 不会超过 255，
// 但浮点/取整误差可能溢出，这里夹一下避免回绕成黑点。
func addByte(a, b uint32) byte {
	if v := a + b; v < 255 {
		return byte(v)
	}
	return 255
}

// rxDiffBounds 求两张"长度不同"的命令流的差异区域。
//
// 旧实现在长度不同时只能取"公共前缀到公共后缀"之间的整段。问题在于差异
// 常常出现在命令流开头（左侧栏、顶栏的 hover 会给图标多画一条圆角底），
// 于是前缀当场断开、整屏都被判成脏区——实测扫一遍鼠标 54 帧里有 8 帧全屏重绘、
// 峰值 60ms，主观上就是"卡"。
//
// 这里允许向前跳跃几条去找回对齐：hover 只增删 1~3 条命令，跳过去之后
// 后面的命令重新对上，脏区就只剩那几条。
//
// 安全性：只有 rxCmdEqual 为真（含坐标完全一致）才算对齐；任何没对上的
// 命令都会被标记成脏区，最坏情况退化成旧行为，不会漏画。
func rxDiffBounds(cur, prev []rxCmd, full image.Rectangle) image.Rectangle {
	const maxSkip = 8
	var out image.Rectangle
	markCur := func(k int) { out = rxUnion(out, cur[k].bounds()) }
	markPrev := func(k int) { out = rxUnion(out, prev[k].bounds()) }

	i, j := 0, 0
	for i < len(cur) && j < len(prev) {
		if rxCmdEqual(cur[i], prev[j]) {
			i++
			j++
			continue
		}
		matched := false
		for d := 1; d <= maxSkip; d++ {
			if i+d < len(cur) && rxCmdEqual(cur[i+d], prev[j]) {
				for k := 0; k < d; k++ {
					markCur(i + k)
				}
				i += d
				matched = true
				break
			}
			if j+d < len(prev) && rxCmdEqual(cur[i], prev[j+d]) {
				for k := 0; k < d; k++ {
					markPrev(j + k)
				}
				j += d
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		markCur(i)
		markPrev(j)
		i++
		j++
	}
	for ; i < len(cur); i++ {
		markCur(i)
	}
	for ; j < len(prev); j++ {
		markPrev(j)
	}
	return out.Intersect(full)
}
