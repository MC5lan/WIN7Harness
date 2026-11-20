package app

import (
	"fmt"
	"strings"

	"win7harness/internal/ui"
	"win7harness/internal/ui/kit"
)

// /compare 并列查看两个会话。
//
// 老机器屏幕小，真正的"双开窗口"不现实，所以做成页面内左右两栏：
// 左栏永远是当前会话，右栏用 /compare N 指定。目的是比较两条分叉
// 或者两个模型对同一问题的答复，不用来回切换丢掉上下文。

// cmdCompare 无参列会话，有参选定右侧会话。
func (a *App) cmdCompare(rest string) {
	if a.v27 == nil {
		a.sysNote("并列视图未初始化。")
		return
	}
	arg := strings.TrimSpace(rest)
	if arg == "" || strings.EqualFold(arg, "list") {
		a.listCompareTargets()
		return
	}
	n := 0
	if _, err := fmt.Sscanf(arg, "%d", &n); err != nil || n <= 0 || n > len(a.convs) {
		a.toast("用法：/compare 2（序号见 /compare）", true)
		return
	}
	if n-1 == a.active {
		a.toast("右侧不能是当前会话，请选另一条", true)
		return
	}
	a.v27.cmpRight = n - 1
	a.page = PageCompare
	a.ver++
}

func (a *App) listCompareTargets() {
	var b strings.Builder
	b.WriteString("并列查看：左栏是本轮当前会话，右栏用 /compare N 指定。\r\n\r\n")
	for i, c := range a.convs {
		mark := "  "
		if i == a.active {
			mark = "▶ "
		}
		b.WriteString(fmt.Sprintf("%s%d. %s（%d 条）\r\n", mark, i+1, convTitle(c), len(c.Msgs)))
	}
	b.WriteString("\r\n进入：/compare 2 · 页面内 ↑↓ 滚动、Esc 返回")
	a.sysNote(b.String())
}

func convTitle(c *Conv) string {
	if c == nil {
		return "(空)"
	}
	if strings.TrimSpace(c.Title) != "" {
		return c.Title
	}
	return "新对话"
}

// paintCompare 绘制左右两栏（kit 组件树）。
func (a *App) paintCompare(c *ui.Canvas) {
	th := a.th
	u := a.ui
	W, H := a.winW, a.winH
	c.FillRect(ui.R(0, 0, W, H), th.Bg)
	// 左侧栏：全屏页之间也能直接互跳，不必先退回对话。
	a.paintRail(u, H)

	c.FillRect(ui.R(a.railW, 0, W-a.railW, 56), ui.Blend(th.Bg, th.Surface, 0.6))
	c.HLine(a.railW, 55, W-a.railW, th.BorderSoft)
	DrawLogo(c, a.railW+24, 16, 24, th.Accent, th.Bg)
	c.TextFace(a.railW+56, 18, "并列查看", ui.FsLg, 700, th.Text, ui.FaceUI)
	if u.Button("cmp.back", "← 返回对话", ui.R(W-140, 14, 116, 30), BtnGhost) {
		a.page = PageChat
	}

	left := a.currentConv()
	var right *Conv
	if a.v27 != nil && a.v27.cmpRight >= 0 && a.v27.cmpRight < len(a.convs) {
		right = a.convs[a.v27.cmpRight]
	}

	ui.SetTokens(a.th.Tokens())
	ktx := a.kit()
	ktx.Painter = kit.NewCanvasAdapter(c)
	ktx.BeginFrame()

	row := kit.Row(12, comparePane(left, "当前会话"), comparePane(right, "对照会话"))
	head := kit.Row(8,
		kit.Label("左栏是本轮当前会话 · 右栏是 /compare N 指定的对照", ui.FsXs, 400, ui.CurrentTokens().TextTertiary),
	)
	foot := kit.Row(8,
		kit.Label("Esc 返回对话 · ↑↓ 滚动 · PageUp/PageDown 翻页", ui.FsXs, 400, ui.CurrentTokens().TextTertiary),
	)

	root := kit.Column(10, head, row, foot)

	area := kit.R(a.railW+8, 66, W-a.railW-16, H-66-8)
	root.Arrange(ktx, area)
	root.Paint(ktx, ktx.Painter)
}

// comparePane 构造一栏的 kit 子树：标题/元信息/消息滚动区。
func comparePane(cv *Conv, label string) kit.Element {
	tk := ui.CurrentTokens()

	var head []kit.Element
	head = append(head, kit.Row(8,
		kit.Label(label, ui.FsSm, 700, tk.TextSecondary),
	))
	if cv == nil {
		hint := "右侧还没有选定会话，先 /compare N 选一个"
		if label == "当前会话" {
			hint = "（没有会话）"
		}
		return kit.Card(14, kit.Column(12, append(head, kit.Label(hint, ui.FsSm, 400, tk.TextTertiary))...))
	}

	meta := fmt.Sprintf("%d 条消息", len(cv.Msgs))
	if cv.Model != "" {
		meta += " · " + cv.Model
	}
	if cv.WorkDir != "" {
		meta += " · " + clipRunes(cv.WorkDir, 28)
	}

	head = append(head,
		kit.Label(clipRunes(convTitle(cv), 80), ui.FsMd, 700, tk.TextPrimary),
		kit.Label(meta, ui.FsXs, 400, tk.TextTertiary),
		kit.Divider(1, tk.BorderHairline, 0),
	)

	rows := []kit.Element{}
	for _, m := range cv.Msgs {
		text := strings.TrimSpace(strings.ReplaceAll(m.Text, "\r\n", " "))
		text = strings.ReplaceAll(text, "\n", " ")
		if text == "" {
			continue
		}
		prefix := "· "
		col := tk.TextSecondary
		if m.Role == "user" {
			prefix = "你："
			col = tk.TextPrimary
		} else if m.Role == "assistant" {
			prefix = "AI："
			col = tk.TextSecondary
		}
		for _, chunk := range wrapCmpsText(text, 72) {
			rows = append(rows, kit.Label(prefix+chunk, ui.FsXs, 400, col))
			prefix = "   "
		}
	}
	if len(rows) == 0 {
		rows = append(rows, kit.Label("（这条会话还没有消息）", ui.FsSm, 400, tk.TextTertiary))
	}
	body := kit.Scroll("cmp:"+label, 0, kit.Column(2, rows...))

	head = append(head, body)
	return kit.Card(14, kit.Column(4, head...))
}

// wrapCmpsText 文字版折行：按 rune 切，每段不超过 maxRune 个字符。
func wrapCmpsText(s string, maxRune int) []string {
	r := []rune(s)
	if maxRune <= 0 {
		maxRune = 72
	}
	var out []string
	for len(r) > maxRune {
		out = append(out, string(r[:maxRune]))
		r = r[maxRune:]
	}
	if len(r) > 0 {
		out = append(out, string(r))
	}
	return out
}
