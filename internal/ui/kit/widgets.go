package kit

import (
	"strings"

	"win7harness/internal/ui"
)

// ---------- Label ----------

type label struct {
	Text   string
	Size   int32
	Weight int32
	Color  uint32
	Face   string
	r      Rect
}

// Label 单行文本标签。
func Label(text string, size, weight int32, color uint32) Element {
	return &label{Text: text, Size: size, Weight: weight, Color: color, Face: ui.FaceUI}
}

// LabelFace 指定字体族的标签（Consolas 等）。
func LabelFace(text string, size, weight int32, color uint32, face string) Element {
	return &label{Text: text, Size: size, Weight: weight, Color: color, Face: face}
}

func (e *label) Measure(c *Ctx, cons Constraint) Size {
	w := int32(0)
	if e.Text != "" {
		w = c.MeasureFace(e.Text, e.Size, e.Weight, e.Face)
	}
	h := c.LineHeight(e.Size)
	return Size{W: w, H: h}
}

func (e *label) Arrange(c *Ctx, r Rect) { e.r = r }

func (e *label) Paint(c *Ctx, g Painter) {
	if e.Text == "" {
		return
	}
	y := e.r.Y + (e.r.H-e.Size)/2
	// 文本 baseline 视觉居中：GDI 以顶部基准绘制，这里按半行距下移
	g.TextFace(e.r.X, y, e.Text, e.Size, e.Weight, e.Color, e.Face)
}

// ---------- Divider ----------

type divider struct {
	Thickness int32
	Color     uint32
	Inset     int32
	r         Rect
}

func Divider(thickness int32, color uint32, inset int32) Element {
	return &divider{Thickness: thickness, Color: color, Inset: inset}
}

func (e *divider) Measure(c *Ctx, cons Constraint) Size {
	return Size{W: cons.MaxW, H: e.Thickness}
}
func (e *divider) Arrange(c *Ctx, r Rect) { e.r = r }
func (e *divider) Paint(c *Ctx, g Painter) {
	g.HLine(e.r.X+e.Inset, e.r.Y, e.r.W-e.Inset*2, e.Color)
}

// ---------- Button ----------

type ButtonKind int

const (
	ButtonPrimary ButtonKind = iota
	ButtonSecondary
	ButtonGhost
	ButtonDanger
)

type button struct {
	ID       string
	Label    string
	Kind     ButtonKind
	Disabled bool
	OnClick  func()
	r        Rect
}

// Button 按钮。注册命中区并负责 hover/press/focus 态。
func Button(id, label string, kind ButtonKind, onClick func()) Element {
	return &button{ID: id, Label: label, Kind: kind, OnClick: onClick}
}

// ButtonDisabled 禁用态（不注册命中）。
func ButtonDisabled(id, label string, kind ButtonKind) Element {
	return &button{ID: id, Label: label, Kind: kind, Disabled: true}
}

func buttonColors(kind ButtonKind, tk *ui.Tokens) (fill, border, text uint32) {
	switch kind {
	case ButtonPrimary:
		return tk.AccentBase, tk.AccentBase, tk.TextInverse
	case ButtonSecondary:
		return tk.BGSurfaceRaise, tk.BorderStrong, tk.TextPrimary
	case ButtonGhost:
		return 0, 0, tk.TextSecondary
	case ButtonDanger:
		return tk.StateDanger, tk.StateDanger, tk.TextInverse
	}
	return tk.BGSurfaceRaise, tk.BorderStrong, tk.TextPrimary
}

func (e *button) Measure(c *Ctx, cons Constraint) Size {
	w := int32(24)
	if e.Label != "" {
		w += c.MeasureFace(e.Label, ui.FsMd, 700, ui.FaceUI)
	}
	return Size{W: w, H: ui.ControlH}
}

func (e *button) Arrange(c *Ctx, r Rect) { e.r = r }

func (e *button) Paint(c *Ctx, g Painter) {
	tk := c.Tokens
	fill, border, text := buttonColors(e.Kind, tk)
	hover := c.IsHovered(e.r)
	pressed := c.IsPressed(e.ID)
	hv := c.HoverT(e.ID, hover || pressed, 3)

	// hover 混入（主题色向 hover 色插值）
	if !e.Disabled && hv > 0 {
		switch e.Kind {
		case ButtonPrimary:
			fill = Blend(fill, tk.AccentHover, hv)
		case ButtonSecondary:
			fill = Blend(fill, tk.BGHover, hv)
			border = Blend(border, tk.BorderHighlight, hv)
		case ButtonGhost:
			fill = Blend(0, tk.BGHover, hv)
		case ButtonDanger:
			fill = Blend(fill, ui.Blend(tk.StateDanger, 0xFFFFFF, 0.12), hv)
		}
	}

	if e.Disabled {
		fill = tk.BGSurface
		border = tk.BorderHairline
		text = tk.TextTertiary
	}

	// 绘制
	if fill != 0 {
		g.Round(e.r, 8, fill)
	}
	if border != 0 {
		g.RoundStroke(e.r, 8, border)
	}

	// 文本居中（DT_CENTER|DT_SINGLELINE = 0x21；kit 不依赖 win32，直接写常量）
	{
		size := int32(ui.FsMd)
		tr := R(e.r.X, e.r.Y+(e.r.H-c.LineHeight(size))/2, e.r.W, c.LineHeight(size))
		if e.Label != "" {
			g.TextRectFace(tr, e.Label, size, 700, text, 0x00000021, ui.FaceUI)
		}
	}

	// 焦点环（disabled 不参与 Tab）
	if !e.Disabled {
		focused := c.RegisterFocus(e.ID)
		if focused {
			fr := e.r.Inset(-1)
			g.RoundStroke(fr, 9, tk.FocusRing)
		}

		// 命中注册
		onClick := e.OnClick
		c.RegisterHit(e.ID, e.r, onClick)
	}
}

// ---------- IconButton ----------

type iconButton struct {
	ID       string
	Icon     string // 见 drawIcon
	Active   bool
	Disabled bool
	OnClick  func()
	r        Rect
}

// IconButton 图标按钮（32×32 热区，16px 图标）。
func IconButton(id, icon string, onClick func()) Element {
	return &iconButton{ID: id, Icon: icon, OnClick: onClick}
}

// IconButtonActive 选中态（有常显底色）。
func IconButtonActive(id, icon string, active bool, onClick func()) Element {
	return &iconButton{ID: id, Icon: icon, Active: active, OnClick: onClick}
}

func (e *iconButton) Measure(c *Ctx, cons Constraint) Size { return Size{W: 32, H: 32} }
func (e *iconButton) Arrange(c *Ctx, r Rect)               { e.r = r }

func (e *iconButton) Paint(c *Ctx, g Painter) {
	tk := c.Tokens
	hover := c.IsHovered(e.r)
	pressed := c.IsPressed(e.ID)
	hv := c.HoverT(e.ID, hover || pressed, 3)

	base := tk.BGSurface
	if hover && !e.Disabled {
		base = Blend(tk.BGSurface, tk.BGHover, 0.55+0.45*hv)
	}
	if e.Active {
		base = tk.AccentSubtle
	}
	if pressed && !e.Disabled {
		base = tk.BGActive
	}
	if e.Disabled {
		base = tk.BGSurface
	}
	g.Round(e.r, 8, base)
	if e.Active {
		g.RoundStroke(e.r, 8, tk.AccentBase)
	}

	iconColor := tk.TextSecondary
	if e.Active {
		iconColor = tk.AccentBase
	}
	if !e.Disabled {
		iconColor = Blend(iconColor, tk.TextPrimary, hv)
	} else {
		iconColor = tk.TextTertiary
	}

	drawIcon(g, e.Icon, e.r, iconColor)

	if !e.Disabled {
		focused := c.RegisterFocus(e.ID)
		if focused {
			fr := e.r.Inset(-1)
			g.RoundStroke(fr, 9, tk.FocusRing)
		}

		onClick := e.OnClick
		c.RegisterHit(e.ID, e.r, onClick)
	}
}

// ---------- Input（单行输入框） ----------

type input struct {
	ID          string
	Placeholder string
	MaxLen      int
	OnSubmit    func()
	r           Rect
}

// Input 单行文本输入。id 同时作为 StateStore key 与焦点名。
func Input(id, placeholder string, maxLen int, onSubmit func()) Element {
	return &input{ID: id, Placeholder: placeholder, MaxLen: maxLen, OnSubmit: onSubmit}
}

func (e *input) Measure(c *Ctx, cons Constraint) Size {
	w := int32(160)
	if cons.MaxW > w {
		w = cons.MaxW
	}
	return Size{W: w, H: ui.ControlH}
}

func (e *input) Arrange(c *Ctx, r Rect) { e.r = r }

func (e *input) Paint(c *Ctx, g Painter) {
	tk := c.Tokens
	st := c.Store(e.ID)
	hover := c.IsHovered(e.r)
	fmt := tk.BorderStrong
	if hover {
		fmt = tk.BorderHighlight
	}
	focused := c.RegisterFocus(e.ID)
	if focused {
		fmt = tk.FocusRing
	}

	// 内槽底 + 发丝描边
	g.Round(e.r, 6, tk.BGSurfaceSunken)
	g.RoundStroke(e.r, 6, fmt)

	// 聚焦双层柔光环：外环 2px focus ring（在控件外描）
	if focused {
		fr := e.r.Inset(-1)
		g.RoundStroke(fr, 7, tk.FocusRing)
	}

	// 文本（水平按光标位置滚动）
	text := string(st.Text)
	display := text
	if e.MaxLen > 0 {
		r := []rune(text)
		if len(r) > e.MaxLen {
			text = string(r[:e.MaxLen])
			display = text
			st.Text = text
		}
	}
	pad := int32(12)
	// 光标位置
	caretX := g.MeasureFace(display, ui.FsMd, 400, ui.FaceUI)
	if int(caretX) > int(e.r.W-2*pad) && e.r.W > 2*pad {
		// 超宽时展示尾部（简化为右对齐，完整滚动留给后续版本）
		_ = caretX
	}
	textY := e.r.Y + (e.r.H-int32(ui.FsMd))/2
	if e.Placeholder != "" && text == "" {
		g.TextFace(e.r.X+pad, textY, e.Placeholder, ui.FsMd, 400, tk.TextTertiary, ui.FaceUI)
	} else if text != "" {
		g.TextFace(e.r.X+pad, textY, display, ui.FsMd, 400, tk.TextPrimary, ui.FaceUI)
	}
	// 光标（仅聚焦时画）
	if focused {
		g.VLine(e.r.X+pad+caretX, e.r.Y+8, e.r.H-16, tk.AccentBase)
	}

	// 命中：设置焦点 + 光标到末尾
	id := e.ID
	c.RegisterHit(id, e.r, func() {
		c.SetFocus(id)
		// 光标定位到点击处：用前缀二分（GDI 度量为真值）
		// 简化：光标置于文本末尾（聊天输入主力场景够用）
		st.Cursor = len([]rune(st.Text))
	})
}

// InputText 返回输入框当前文本（外部读）。
func InputText(c *Ctx, id string) string { return c.Store(id).Text }

// InputSetText 强制设置输入框文本。
func InputSetText(c *Ctx, id, v string) {
	st := c.Store(id)
	st.Text = v
	st.Cursor = len([]rune(v))
}

// InputKey 处理一次按键（聚焦于 id 时）。backspace=true 表示退格。
func InputKey(c *Ctx, id string, ch rune, backspace bool) {
	st := c.Store(id)
	r := []rune(st.Text)
	if backspace {
		if st.Cursor > 0 && len(r) > 0 && st.Cursor <= len(r) {
			r = append(r[:st.Cursor-1], r[st.Cursor:]...)
			st.Cursor--
			st.Text = string(r)
		}
		return
	}
	if ch >= 32 {
		r = append(r[:st.Cursor], append([]rune{ch}, r[st.Cursor:]...)...)
		st.Cursor++
		st.Text = string(r)
	}
}

// ---------- Toggle ----------

type toggle struct {
	ID       string
	OnChange func(on bool)
	r        Rect
}

// Toggle 开关（40×22）。值保存在 StateStore[key].Text = "1"|"0"。
func Toggle(id string, onChange func(on bool)) Element {
	return &toggle{ID: id, OnChange: onChange}
}

func ToggleValue(c *Ctx, id string) bool { return c.Store(id).Text == "1" }
func ToggleSet(c *Ctx, id string, v bool) {
	if v {
		c.Store(id).Text = "1"
	} else {
		c.Store(id).Text = "0"
	}
}

func (e *toggle) Measure(c *Ctx, cons Constraint) Size { return Size{W: 40, H: 22} }
func (e *toggle) Arrange(c *Ctx, r Rect)               { e.r = r }

func (e *toggle) Paint(c *Ctx, g Painter) {
	tk := c.Tokens
	st := c.Store(e.ID)
	on := st.Text == "1"
	hover := c.IsHovered(e.r)
	hv := c.HoverT(e.ID, hover, 3)

	track := tk.BorderStrong
	if on {
		track = tk.AccentBase
	}
	if hover && !on {
		track = Blend(tk.BorderStrong, tk.BorderHighlight, hv)
	}
	g.Round(e.r, 11, track)

	knobR := int32(8)
	knobX := e.r.X + 4 + knobR
	if on {
		knobX = e.r.X + e.r.W - 4 - knobR
	}
	// 圆形白色圆点（用两笔同色圆近似：实心 Dot）
	g.Dot(knobX, e.r.Y+e.r.H/2, knobR, 0x00FFFFFF)
	g.Ring(knobX, e.r.Y+e.r.H/2, knobR, Blend(tk.BorderStrong, tk.BGCanvas, 0.5))

	// 命中
	id := e.ID
	c.RegisterHit(id, e.r, func() {
		st := c.Store(id)
		on := st.Text != "1"
		if on {
			st.Text = "1"
		} else {
			st.Text = "0"
		}
		if e.OnChange != nil {
			e.OnChange(on)
		}
	})
}

// ---------- Card ----------

type card struct {
	Pad     int32
	Child   Element
	OnClick func()
	r       Rect
}

// Card 卡片容器。
func Card(pad int32, child Element) Element {
	return &card{Pad: pad, Child: child}
}

func (e *card) Measure(c *Ctx, cons Constraint) Size {
	s := e.Child.Measure(c, shrinkConstraint(cons, e.Pad*2))
	return Size{W: s.W + e.Pad*2, H: s.H + e.Pad*2}
}

func (e *card) Arrange(c *Ctx, r Rect) {
	e.r = r
	e.Child.Arrange(c, R(r.X+e.Pad, r.Y+e.Pad, r.W-e.Pad*2, r.H-e.Pad*2))
}

func (e *card) Paint(c *Ctx, g Painter) {
	tk := c.Tokens
	g.Round(e.r, 10, tk.BGSurfaceRaise)
	g.RoundStroke(e.r, 10, tk.BorderHairline)
	// 顶边 1px 高光（L2 卡片：顶边略亮）
	g.HLine(e.r.X+10, e.r.Y, e.r.W-20, tk.BorderHighlight)
	e.Child.Paint(c, g)
}

// ---------- 辅助 ----------

// Empty 绘制空状态（居中标题+说明+可选操作按钮）。
func Empty(c *Ctx, g Painter, r Rect, title, sub string) {
	tk := c.Tokens
	size := int32(16)
	w := c.MeasureFace(title, size, 700, ui.FaceUI)
	if w < 120 {
		w = 120
	}
	g.TextFace(r.X+(r.W-w)/2, r.Y+r.H/2-28, title, size, 700, tk.TextPrimary, ui.FaceUI)
	w2 := c.MeasureFace(sub, 12, 400, ui.FaceUI)
	g.TextFace(r.X+(r.W-w2)/2, r.Y+r.H/2+4, sub, 12, 400, tk.TextTertiary, ui.FaceUI)
}

// TextTrunc 文本右截断（GDI 度量为真值，二分找最大长度）。
func TextTrunc(c *Ctx, s string, size, weight int32, maxW int32, face string) string {
	if s == "" || maxW <= 0 {
		return ""
	}
	full := c.MeasureFace(s, size, weight, face)
	if full <= maxW {
		return s
	}
	r := []rune(s)
	lo, hi := 0, len(r)
	for lo < hi {
		mid := (lo + hi + 1) / 2
		w := c.MeasureFace(string(r[:mid])+"…", size, weight, face)
		if w <= maxW {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return string(r[:lo]) + "…"
}

// ClipAll 裁剪到 r 并立即弹出。
func ClipAll(g Painter, r Rect) func() {
	g.Clip(r)
	return func() { g.PopClip() }
}

// HasPrefix 报告 s 是否以 prefix 开头（UTF-8 安全，kit 内部用）。
func HasPrefix(s, prefix string) bool { return strings.HasPrefix(s, prefix) }
