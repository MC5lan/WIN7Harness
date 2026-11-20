package app

// 主输入框：暖纸卡片 + 多行编辑 + 滚动 + 附件 + 发送。
//
// 结构（自上而下）：
//   [附件缩略图条（可选）]
//   [文本区（滚动视图）：顶/底内边距 10，行高 22/20]
//   [操作条：左上下文提示 · 右 [成本chip][模型chip][图片][截图][命令][发送]]
//
// 超过可视行数时文本区内部滚动（滚动条 + 滚轮/↑↓键），内容不丢行、光标始终可见。
// 从 app.go 抽取并重写：换行/命中/高度统一走"视觉行"模型，
// 修复长文换行后点按错位、超过 7 行内容被裁掉、chip 文字溢出等问题。

import (
	"fmt"
	"math"
	"strings"

	"win7harness/internal/core"
	"win7harness/internal/ui"
)

const (
	inputPadX     = 14
	inputPadTop   = 10
	inputPadBot   = 10
	inputLineH    = 22
	inputActionH  = 34
	inputMaxLines = 8
	inputBarW     = 5
)

type inputVisLine struct {
	start, end int
	text       string
	width      int32
}

func inputMax32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}

// inputWrapW 换行宽度；绘制/命中/高度共用，保证三者一致。
func (a *App) inputWrapW() int32 {
	w := a.chatW - 32 - inputPadX*2 - 14
	if w < 120 {
		w = 120
	}
	return w
}

// wrapInput 把输入文本按 rune 宽度折成视觉行。
func (a *App) wrapInput() []inputVisLine {
	var lines []inputVisLine
	inner := a.inputWrapW()
	approx := int32(13)
	if a.ui == nil {
		// 无画布：按近似宽度折行（仅用于首帧高度估算）
		per := int(inner / approx)
		if per < 1 {
			per = 1
		}
		start := 0
		for i := 0; i <= len(a.input); i++ {
			if i != len(a.input) && a.input[i] != '\n' {
				continue
			}
			for j := start; j < i; j += per {
				end := j + per
				if end > i {
					end = i
				}
				lines = append(lines, inputVisLine{start: j, end: end, text: string(a.input[j:end]), width: int32(end-j) * approx})
			}
			if i == start {
				lines = append(lines, inputVisLine{start: start, end: i})
			}
			start = i + 1
		}
		return lines
	}
	start := 0
	for i := 0; i <= len(a.input); i++ {
		if i != len(a.input) && a.input[i] != '\n' {
			continue
		}
		if start == i {
			lines = append(lines, inputVisLine{start: i, end: i})
			start = i + 1
			continue
		}
		lineStart := start
		acc := int32(0)
		for j := start; j < i; j++ {
			w := a.ui.MeasureFace(string(a.input[j]), ui.FsBase, 400, ui.FaceUI)
			if w == 0 {
				w = 8
			}
			if acc+w > inner && j > lineStart {
				lines = append(lines, inputVisLine{start: lineStart, end: j, text: string(a.input[lineStart:j]), width: acc})
				lineStart = j
				acc = 0
			}
			acc += w
		}
		lines = append(lines, inputVisLine{start: lineStart, end: i, text: string(a.input[lineStart:i]), width: acc})
		start = i + 1
	}
	return lines
}

// inputTextTop 文本区顶部 y（滚动/命中/插入符共用）
func (a *App) inputTextTop() int32 { return a.inputRect.Y + inputPadTop }

// inputVisibleLines 可视行数（按内高算）
func (a *App) inputVisibleLines() int {
	vis := int((a.inputRect.Bottom() - inputActionH - inputPadBot - a.inputTextTop()) / inputLineH)
	if vis < 1 {
		vis = 1
	}
	return vis
}

// inputHeightBox 卡高（基于视觉行数；预留操作条/内边距）
func (a *App) inputHeightBox() int32 {
	lines := a.wrapInput()
	vis := len(lines)
	if vis < 1 {
		vis = 1
	}
	if vis > inputMaxLines {
		vis = inputMaxLines
	}
	h := int32(vis)*inputLineH + inputPadTop + inputActionH + inputPadBot
	if a.dense() {
		h = int32(vis)*(inputLineH-2) + inputPadTop + inputActionH + inputPadBot
	}
	if len(a.pendingImgs) > 0 {
		h += ThumbH + 12
	}
	return h
}

// clampInputScroll 把滚动夹到合法区间并保证光标行可见（原地更新 a.inputScroll）
func (a *App) clampInputScroll(curLine, total, visible int) int32 {
	scroll := int(a.inputScroll)
	if scroll > total-visible {
		scroll = total - visible
	}
	if scroll < 0 {
		scroll = 0
	}
	for scroll+visible <= curLine {
		scroll++
	}
	for scroll > curLine {
		scroll--
	}
	if scroll > total-visible {
		scroll = total - visible
	}
	if scroll < 0 {
		scroll = 0
	}
	a.inputScroll = int32(scroll)
	return a.inputScroll
}

// inputHitRune 点按位置 → 光标 rune 索引（按视觉行命中，长文换行也准确）
func (a *App) inputHitRune(x, y int32) int {
	if a.ui == nil {
		return a.inputCur
	}
	ty := a.inputTextTop()
	if y < ty {
		return a.inputCur
	}
	lines := a.wrapInput()
	if len(lines) == 0 {
		return 0
	}
	scroll := int(a.inputScroll)
	li := int((y-ty)/inputLineH) + scroll
	if li < 0 {
		li = 0
	}
	if li >= len(lines) {
		li = len(lines) - 1
	}
	ln := lines[li]
	acc := int32(0)
	for i := ln.start; i < ln.end; i++ {
		w := a.ui.MeasureFace(string(a.input[i]), ui.FsBase, 400, ui.FaceUI)
		if acc+w/2 > x-(a.inputRect.X+inputPadX) {
			return i
		}
		acc += w
	}
	return ln.end
}

// colorLum 计算 COLORREF 的相对亮度（0=黑，1=白）。
func colorLum(c uint32) float64 {
	lin := func(v uint32) float64 {
		x := float64(v) / 255
		if x <= 0.03928 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	r := lin(c & 0xFF)
	g := lin((c >> 8) & 0xFF)
	b := lin((c >> 16) & 0xFF)
	return 0.2126*r + 0.7152*g + 0.0722*b
}

// readableOn 保证文字和背景有足够对比度；主题令牌异常时退回黑/白。
func readableOn(fg, bg uint32, min float64) uint32 {
	l1, l2 := colorLum(fg), colorLum(bg)
	if l1 < l2 {
		l1, l2 = l2, l1
	}
	if (l1+0.05)/(l2+0.05) >= min {
		return fg
	}
	if colorLum(bg) > 0.5 {
		return 0x000000
	}
	return 0x00FFFFFF
}

// paintInputBox 绘制整个输入卡。（从 app.go paintInput 抽取并重写）
func (a *App) paintInputBox(u *UI, x, y, w, H int32) {
	c := u.c
	th := a.th
	inputText := readableOn(th.Text, th.Card, 4.5)
	inputHint := readableOn(th.TextMute, th.Card, 3.0)

	h := a.inputHeight()
	r := ui.R(x+16, y+10, w-32, h-20)
	a.inputRect = r

	// 浮起：暖色外层 + 卡面 + 焦点描边
	const radius = 12
	c.Round(ui.R(r.X+2, r.Y+3, r.W-4, r.H-2), radius, ui.Blend(th.Surface, th.Border, 0.40))
	c.Round(r, radius, th.Card)
	border := th.BorderSoft
	if u.Focus() == "input" {
		border = th.InputFocus
	}
	c.RoundStroke(r, radius, border)
	if u.Focus() == "input" {
		fx := r.Inset(-2)
		c.RoundStroke(fx, radius+1, th.InputFocus)
	}
	u.Hit("input", ui.R(r.X, r.Y, r.W, r.Bottom()-inputActionH))

	textY := r.Y + inputPadTop

	// 附件缩略图（点击移除）
	a.chipRects = nil
	if len(a.pendingImgs) > 0 {
		rects := a.pendingImageRects(r.X+inputPadX, r.Y+10)
		a.chipRects = rects
		for i, rc := range rects {
			d := imgDib(a.pendingImgs[i])
			if d != nil {
				c.DrawImg(d, rc)
			}
			c.RoundStroke(rc, 6, th.InputFocus)
			cx, cy := rc.Right()-6, rc.Y+6
			c.Round(ui.R(cx-6, cy-6, 12, 12), 6, th.Danger)
			drawIcon(c, "close", ui.R(cx-3, cy-3, 7, 7), 0xFFFFFF)
		}
		textY += ThumbH + 12
	}

	// 文本区几何
	lineH := int32(inputLineH)
	if a.dense() {
		lineH = int32(inputLineH - 2)
	}
	textH := r.Bottom() - inputActionH - inputPadBot - textY
	visible := int(textH / lineH)
	if visible < 1 {
		visible = 1
	}
	a.inputTy = textY
	a.inputLineH = lineH
	a.curCaretX, a.curCaretY = r.X+inputPadX, textY

	lines := a.wrapInput()
	if len(lines) == 0 {
		lines = []inputVisLine{{start: 0, end: 0}}
	}
	total := len(lines)

	// 光标所在视觉行
	curLine := -1
	for i, ln := range lines {
		if a.inputCur >= ln.start && a.inputCur <= ln.end {
			curLine = i
			break
		}
	}
	if curLine < 0 {
		curLine = len(lines) - 1
	}
	// 滚动：编辑后自动跟随光标；滚轮手动滚动则只夹范围不抢回
	if a.inputPinScroll {
		if int(a.inputScroll) > total-visible {
			a.inputScroll = int32(total - visible)
		}
		if a.inputScroll < 0 {
			a.inputScroll = 0
		}
	} else {
		a.clampInputScroll(curLine, total, visible)
	}
	scroll := a.inputScroll

	// 文本区裁剪
	textArea := ui.R(r.X+2, textY, r.W-4, textH)
	c.Clip(textArea)

	// 选区高亮
	if lo, hi := a.selRange(); lo >= 0 {
		for i := int(scroll); i < int(scroll)+visible && i < len(lines); i++ {
			ln := lines[i]
			s0, s1 := lo, hi
			if s0 < ln.start {
				s0 = ln.start
			}
			if s1 > ln.end {
				s1 = ln.end
			}
			if s1 <= s0 {
				continue
			}
			w0 := a.textWidth(ln.text[:s0-ln.start])
			w1 := a.textWidth(ln.text[:s1-ln.start])
			yy := textY + int32(i-int(scroll))*lineH
			c.Round(ui.R(r.X+inputPadX+w0, yy-2, w1-w0+2, lineH), 3, th.SelectBg)
		}
	}

	// 文本
	if len(a.input) == 0 {
		c.TextFace(r.X+inputPadX, textY, "描述你要做的事，或粘贴报错信息…", ui.FsBase, 400, inputHint, ui.FaceUI)
	} else {
		for i := int(scroll); i < int(scroll)+visible && i < len(lines); i++ {
			ln := lines[i]
			yy := textY + int32(i-int(scroll))*lineH
			c.TextFace(r.X+inputPadX, yy, ln.text, ui.FsBase, 400, inputText, ui.FaceUI)
		}
		ln := lines[curLine]
		off := a.textWidth(string(a.input[ln.start:a.inputCur]))
		a.curCaretX = r.X + inputPadX + off
		a.curCaretY = textY + int32(curLine-int(scroll))*lineH
	}
	c.PopClip()

	// 滚动条（仅溢出时）
	if total > visible {
		track := ui.R(r.Right()-8, textY-1, inputBarW, textH+2)
		c.Round(track, 2, ui.Blend(th.BorderSoft, th.TextMute, 0.25))
		thumbH := track.H * int32(visible) / int32(total)
		if thumbH < 16 {
			thumbH = 16
		}
		maxScroll := int32(total - visible)
		thumbY := track.Y
		if maxScroll > 0 {
			thumbY += (track.H - thumbH) * scroll / maxScroll
		}
		c.Round(ui.R(track.X, thumbY, track.W, thumbH), 2, th.TextMute)
	}

	// 插入符
	if u.Focus() == "input" && blinkOn(a.phase) {
		cx := a.curCaretX
		if cx < r.X+4 {
			cx = r.X + 4
		}
		if cx > r.Right()-40 {
			cx = r.Right() - 40
		}
		c.VLine(cx, a.curCaretY+2, lineH-5, th.Accent)
	}

	// ---------- 操作条 ----------
	actionY := r.Bottom() - inputActionH

	// 左：上下文提示
	hint, hintCol := a.inputHint()
	if maxW := r.Right() - r.X - inputPadX - 190; maxW > 60 {
		hint = ellipsis(c, hint, ui.FsXs, 400, maxW)
		c.TextFace(r.X+inputPadX, actionY+9, hint, ui.FsXs, 400, hintCol, ui.FaceUI)
	}

	// 右：从右往左排布
	right := r.Right() - 10
	sby := actionY + 3
	canSend := strings.TrimSpace(string(a.input)) != "" || len(a.pendingImgs) > 0
	streaming := a.eng != nil && a.streaming

	// 发送 / 停止（圆形 accent 实心钮）
	sendR := ui.R(right-34, sby, 34, 28)
	u.Hit("input.send", sendR)
	drawSendButton(c, th, sendR, canSend, streaming, u.hoverT("input.send"), a.isSendPressed(u))
	right -= 34 + 6

	// 点击派发（此处集中；streaming → 停止 / 可发送 → 发送）
	if u.Clicked("input.send") {
		switch {
		case streaming:
			a.stopGeneration()
		case canSend:
			a.send()
		}
	}

	// 命令（/）
	slashR := ui.R(right-26, sby, 26, 28)
	if u.IconButton("input.cmd", slashR, "ico:slash", false) {
	}
	right -= 26 + 4
	// 截图
	snipR := ui.R(right-26, sby, 26, 28)
	if u.IconButton("input.snip", snipR, "ico:camera", false) {
	}
	right -= 26 + 4
	// 附件（图片）
	imgR := ui.R(right-26, sby, 26, 28)
	if u.IconButton("input.img", imgR, "ico:image", false) {
	}
	right -= 26 + 10

	// 模型 chip（空间足够时）
	if a.eng != nil {
		model := a.convModel()
		if strings.TrimSpace(model) == "" {
			model = "未设置模型"
		}
		maxChipW := right - r.X - inputPadX
		if maxChipW > 92 {
			label := ellipsis(c, model, ui.FsXs, 500, maxInt32(maxChipW-inputChipMetaW, 64))
			mw := c.MeasureFace(label, ui.FsXs, 500, ui.FaceUI)
			chipW := mw + 40 // 图标12 + 左内缩8 + caret 14 + 右内缩8
			mc := ui.R(right-chipW, sby+2, chipW, 26)
			if u.Hit("input.model", mc) {
				c.Round(mc, 13, ui.Blend(th.Surface, th.Accent, 0.10))
			}
			c.RoundStroke(mc, 13, th.BorderSoft)
			drawIcon(c, "model", ui.R(mc.X+8, mc.Y+6, 13, 13), th.TextMute)
			c.TextFace(mc.X+24, mc.CY()-7, label, ui.FsXs, 500, th.TextDim, ui.FaceUI)
			drawIcon(c, "caret-down", ui.R(mc.Right()-18, mc.Y+8, 12, 12), th.TextMute)
			if u.Clicked("input.model") {
				a.editReset()
				a.openModelMenu(mc, true)
			}
			right = mc.X - 8
		}
	}

	// 成本 chip（有用量时显示）
	if a.eng != nil && (a.cfg == nil || a.cfg.CostBoard) {
		snap := a.eng.CostSnapshot()
		if snap.Turns > 0 {
			txt := fmt.Sprintf("%s · 缓存 %d%%", core.Money(snap.Cost), int(snap.HitRate*100+0.5))
			tw := c.MeasureFace(txt, ui.FsXs, 500, ui.FaceUI) + 34
			if right-tw > r.X+inputPadX+80 {
				cc := ui.R(right-tw, sby+2, tw, 26)
				if u.Hit("input.cost", cc) {
					c.Round(cc, 13, ui.Blend(th.Surface, th.Accent, 0.10))
				}
				c.RoundStroke(cc, 13, th.BorderSoft)
				drawIcon(c, "coin", ui.R(cc.X+8, cc.Y+7, 13, 13), th.TextMute)
				c.TextFace(cc.X+25, cc.CY()-7, txt, ui.FsXs, 500, th.TextDim, ui.FaceUI)
				if u.Clicked("input.cost") {
					a.sysNote(a.eng.CostBoard())
				}
				right = cc.X - 8
			}
		}
	}

	// 补全浮层（斜杠/文件引用）
	a.paintComplete(u, x, y, w)
}

const inputChipMetaW = 32

func maxInt32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}

// isSendPressed 报告发送/停止按钮处于按下态（press 未松开且同按钮）。
func (a *App) isSendPressed(u *UI) bool {
	pid, pbtn := u.Pressed()
	return pid == "input.send" && pbtn == 0
}

// inputHint 左侧上下文提示（随状态变化）。
func (a *App) inputHint() (string, uint32) {
	th := a.th
	if a.eng != nil {
		switch {
		case a.streaming:
			return "Esc 停止生成", th.Accent
		case a.eng.PlanMode:
			return "计划模式 · 只读（/plan 退出）", th.Accent
		}
		if a.eng.CurrentRound() > 1 || a.eng.CompactCount() > 0 {
			return fmt.Sprintf("第 %d 轮工具调用 · 已压缩 %d 次 · Esc 停止", a.eng.CurrentRound(), a.eng.CompactCount()), th.Accent
		}
	}
	if len(a.pendingImgs) > 0 {
		return fmt.Sprintf("已附 %d 张图 · Enter 发送", len(a.pendingImgs)), th.Accent
	}
	return "Enter 发送 · Shift+Enter 换行 · / 命令", th.TextMute
}

// drawSendButton 发送/停止按钮（圆形实心）。
func drawSendButton(c *ui.Canvas, th *ui.Theme, r ui.Rect, canSend, streaming bool, hover float32, pressed bool) {
	bg := th.Border
	fg := th.TextMute
	if streaming {
		bg = th.Danger
		fg = 0xFFFFFF
	} else if canSend {
		bg = th.Accent
		fg = 0xFFFFFF
		if hover > 0 {
			bg = ui.Blend(th.Accent, 0xFFFFFF, 0.10*hover)
		}
		if pressed {
			bg = ui.Blend(th.Accent, 0x000000, 0.12)
		}
	} else if hover > 0 {
		bg = ui.Blend(th.Border, th.Text, 0.07+0.05*hover)
	}
	c.Round(r, r.H/2, bg)
	if streaming {
		drawIcon(c, "stop", ui.R(r.X+9, r.Y+7, 16, 16), fg)
		return
	}
	drawIcon(c, "up", ui.R(r.X+9, r.Y+7, 16, 16), fg)
}

// textWidth 测量文本像素宽度（与 FaceUI/FsBase 同源）
func (a *App) textWidth(s string) int32 {
	if a.ui == nil {
		return int32(len([]rune(s))) * 13
	}
	return a.ui.MeasureFace(s, ui.FsBase, 400, ui.FaceUI)
}
