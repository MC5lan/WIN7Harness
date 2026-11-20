package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// ExpandAtRefs 把输入里的 @相对路径 解析为附带文件内容块（仅发给模型，界面保留原文）。
// 预算上限 120KB，超出的引用直接跳过，避免撑爆上下文。
func ExpandAtRefs(text, workDir string) string {
	if !strings.Contains(text, "@") || workDir == "" {
		return text
	}
	// 预算随内存档位变化：低内存模式下收紧到 48KB（见 mempolicy.go）。
	budget := MemProfile().AtRefBudget
	left := budget
	seen := map[string]bool{}
	var blocks strings.Builder
	for _, tok := range strings.Fields(text) {
		if !strings.HasPrefix(tok, "@") || len(tok) < 2 {
			continue
		}
		rel := strings.TrimPrefix(tok, "@")
		rel = strings.Trim(rel, "`\"'，。；：、）)")
		rel = strings.ReplaceAll(rel, "\\ ", " ")
		if rel == "" || strings.Contains(rel, "..") {
			continue
		}
		// 支持 @file.go:123 与 @file.go:120-180 的行段引用（补全选中符号时会带上行号）
		rel, lineFrom, lineTo := ParsePathLineRef(rel)
		p := ResolvePath(workDir, filepath.FromSlash(rel))
		if IsForbiddenPath(p) {
			continue
		}
		st, err := os.Stat(p)
		if err != nil || st.IsDir() || st.Size() > int64(left) {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil || !utf8.Valid(data) {
			continue
		}
		key := strings.ToLower(p)
		if seen[key] {
			continue
		}
		body := string(data)
		if lineFrom > 0 {
			lines := strings.Split(body, "\n")
			if lineFrom > len(lines) {
				lineFrom = len(lines)
			}
			if lineTo <= 0 || lineTo < lineFrom {
				// 单行引用时给上下文窗口，方便模型看清所在函数
				lineTo = lineFrom
			}
			start := lineFrom - 40
			if start < 1 {
				start = 1
			}
			end := lineTo + 40
			if end > len(lines) {
				end = len(lines)
			}
			rel = fmt.Sprintf("%s:%d-%d", rel, start, end)
			body = strings.Join(lines[start-1:end], "\n")
		}
		if len(body) > left {
			body = body[:left]
		}
		seen[key] = true
		left -= len(body)
		fmt.Fprintf(&blocks, "\n<file path=%q>\n%s\n</file>\n", filepath.ToSlash(rel), body)
	}
	if blocks.Len() == 0 {
		return text
	}
	return text + "\n\n以下是本次消息中 @ 引用的文件内容：" + blocks.String()
}

// ParsePathLineRef 从路径串里剥出可选的 :行 或 :起-止 后缀。
// 返回 (纯路径, 起始行, 结束行)；起始行为 0 表示没有合法行号，此时 path 原样返回。
// 供 @ 引用展开与界面里"点击文件路径跳行"共用同一套解析规则。
func ParsePathLineRef(rel string) (path string, from, to int) {
	path = rel
	i := strings.LastIndex(rel, ":")
	if i <= 0 || i >= len(rel)-1 {
		return path, 0, 0
	}
	spec := rel[i+1:]
	ok := true
	for k := 0; k < len(spec); k++ {
		c := spec[k]
		if (c < '0' || c > '9') && c != '-' {
			ok = false
			break
		}
	}
	if !ok {
		return path, 0, 0
	}
	if j := strings.IndexByte(spec, '-'); j >= 0 {
		from = atoiSafe(spec[:j])
		to = atoiSafe(spec[j+1:])
	} else {
		from = atoiSafe(spec)
	}
	if from > 0 {
		path = rel[:i]
		return path, from, to
	}
	return path, 0, 0
}

// atoiSafe 解析非负整数，失败返回 0。
func atoiSafe(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
		if n > 10_000_000 {
			return 0
		}
	}
	return n
}
