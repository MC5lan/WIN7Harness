//go:build windows

package app

import (
	"strings"
	"time"

	"win7harness/internal/ui"
	"win7harness/internal/win32"
)

// hit 一帧内的可点击区域。
type hit struct {
	id   string
	rect ui.Rect
}

// fieldState 文本框状态（跨帧保存）。
type fieldState struct {
	Text   []rune
	Cur    int
	Scroll int32
	Masked bool
	// Password 表示这是密码框：始终遮罩，只有 Reveal 为 true 时才显示明文。
	// 普通 API Key 框仍用 Masked 的“聚焦时显示、失焦时遮罩”行为。
	Password bool
	Reveal   bool
}

func (f *fieldState) String() string { return string(f.Text) }

func (f *fieldState) Set(s string) {
	f.Text = []rune(s)
	f.Cur = len(f.Text)
}

func (f *fieldState) Insert(s string) {
	rs := []rune(s)
	out := make([]rune, 0, len(f.Text)+len(rs))
	out = append(out, f.Text[:f.Cur]...)
	out = append(out, rs...)
	out = append(out, f.Text[f.Cur:]...)
	f.Text = out
	f.Cur += len(rs)
}

func (f *fieldState) Backspace() {
	if f.Cur <= 0 {
		return
	}
	f.Text = append(f.Text[:f.Cur-1], f.Text[f.Cur:]...)
	f.Cur--
}

func (f *fieldState) Delete() {
	if f.Cur >= len(f.Text) {
		return
	}
	f.Text = append(f.Text[:f.Cur], f.Text[f.Cur+1:]...)
}

// UI 即时模式绘图层。每帧重建 hit 列表，点击通过 id 匹配。
type UI struct {
	c       *ui.Canvas
	th      *ui.Theme
	mx, my  int32
	hits    []hit
	click   string
	pending string // MouseUp 投递的点击，下一帧 Begin 统一消费一次
	focus   string
	fields  map[string]*fieldState
	phase   float32

	// pressId/pressBtn 记录"按下"命中的控件与鼠标键。
	// 抬起时只有同控件、左键才派发，避免按下 A 抬起 B 或右键误触发。
	pressId  string
	pressBtn int

	// 当前聚焦输入框的光标位置（逻辑像素），供输入法组字窗口定位。
	caretX, caretY int32
	caretOK        bool

	// hover 过渡：每个控件一条 80ms 的 0↔1 曲线，hover 底色/描边淡入淡出。
	// 即时模式每帧重绘，所以过渡只是"按时间插值颜色"，不需要真的做动画帧。
	hoverAnim map[string]*hoverAnim
	animating bool
}

// hoverAnim 一条 hover 过渡的进度。
type hoverAnim struct {
	v    float32 // 0=离开 1=悬停（线性推进）
	last time.Time
}

// NewUI 创建绘图层。
func NewUI(c *ui.Canvas, th *ui.Theme) *UI {
	return &UI{c: c, th: th, fields: map[string]*fieldState{}}
}

// Field 取或创建输入框状态。
func (u *UI) Field(id, initial string) *fieldState {
	f, ok := u.fields[id]
	if !ok {
		f = &fieldState{Masked: false}
		f.Set(initial)
		u.fields[id] = f
	}
	return f
}

// SetField 强制设置输入框内容。
func (u *UI) SetField(id, v string) {
	f := u.Field(id, "")
	f.Set(v)
}

// Focus 返回当前焦点 id。
func (u *UI) Focus() string { return u.focus }

// SetFocus 设置焦点。
func (u *UI) SetFocus(id string) { u.focus = id }

// MeasureFace 暴露文本测量，供主输入框做鼠标定位光标时使用。
func (u *UI) MeasureFace(s string, size, weight int32, face string) int32 {
	return u.c.MeasureFace(s, size, weight, face)
}

// Begin 每帧开始：记录鼠标位置并清空命中列表。
func (u *UI) Begin(mx, my int32, phase float32) {
	u.mx, u.my = mx, my
	u.phase = phase
	u.hits = u.hits[:0]
	u.animating = false
	// 点击在 MouseUp 里投递，必须活到下一帧绘制才能被 Button/Clicked 读到；
	// 过去每帧直接清空，导致按钮和输入框点了没反应。
	u.click = u.pending
	u.pending = ""
	u.caretOK = false
}

// Caret 返回本帧聚焦输入框的光标位置，用于输入法组字窗口定位。
func (u *UI) Caret() (x, y int32, ok bool) { return u.caretX, u.caretY, u.caretOK }

// Hit 注册可点击区域；返回是否悬停。
func (u *UI) Hit(id string, r ui.Rect) bool {
	hover := r.Contains(u.mx, u.my)
	u.advanceHover(id, hover)
	u.hits = append(u.hits, hit{id: id, rect: r})
	return hover
}

// advanceHover 把 id 的 hover 值向目标推进一步（80ms 走完）。
func (u *UI) advanceHover(id string, hover bool) {
	if u.hoverAnim == nil {
		u.hoverAnim = map[string]*hoverAnim{}
	}
	now := time.Now()
	s := u.hoverAnim[id]
	if s == nil {
		s = &hoverAnim{}
		u.hoverAnim[id] = s
	}
	target := float32(0)
	if hover {
		target = 1
	}
	if s.v == target {
		s.last = now
		return
	}
	step := float32(now.Sub(s.last).Seconds()) / 0.08
	s.last = now
	if target > s.v {
		s.v += step
		if s.v > target {
			s.v = target
		}
	} else {
		s.v -= step
		if s.v < target {
			s.v = target
		}
	}
	if s.v != target {
		u.animating = true
	}
}

// hoverT 返回 id 的 hover 过渡值（0..1，easeOutCubic）；Hit 之后调用。
func (u *UI) hoverT(id string) float32 {
	if u.hoverAnim == nil {
		return 0
	}
	s := u.hoverAnim[id]
	if s == nil || s.v <= 0 {
		return 0
	}
	if s.v >= 1 {
		return 1
	}
	t := s.v
	return 1 - (1-t)*(1-t)*(1-t)
}

// Animating 是否有 hover 过渡进行中；Timer 据此保持高帧率。
func (u *UI) Animating() bool { return u.animating }

// Clicked 判断本帧某 id 是否被点击。
func (u *UI) Clicked(id string) bool { return u.click == id }

// HitAt 从本帧已注册的命中区里反查控件 id（后注册的优先）。
func (u *UI) HitAt(x, y int32) string {
	for i := len(u.hits) - 1; i >= 0; i-- {
		if u.hits[i].rect.Contains(x, y) {
			return u.hits[i].id
		}
	}
	return ""
}

// Press / Pressed / ClearPress 维护一次"按下"的状态。
func (u *UI) Press(id string, button int) { u.pressId, u.pressBtn = id, button }
func (u *UI) Pressed() (string, int)      { return u.pressId, u.pressBtn }
func (u *UI) ClearPress()                 { u.pressId, u.pressBtn = "", -1 }

// HandleClick 在鼠标抬起时调用，返回被点击的 id。
func (u *UI) HandleClick(x, y int32) string {
	id := u.HitAt(x, y)
	if id == "" {
		return ""
	}
	u.click = id
	u.pending = id
	// 命中的是输入框（TextField 已登记进 fields）时把键盘焦点交给它。
	// 引导页/设置页的表单没有逐个手写 Clicked 分支，统一在这里接上。
	if _, ok := u.fields[id]; ok {
		u.focus = id
	}
	return id
}

// Panel 绘制圆角面板。
func (u *UI) Panel(r ui.Rect, fill, border uint32, radius int32) {
	u.c.Round(r, radius, fill)
	if border != 0 {
		u.c.RoundStroke(r, radius, border)
	}
}

// ButtonKind 按钮样式（暖纸设计：珊瑚主按钮 + 暖纸描边次按钮 + 幽灵按钮）
type ButtonKind int

const (
	BtnPrimary ButtonKind = iota
	BtnGhost
	BtnDanger
	BtnSubtle
)

// Button 绘制按钮，返回是否被点击。
func (u *UI) Button(id, label string, r ui.Rect, kind ButtonKind) bool {
	hover := u.Hit(id, r)
	hvt := u.hoverT(id)
	pressed := hover && u.pressId == id && u.pressBtn == 0
	disabled := u.disabledID(id)
	th := u.th
	const radius = 8 // 暖纸风格：比 6 更大的圆角，更像胶囊/纸面按钮

	var bg, fg, border uint32
	switch kind {
	case BtnPrimary:
		bg, fg, border = th.Accent, 0xFFFFFF, th.Accent
		if hvt > 0 && !disabled {
			bg = ui.Blend(th.Accent, 0xFFFFFF, 0.12*hvt)
			border = bg
		}
		if disabled {
			bg = ui.Blend(th.Accent, th.Surface, 0.62)
			fg = ui.Blend(0xFFFFFF, th.Surface, 0.35)
			border = bg
		}
	case BtnDanger:
		bg, fg, border = th.Danger, 0xFFFFFF, th.Danger
		if hvt > 0 {
			bg = ui.Blend(th.Danger, 0xFFFFFF, 0.14*hvt)
			border = bg
		}
	case BtnGhost:
		bg, fg, border = th.Card, th.Text, th.Border
		if hvt > 0 {
			bg = ui.Blend(th.Card, th.CardHover, hvt)
			border = ui.Blend(th.Border, th.Accent, 0.28*hvt)
		}
	case BtnSubtle:
		bg, fg = 0, th.TextDim
		if hvt > 0 {
			bg = ui.Blend(th.Surface, th.CardHover, hvt)
			fg = ui.Blend(th.TextDim, th.Text, hvt)
		}
	}
	if pressed && !disabled && bg != 0 {
		bg = ui.Blend(bg, 0x000000, 0.10)
	}
	if bg != 0 {
		u.c.Round(r, radius, bg)
	}
	if border != 0 {
		u.c.RoundStroke(r, radius, border)
	}
	// 悬停态主按钮加微弱柔光环（区别于禁用态）
	if kind == BtnPrimary && !disabled && hvt > 0 {
		u.c.RoundStroke(r.Inset(-1), radius+1, ui.Blend(th.Accent, 0xFFFFFF, 0.40*hvt))
	}
	if u.focus == id {
		u.c.RoundStroke(r.Inset(-3), radius+2, th.InputFocus)
	}
	if ik, ok := iconKind(label); ok {
		if border == 0 && bg == 0 {
			fg = th.TextDim
			if hvt > 0 {
				fg = ui.Blend(th.TextDim, th.Text, hvt)
			}
		}
		drawIcon(u.c, ik, ui.R(r.CX()-8, r.CY()-8, 16, 16), fg)
		return !disabled && u.click == id
	}
	// 只有确实放不下时才省略；小按钮（如侧栏「+ 新建」）保持原文，
	// 否则 34px 宽的按钮会被截成一个省略号，看着像坏了。
	if tw := u.c.MeasureFace(label, ui.FsSm, 500, ui.FaceUI); tw > r.W-16 && r.W > 44 {
		label = ellipsis(u.c, label, ui.FsSm, 500, r.W-16)
	}
	w := u.c.MeasureFace(label, ui.FsSm, 500, ui.FaceUI)
	u.c.TextFace(r.CX()-w/2, r.CY()-8, label, ui.FsSm, 500, fg, ui.FaceUI)
	return !disabled && u.click == id
}

// disabledID 由 App 注入禁用判断。
var disabledIDs = map[string]bool{}

func (u *UI) disabledID(id string) bool { return disabledIDs[id] }

// IconButton 绘制一个图标按钮（用字符或自绘图形）。
func (u *UI) IconButton(id string, r ui.Rect, glyph string, active bool) bool {
	u.Hit(id, r)
	hvt := u.hoverT(id)
	pressed := u.pressId == id && u.pressBtn == 0
	th := u.th
	if active {
		u.c.Round(r, 6, ui.Blend(th.Surface, th.Accent, 0.16))
	} else if hvt > 0 {
		u.c.Round(r, 6, ui.Blend(th.Surface, th.CardHover, hvt))
	}
	if pressed {
		u.c.Round(r, 6, ui.Blend(th.CardHover, th.Accent, 0.24))
	}
	fg := th.TextDim
	if active {
		fg = th.Accent
	} else if hvt > 0 {
		fg = ui.Blend(th.TextDim, th.Text, hvt)
	}
	if u.focus == id {
		u.c.RoundStroke(r.Inset(-2), 8, th.InputFocus)
	}
	if ik, ok := iconKind(glyph); ok {
		drawIcon(u.c, ik, ui.R(r.CX()-8, r.CY()-8, 16, 16), fg)
	} else {
		w := u.c.MeasureFace(glyph, ui.FsSm, 500, ui.FaceUI)
		u.c.TextFace(r.CX()-w/2, r.CY()-8, glyph, ui.FsSm, 500, fg, ui.FaceUI)
	}
	return u.click == id
}

func (u *UI) TextField(id string, r ui.Rect, initial, hint string) string {
	f := u.Field(id, initial)
	focused := u.focus == id
	hover := u.Hit(id, r)
	th := u.th

	bg, border := th.InputBg, th.InputBorder
	if focused {
		border = th.InputFocus
	} else if hover {
		border = ui.Blend(th.InputBorder, th.Text, 0.22)
	}
	u.c.Round(r, 6, bg)
	u.c.RoundStroke(r, 6, border)
	if focused {
		// 外圈柔光环：聚焦状态一眼可辨，又不至于把边框糊成一团。
		u.c.RoundStroke(r.Inset(-2), 8, ui.Blend(th.InputFocus, bg, 0.55))
	}

	pad := int32(12)
	text := string(f.Text)
	display := text
	if f.Password && !f.Reveal && text != "" {
		display = strings.Repeat("•", len([]rune(text)))
	} else if f.Masked && !focused && text != "" {
		display = strings.Repeat("•", len([]rune(text)))
	}

	lineH := int32(18)
	ty := r.CY() - lineH/2 - 1
	clip := r.Shrink(pad, 0, pad, 0)
	u.c.Clip(clip)
	if display == "" {
		if hint != "" {
			h := ellipsis(u.c, hint, ui.FsBase, 400, r.W-pad*2)
			u.c.TextFace(r.X+pad, ty, h, ui.FsBase, 400, th.TextMute, ui.FaceUI)
		}
	} else {
		curX := u.c.MeasureFace(string(f.Text[:f.Cur]), ui.FsBase, 400, ui.FaceUI)
		maxW := clip.W
		if f.Scroll > curX {
			f.Scroll = curX
		}
		if curX-f.Scroll > maxW-int32(4) {
			f.Scroll = curX - maxW + 4
		}
		if f.Scroll < 0 {
			f.Scroll = 0
		}
		u.c.TextFace(r.X+pad-f.Scroll, ty, display, ui.FsBase, 400, th.Text, ui.FaceUI)
	}
	u.c.PopClip()

	if focused {
		curX := u.c.MeasureFace(string(f.Text[:f.Cur]), ui.FsBase, 400, ui.FaceUI)
		cx := r.X + pad + curX - f.Scroll
		if cx < r.X+pad {
			cx = r.X + pad
		}
		if cx > r.Right()-pad {
			cx = r.Right() - pad
		}
		u.caretX, u.caretY, u.caretOK = cx, r.Y+6, true
		if blinkOn(u.phase) {
			u.c.VLine(cx, r.Y+8, r.H-16, th.Accent)
		}
	}
	return text
}

func blinkOn(phase float32) bool {
	// 1 秒周期，前 60% 亮
	p := phase - float32(int(phase))
	return p < 0.6
}

// Toggle 开关控件，返回新值。
func (u *UI) Toggle(id string, r ui.Rect, on bool) bool {
	return u.ToggleHit(id, r, r, on)
}

// ToggleHit 命中区与绘制区分离的开关：让整行标签都能点，
// 而不是逼用户去瞄准那个 38×20 的小胶囊。
func (u *UI) ToggleHit(id string, hitR, r ui.Rect, on bool) bool {
	hover := u.Hit(id, hitR)
	th := u.th
	track := ui.R(r.X, r.CY()-11, 40, 22)
	bg := th.Border
	if on {
		bg = th.Accent
	}
	if hover {
		bg = ui.Blend(bg, th.Text, 0.12)
	}
	u.c.Round(track, 11, bg)
	knobX := track.X + 3
	if on {
		knobX = track.Right() - 19
	}
	u.c.Dot(knobX+8, track.CY(), 8, 0xFFFFFF)
	if u.click == id {
		return !on
	}
	return on
}

func (u *UI) Label(x, y int32, s string, size int32, color uint32) {
	u.c.TextFace(x, y, s, size, 400, color, ui.FaceUI)
}

// Hint 绘制次要说明（12 号字）。
func (u *UI) Hint(x, y int32, s string) {
	u.c.TextFace(x, y, s, ui.FsSm, 400, u.th.TextMute, ui.FaceUI)
}

// Change 判断本帧焦点框内容是否被改动（简易版：由 App 直接比较）。
func fieldChanged(f *fieldState, before string) bool { return string(f.Text) != before }

// Scrollbar 绘制竖向滚动条。
func (u *UI) Scrollbar(track ui.Rect, total, visible, offset int32) {
	if total <= visible {
		return
	}
	u.c.Round(track, 3, ui.Blend(u.th.Bg, u.th.Text, 0.04))
	h := track.H * visible / total
	if h < 24 {
		h = 24
	}
	maxOff := total - visible
	pos := int32(0)
	if maxOff > 0 {
		pos = (track.H - h) * offset / maxOff
	}
	u.c.Round(ui.R(track.X, track.Y+pos, 6, h), 3, u.th.ScrollThumb)
}

// 常用文本对齐标志
const (
	alignLeft   = win32.DT_LEFT
	alignCenter = win32.DT_CENTER
	alignRight  = win32.DT_RIGHT
)
