// Package kit 是 Option A 方案下的 UI 组件框架。
//
// 设计原则（替代现有 app.UI 的手写即时模式）：
//   - 声明式 + 每帧重建：Element 树每帧从零构建，不存在"更新 widget"代码；
//     交互状态（输入框文本、滚动偏移等）全部外提到 Ctx.StateStore，按稳定 key 存取；
//   - 三趟遍历：Measure（算"我想要多大"）→ Arrange（拿到真实矩形）→ Paint（画）；
//     事件走第四趟——命中区在 Paint 阶段按稳定 id 注册（注册顺序 = 文档序），
//     点击在"本帧 Paint 之后"按逆序命中（浮层先命中）；
//   - 文本一律走 GDI 度量/绘制（Painter 接口 + ui.Canvas），几何一律走 canvas 命令流；
//   - 焦点与 Tab 顺序 = 文档序（注册顺序）；
//   - 半透明只走 canvas 的 rxScrim（由 Painter 实现保证），不画 alpha 色块。
//
// 与 ui.Canvas 的分层：kit 本身不依赖 Win32；Painter 接口可在测试里打桩。
package kit

import (
	"math"

	"win7harness/internal/ui"
)

// Painter 是 kit 的渲染后端。生产实现 ui.Canvas（双缓冲 + 脏区重放），
// 测试里用 mockPainter 记录绘制命令。
type Painter interface {
	FillRect(r ui.Rect, color uint32)
	Round(r ui.Rect, radius int32, color uint32)
	RoundStroke(r ui.Rect, radius int32, color uint32)
	Line(x1, y1, x2, y2 int32, color uint32)
	LineW(x1, y1, x2, y2 int32, color uint32, width int32)
	HLine(x, y, w int32, color uint32)
	VLine(x, y, h int32, color uint32)
	Dot(cx, cy, r int32, color uint32)
	Ring(cx, cy, radius int32, color uint32)
	Text(x, y int32, s string, size, weight int32, color uint32)
	TextFace(x, y int32, s string, size, weight int32, color uint32, face string)
	TextRect(r ui.Rect, s string, size, weight int32, color uint32, flags uint32)
	TextRectFace(r ui.Rect, s string, size, weight int32, color uint32, flags uint32, face string)
	MeasureFace(s string, size, weight int32, face string) int32
	Clip(r ui.Rect)
	PopClip()
	LineHeight(size int32) int32
}

// Rect/Size 与 ui 对齐（逻辑像素，左上原点）。
type Rect = ui.Rect
type Size struct{ W, H int32 }

func R(x, y, w, h int32) Rect { return ui.R(x, y, w, h) }

// Element 是一棵 UI 节点树：每帧重建，不保留内部可变状态
// （交互状态在 Ctx.StateStore 里）。
type Element interface {
	// Measure 根据约束算出自己期望的尺寸。子节点先测。
	Measure(c *Ctx, cons Constraint) Size
	// Arrange 拿到真实矩形，并把子节点放到位。
	Arrange(c *Ctx, r Rect)
	// Paint 只画自己负责的部分（子节点由容器驱动 Paint）。
	Paint(c *Ctx, g Painter)
}

// Constraint 是父节点给子节点的尺寸上下限。
type Constraint struct{ MinW, MaxW, MinH, MaxH int32 }

// 无限制约束（滚动容器主轴用）。
func unbounded() Constraint {
	return Constraint{MinW: 0, MaxW: math.MaxInt32, MinH: 0, MaxH: math.MaxInt32}
}

// ---------------- Ctx ----------------

// Ctx 是一帧的上下文：后端、令牌、状态库、焦点、命中注册。
// 每个窗口一个 Ctx；每帧 BeginFrame 复位命中注册表。
type Ctx struct {
	Painter Painter
	Tokens  *ui.Tokens

	mouseX, mouseY int32
	phase          float32
	frame          int

	store map[string]*KeyState
	anims map[string]*animState

	measure  map[measureKey]int32
	measures int
	lineH    map[int32]int32

	clip      Rect
	clipSet   bool
	clipStack []clipFrame

	hits     []hit
	pressId  string
	pressBtn int

	focus      string
	focusOrder []string
}

type clipFrame struct {
	r   Rect
	set bool
}

type measureKey struct {
	text         string
	size, weight int32
	face         string
}

// PushClip / PopClip 让 Ctx 知道当前绘制裁剪区，容器据此跳过滚出视口的子树。
// 实际的 Painter.Clip 仍由 Scroll.Paint 调用；这里只维护一份逻辑矩形。
func (c *Ctx) PushClip(r Rect) {
	c.clipStack = append(c.clipStack, clipFrame{r: c.clip, set: c.clipSet})
	c.clip, c.clipSet = r, true
}

func (c *Ctx) PopClip() {
	n := len(c.clipStack)
	if n == 0 {
		c.clipSet = false
		return
	}
	f := c.clipStack[n-1]
	c.clipStack = c.clipStack[:n-1]
	c.clip, c.clipSet = f.r, f.set
}

// Visible 报告元素矩形是否与当前裁剪区相交。没有裁剪区时一律可见。
func (c *Ctx) Visible(r Rect) bool {
	if !c.clipSet {
		return true
	}
	i := r.Intersect(c.clip)
	return i.W > 0 && i.H > 0
}

// KeyState 是稳定 key 的持久化交互状态。
type KeyState struct {
	Text     string
	Cursor   int // rune 偏移
	SelStart int // rune 偏移；-1 表示无选区
	SelEnd   int
	Scroll   int32 // 列表纵向偏移
	// ScrollMax / ViewH 由 Scroll.Arrange 每帧写回：滚轮、PageUp/PageDown、
	// Home/End 都靠它把偏移夹在有效范围内，不再依赖"下一帧再修正"。
	ScrollMax int32
	ViewH     int32
}

type animState struct {
	v    float32
	last int64
}

type hit struct {
	id      string
	r       Rect
	onClick func()
}

// New 建一个 Ctx。
func New(p Painter) *Ctx {
	tk := ui.CurrentTokens()
	return &Ctx{
		Painter: p,
		Tokens:  &tk,
		store:   map[string]*KeyState{},
		anims:   map[string]*animState{},
		measure: map[measureKey]int32{},
		mouseX:  -1000,
		mouseY:  -1000,
	}
}

// MeasureFace 带缓存的 GDI 文本测量。
//
// kit 的布局是 Measure → Arrange → Paint 三趟，每个 Label 在一帧里至少被
// 测量 2~3 次；60 行列表 + TextTrunc 的二分查找会放大到上千次 GDI 调用。
// 文本/字号/字重/字体不变时直接命中缓存，滚动与悬停的帧成本能降一大截。
func (c *Ctx) MeasureFace(s string, size, weight int32, face string) int32 {
	if c.Painter == nil {
		return 0
	}
	if s == "" {
		return 0
	}
	k := measureKey{text: s, size: size, weight: weight, face: face}
	if v, ok := c.measure[k]; ok {
		return v
	}
	v := c.Painter.MeasureFace(s, size, weight, face)
	// 内容变化（搜索框、时间戳）会让缓存增长；到上限整体清空即可，
	// 不做 LRU 是刻意的：这里追求的是零分配、零链表操作。
	if len(c.measure) > 8192 {
		c.measure = map[measureKey]int32{}
	}
	c.measure[k] = v
	c.measures++
	return v
}

// LineHeight 带缓存的单行高度。行高只由字号决定，一张小表就够。
func (c *Ctx) LineHeight(size int32) int32 {
	if c.Painter == nil {
		return size
	}
	if c.lineH == nil {
		c.lineH = map[int32]int32{}
	}
	if v, ok := c.lineH[size]; ok {
		return v
	}
	v := c.Painter.LineHeight(size)
	c.lineH[size] = v
	return v
}

// ---------- 帧生命周期 ----------

// BeginFrame 开新一帧：清空命中注册表。窗口每收到需要重绘的消息时调用。
func (c *Ctx) BeginFrame() {
	c.frame++
	c.hits = c.hits[:0]
	c.focusOrder = c.focusOrder[:0]
	// 上一帧若因异常提前退出，裁剪栈可能没弹干净；新帧必须从未裁剪开始，
	// 否则整页会被一个陈旧的裁剪矩形裁成空白。
	c.clipSet = false
	c.clipStack = c.clipStack[:0]
}

// Frame 返回当前帧号（调试用）。
func (c *Ctx) Frame() int { return c.frame }

// SetPhase 设置一帧的动画相位。
func (c *Ctx) SetPhase(p float32) { c.phase = p }

// Phase 返回动画相位。
func (c *Ctx) Phase() float32 { return c.phase }

// Mouse 告知当前鼠标位置（逻辑像素）。窗口每次鼠标事件调用。
func (c *Ctx) Mouse(x, y int32) { c.mouseX, c.mouseY = x, y }

// Store 取（或建）稳定 key 的持久化状态。
func (c *Ctx) Store(key string) *KeyState {
	s, ok := c.store[key]
	if !ok {
		s = &KeyState{SelStart: -1, SelEnd: -1}
		c.store[key] = s
	}
	return s
}

// StateSnapshot 导出全部持久化状态（调试/自检用）。
func (c *Ctx) StateSnapshot() map[string]KeyState {
	out := make(map[string]KeyState, len(c.store))
	for k, v := range c.store {
		out[k] = *v
	}
	return out
}

// ---------- 命中 / 点击 ----------

// RegisterHit 登记一个可点击区域。id 主要用于 hover/press 视觉与焦点管理，
// onClick 为实际动作（nil 表示仅注册用于 hover/光标）。
func (c *Ctx) RegisterHit(id string, r Rect, onClick func()) {
	c.hits = append(c.hits, hit{id: id, r: r, onClick: onClick})
}

// IsHovered 报告坐标是否在 r 内（本帧鼠标位置）。
func (c *Ctx) IsHovered(r Rect) bool { return r.Contains(c.mouseX, c.mouseY) }

// HitAt 逆序反查 id。
func (c *Ctx) HitAt(x, y int32) (string, bool) {
	for i := len(c.hits) - 1; i >= 0; i-- {
		if c.hits[i].r.Contains(x, y) {
			return c.hits[i].id, true
		}
	}
	return "", false
}

// Press 记录"本帧按下"的 id 与键。
func (c *Ctx) Press(id string, button int) { c.pressId, c.pressBtn = id, button }

// ClearPress 清按下态。
func (c *Ctx) ClearPress() { c.pressId, c.pressBtn = "", -1 }

// PressInfo 返回当前按下信息。
func (c *Ctx) PressInfo() (string, int) { return c.pressId, c.pressBtn }

// IsPressed 报告 id 是否处于按下态。
func (c *Ctx) IsPressed(id string) bool { return c.pressId == id && c.pressBtn == 0 }

// MouseDown 处理按下：命中则记录按下态，返回是否命中。
func (c *Ctx) MouseDown(x, y int32, button int) bool {
	id, ok := c.HitAt(x, y)
	if !ok || id == "" {
		return false
	}
	c.Press(id, button)
	return true
}

// MouseUp 处理抬起：仅"同控件 + 左键"才派发点击（先按后抬同一控件才算）。
func (c *Ctx) MouseUp(x, y int32, button int) bool {
	defer c.ClearPress()
	if button != 0 {
		return false
	}
	id, ok := c.HitAt(x, y)
	if !ok || id != c.pressId {
		return false
	}
	// 派发：逆序找 onClick
	for i := len(c.hits) - 1; i >= 0; i-- {
		h := c.hits[i]
		if h.id == id && h.onClick != nil {
			if h.r.Contains(x, y) {
				h.onClick()
				return true
			}
			return false
		}
	}
	return false
}

// DispatchClick 直接按 id 派发一次点击（程序化触发，比如键盘 Enter 激活当前焦点控件）。
func (c *Ctx) DispatchClick(id string) bool {
	for i := len(c.hits) - 1; i >= 0; i-- {
		if c.hits[i].id == id && c.hits[i].onClick != nil {
			c.hits[i].onClick()
			return true
		}
	}
	return false
}

// ---------- 焦点 ----------

// RegisterFocus 登记可聚焦 id（文档序 = Tab 顺序）并返回是否已聚焦。
func (c *Ctx) RegisterFocus(id string) bool {
	c.focusOrder = append(c.focusOrder, id)
	return c.focus == id
}

// SetFocus 设置焦点（空串清焦点）。
func (c *Ctx) SetFocus(id string) { c.focus = id }

// Focus 返回当前焦点 id。
func (c *Ctx) Focus() string { return c.focus }

// MoveFocus 按文档序移动焦点（Tab / Shift+Tab）。
func (c *Ctx) MoveFocus(shift bool) {
	n := len(c.focusOrder)
	if n == 0 {
		return
	}
	cur := -1
	for i, id := range c.focusOrder {
		if id == c.focus {
			cur = i
			break
		}
	}
	step := 1
	if shift {
		step = -1
	}
	var next int
	if cur < 0 {
		if shift {
			next = n - 1
		} else {
			next = 0
		}
	} else {
		next = (cur + step + n) % n
	}
	c.focus = c.focusOrder[next]
}

// ---------- hover 过渡 ----------

// HoverT 返回 id 的 0..1 过渡值（easeOutCubic），约 80ms 走完。
// 用帧数近似时间（33fps 驱动），不引入 time 依赖。
func (c *Ctx) HoverT(id string, hover bool, durFrames float32) float32 {
	if durFrames <= 0 {
		durFrames = 3 // ~90ms
	}
	s := c.anims[id]
	if s == nil {
		s = &animState{}
		c.anims[id] = s
	}
	if s.last == 0 {
		s.last = int64(c.frame)
	}
	step := float32(c.frame) - float32(s.last)
	s.last = int64(c.frame)
	target := float32(0)
	if hover {
		target = 1
	}
	if step > 4 {
		step = 4 // 帧间隔过大时不要跳变太多
	}
	if s.v < target {
		s.v += step / durFrames
		if s.v > target {
			s.v = target
		}
	} else if s.v > target {
		s.v -= step / durFrames
		if s.v < target {
			s.v = target
		}
	}
	if s.v <= 0 {
		return 0
	}
	if s.v >= 1 {
		return 1
	}
	t := s.v
	u := 1 - t
	return 1 - u*u*u
}

// ---------- 颜色混合 ----------

// Blend 与 ui.Blend 同签名（kit 内用，避免到处 import cycles）。
func Blend(a, b uint32, t float32) uint32 { return ui.Blend(a, b, t) }
