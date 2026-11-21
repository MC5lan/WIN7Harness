package ui

// 组件原语层（headless）：组件只描述"状态"，配色由 Resolve 统一映射到令牌。
//
// 为什么要把状态与配色拆开：
//   历史上每个绘制点各自写死颜色，同一个"次按钮"在三个文件里出现了三种灰，
//   改一次视觉要改十几处、还必然漏。这里把"取色"收成唯一入口 Resolve，
//   组件函数只决定几何与状态，颜色永远来自 Tokens。
//
// 约束：
//   - 本文件不允许出现十六进制色值（除 Tokens 定义处）。
//   - 文本一律走 Canvas 的 GDI 度量/绘制，几何走 canvas 命令流。
//   - 字重只用 400 / 700（Win7 GDI 的 600 会被合成粗体，笔画粘连）。

// State 是组件的交互状态。
type State uint8

const (
	StateDefault State = iota
	StateHover
	StateActive
	StateSelected
	StateFocus
	StateDisabled
)

// ComponentKind 是组件种类。新增组件必须先在这里登记，否则 Resolve 无值可返。
type ComponentKind uint8

const (
	KindButtonPrimary ComponentKind = iota
	KindButtonSecondary
	KindButtonGhost
	KindButtonDanger
	KindInput
	KindSelect
	KindTextarea
	KindToggle

	KindTab
	KindCard
	KindListItem
	KindBadge
	KindBadgeSuccess
	KindBadgeWarn
	KindBadgeDanger

	KindTableHeader
	KindTableRow
	KindScrollbar
	KindSplitter
	KindStatusBar
	KindTooltip
	KindToastSuccess
	KindToastWarn
	KindToastDanger
	KindDrawer
)

// Style 是解析后的最终配色。BorderWidth 为 0 表示不描边。
type Style struct {
	Fill        uint32
	Border      uint32
	Text        uint32
	BorderWidth int
}

// currentTokens 是"当前生效的令牌表"。由 SetTokens 在主题切换/启动注入。
// 组件函数不接收令牌参数，保证调用点不会各拿一套色。
var currentTokens = DarkTokens()

// SetTokens 切换全局令牌表。主题变化、高对比切换都走这里。
func SetTokens(tk Tokens) { currentTokens = tk }

// CurrentTokens 返回当前令牌副本，供需要直接取色的绘制代码使用。
func CurrentTokens() Tokens { return currentTokens }

// Resolve 是唯一取色入口：给定组件种类与状态，返回最终配色。
// 它是纯函数（只依赖当前令牌），因此可以直接单测。
func Resolve(kind ComponentKind, s State) Style {
	tk := currentTokens

	// 禁用态统一：底色退到表面色，文字退到三级文字色。
	// 不用半透明降亮度——软光栅上半透明文本会发灰发糊。
	if s == StateDisabled {
		return resolveDisabled(kind, tk)
	}

	switch kind {
	case KindButtonPrimary:
		fill := tk.AccentBase
		switch s {
		case StateHover:
			fill = tk.AccentHover
		case StateActive:
			fill = tk.AccentPressed
		}
		return Style{Fill: fill, Border: fill, Text: tk.TextInverse, BorderWidth: 1}

	case KindButtonDanger:
		fill := tk.StateDanger
		if s == StateHover {
			fill = blendColor(tk.StateDanger, 0xFFFFFF, 0.12)
		} else if s == StateActive {
			fill = blendColor(tk.StateDanger, 0x000000, 0.12)
		}
		return Style{Fill: fill, Border: fill, Text: tk.TextInverse, BorderWidth: 1}

	case KindButtonSecondary:
		fill := tk.BGSurfaceRaise
		border := tk.BorderStrong
		if s == StateHover {
			fill = tk.BGHover
			border = tk.BorderHighlight
		} else if s == StateActive {
			fill = tk.BGActive
		}
		return Style{Fill: fill, Border: border, Text: tk.TextPrimary, BorderWidth: 1}

	case KindButtonGhost:
		fill := uint32(0)
		text := tk.TextSecondary
		if s == StateHover {
			fill = tk.BGHover
			text = tk.TextPrimary
		} else if s == StateActive {
			fill = tk.BGActive
			text = tk.TextPrimary
		}
		return Style{Fill: fill, Border: 0, Text: text}

	case KindInput, KindTextarea:
		border := tk.BorderStrong
		if s == StateFocus {
			border = tk.FocusRing
		} else if s == StateHover {
			border = tk.BorderHighlight
		}
		return Style{Fill: tk.BGSurfaceSunken, Border: border, Text: tk.TextPrimary, BorderWidth: 1}

	case KindSelect:
		border := tk.BorderStrong
		if s == StateHover {
			border = tk.BorderHighlight
		} else if s == StateFocus {
			border = tk.FocusRing
		}
		return Style{Fill: tk.BGSurfaceSunken, Border: border, Text: tk.TextPrimary, BorderWidth: 1}

	case KindToggle:
		fill := tk.BorderStrong
		if s == StateSelected || s == StateActive {
			fill = tk.AccentBase
		}
		return Style{Fill: fill, Border: 0, Text: tk.TextPrimary}

	case KindTab:
		text := tk.TextSecondary
		if s == StateSelected {
			text = tk.TextPrimary
		} else if s == StateHover {
			text = tk.TextPrimary
		}
		return Style{Fill: 0, Border: 0, Text: text}

	case KindCard, KindDrawer:
		return Style{Fill: tk.BGSurfaceRaise, Border: tk.BorderHairline, Text: tk.TextPrimary, BorderWidth: 1}

	case KindListItem:
		fill := uint32(0)
		if s == StateSelected {
			fill = tk.AccentSubtle
		} else if s == StateHover {
			fill = tk.BGHover
		}
		return Style{Fill: fill, Border: 0, Text: tk.TextPrimary}

	case KindBadge:
		return Style{Fill: tk.BGSurfaceSunken, Border: tk.BorderHairline, Text: tk.TextSecondary, BorderWidth: 1}
	case KindBadgeSuccess:
		return Style{Fill: tk.BGSurfaceSunken, Border: tk.StateSuccess, Text: tk.StateSuccess, BorderWidth: 1}
	case KindBadgeWarn:
		return Style{Fill: tk.BGSurfaceSunken, Border: tk.StateWarn, Text: tk.StateWarn, BorderWidth: 1}
	case KindBadgeDanger:
		return Style{Fill: tk.BGSurfaceSunken, Border: tk.StateDanger, Text: tk.StateDanger, BorderWidth: 1}

	case KindTableHeader:
		return Style{Fill: tk.BGSurface, Border: tk.BorderHairline, Text: tk.TextSecondary, BorderWidth: 1}
	case KindTableRow:
		fill := tk.BGSurfaceSunken
		if s == StateSelected {
			fill = tk.AccentSubtle
		} else if s == StateHover {
			fill = tk.BGHover
		}
		return Style{Fill: fill, Border: tk.BorderHairline, Text: tk.TextPrimary}

	case KindScrollbar:
		fill := tk.BorderStrong
		if s == StateHover || s == StateActive {
			fill = tk.BorderHighlight
		}
		return Style{Fill: fill}

	case KindSplitter:
		if s == StateHover || s == StateActive {
			return Style{Fill: tk.AccentBase}
		}
		return Style{Fill: 0}

	case KindStatusBar:
		return Style{Fill: tk.BGCanvas, Border: tk.BorderHairline, Text: tk.TextTertiary, BorderWidth: 1}

	case KindTooltip:
		return Style{Fill: tk.BGSurfaceRaise, Border: tk.BorderStrong, Text: tk.TextPrimary, BorderWidth: 1}

	case KindToastSuccess:
		return Style{Fill: tk.BGSurfaceRaise, Border: tk.StateSuccess, Text: tk.TextPrimary, BorderWidth: 1}
	case KindToastWarn:
		return Style{Fill: tk.BGSurfaceRaise, Border: tk.StateWarn, Text: tk.TextPrimary, BorderWidth: 1}
	case KindToastDanger:
		return Style{Fill: tk.BGSurfaceRaise, Border: tk.StateDanger, Text: tk.TextPrimary, BorderWidth: 1}
	}
	return Style{Fill: tk.BGSurface, Border: tk.BorderHairline, Text: tk.TextPrimary, BorderWidth: 1}
}

// resolveDisabled 返回禁用态样式：底色退到表面色，文字退到三级文字色。
func resolveDisabled(kind ComponentKind, tk Tokens) Style {
	switch kind {
	case KindButtonPrimary, KindButtonDanger:
		return Style{Fill: tk.BGSurface, Border: tk.BorderHairline, Text: tk.TextTertiary, BorderWidth: 1}
	case KindButtonSecondary, KindInput, KindSelect, KindTextarea:
		return Style{Fill: tk.BGSurfaceSunken, Border: tk.BorderHairline, Text: tk.TextTertiary, BorderWidth: 1}
	case KindButtonGhost:
		return Style{Fill: 0, Border: 0, Text: tk.TextTertiary}
	}
	return Style{Fill: tk.BGSurface, Border: tk.BorderHairline, Text: tk.TextTertiary, BorderWidth: 1}
}

// blendColor 是包内取色混合（COLORREF 分通道线性插值）。
// gfx.go 里的 Blend 已存在，这里复用避免两套实现漂移。
func blendColor(a, b uint32, t float32) uint32 { return Blend(a, b, t) }

/* ---------------- 组件绘制 ---------------- */

// textTop 让文字在矩形内垂直居中。TextFace 的 y 是文本顶部，不是基线。
func textTop(c *Canvas, r Rect, size int32) int32 {
	lh := c.LineHeight(size)
	if lh <= 0 {
		lh = size
	}
	return r.CY() - lh/2
}

// clipText 按可用宽度截断，尾部加省略号。
// 度量与绘制都走 GDI，避免 canvas 自算宽度与 GDI 差 1-2px 造成抖动。
func clipText(c *Canvas, s string, size, weight int32, maxW int32, face string) string {
	if maxW <= 0 || s == "" {
		return ""
	}
	if c.MeasureFace(s, size, weight, face) <= maxW {
		return s
	}
	rs := []rune(s)
	for len(rs) > 1 {
		rs = rs[:len(rs)-1]
		if c.MeasureFace(string(rs)+"…", size, weight, face) <= maxW {
			return string(rs) + "…"
		}
	}
	return "…"
}

// DrawButton 画一个按钮。变体由 kind 决定，状态由 st 决定。
func DrawButton(c *Canvas, r Rect, label string, kind ComponentKind, st State) {
	sp := Resolve(kind, st)
	if sp.Fill != 0 {
		c.Round(r, RadiusControl, sp.Fill)
	}
	if sp.BorderWidth > 0 && sp.Border != 0 {
		c.RoundStroke(r, RadiusControl, sp.Border)
	}
	size := int32(Scaled(FSBody))
	pad := int32(Scaled(Space3))
	txt := clipText(c, label, size, 700, r.W-pad*2, FaceUI)
	w := c.MeasureFace(txt, size, 700, FaceUI)
	c.TextFace(r.CX()-w/2, textTop(c, r, size), txt, size, 700, sp.Text, FaceUI)
	if st == StateFocus {
		c.RoundStroke(r.Inset(-2), RadiusControl+2, CurrentTokens().FocusRing)
	}
}

// DrawInput 画单行输入框（含 placeholder 与焦点环）。
func DrawInput(c *Canvas, r Rect, text, placeholder string, st State) {
	sp := Resolve(KindInput, st)
	c.Round(r, RadiusControl, sp.Fill)
	c.RoundStroke(r, RadiusControl, sp.Border)
	if st == StateFocus {
		c.RoundStroke(r.Inset(-2), RadiusControl+2, blendColor(CurrentTokens().FocusRing, sp.Fill, 0.55))
	}
	size := int32(Scaled(FSBody))
	pad := int32(Scaled(Space3))
	clip := r.Shrink(pad, 0, pad, 0)
	c.Clip(clip)
	if text == "" {
		h := clipText(c, placeholder, size, 400, clip.W, FaceUI)
		c.TextFace(r.X+pad, textTop(c, r, size), h, size, 400, CurrentTokens().TextTertiary, FaceUI)
	} else {
		t := clipText(c, text, size, 400, clip.W, FaceUI)
		c.TextFace(r.X+pad, textTop(c, r, size), t, size, 400, sp.Text, FaceUI)
	}
	c.PopClip()
}

// DrawSelect 画下拉框：左文右 ▾（三角用几何绘制，不用字体符号）。
func DrawSelect(c *Canvas, r Rect, value string, st State) {
	sp := Resolve(KindSelect, st)
	c.Round(r, RadiusControl, sp.Fill)
	c.RoundStroke(r, RadiusControl, sp.Border)
	if st == StateFocus {
		c.RoundStroke(r.Inset(-2), RadiusControl+2, CurrentTokens().FocusRing)
	}
	size := int32(Scaled(FSBody))
	pad := int32(Scaled(Space3))
	t := clipText(c, value, size, 400, r.W-pad*2-14, FaceUI)
	c.TextFace(r.X+pad, textTop(c, r, size), t, size, 400, sp.Text, FaceUI)
	// 8x5 的向下三角
	cx := r.Right() - pad - 4
	cy := r.CY()
	c.Line(cx-4, cy-2, cx, cy+2, CurrentTokens().TextSecondary)
	c.Line(cx, cy+2, cx+4, cy-2, CurrentTokens().TextSecondary)
}

// DrawTextarea 画多行输入框（最小高度 TextareaMinH）。
func DrawTextarea(c *Canvas, r Rect, lines []string, st State) {
	sp := Resolve(KindTextarea, st)
	c.Round(r, RadiusControl, sp.Fill)
	c.RoundStroke(r, RadiusControl, sp.Border)
	if st == StateFocus {
		c.RoundStroke(r.Inset(-2), RadiusControl+2, CurrentTokens().FocusRing)
	}
	size := int32(Scaled(FSBody))
	lh := c.LineHeight(size)
	pad := int32(Scaled(Space3))
	c.Clip(r.Shrink(pad, pad, pad, pad))
	y := r.Y + pad
	for _, ln := range lines {
		if y+lh > r.Bottom()-pad {
			break
		}
		t := clipText(c, ln, size, 400, r.W-pad*2, FaceUI)
		c.TextFace(r.X+pad, y, t, size, 400, sp.Text, FaceUI)
		y += lh
	}
	c.PopClip()
}

// DrawToggle 画开关：32×18 轨道 + 14 白点。
func DrawToggle(c *Canvas, r Rect, on bool, st State) {
	sp := Resolve(KindToggle, st)
	tw := int32(Scaled(32))
	th := int32(Scaled(18))
	track := Rect{X: r.X, Y: r.CY() - th/2, W: tw, H: th}
	knobR := int32(Scaled(7))
	if on {
		c.Round(track, th/2, sp.Fill)
		c.Dot(track.Right()-3-knobR, track.CY(), knobR, CurrentTokens().TextInverse)
	} else {
		c.Round(track, th/2, sp.Fill)
		c.Dot(track.X+3+knobR, track.CY(), knobR, CurrentTokens().TextPrimary)
	}
	if st == StateFocus {
		c.RoundStroke(track.Inset(-2), th/2+2, CurrentTokens().FocusRing)
	}
}

// DrawTab 画页签：底部 2px 指示条。
func DrawTab(c *Canvas, r Rect, label string, selected bool, st State) {
	sp := Resolve(KindTab, st)
	size := int32(Scaled(FSBody))
	t := clipText(c, label, size, 700, r.W-int32(Scaled(Space3))*2, FaceUI)
	w := c.MeasureFace(t, size, 700, FaceUI)
	c.TextFace(r.CX()-w/2, textTop(c, r, size), t, size, 700, sp.Text, FaceUI)
	if selected {
		c.FillRect(Rect{X: r.X, Y: r.Bottom() - 2, W: r.W, H: 2}, CurrentTokens().AccentBase)
	}
}

// DrawCard 画卡片：底色 + 1px 描边 + 顶边 1px 高光（不用阴影）。
func DrawCard(c *Canvas, r Rect, title string) {
	sp := Resolve(KindCard, StateDefault)
	c.Round(r, RadiusCard, sp.Fill)
	c.RoundStroke(r, RadiusCard, sp.Border)
	c.HLine(r.X+RadiusCard, r.Y+1, r.W-RadiusCard*2, CurrentTokens().BorderHighlight)
	if title != "" {
		size := int32(Scaled(FSH2))
		pad := int32(Scaled(Space4))
		c.TextFace(r.X+pad, r.Y+pad, clipText(c, title, size, 700, r.W-pad*2, FaceUI), size, 700, sp.Text, FaceUI)
	}
}

// DrawListItem 画列表行：选中时左侧 2px 强调条。
func DrawListItem(c *Canvas, r Rect, label, hint string, selected bool, st State) {
	sp := Resolve(KindListItem, st)
	if sp.Fill != 0 {
		c.Round(r, RadiusControl, sp.Fill)
	}
	if selected {
		c.FillRect(Rect{X: r.X, Y: r.Y + 4, W: 2, H: r.H - 8}, CurrentTokens().AccentBase)
	}
	size := int32(Scaled(FSBody))
	pad := int32(Scaled(Space3))
	hintW := int32(0)
	if hint != "" {
		hs := int32(Scaled(FSCaption))
		hint = clipText(c, hint, hs, 400, r.W/2, FaceUI)
		hintW = c.MeasureFace(hint, hs, 400, FaceUI)
		c.TextFace(r.Right()-pad-hintW, textTop(c, r, hs), hint, hs, 400, CurrentTokens().TextTertiary, FaceUI)
	}
	maxW := r.W - pad*2 - hintW - int32(Scaled(Space2))
	c.TextFace(r.X+pad, textTop(c, r, size), clipText(c, label, size, 400, maxW, FaceUI), size, 400, sp.Text, FaceUI)
}

// DrawBadge 画徽标：小圆角胶囊，语义色只描边不铺满。
func DrawBadge(c *Canvas, r Rect, label string, kind ComponentKind) {
	sp := Resolve(kind, StateDefault)
	c.Round(r, r.H/2, sp.Fill)
	c.RoundStroke(r, r.H/2, sp.Border)
	size := int32(Scaled(FSCaption))
	t := clipText(c, label, size, 400, r.W-int32(Scaled(Space2))*2, FaceUI)
	w := c.MeasureFace(t, size, 400, FaceUI)
	c.TextFace(r.CX()-w/2, textTop(c, r, size), t, size, 400, sp.Text, FaceUI)
}

// TableCol 是表格列定义。
type TableCol struct {
	Title string
	W     int32
	Right bool // 数值列右对齐
}

// DrawTableHeader 画表头。
func DrawTableHeader(c *Canvas, r Rect, cols []TableCol) {
	sp := Resolve(KindTableHeader, StateDefault)
	c.FillRect(r, sp.Fill)
	c.HLine(r.X, r.Bottom()-1, r.W, sp.Border)
	size := int32(Scaled(FSBodySm))
	x := r.X + int32(Scaled(Space3))
	for _, col := range cols {
		t := clipText(c, col.Title, size, 700, col.W, FaceUI)
		tx := x
		if col.Right {
			tx = x + col.W - c.MeasureFace(t, size, 700, FaceUI)
		}
		c.TextFace(tx, textTop(c, r, size), t, size, 700, sp.Text, FaceUI)
		x += col.W + int32(Scaled(Space2))
	}
}

// DrawTableRow 画表格行。zebra 为斑马纹（用底色不带 alpha）。
func DrawTableRow(c *Canvas, r Rect, cells []string, cols []TableCol, zebra bool, selected bool) {
	st := StateDefault
	if selected {
		st = StateSelected
	}
	sp := Resolve(KindTableRow, st)
	if selected || zebra {
		c.FillRect(r, sp.Fill)
	}
	c.HLine(r.X, r.Bottom()-1, r.W, sp.Border)
	size := int32(Scaled(FSBodySm))
	x := r.X + int32(Scaled(Space3))
	for i, col := range cols {
		if i >= len(cells) {
			break
		}
		t := clipText(c, cells[i], size, 400, col.W-4, FaceUI)
		tx := x
		if col.Right {
			tx = x + col.W - c.MeasureFace(t, size, 400, FaceUI)
		}
		c.TextFace(tx, textTop(c, r, size), t, size, 400, sp.Text, FaceUI)
		x += col.W + int32(Scaled(Space2))
	}
}

// DrawScrollbar 画滚动条：8px 宽，无箭头。
func DrawScrollbar(c *Canvas, track Rect, thumbY, thumbH int32, hot bool) {
	st := StateDefault
	if hot {
		st = StateHover
	}
	sp := Resolve(KindScrollbar, st)
	w := int32(Scaled(8))
	x := track.Right() - w
	if thumbH < int32(Scaled(16)) {
		thumbH = int32(Scaled(16))
	}
	c.Round(Rect{X: x, Y: thumbY, W: w, H: thumbH}, w/2, sp.Fill)
}

// DrawSplitter 画分隔条热区：命中时中间 2px 高亮。
func DrawSplitter(c *Canvas, r Rect, hot bool) {
	sp := Resolve(KindSplitter, StateDefault)
	if hot {
		sp = Resolve(KindSplitter, StateHover)
	}
	if sp.Fill != 0 {
		cx := r.CX()
		c.FillRect(Rect{X: cx - 1, Y: r.Y, W: 2, H: r.H}, sp.Fill)
	}
}

// DrawStatusBar 画状态栏：高 24，顶部 1px 分隔线。
func DrawStatusBar(c *Canvas, r Rect, left, right string) {
	sp := Resolve(KindStatusBar, StateDefault)
	c.FillRect(r, sp.Fill)
	c.HLine(r.X, r.Y, r.W, sp.Border)
	size := int32(Scaled(FSCaption))
	pad := int32(Scaled(Space3))
	t := textTop(c, r, size)
	rw := int32(0)
	if right != "" {
		rw = c.MeasureFace(right, size, 400, FaceUI)
		c.TextFace(r.Right()-pad-rw, t, right, size, 400, sp.Text, FaceUI)
	}
	lw := r.W - pad*2 - rw - int32(Scaled(Space2))
	c.TextFace(r.X+pad, t, clipText(c, left, size, 400, lw, FaceUI), size, 400, sp.Text, FaceUI)
}

// DrawTooltip 画提示气泡。
func DrawTooltip(c *Canvas, r Rect, text string) {
	sp := Resolve(KindTooltip, StateDefault)
	c.Round(r, RadiusControl, sp.Fill)
	c.RoundStroke(r, RadiusControl, sp.Border)
	size := int32(Scaled(FSBodySm))
	pad := int32(Scaled(Space2))
	c.TextFace(r.X+pad, textTop(c, r, size), clipText(c, text, size, 400, r.W-pad*2, FaceUI), size, 400, sp.Text, FaceUI)
}

// DrawToast 画通知条：左侧 3px 语义色竖条。
func DrawToast(c *Canvas, r Rect, text string, kind ComponentKind) {
	sp := Resolve(kind, StateDefault)
	c.Round(r, RadiusCard, sp.Fill)
	c.RoundStroke(r, RadiusCard, sp.Border)
	c.FillRect(Rect{X: r.X + 1, Y: r.Y + RadiusCard, W: 3, H: r.H - RadiusCard*2}, sp.Border)
	size := int32(Scaled(FSBody))
	pad := int32(Scaled(Space3))
	c.TextFace(r.X+pad+4, textTop(c, r, size), clipText(c, text, size, 400, r.W-pad*2-6, FaceUI), size, 400, sp.Text, FaceUI)
}

// DrawDrawer 画抽屉本体（贴边圆角 0，浮层圆角 RadiusDialog）。
func DrawDrawer(c *Canvas, r Rect, title string) {
	sp := Resolve(KindDrawer, StateDefault)
	c.FillRect(r, sp.Fill)
	c.VLine(r.X, r.Y, r.H, sp.Border)
	if title != "" {
		size := int32(Scaled(FSH2))
		pad := int32(Scaled(Space4))
		c.TextFace(r.X+pad, r.Y+pad, clipText(c, title, size, 700, r.W-pad*2, FaceUI), size, 700, sp.Text, FaceUI)
		c.HLine(r.X, r.Y+int32(Scaled(44)), r.W, sp.Border)
	}
}
