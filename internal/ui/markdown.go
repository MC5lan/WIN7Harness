package ui

import "strings"

// BlockKind 富文本块类型。
type BlockKind int

const (
	BlockPara BlockKind = iota
	BlockHeading
	BlockCode
	BlockBullet
	BlockNumber
	BlockQuote
	BlockHr
	BlockTodo
	BlockTable
)

// Span 行内样式片段。
type Span struct {
	Text string
	Bold bool
	Code bool
	Dim  bool
}

// Block 一个排版单元。
type Block struct {
	Kind  BlockKind
	Level int    // 标题级别 1-6
	Lang  string // 代码块语言
	// Lines 代码块的原始行，或段落 / 引用的软换行行。
	// 保留逐行信息是必要的：命令输出、统计报表、地址块都靠换行表达结构，
	// 早先把同段多行用一个空格拼成一行，导致 /stats、/help 在界面上糊成一坨。
	Lines  []string
	Spans  []Span
	Index  int           // 列表序号
	Cells  [][]TableCell // 表格：行 × 列
	Aligns []int         // 表格列对齐：0 左 1 中 2 右
}

// TableCell 一个表格单元格，保留行内样式（粗体、行内代码）。
type TableCell struct {
	Spans []Span
}

// ParseMarkdown 解析为块序列（覆盖大模型常用输出子集，容错流式不完整输入）。
func ParseMarkdown(text string) []Block {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	rawLines := strings.Split(text, "\n")

	var blocks []Block
	var para []string
	var quote []string
	var code []string
	var inCode bool
	var codeLang string
	orderIdx := 0

	flushQuote := func() {
		if len(quote) == 0 {
			return
		}
		blocks = append(blocks, Block{Kind: BlockQuote, Lines: append([]string{}, quote...)})
		quote = quote[:0]
	}

	flushPara := func() {
		if len(para) == 0 {
			return
		}
		lines := append([]string{}, para...)
		para = para[:0]
		blocks = append(blocks, Block{
			Kind:  BlockPara,
			Lines: lines,
			Spans: ParseInline(strings.Join(lines, " ")),
		})
	}

	// flushAll 按"段落优先、引用其次"的顺序落块；解析过程中两者不会同时非空。
	flushAll := func() {
		flushPara()
		flushQuote()
	}

	for i := 0; i < len(rawLines); i++ {
		ln := rawLines[i]
		trim := strings.TrimSpace(ln)

		// 代码围栏
		if strings.HasPrefix(trim, "```") {
			if inCode {
				blocks = append(blocks, Block{Kind: BlockCode, Lang: codeLang, Lines: append([]string{}, code...)})
				code = code[:0]
				codeLang = ""
				inCode = false
			} else {
				flushAll()
				inCode = true
				codeLang = strings.TrimSpace(strings.TrimPrefix(trim, "```"))
			}
			continue
		}
		if inCode {
			code = append(code, ln)
			continue
		}

		if trim == "" {
			flushAll()
			orderIdx = 0
			continue
		}

		// 表格：当前行含竖线，且下一行是 |---|---| 分隔行
		if strings.Contains(trim, "|") && i+1 < len(rawLines) && isTableSep(strings.TrimSpace(rawLines[i+1])) {
			flushAll()
			header := splitTableRow(trim)
			aligns := parseTableAligns(strings.TrimSpace(rawLines[i+1]))
			rows := [][]TableCell{header}
			j := i + 2
			for ; j < len(rawLines); j++ {
				t := strings.TrimSpace(rawLines[j])
				if t == "" || !strings.Contains(t, "|") {
					break
				}
				rows = append(rows, splitTableRow(t))
			}
			// 列数对齐到表头列数，缺的补空、多的截断
			cols := len(header)
			if cols > 0 {
				for ri := range rows {
					for len(rows[ri]) < cols {
						rows[ri] = append(rows[ri], TableCell{})
					}
					if len(rows[ri]) > cols {
						rows[ri] = rows[ri][:cols]
					}
				}
				blocks = append(blocks, Block{Kind: BlockTable, Cells: rows, Aligns: aligns})
			}
			i = j - 1
			continue
		}

		// 分割线
		if trim == "---" || trim == "***" || trim == "___" {
			flushAll()
			blocks = append(blocks, Block{Kind: BlockHr})
			continue
		}

		// 标题
		if strings.HasPrefix(trim, "#") {
			lvl := 0
			for lvl < len(trim) && trim[lvl] == '#' {
				lvl++
			}
			if lvl >= 1 && lvl <= 6 && lvl < len(trim) && trim[lvl] == ' ' {
				flushAll()
				blocks = append(blocks, Block{Kind: BlockHeading, Level: lvl, Spans: ParseInline(strings.TrimSpace(trim[lvl:]))})
				continue
			}
		}

		// 引用（连续的 > 行合并成一个块，避免多行引用被拆成多条竖线）
		if strings.HasPrefix(trim, "> ") || trim == ">" {
			flushPara()
			quote = append(quote, strings.TrimSpace(strings.TrimPrefix(trim, ">")))
			continue
		}
		flushQuote()

		// 任务列表
		if strings.HasPrefix(trim, "- [ ] ") || strings.HasPrefix(trim, "- [x] ") || strings.HasPrefix(trim, "- [X] ") {
			flushAll()
			done := trim[3] == 'x' || trim[3] == 'X'
			blocks = append(blocks, Block{Kind: BlockTodo, Level: b2i(done), Spans: ParseInline(strings.TrimSpace(trim[6:]))})
			continue
		}

		// 无序列表
		if strings.HasPrefix(trim, "- ") || strings.HasPrefix(trim, "* ") || strings.HasPrefix(trim, "+ ") {
			flushAll()
			blocks = append(blocks, Block{Kind: BlockBullet, Spans: ParseInline(strings.TrimSpace(trim[2:]))})
			continue
		}

		// 有序列表
		if n := orderedPrefix(trim); n > 0 {
			flushAll()
			orderIdx++
			blocks = append(blocks, Block{Kind: BlockNumber, Index: orderIdx, Spans: ParseInline(strings.TrimSpace(trim[n:]))})
			continue
		} else {
			orderIdx = 0
		}

		para = append(para, strings.TrimRight(ln, " \t"))
	}

	if inCode {
		// 流式未闭合：按代码块渲染，避免闪烁
		blocks = append(blocks, Block{Kind: BlockCode, Lang: codeLang, Lines: append([]string{}, code...)})
	}
	flushAll()
	return blocks
}

// isTableSep 判断是否为表格分隔行，如 |---|:--:|---|。
func isTableSep(s string) bool {
	if !strings.Contains(s, "-") || !strings.Contains(s, "|") {
		return false
	}
	for _, r := range s {
		switch r {
		case '-', '|', ':', ' ', '\t':
		default:
			return false
		}
	}
	return true
}

// splitTableRow 把一行表格文本切成单元格行内片段。
func splitTableRow(s string) []TableCell {
	s = strings.TrimSpace(s)
	// 转义的竖线先换成占位符，避免被当成分隔符
	s = strings.ReplaceAll(s, `\|`, "\u0001")
	s = strings.TrimPrefix(s, "|")
	s = strings.TrimSuffix(s, "|")
	parts := strings.Split(s, "|")
	out := make([]TableCell, 0, len(parts))
	for _, p := range parts {
		cell := strings.ReplaceAll(strings.TrimSpace(p), "\u0001", "|")
		spans := ParseInline(cell)
		if len(spans) == 0 {
			spans = []Span{{Text: ""}}
		}
		out = append(out, TableCell{Spans: spans})
	}
	return out
}

// parseTableAligns 解析分隔行里的对齐标记。
func parseTableAligns(s string) []int {
	s = strings.TrimPrefix(strings.TrimSuffix(strings.TrimSpace(s), "|"), "|")
	parts := strings.Split(s, "|")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		left := strings.HasPrefix(p, ":")
		right := strings.HasSuffix(p, ":")
		switch {
		case left && right:
			out = append(out, 1)
		case right:
			out = append(out, 2)
		default:
			out = append(out, 0)
		}
	}
	return out
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// orderedPrefix 返回 "1. " 形式的序号长度，无则返回 0。
func orderedPrefix(s string) int {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i > 0 && i+1 < len(s) && (s[i] == '.' || s[i] == ')') && s[i+1] == ' ' {
		return i + 2
	}
	return 0
}

// ParseInline 解析 **粗体**、`代码`、~~删除~~ 等行内标记。
func ParseInline(s string) []Span {
	var spans []Span
	var buf strings.Builder

	flush := func(bold, code, dim bool) {
		if buf.Len() == 0 {
			return
		}
		spans = append(spans, Span{Text: buf.String(), Bold: bold, Code: code, Dim: dim})
		buf.Reset()
	}

	i := 0
	bold, code, dim := false, false, false
	for i < len(s) {
		if s[i] == '`' {
			flush(bold, code, dim)
			code = !code
			i++
			continue
		}
		if !code && strings.HasPrefix(s[i:], "**") {
			flush(bold, code, dim)
			bold = !bold
			i += 2
			continue
		}
		if !code && strings.HasPrefix(s[i:], "~~") {
			flush(bold, code, dim)
			dim = !dim
			i += 2
			continue
		}
		buf.WriteByte(s[i])
		i++
	}
	flush(bold, code, dim)
	if len(spans) == 0 {
		spans = []Span{{Text: s}}
	}
	return spans
}
