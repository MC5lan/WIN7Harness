//go:build windows

package app

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"win7harness/internal/core"
	"win7harness/internal/ui"
	"win7harness/internal/win32"
)

// keyMatches 查询配置里的按键绑定；用户没改过时等价于出厂快捷键。
func (a *App) keyMatches(action string, vk int32, ctrl, shift, alt bool) bool {
	var cfg map[string]string
	if a.cfg != nil {
		cfg = a.cfg.KeyBindings
	}
	return core.MatchBinding(action, cfg, vk, ctrl, shift, alt)
}

// openHistory 打开 Ctrl+R 提示词历史浮层。重复按 Ctrl+R 关闭。
func (a *App) openHistory() {
	if a.histOpen {
		a.closeHistory()
		return
	}
	a.histOpen = true
	a.histQ = nil
	a.histCur = 0
	a.histIdx = 0
	a.histResults = append([]string(nil), a.promptHistory...)
	a.Redraw()
}

func (a *App) closeHistory() {
	a.histOpen = false
	a.histQ = nil
	a.histResults = nil
	a.Redraw()
}

// fuzzyScore 子序列打分：完全相等 > 前缀 > 包含 > 离散子序列。
func fuzzyScore(query, candidate string) int {
	q, s := []rune(strings.ToLower(query)), []rune(strings.ToLower(candidate))
	if len(q) == 0 {
		return 1
	}
	if len(s) == 0 {
		return 0
	}
	if string(s) == string(q) {
		return 100
	}
	if strings.HasPrefix(string(s), string(q)) {
		return 80
	}
	if strings.Contains(string(s), string(q)) {
		return 40
	}
	score, ji, prev := 0, 0, -2
	for i := 0; i < len(s) && ji < len(q); i++ {
		if s[i] != q[ji] {
			continue
		}
		score += 2
		if prev == i-1 {
			score += 3
		}
		if i == 0 || s[i-1] == ' ' || s[i-1] == '/' || s[i-1] == '.' || s[i-1] == '_' {
			score += 4
		}
		prev = i
		ji++
	}
	if ji == len(q) {
		return score
	}
	return 0
}

func (a *App) histMatches() []string {
	q := string(a.histQ)
	if strings.TrimSpace(q) == "" {
		return a.promptHistory
	}
	type hit struct {
		text  string
		score int
	}
	var hits []hit
	for _, s := range a.promptHistory {
		if sc := fuzzyScore(q, s); sc > 0 {
			hits = append(hits, hit{s, sc})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].score > hits[j].score })
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.text
	}
	return out
}

func (a *App) histBound() {
	n := len(a.histResults)
	if n == 0 {
		a.histIdx = 0
		return
	}
	if a.histIdx < 0 {
		a.histIdx = n - 1
	}
	if a.histIdx >= n {
		a.histIdx = 0
	}
}

func (a *App) histKey(vk int32) bool {
	if a.keyMatches("historySearch", vk,
		win32.GetKeyState(win32.VK_CONTROL), win32.GetKeyState(win32.VK_SHIFT), win32.GetKeyState(win32.VK_MENU)) {
		a.closeHistory()
		return true
	}
	switch vk {
	case win32.VK_ESCAPE:
		a.closeHistory()
	case win32.VK_UP:
		a.histIdx--
		a.histBound()
	case win32.VK_DOWN:
		a.histIdx++
		a.histBound()
	case win32.VK_PRIOR:
		a.histIdx -= 5
		a.histBound()
	case win32.VK_NEXT:
		a.histIdx += 5
		a.histBound()
	case win32.VK_BACK, win32.VK_DELETE:
		if len(a.histQ) > 0 && a.histCur > 0 {
			if vk == win32.VK_BACK {
				a.histQ = append(a.histQ[:a.histCur-1], a.histQ[a.histCur:]...)
				a.histCur--
			} else if a.histCur < len(a.histQ) {
				a.histQ = append(a.histQ[:a.histCur], a.histQ[a.histCur+1:]...)
			}
			a.histIdx = 0
		}
	case win32.VK_RETURN:
		a.histRun()
	default:
		// 方向键移动查询串光标（与输入框一致）。
		if vk == win32.VK_LEFT && a.histCur > 0 {
			a.histCur--
		} else if vk == win32.VK_RIGHT && a.histCur < len(a.histQ) {
			a.histCur++
		}
	}
	a.Redraw()
	return true
}

func (a *App) histType(ch rune) {
	if ch < 32 || ch == 127 {
		return
	}
	a.histQ = append(a.histQ, 0)
	copy(a.histQ[a.histCur+1:], a.histQ[a.histCur:])
	a.histQ[a.histCur] = ch
	a.histCur++
	a.histIdx = 0
	a.Redraw()
}

func (a *App) histRun() {
	a.histMatches()
	a.histBound()
	if len(a.histResults) == 0 {
		return
	}
	text := a.histResults[a.histIdx]
	a.closeHistory()
	a.insertInput(text)
	a.ui.SetFocus("input")
}

func (a *App) histClick(x, y int32) {
	id := a.ui.HandleClick(x, y)
	if id == "" {
		a.closeHistory()
		return
	}
	if strings.HasPrefix(id, "hist:") {
		var i int
		if _, err := fmt.Sscanf(id, "hist:%d", &i); err == nil && i >= 0 && i < len(a.histResults) {
			text := a.histResults[i]
			a.closeHistory()
			a.insertInput(text)
			a.ui.SetFocus("input")
		}
	}
	a.Redraw()
}

func (a *App) paintHistory(c *ui.Canvas) {
	a.histResults = a.histMatches()
	a.histBound()
	th := a.th
	c.FillRect(ui.R(0, 0, a.winW, a.winH), 0x0A0C10)

	const rowH, pitch = int32(34), int32(38)
	const maxRows = 8
	n := int32(len(a.histResults))
	if n > maxRows {
		n = maxRows
	}
	w := int32(620)
	if w > a.winW-60 {
		w = a.winW - 60
	}
	h := 58 + n*pitch + 34
	r := ui.R((a.winW-w)/2, 88, w, h)
	if r.Y+h > a.winH-20 {
		r.Y = a.winH - 20 - h
	}
	if r.Y < 20 {
		r.Y = 20
	}
	c.Round(r, 10, th.Surface)
	c.RoundStroke(r, 10, th.Border)

	// 查询行
	c.TextFace(r.X+18, r.Y+16, "›", ui.FsMd, 700, th.Accent, ui.FaceMono)
	q := string(a.histQ)
	qw := c.MeasureFace(q, ui.FsMd, 400, ui.FaceMono)
	c.TextFace(r.X+34, r.Y+16, q, ui.FsMd, 400, th.Text, ui.FaceMono)
	if blinkOn(a.phase) {
		c.FillRect(ui.R(r.X+36+qw, r.Y+14, 2, 20), th.Accent)
	}

	// 结果行
	for i := int32(0); i < n; i++ {
		row := ui.R(r.X+10, r.Y+52+i*pitch, r.W-20, rowH)
		id := "hist:" + strconv.Itoa(int(i))
		hover := a.ui.Hit(id, row)
		if int(i) == a.histIdx || hover {
			c.Round(row, 6, th.CardHover)
		}
		text := a.histResults[i]
		text = strings.ReplaceAll(text, "\n", " ↵ ")
		if len([]rune(text)) > 90 {
			text = string([]rune(text)[:90]) + "…"
		}
		c.TextFace(row.X+12, row.Y+9, text, ui.FsSm, 400, th.Text, ui.FaceUI)
	}
	if len(a.histResults) == 0 {
		c.TextFace(r.X+20, r.Y+58, "没有匹配的历史", ui.FsSm, 400, th.TextMute, ui.FaceUI)
	}
	c.TextFace(r.X+18, r.Bottom()-26, "上下键选择 · Enter 回填输入框 · Ctrl+R / Esc 关闭", ui.FsXs, 400, th.TextMute, ui.FaceUI)
}
