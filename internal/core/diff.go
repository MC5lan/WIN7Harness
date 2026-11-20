package core

import (
	"fmt"
	"strings"
)

// 轻量行级 diff：用于写盘前的差异预览。纯标准库，不做三方依赖。
// 采用 LCS 动态规划，超过阈值时降级为"整段替换"式展示，避免大文件卡顿。

const (
	diffMaxLines   = 4000 // 参与 LCS 的最大行数，超出则降级
	diffMaxOutput  = 200  // 输出最大行数
	diffContextRow = 3    // 上下文行数
)

type diffOp struct {
	kind byte // ' ' 相同, '-' 删除, '+' 新增
	text string
}

// UnifiedDiff 生成统一格式差异，带行号与上下文裁剪。
func UnifiedDiff(oldText, newText, path string, contextRows int) string {
	if contextRows <= 0 {
		contextRows = diffContextRow
	}
	oldLines := splitLines(oldText)
	newLines := splitLines(newText)

	var ops []diffOp
	if len(oldLines) > diffMaxLines || len(newLines) > diffMaxLines {
		ops = append(ops, diffOp{'@', fmt.Sprintf("文件较大（%d → %d 行），按整段替换展示", len(oldLines), len(newLines))})
		for _, l := range oldLines {
			ops = append(ops, diffOp{'-', l})
		}
		for _, l := range newLines {
			ops = append(ops, diffOp{'+', l})
		}
	} else {
		ops = lcsDiff(oldLines, newLines)
	}

	// 统计改动规模
	added, removed := 0, 0
	for _, o := range ops {
		if o.kind == '+' {
			added++
		} else if o.kind == '-' {
			removed++
		}
	}

	var b strings.Builder
	b.WriteString(path + "\n")
	b.WriteString(fmt.Sprintf("@@ +%d 行 / -%d 行 @@\n", added, removed))

	// 只保留改动附近的行
	keep := make([]bool, len(ops))
	for i, o := range ops {
		if o.kind == ' ' {
			continue
		}
		lo, hi := i-contextRows, i+contextRows
		if lo < 0 {
			lo = 0
		}
		if hi >= len(ops) {
			hi = len(ops) - 1
		}
		for j := lo; j <= hi; j++ {
			keep[j] = true
		}
	}

	outLines := 0
	skipped := false
	for i, o := range ops {
		if !keep[i] {
			if !skipped {
				b.WriteString("…\n")
				skipped = true
			}
			continue
		}
		skipped = false
		if outLines >= diffMaxOutput {
			b.WriteString("…（差异过长，已截断）\n")
			break
		}
		b.WriteString(string(o.kind) + o.text + "\n")
		outLines++
	}
	if added == 0 && removed == 0 {
		b.WriteString("（内容无变化）\n")
	}
	return b.String()
}

// DiffLine 是结构化差异里的一行。OldNo/NewNo 为 0 表示该侧没有对应行。
// Prefix: ' ' 上下文, '-' 删除, '+' 新增, '@' 说明行。
type DiffLine struct {
	OldNo, NewNo int
	Prefix       byte
	Text         string
}

// DiffHunk 是一段连续改动（含前后 contextRows 行上下文）。
type DiffHunk struct {
	Title string
	Lines []DiffLine
}

// DiffHunks 与 UnifiedDiff 用同一套 LCS，但保留行号与 hunk 边界，
// 供界面渲染带行号/配色的差异，并支持逐 hunk 接受或拒绝。
func DiffHunks(oldText, newText, path string, contextRows int) []DiffHunk {
	if contextRows <= 0 {
		contextRows = diffContextRow
	}
	oldLines, newLines := splitLines(oldText), splitLines(newText)
	var ops []diffOp
	if len(oldLines) > diffMaxLines || len(newLines) > diffMaxLines {
		ops = append(ops, diffOp{'@', fmt.Sprintf("文件较大（%d → %d 行），按整段替换展示", len(oldLines), len(newLines))})
		for _, l := range oldLines {
			ops = append(ops, diffOp{'-', l})
		}
		for _, l := range newLines {
			ops = append(ops, diffOp{'+', l})
		}
	} else {
		ops = lcsDiff(oldLines, newLines)
	}
	if len(ops) == 0 {
		return nil
	}

	type numbered struct {
		op           diffOp
		oldNo, newNo int
	}
	seq := make([]numbered, len(ops))
	on, nn, changed := 1, 1, 0
	for i, op := range ops {
		n := numbered{op: op}
		switch op.kind {
		case ' ':
			n.oldNo, n.newNo = on, nn
			on++
			nn++
		case '-':
			n.oldNo, n.newNo = on, 0
			on++
			changed++
		case '+':
			n.oldNo, n.newNo = 0, nn
			nn++
			changed++
		default:
			changed++
		}
		seq[i] = n
	}
	if changed == 0 {
		return nil
	}

	// 把每个改动点向前后扩 contextRows，重叠的区间并成一个 hunk。
	type span struct{ lo, hi int }
	var spans []span
	for i, op := range ops {
		if op.kind == ' ' {
			continue
		}
		lo, hi := i-contextRows, i+contextRows+1
		if lo < 0 {
			lo = 0
		}
		if hi > len(ops) {
			hi = len(ops)
		}
		if n := len(spans); n > 0 && lo <= spans[n-1].hi {
			if hi > spans[n-1].hi {
				spans[n-1].hi = hi
			}
		} else {
			spans = append(spans, span{lo, hi})
		}
	}

	var out []DiffHunk
	total := 0
	for _, sp := range spans {
		h := DiffHunk{}
		oldStart, newStart, oc, nc := 0, 0, 0, 0
		for _, n := range seq[sp.lo:sp.hi] {
			if n.oldNo > 0 {
				if oldStart == 0 {
					oldStart = n.oldNo
				}
				oc++
			}
			if n.newNo > 0 {
				if newStart == 0 {
					newStart = n.newNo
				}
				nc++
			}
		}
		h.Title = fmt.Sprintf("@@ -%d,%d +%d,%d @@ %s", oldStart, oc, newStart, nc, path)
		for _, n := range seq[sp.lo:sp.hi] {
			if total >= diffMaxOutput {
				h.Lines = append(h.Lines, DiffLine{Prefix: '@', Text: "…（差异过长，已截断）"})
				out = append(out, h)
				return out
			}
			h.Lines = append(h.Lines, DiffLine{OldNo: n.oldNo, NewNo: n.newNo, Prefix: n.op.kind, Text: n.op.text})
			total++
		}
		out = append(out, h)
	}
	return out
}

// DiffStat 返回新增/删除行数，用于判断是否值得打断用户。
func DiffStat(oldText, newText string) (added, removed int) {
	oldLines := splitLines(oldText)
	newLines := splitLines(newText)
	if len(oldLines) > diffMaxLines || len(newLines) > diffMaxLines {
		return len(newLines), len(oldLines)
	}
	for _, o := range lcsDiff(oldLines, newLines) {
		if o.kind == '+' {
			added++
		} else if o.kind == '-' {
			removed++
		}
	}
	return added, removed
}

func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// lcsDiff 行级 LCS 差异。内存 O(n*m)，由 diffMaxLines 兜底。
func lcsDiff(a, b []string) []diffOp {
	n, m := len(a), len(b)
	// 前后缀剪枝：大多数编辑只动局部，能显著缩小 DP 规模
	pre := 0
	for pre < n && pre < m && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < n-pre && suf < m-pre && a[n-1-suf] == b[m-1-suf] {
		suf++
	}

	var ops []diffOp
	for i := 0; i < pre; i++ {
		ops = append(ops, diffOp{' ', a[i]})
	}
	ca, cb := a[pre:n-suf], b[pre:m-suf]

	if len(ca) == 0 {
		for _, l := range cb {
			ops = append(ops, diffOp{'+', l})
		}
	} else if len(cb) == 0 {
		for _, l := range ca {
			ops = append(ops, diffOp{'-', l})
		}
	} else {
		dp := make([][]int32, len(ca)+1)
		for i := range dp {
			dp[i] = make([]int32, len(cb)+1)
		}
		for i := len(ca) - 1; i >= 0; i-- {
			for j := len(cb) - 1; j >= 0; j-- {
				if ca[i] == cb[j] {
					dp[i][j] = dp[i+1][j+1] + 1
				} else if dp[i+1][j] >= dp[i][j+1] {
					dp[i][j] = dp[i+1][j]
				} else {
					dp[i][j] = dp[i][j+1]
				}
			}
		}
		i, j := 0, 0
		for i < len(ca) && j < len(cb) {
			switch {
			case ca[i] == cb[j]:
				ops = append(ops, diffOp{' ', ca[i]})
				i++
				j++
			case dp[i+1][j] >= dp[i][j+1]:
				ops = append(ops, diffOp{'-', ca[i]})
				i++
			default:
				ops = append(ops, diffOp{'+', cb[j]})
				j++
			}
		}
		for ; i < len(ca); i++ {
			ops = append(ops, diffOp{'-', ca[i]})
		}
		for ; j < len(cb); j++ {
			ops = append(ops, diffOp{'+', cb[j]})
		}
	}

	for i := n - suf; i < n; i++ {
		ops = append(ops, diffOp{' ', a[i]})
	}
	return ops
}
