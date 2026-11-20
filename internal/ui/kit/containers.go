package kit

import "math"

// ---------- Column（纵向容器） ----------

type column struct {
	Gap      int32
	Children []Element
	r        Rect
	ys       []int32
	hs       []int32
}

func Column(gap int32, children ...Element) Element {
	return &column{Gap: gap, Children: children}
}

func (e *column) Measure(c *Ctx, cons Constraint) Size {
	var w, h int32
	for i, ch := range e.Children {
		s := ch.Measure(c, Constraint{MinW: 0, MaxW: cons.MaxW, MinH: 0, MaxH: math.MaxInt32})
		if s.W > w {
			w = s.W
		}
		h += s.H
		if i > 0 {
			h += e.Gap
		}
	}
	if w > cons.MaxW {
		w = cons.MaxW
	}
	return Size{W: w, H: h}
}

func (e *column) Arrange(c *Ctx, r Rect) {
	e.r = r
	// 纵向顺排，把“剩余高度”按顺序交给子节点：
	// 前面的子节点量走自己要的高度，最后一个 Scroll（Height=0）则吃到全部剩余，
	// 这样滚动容器才能拿到有限的视口高度（否则它会拿到 MaxInt32，滚轮永远失效）。
	e.ys = e.ys[:0]
	e.hs = e.hs[:0]
	y := r.Y
	remaining := r.H
	for i, ch := range e.Children {
		if i > 0 {
			remaining -= e.Gap
		}
		if remaining < 0 {
			remaining = 0
		}
		s := ch.Measure(c, Constraint{MinW: 0, MaxW: r.W, MinH: 0, MaxH: remaining})
		ch.Arrange(c, R(r.X, y, r.W, s.H))
		e.ys = append(e.ys, y)
		e.hs = append(e.hs, s.H)
		y += s.H + e.Gap
		remaining -= s.H
	}
}

func (e *column) Paint(c *Ctx, g Painter) {
	for i, ch := range e.Children {
		// 滚出视口的行不画：列表有 60~200 行时，这一条能把文本绘制量从
		// “全部行”降到“可见行”。布局仍会测量全部行，但测量有缓存。
		if i < len(e.ys) && !c.Visible(R(e.r.X, e.ys[i], e.r.W, e.hs[i])) {
			continue
		}
		ch.Paint(c, g)
	}
}

// ---------- Row（横向容器） ----------

type row struct {
	Gap      int32
	Children []Element
	r        Rect
}

func Row(gap int32, children ...Element) Element {
	return &row{Gap: gap, Children: children}
}

// weightedElement 标记可参与弹性分配的元素（Expanded/Spacer）。
type weightedElement interface {
	weight() int32
}

func (e *row) layout(c *Ctx, maxW int32) ([]Size, Size) {
	n := len(e.Children)
	sizes := make([]Size, n)
	var totalWeight int32
	var fixedRest, maxH int32
	for i, ch := range e.Children {
		if w, ok := ch.(weightedElement); ok {
			totalWeight += w.weight()
			continue
		}
		s := ch.Measure(c, Constraint{MinW: 0, MaxW: maxW, MinH: 0, MaxH: math.MaxInt32})
		sizes[i] = s
		fixedRest += s.W
		if s.H > maxH {
			maxH = s.H
		}
	}
	gaps := int32(0)
	if n > 1 {
		gaps = e.Gap * int32(n-1)
	}
	remaining := int32(0)
	if totalWeight > 0 && maxW > fixedRest+gaps {
		remaining = maxW - fixedRest - gaps
	}
	for i, ch := range e.Children {
		if w, ok := ch.(weightedElement); ok {
			share := int32(0)
			if totalWeight > 0 {
				share = remaining * w.weight() / totalWeight
			}
			s := ch.Measure(c, Constraint{MinW: share, MaxW: share, MinH: 0, MaxH: math.MaxInt32})
			if s.H > maxH {
				maxH = s.H
			}
			sizes[i] = Size{W: share, H: s.H}
		}
	}
	totalW := fixedRest + remaining + gaps
	if totalW > maxW {
		totalW = maxW
	}
	return sizes, Size{W: totalW, H: maxH}
}

func (e *row) Measure(c *Ctx, cons Constraint) Size {
	_, s := e.layout(c, cons.MaxW)
	if s.W > cons.MaxW {
		s.W = cons.MaxW
	}
	return s
}

func (e *row) Arrange(c *Ctx, r Rect) {
	e.r = r
	sizes, total := e.layout(c, r.W)
	_ = total
	x := r.X
	for i, ch := range e.Children {
		ch.Arrange(c, R(x, r.Y, sizes[i].W, sizes[i].H))
		x += sizes[i].W + e.Gap
	}
}

func (e *row) Paint(c *Ctx, g Painter) {
	for _, ch := range e.Children {
		ch.Paint(c, g)
	}
}

// ---------- Stack（层叠：浮层/叠放） ----------

type stack struct {
	Children []Element
	r        Rect
}

func Stack(children ...Element) Element { return &stack{Children: children} }

func (e *stack) Measure(c *Ctx, cons Constraint) Size {
	var w, h int32
	for _, ch := range e.Children {
		s := ch.Measure(c, cons)
		if s.W > w {
			w = s.W
		}
		if s.H > h {
			h = s.H
		}
	}
	return Size{W: w, H: h}
}

func (e *stack) Arrange(c *Ctx, r Rect) {
	e.r = r
	for _, ch := range e.Children {
		ch.Arrange(c, r)
	}
}

func (e *stack) Paint(c *Ctx, g Painter) {
	// 文档序绘制：后绘制的浮在上面
	for _, ch := range e.Children {
		ch.Paint(c, g)
	}
}

// ---------- Padding ----------

type padding struct {
	Pad   int32
	Child Element
	r     Rect
}

func Padding(pad int32, child Element) Element { return &padding{Pad: pad, Child: child} }

func shrinkConstraint(c Constraint, d int32) Constraint {
	c.MinW -= d
	c.MaxW -= d
	c.MinH -= d
	c.MaxH -= d
	if c.MinW < 0 {
		c.MinW = 0
	}
	if c.MaxW < 0 {
		c.MaxW = 0
	}
	if c.MinH < 0 {
		c.MinH = 0
	}
	if c.MaxH < 0 {
		c.MaxH = 0
	}
	return c
}

func (e *padding) Measure(c *Ctx, cons Constraint) Size {
	s := e.Child.Measure(c, shrinkConstraint(cons, e.Pad*2))
	return Size{W: s.W + e.Pad*2, H: s.H + e.Pad*2}
}

func (e *padding) Arrange(c *Ctx, r Rect) {
	e.r = r
	e.Child.Arrange(c, R(r.X+e.Pad, r.Y+e.Pad, r.W-e.Pad*2, r.H-e.Pad*2))
}

func (e *padding) Paint(c *Ctx, g Painter) { e.Child.Paint(c, g) }

// ---------- SizedBox ----------

type sizedBox struct {
	W, H  int32
	Child Element
	r     Rect
}

// SizedBox 固定尺寸盒执行盒。w 或 h 传 0 表示"不指定"（用子节点自然尺寸）。
func SizedBox(w, h int32, child Element) Element {
	return &sizedBox{W: w, H: h, Child: child}
}

func (e *sizedBox) Measure(c *Ctx, cons Constraint) Size {
	s := e.Child.Measure(c, cons)
	if e.W > 0 {
		s.W = e.W
	}
	if e.H > 0 {
		s.H = e.H
	}
	return s
}

func (e *sizedBox) Arrange(c *Ctx, r Rect) {
	e.r = r
	e.Child.Arrange(c, r)
}

func (e *sizedBox) Paint(c *Ctx, g Painter) { e.Child.Paint(c, g) }

// ---------- Left（行首固定对齐） ----------

// Left 把子元素放在 Column 行首（按钮按自然宽度排列，不会被拉满整宽后文字居中）。
func Left(children ...Element) Element {
	kids := append([]Element{}, children...)
	kids = append(kids, Spacer())
	return Row(8, kids...)
}

// ---------- Spacer / Expanded ----------

// Spacer 在 Row/Column 中撑开剩余空间（弹性 1）。
type spacer struct{}

func Spacer() Element           { return &spacer{} }
func (e *spacer) weight() int32 { return 1 }

func (e *spacer) Measure(c *Ctx, cons Constraint) Size {
	return Size{W: 0, H: 0}
}
func (e *spacer) Arrange(c *Ctx, r Rect)  {}
func (e *spacer) Paint(c *Ctx, g Painter) {}

// Expanded 包裹子节点：Row 里占满剩余宽度（弹性 1）。
type expanded struct{ Child Element }

func Expanded(child Element) Element { return &expanded{Child: child} }
func (e *expanded) weight() int32    { return 1 }

func (e *expanded) Measure(c *Ctx, cons Constraint) Size {
	s := e.Child.Measure(c, Constraint{MinW: 0, MaxW: cons.MaxW, MinH: 0, MaxH: math.MaxInt32})
	return s
}
func (e *expanded) Arrange(c *Ctx, r Rect)  { e.Child.Arrange(c, r) }
func (e *expanded) Paint(c *Ctx, g Painter) { e.Child.Paint(c, g) }

// ---------- Scroll（纵向滚动容器） ----------

type scroll struct {
	Key    string
	Child  Element
	Height int32
	r      Rect
	childH int32
}

// Scroll 纵向滚动容器；offset 保存在 StateStore[key].Scroll。
// 滚轮事件由窗口层分发到 ScrollBy。
func Scroll(key string, height int32, child Element) Element {
	return &scroll{Key: key, Child: child, Height: height}
}

func (e *scroll) Measure(c *Ctx, cons Constraint) Size {
	s := e.Child.Measure(c, Constraint{MinW: 0, MaxW: cons.MaxW, MinH: 0, MaxH: math.MaxInt32})
	// Height=0 表示“填满父容器给我的剩余高度”：
	// 父约束是有限值时取 min(自然高度, 父约束)；父约束无界（被滚动容器反向询问自然高度时）取自然高度。
	// 不能原样返回 cons.MaxH（可能是 MaxInt32），否则外层自然高度会被撑成天文数字、滚轮失效。
	h := s.H
	if e.Height > 0 {
		h = e.Height
	} else if cons.MaxH < h {
		h = cons.MaxH
	}
	return Size{W: s.W, H: h}
}

func (e *scroll) Arrange(c *Ctx, r Rect) {
	e.r = r
	e.childH = e.Child.Measure(c, Constraint{MinW: 0, MaxW: r.W, MinH: 0, MaxH: math.MaxInt32}).H
	st := c.Store(e.Key)
	maxScroll := int32(0)
	if e.childH > r.H {
		maxScroll = e.childH - r.H
	}
	st.ScrollMax = maxScroll
	st.ViewH = r.H
	if st.Scroll > maxScroll {
		st.Scroll = maxScroll
	}
	if st.Scroll < 0 {
		st.Scroll = 0
	}
	e.Child.Arrange(c, R(r.X, r.Y-st.Scroll, r.W, e.childH))
}

func (e *scroll) Paint(c *Ctx, g Painter) {
	st := c.Store(e.Key)
	g.Clip(e.r)
	c.PushClip(e.r)
	e.Child.Paint(c, g)
	c.PopClip()
	g.PopClip()

	// 滚动条：kit 页过去完全没有滚动反馈，滚轮再灵也像"页面是死的"。
	// 这里用一条 6px 的细轨 + 圆角滑块，只在内容超过视口时出现。
	if st.ScrollMax <= 0 || e.r.H <= 0 {
		return
	}
	tk := c.Tokens
	const trackW = int32(7)
	track := R(e.r.Right()-trackW-4, e.r.Y+4, trackW, e.r.H-8)
	if track.H < 8 {
		return
	}
	g.Round(track, 4, tk.BorderHairline)
	viewH := float32(e.r.H)
	thumbH := int32(viewH * viewH / (viewH + float32(st.ScrollMax)))
	if thumbH < 28 {
		thumbH = 28
	}
	if thumbH > track.H {
		thumbH = track.H
	}
	travel := track.H - thumbH
	pos := int32(0)
	if st.ScrollMax > 0 && travel > 0 {
		pos = int32(float32(st.Scroll) / float32(st.ScrollMax) * float32(travel))
	}
	thumb := R(track.X, track.Y+pos, trackW, thumbH)
	col := tk.TextTertiary
	if c.IsHovered(e.r) {
		col = tk.TextSecondary
	}
	g.Round(thumb, 4, col)
}

// ScrollBy 滚动指定容器（滚轮入口）。返回是否发生滚动。
// 偏移在这里就夹到 Scroll.Arrange 写回的有效范围内，避免滚到底后
// 再滚轮时偏移继续增长、往下滚要先把"欠账"滚回来。
func ScrollBy(c *Ctx, key string, delta int32) bool {
	st := c.Store(key)
	old := st.Scroll
	st.Scroll += delta
	if st.Scroll < 0 {
		st.Scroll = 0
	}
	if st.ScrollMax > 0 && st.Scroll > st.ScrollMax {
		st.Scroll = st.ScrollMax
	}
	return st.Scroll != old
}

// ScrollPage 按视口高度翻页，dir 为 -1（上）或 +1（下）。
func ScrollPage(c *Ctx, key string, dir int32) bool {
	st := c.Store(key)
	step := st.ViewH * 8 / 10
	if step < 40 {
		step = 40
	}
	if dir < 0 {
		step = -step
	}
	return ScrollBy(c, key, step)
}

// ScrollHome / ScrollEnd 跳到顶部/底部。
func ScrollHome(c *Ctx, key string) bool { return ScrollTo(c, key, 0) }

func ScrollEnd(c *Ctx, key string) bool {
	st := c.Store(key)
	return ScrollTo(c, key, st.ScrollMax)
}

// ScrollTo 直接设置偏移并夹紧。
func ScrollTo(c *Ctx, key string, offset int32) bool {
	st := c.Store(key)
	old := st.Scroll
	if offset < 0 {
		offset = 0
	}
	if st.ScrollMax > 0 && offset > st.ScrollMax {
		offset = st.ScrollMax
	}
	st.Scroll = offset
	return st.Scroll != old
}
