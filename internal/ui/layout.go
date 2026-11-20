//go:build windows

package ui

import (
	"strings"
)

// Run 一段同一样式的文本片段。
type Run struct {
	X     int32
	Text  string
	Bold  bool
	Mono  bool
	Dim   bool
	Color uint32
	Size  int32
}

// VisLine 一条已排版的可视行。
type VisLine struct {
	Kind int // 0 普通, 1 代码, 2 分隔线, 3 代码块首行, 4 代码块末行
	// Block 是产生该行的 Block 在 ParseMarkdown 结果里的下标，-1 表示未知。
	// 供上层按块定位代码块范围（复制/保存按钮），不参与绘制。
	Block  int
	X, Y   int32
	H      int32
	Runs   []Run
	Bg     uint32
	Indent int32
}

// IsCode 报告该行属于代码块（DrawLines 会为其留 12px 左内边距）。
// 导出给 app 层做命中区坐标换算，避免把 lineCode* 私有常量散落到别的包。
func (ln VisLine) IsCode() bool {
	return ln.Kind == lineCode || ln.Kind == lineCodeFirst || ln.Kind == lineCodeLast
}

const (
	lineText = iota
	lineCode
	lineHr
	lineCodeFirst
	lineCodeLast
	lineQuote // 引用行：左侧画竖线
	lineTable // 表格行：可带表头底色
)

type charKey struct {
	ch   rune
	size int32
	bold bool
	mono bool
}

// charWidths 字符宽度缓存，避免流式渲染时反复调用 GDI 测量。
var charWidths = map[charKey]int32{}

func (c *Canvas) measureRune(ch rune, size int32, bold, mono bool) int32 {
	key := charKey{ch, size, bold, mono}
	if w, ok := charWidths[key]; ok {
		return w
	}
	face := FaceUI
	w := bold
	if mono {
		face = FaceMono
		w = false
	}
	weight := int32(400)
	if w {
		weight = 700
	}
	str := string(ch)
	width := int32(0)
	if ch == ' ' {
		// 空格用固定比例，避免个别字体返回 0
		width = c.MeasureFace("a a", size, weight, face) - c.MeasureFace("aa", size, weight, face)
		if width <= 0 {
			width = size / 3
		}
	} else {
		width = c.MeasureFace(str, size, weight, face)
	}
	charWidths[key] = width
	return width
}

// stringWidth 计算字符串宽度。
func (c *Canvas) stringWidth(s string, size int32, bold, mono bool) int32 {
	var w int32
	for _, r := range s {
		w += c.measureRune(r, size, bold, mono)
	}
	return w
}

// token 换行单位。
type token struct {
	text string
	bold bool
	mono bool
	dim  bool
	spc  bool // 是否为可断行空格
}

func spansToTokens(spans []Span) []token {
	var out []token
	for _, sp := range spans {
		// 代码片段整体不可断行，普通文本按中英文切分
		if sp.Code {
			out = append(out, token{text: sp.Text, bold: sp.Bold, mono: true, dim: sp.Dim})
			continue
		}
		var cur strings.Builder
		flushWord := func() {
			if cur.Len() > 0 {
				out = append(out, token{text: cur.String(), bold: sp.Bold, dim: sp.Dim})
				cur.Reset()
			}
		}
		for _, r := range sp.Text {
			if r == ' ' {
				flushWord()
				out = append(out, token{text: " ", spc: true, bold: sp.Bold, dim: sp.Dim})
			} else if r < 128 {
				cur.WriteRune(r)
			} else {
				flushWord()
				out = append(out, token{text: string(r), bold: sp.Bold, dim: sp.Dim})
			}
		}
		flushWord()
	}
	return out
}

// LayoutMarkdown 把块序列排版为可视行，width 为可用宽度，size 为基础字号。
func LayoutMarkdown(c *Canvas, blocks []Block, width, size int32, th *Theme) []VisLine {
	var lines []VisLine
	y := int32(0)
	paraGap := int32(9)
	curBlock := -1

	emit := func(runs []Run, h int32) {
		lines = append(lines, VisLine{Kind: lineText, Block: curBlock, X: 0, Y: y, H: h, Runs: runs})
		y += h
	}

	for bi, b := range blocks {
		curBlock = bi
		switch b.Kind {
		case BlockHeading:
			sz := size + 3
			if b.Level == 1 {
				sz = size + 7
			} else if b.Level == 2 {
				sz = size + 4
			} else if b.Level >= 4 {
				sz = size + 1
			}
			wrapRuns := c.wrapRuns(b.Spans, width, sz, th.Text)
			for i, r := range wrapRuns {
				// 标题加粗
				for j := range r {
					r[j].Bold = true
					r[j].Size = sz
					r[j].Color = th.Text
				}
				emit(r, sz*3/2)
				_ = i
			}
			y += paraGap / 2

		case BlockPara:
			// 逐行排版：段落里的软换行是作者意图，不能被合并成一行。
			for _, one := range blockLines(b) {
				spans := b.Spans
				if len(b.Lines) > 0 {
					spans = ParseInline(one)
				}
				for _, r := range c.wrapRuns(spans, width, size, th.Text) {
					emit(r, size*3/2)
				}
			}
			y += paraGap

		case BlockBullet, BlockNumber, BlockTodo:
			indent := int32(18)
			prefix := "•  "
			if b.Kind == BlockNumber {
				prefix = itoa(int32(b.Index)) + ".  "
			}
			spans := append([]Span{{Text: prefix, Bold: true}}, b.Spans...)
			wrapRuns := c.wrapRunsIndent(spans, width-indent, size, th.Text, indent, prefix)
			for i, r := range wrapRuns {
				if i == 0 && b.Kind == BlockTodo {
					// 任务框标记
					done := b.Level == 1
					box := "☑  "
					if !done {
						box = "☐  "
					}
					r = append([]Run{{X: 0, Text: box, Size: size, Color: th.Accent}}, r...)
				}
				emit(r, size*3/2)
			}
			y += 3

		case BlockQuote:
			for _, one := range blockLines(b) {
				spans := b.Spans
				if len(b.Lines) > 0 {
					spans = ParseInline(one)
				}
				for _, r := range c.wrapRuns(spans, width-20, size, th.TextDim) {
					for j := range r {
						r[j].X += 20
						r[j].Color = th.TextDim
					}
					lines = append(lines, VisLine{Kind: lineQuote, Block: curBlock, X: 0, Y: y, H: size * 3 / 2, Runs: r})
					y += size * 3 / 2
				}
			}
			y += 6

		case BlockTable:
			tblLines, h := c.layoutTable(b, width, size, th, y)
			for k := range tblLines {
				tblLines[k].Block = curBlock
			}
			lines = append(lines, tblLines...)
			y += h + paraGap

		case BlockHr:
			lines = append(lines, VisLine{Kind: lineHr, Block: curBlock, X: 0, Y: y + 6, H: 12})
			y += 12

		case BlockCode:
			codeSize := size - 1
			if codeSize < 11 {
				codeSize = 11
			}
			lh := codeSize * 3 / 2
			pad := int32(8)
			if len(b.Lines) == 0 {
				b.Lines = []string{}
			}
			labelW := int32(0)
			if b.Lang != "" {
				labelW = c.stringWidth(b.Lang, codeSize-1, false, false)
			}
			for i, src := range b.Lines {
				runs := highlightLine(c, src, codeSize, th, pad+6)
				kind := lineCode
				if i == 0 {
					kind = lineCodeFirst
				}
				if i == len(b.Lines)-1 {
					if kind == lineCodeFirst {
						kind = lineCodeFirst // 单行代码块
					} else {
						kind = lineCodeLast
					}
				}
				if i == 0 && b.Lang != "" {
					runs = append(runs, Run{
						X: width - labelW - 10, Text: b.Lang, Size: codeSize - 1,
						Color: th.TextMute,
					})
				}
				lines = append(lines, VisLine{
					Kind: kind, Block: curBlock, X: 0, Y: y, H: lh, Runs: runs, Bg: th.CodeBg,
				})
				y += lh
			}
			y += paraGap
		}
	}
	return lines
}

// wrapRuns 把行内片段排成多行。
func (c *Canvas) wrapRuns(spans []Span, width, size int32, color uint32) [][]Run {
	return c.wrapRunsIndent(spans, width, size, color, 0, "")
}

func (c *Canvas) wrapRunsIndent(spans []Span, width, size int32, color uint32, indent int32, prefix string) [][]Run {
	tokens := spansToTokens(spans)
	var lines [][]Run
	var curLine []Run
	var curX int32
	first := true

	pushLine := func() {
		lines = append(lines, curLine)
		curLine = nil
		curX = indent
		first = false
	}
	curX = indent

	for _, tk := range tokens {
		sz := size
		mono := tk.mono
		if mono {
			sz = size - 1
		}
		w := c.stringWidth(tk.text, sz, tk.bold, mono)
		if curX+w > width+indent && curX > 0 && !(tk.spc && len(curLine) == 0) {
			pushLine()
			if tk.spc {
				continue
			}
		}
		if tk.spc && len(curLine) == 0 {
			continue
		}
		clr := color
		if tk.mono {
			clr = Blend(color, 0xD0C088, 0.55)
		}
		if tk.dim {
			clr = Blend(color, 0x808080, 0.6)
		}
		curLine = append(curLine, Run{X: curX, Text: tk.text, Bold: tk.bold, Mono: mono, Size: sz, Color: clr, Dim: tk.dim})
		curX += w
	}
	if len(curLine) > 0 || len(lines) == 0 {
		lines = append(lines, curLine)
	}
	_ = first
	_ = prefix
	return lines
}

// DrawLines 绘制可视行，top 为 Y 偏移，clip 为裁剪区。
func DrawLines(c *Canvas, lines []VisLine, ox, oy int32, clip Rect, th *Theme) {
	c.Clip(clip)
	defer c.PopClip()
	for _, ln := range lines {
		yy := oy + ln.Y
		if yy > clip.Bottom()+40 {
			continue
		}
		if yy+ln.H < clip.Y-40 {
			continue
		}
		if ln.Bg != 0 {
			c.FillRect(Rect{X: ox, Y: yy, W: clip.W, H: ln.H}, ln.Bg)
		}
		if ln.Kind == lineQuote {
			c.FillRect(Rect{X: ox + 3, Y: yy + 3, W: 2, H: ln.H - 6}, th.Border)
		}
		switch ln.Kind {
		case lineHr:
			c.HLine(ox+6, yy+6, clip.W-24, th.Border)
			continue
		case lineCode, lineCodeFirst, lineCodeLast:
			r := Rect{X: ox, Y: yy, W: clip.W, H: ln.H}
			c.FillRect(r, ln.Bg)
			// 代码块只用"左侧一条强调色 + 上下收边"，不再四面描 1px 灰框。
			// 四边框会把代码块切得像表格单元格，整页看起来又碎又廉价；
			// 一条 3px 的竖条既能界定范围，又能和正文拉开层次。
			c.FillRect(Rect{X: ox, Y: yy, W: 3, H: ln.H}, th.CodeBar)
			if ln.Kind == lineCodeFirst {
				c.HLine(ox+3, yy, clip.W-3, th.CodeBorder)
			}
			if ln.Kind == lineCodeLast {
				c.HLine(ox+3, yy+ln.H-1, clip.W-3, th.CodeBorder)
			}
		}
		for _, run := range ln.Runs {
			if run.Text == "" {
				continue
			}
			face := FaceUI
			if run.Mono {
				face = FaceMono
			}
			weight := int32(400)
			if run.Bold {
				weight = 700
			}
			// 垂直居中
			ty := yy + (ln.H-run.Size)/2 - 2
			// 代码行左侧留出内边距，否则字紧贴着强调条，挤得慌。
			pad := int32(0)
			if ln.Kind == lineCode || ln.Kind == lineCodeFirst || ln.Kind == lineCodeLast {
				pad = 12
			}
			c.TextFace(ox+run.X+pad, ty, run.Text, run.Size, weight, run.Color, face)
		}
	}
}

// LayoutHeight 返回排版总高度。
func LayoutHeight(lines []VisLine) int32 {
	if len(lines) == 0 {
		return 0
	}
	last := lines[len(lines)-1]
	return last.Y + last.H
}

func itoa(n int32) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}

// blockLines 返回块的逐行文本；老调用方可能只填了 Spans，这里给出兼容回退。
func blockLines(b Block) []string {
	if len(b.Lines) > 0 {
		return b.Lines
	}
	var sb strings.Builder
	for _, sp := range b.Spans {
		sb.WriteString(sp.Text)
	}
	return []string{sb.String()}
}

// spanWidth 计算一组行内片段的自然宽度。
func spanWidth(c *Canvas, spans []Span, size int32) int32 {
	var w int32
	for _, sp := range spans {
		sz := size
		if sp.Code {
			sz = size - 1
		}
		w += c.stringWidth(sp.Text, sz, sp.Bold, sp.Code)
	}
	return w
}

// layoutTable 把表格块排版成若干可视行：表头带底色，列宽按内容自然宽度分配，
// 总宽超出可用区时等比压缩。表格是模型最常输出的结构，必须真正画出来。
func (c *Canvas) layoutTable(b Block, width, size int32, th *Theme, y int32) ([]VisLine, int32) {
	if len(b.Cells) == 0 {
		return nil, 0
	}
	cols := 0
	for _, row := range b.Cells {
		if len(row) > cols {
			cols = len(row)
		}
	}
	if cols == 0 {
		return nil, 0
	}
	pad := int32(16)
	nat := make([]int32, cols)
	for _, row := range b.Cells {
		for ci := 0; ci < cols && ci < len(row); ci++ {
			w := spanWidth(c, row[ci].Spans, size)
			if w > nat[ci] {
				nat[ci] = w
			}
		}
	}
	var total int32
	for i := range nat {
		nat[i] += pad
		if nat[i] < 64 {
			nat[i] = 64
		}
		total += nat[i]
	}
	avail := width - 4
	if total > avail && total > 0 {
		var sum int32
		for i := range nat {
			nat[i] = nat[i] * avail / total
			if nat[i] < 44 {
				nat[i] = 44
			}
			sum += nat[i]
		}
		total = sum
	}
	rowH := size*3/2 + 12
	var out []VisLine
	yy := y
	for ri, row := range b.Cells {
		var runs []Run
		var x int32
		for ci := 0; ci < cols; ci++ {
			var spans []Span
			if ci < len(row) {
				spans = row[ci].Spans
			}
			clr := th.TextDim
			bold := false
			if ri == 0 {
				clr = th.Text
				bold = true
			}
			align := 0
			if ci < len(b.Aligns) {
				align = b.Aligns[ci]
			}
			runs = append(runs, c.cellRuns(spans, x+pad/2, nat[ci]-pad, size, clr, bold, align)...)
			x += nat[ci]
		}
		ln := VisLine{Kind: lineTable, X: 0, Y: yy, H: rowH, Runs: runs}
		if ri == 0 {
			ln.Bg = th.Surface2
		}
		out = append(out, ln)
		yy += rowH
		out = append(out, VisLine{Kind: lineHr, X: 0, Y: yy, H: 1})
		yy += 1
	}
	return out, yy - y
}

// cellRuns 把单元格内容排成单行，超出列宽时按字符截断并补省略号。
func (c *Canvas) cellRuns(spans []Span, x, maxW, size int32, color uint32, bold bool, align int) []Run {
	type glyph struct {
		s    string
		mono bool
		dim  bool
	}
	var gs []glyph
	for _, sp := range spans {
		b := sp.Bold || bold
		_ = b
		for _, r := range sp.Text {
			gs = append(gs, glyph{s: string(r), mono: sp.Code, dim: sp.Dim})
		}
	}
	sizeOf := func(mono bool) int32 {
		if mono {
			return size - 1
		}
		return size
	}
	var w int32
	lim := len(gs)
	for i, g := range gs {
		cw := c.stringWidth(g.s, sizeOf(g.mono), bold, g.mono)
		if w+cw > maxW && i > 0 {
			lim = i
			break
		}
		w += cw
	}
	truncated := lim < len(gs)
	if truncated && lim > 0 {
		lim--
	}
	var runs []Run
	var cx int32
	for _, g := range gs[:lim] {
		sz := sizeOf(g.mono)
		cw := c.stringWidth(g.s, sz, bold, g.mono)
		clr := color
		if g.dim {
			clr = Blend(color, 0x808080, 0.6)
		}
		if n := len(runs); n > 0 && runs[n-1].Mono == g.mono && runs[n-1].Color == clr && runs[n-1].Bold == bold && runs[n-1].Size == sz {
			runs[n-1].Text += g.s
		} else {
			runs = append(runs, Run{X: cx, Text: g.s, Bold: bold, Mono: g.mono, Size: sz, Color: clr})
		}
		cx += cw
	}
	if truncated {
		runs = append(runs, Run{X: cx, Text: "…", Bold: bold, Size: size, Color: color})
		cx += c.stringWidth("…", size, bold, false)
	}
	off := x
	if align == 1 {
		off = x + (maxW-cx)/2
	} else if align == 2 {
		off = x + maxW - cx
	}
	for i := range runs {
		runs[i].X += off
	}
	return runs
}

// LineWidth 返回一条已排版行的实际像素宽度（用于把气泡收紧到内容宽度）。
func LineWidth(c *Canvas, ln VisLine) int32 {
	var w int32
	for _, r := range ln.Runs {
		weight := int32(400)
		if r.Bold {
			weight = 700
		}
		face := FaceUI
		if r.Mono {
			face = FaceMono
		}
		if x := r.X + c.MeasureFace(r.Text, r.Size, weight, face); x > w {
			w = x
		}
	}
	return w
}
