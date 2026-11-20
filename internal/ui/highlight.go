//go:build windows

package ui

import (
	"strings"
)

// 关键字表覆盖主流语言，统一词表可在未知语言下也获得尚可的高亮效果。
var keywords = map[string]bool{}

func init() {
	words := []string{
		// 通用 / Go
		"package", "import", "func", "return", "if", "else", "for", "range", "switch", "case",
		"default", "break", "continue", "struct", "interface", "type", "var", "const", "map",
		"chan", "go", "defer", "select", "nil", "true", "false", "byte", "rune", "string",
		"int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64",
		"float32", "float64", "bool", "error", "make", "new", "len", "cap", "append", "copy", "delete",
		// JS / TS
		"function", "const", "let", "async", "await", "export", "from", "class", "extends",
		"constructor", "this", "super", "try", "catch", "finally", "throw", "typeof", "instanceof",
		"null", "undefined", "yield", "static", "public", "private", "protected", "readonly",
		"implements", "enum", "namespace", "declare", "as", "satisfies", "of", "in", "void", "never", "any",
		// Python
		"def", "elif", "while", "with", "lambda", "pass", "raise", "except", "global", "nonlocal",
		"assert", "del", "is", "not", "and", "or", "None", "True", "False", "self", "print",
		// Java / C#
		"void", "final", "native", "synchronized", "throws", "abstract", "volatile", "transient",
		"using", "namespace", "get", "set", "override", "virtual", "partial", "params", "ref", "out",
		// C / C++
		"include", "define", "ifdef", "endif", "sizeof", "typedef", "unsigned", "signed", "long",
		"short", "char", "double", "extern", "inline", "register", "goto", "union", "class", "template",
		// Rust
		"fn", "mut", "impl", "trait", "pub", "crate", "match", "loop", "where", "unsafe", "dyn",
		// SQL / Shell
		"select", "from", "where", "insert", "update", "delete", "create", "table", "join", "group",
		"order", "limit", "echo", "cd", "export", "source", "then", "fi", "do", "done", "esac",
		// 伪代码/通用
		"function", "end", "begin", "null", "true", "false",
	}
	for _, w := range words {
		keywords[w] = true
	}
}

// palette 代码配色。
type codePalette struct {
	Keyword uint32
	String  uint32
	Comment uint32
	Number  uint32
	Func    uint32
	Type    uint32
	Punct   uint32
	Plain   uint32
}

func paletteFor(th *Theme) codePalette {
	if th.Name == "light" {
		return codePalette{
			Keyword: 0xED3A7C, String: 0x327D0A, Comment: 0xAEA39A, Number: 0x0953B4,
			Func: 0xD84E1D, Type: 0x6E760F, Punct: 0x80726B, Plain: th.CodeText,
		}
	}
	return codePalette{
		Keyword: 0xEA92C7, String: 0x6ACE9E, Comment: 0x73675C, Number: 0x6C8CF7,
		Func: 0xFFAA82, Type: 0xCADB7F, Punct: 0x887A6B, Plain: th.CodeText,
	}
}

// highlightLine 把一行源码切成带颜色的 run 序列。
// HighlightLine 把一行源码切成带颜色的 Run，供差异弹窗等 ui 包外的调用方复用。
// startX 为第一个 Run 的 X 偏移（内部制表符展开会按它对齐）。
func HighlightLine(c *Canvas, src string, size int32, th *Theme, startX int32) []Run {
	return highlightLine(c, src, size, th, startX)
}

func highlightLine(c *Canvas, src string, size int32, th *Theme, startX int32) []Run {
	pal := paletteFor(th)
	line := expandTabs(src)
	rs := []rune(line)
	n := len(rs)
	x := startX
	var runs []Run

	addRun := func(text string, color uint32) {
		if text == "" {
			return
		}
		w := c.stringWidth(text, size, false, true)
		runs = append(runs, Run{X: x, Text: text, Mono: true, Size: size, Color: color})
		x += w
	}

	i := 0
	// 行首缩进整体输出（保持对齐）
	for i < n {
		// 注释
		if (rs[i] == '/' && i+1 < n && rs[i+1] == '/') || rs[i] == '#' ||
			(rs[i] == '-' && i+1 < n && rs[i+1] == '-') {
			addRun(string(rs[i:]), pal.Comment)
			return runs
		}
		break
	}

	for i < n {
		ch := rs[i]
		// 行内注释
		if ch == '/' && i+1 < n && rs[i+1] == '/' {
			addRun(string(rs[i:]), pal.Comment)
			break
		}
		// 字符串
		if ch == '"' || ch == '\'' || ch == '`' {
			j := i + 1
			for j < n {
				if rs[j] == '\\' && j+1 < n {
					j += 2
					continue
				}
				if rs[j] == ch {
					j++
					break
				}
				j++
			}
			addRun(string(rs[i:j]), pal.String)
			i = j
			continue
		}
		// 数字
		if ch >= '0' && ch <= '9' {
			j := i
			for j < n && (isAlnum(rs[j]) || rs[j] == '.' || rs[j] == 'x' || rs[j] == '_') {
				j++
			}
			addRun(string(rs[i:j]), pal.Number)
			i = j
			continue
		}
		// 标识符
		if isIdentStart(ch) {
			j := i
			for j < n && isIdent(rs[j]) {
				j++
			}
			word := string(rs[i:j])
			lower := strings.ToLower(word)
			color := pal.Plain
			switch {
			case keywords[word] || keywords[lower]:
				color = pal.Keyword
			default:
				// 后跟 ( 视为函数调用
				k := j
				for k < n && rs[k] == ' ' {
					k++
				}
				if k < n && rs[k] == '(' {
					color = pal.Func
				} else if ch >= 'A' && ch <= 'Z' {
					color = pal.Type
				}
			}
			addRun(word, color)
			// 保留词后空格
			k := j
			for k < n && rs[k] == ' ' {
				k++
			}
			if k > j {
				addRun(string(rs[j:k]), pal.Plain)
			}
			i = k
			continue
		}
		// 标点 / 空白
		j := i
		for j < n && !isIdent(rs[j]) && rs[j] != '"' && rs[j] != '\'' && rs[j] != '`' &&
			!(rs[j] >= '0' && rs[j] <= '9') && rs[j] != '/' {
			j++
		}
		if j == i {
			j++
		}
		seg := string(rs[i:j])
		trim := strings.TrimSpace(seg)
		if trim == "" {
			addRun(seg, pal.Plain)
		} else {
			addRun(seg, pal.Punct)
		}
		i = j
	}
	return runs
}

func expandTabs(s string) string {
	if !strings.Contains(s, "\t") {
		return s
	}
	return strings.ReplaceAll(s, "\t", "    ")
}

func isIdentStart(r rune) bool {
	return r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r > 127
}

func isIdent(r rune) bool {
	return isIdentStart(r) || (r >= '0' && r <= '9')
}

func isAlnum(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}
