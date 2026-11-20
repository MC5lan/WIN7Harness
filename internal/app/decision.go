//go:build windows

package app

import (
	"fmt"
	"strings"
	"time"

	"win7harness/internal/core"
	"win7harness/internal/ui"
)

// diffReq 是工具层发来的富差分确认请求。
type diffReq struct {
	Title        string
	Path         string
	Hunks        []core.DiffHunk
	AllowPartial bool
	Resp         chan []int
}

// planDecision 是方案审批的结果。Action: approve / reject / modify。
type planDecision struct {
	Action string
	Text   string
}

// planReq 是引擎发来的方案审批请求。
type planReq struct {
	Title string
	Plan  string
	Steps int
	Resp  chan planDecision
}

// confirmDiff 由工具 goroutine 调用，阻塞直到用户在富差分弹窗里作答。
// 返回 nil 表示取消；空切片表示全不选。
func (a *App) confirmDiff(title, path string, hunks []core.DiffHunk, allowPartial bool) []int {
	req := diffReq{Title: title, Path: path, Hunks: hunks, AllowPartial: allowPartial, Resp: make(chan []int, 1)}
	select {
	case a.diffCh <- req:
	case <-time.After(60 * time.Second):
		core.Logf("差分确认投递超时（%s）", title)
		return nil
	}
	select {
	case v := <-req.Resp:
		return v
	case <-time.After(10 * time.Minute):
		a.remoteClose.Store(true)
		a.Redraw()
		return nil
	}
}

// confirmPlan 由引擎在 plan gate 处调用，阻塞等待用户的批准/拒绝/修改。
func (a *App) confirmPlan(title, plan string, steps int) planDecision {
	req := planReq{Title: title, Plan: plan, Steps: steps, Resp: make(chan planDecision, 1)}
	select {
	case a.planCh <- req:
	case <-time.After(60 * time.Second):
		return planDecision{Action: "reject"}
	}
	select {
	case v := <-req.Resp:
		return v
	case <-time.After(10 * time.Minute):
		return planDecision{Action: "reject"}
	}
}

// installNextModal 按 普通确认 → 差分 → 方案 的顺序挂载下一个待处理弹窗。
func (a *App) installNextModal() {
	if a.modal != nil {
		return
	}
	if len(a.pendingConfirms) > 0 {
		req := a.pendingConfirms[0]
		a.pendingConfirms = a.pendingConfirms[1:]
		a.modal = &Modal{Title: req.Title, Detail: req.Detail, Resp: req.Resp, Confirm: true, Kind: modalConfirm}
		a.modalOKFocused = false
		return
	}
	if len(a.pendingDiffs) > 0 {
		req := a.pendingDiffs[0]
		a.pendingDiffs = a.pendingDiffs[1:]
		acc := make(map[int]bool, len(req.Hunks))
		for i := range req.Hunks {
			acc[i] = true
		}
		a.modal = &Modal{
			Kind: modalDiff, Title: req.Title, Path: req.Path, Hunks: req.Hunks,
			AllowPartial: req.AllowPartial, HunkAccept: acc, DiffResp: req.Resp,
		}
		a.modalOKFocused = false
		return
	}
	if len(a.pendingPlans) > 0 {
		req := a.pendingPlans[0]
		a.pendingPlans = a.pendingPlans[1:]
		a.modal = &Modal{
			Kind: modalPlan, Title: req.Title, Plan: req.Plan, Steps: req.Steps,
			PlanResp: req.Resp,
		}
		a.modalOKFocused = false
	}
}

// drainDecisionRequests 由主循环 Timer 调用，把三路请求收进队列。
func (a *App) drainDecisionRequests() {
	select {
	case req := <-a.diffCh:
		a.pendingDiffs = append(a.pendingDiffs, req)
	default:
	}
	select {
	case req := <-a.planCh:
		a.pendingPlans = append(a.pendingPlans, req)
	default:
	}
	a.installNextModal()
}

// modalAnswerDiff 提交当前 hunk 选择；modalAnswerPlan 提交方案决策。
func (a *App) modalAnswerDiff(acceptAll bool) {
	if a.modal == nil {
		return
	}
	var res []int
	if acceptAll {
		res = make([]int, len(a.modal.Hunks))
		for i := range res {
			res[i] = i
		}
	} else {
		for i := range a.modal.Hunks {
			if a.modal.HunkAccept[i] {
				res = append(res, i)
			}
		}
		if res == nil {
			res = []int{}
		}
	}
	if a.modal.DiffResp != nil {
		select {
		case a.modal.DiffResp <- res:
		default:
		}
	}
	a.modal = nil
	a.modalOKFocused = false
	a.installNextModal()
}

func (a *App) modalCancelDiff() {
	if a.modal == nil {
		return
	}
	if a.modal.DiffResp != nil {
		select {
		case a.modal.DiffResp <- nil:
		default:
		}
	}
	a.modal = nil
	a.modalOKFocused = false
	a.installNextModal()
}

func (a *App) modalAnswerPlan(action, text string) {
	if a.modal == nil {
		return
	}
	if a.modal.PlanResp != nil {
		select {
		case a.modal.PlanResp <- planDecision{Action: action, Text: text}:
		default:
		}
	}
	a.modal = nil
	a.modalOKFocused = false
	a.installNextModal()
}

// paintDiffModal 渲染带双行号、增删底色与语法高亮的差异弹窗。
func (a *App) paintDiffModal(c *ui.Canvas, r ui.Rect, u *UI, th *ui.Theme) {
	body := ui.R(r.X+16, r.Y+64, r.W-32, r.H-150)
	c.Clip(body)

	const gutterW = int32(64)
	lineH := int32(18)
	// 先算内容总高度：滚轮上限必须和实际行数一致，否则会滚出一大片空白。
	total := int32(0)
	for _, h := range a.modal.Hunks {
		total += lineH + 2
		total += int32(len(h.Lines)) * lineH
		total += 6
	}
	a.modal.ScrollMax = float32(total - body.H)
	if a.modal.ScrollMax < 0 {
		a.modal.ScrollMax = 0
	}
	if a.modal.Scroll > a.modal.ScrollMax {
		a.modal.Scroll = a.modal.ScrollMax
	}
	y := body.Y - int32(a.modal.Scroll)
	for hi, h := range a.modal.Hunks {
		if y > body.Bottom() {
			break
		}
		if a.modal.AllowPartial {
			label := "接受"
			if !a.modal.HunkAccept[hi] {
				label = "拒绝"
			}
			btn := ui.R(body.Right()-62, y, 56, 18)
			if u.Button(fmt.Sprintf("modal.hunk:%d", hi), label, btn, BtnGhost) {
				a.modal.HunkAccept[hi] = !a.modal.HunkAccept[hi]
			}
		}
		hcol := th.Text
		if a.modal.AllowPartial && !a.modal.HunkAccept[hi] {
			hcol = th.TextMute
		}
		c.TextFace(body.X+4, y, h.Title, ui.FsXs, 700, hcol, ui.FaceMono)
		y += lineH + 2

		for _, dl := range h.Lines {
			if y > body.Bottom() {
				break
			}
			oldNo, newNo := "", ""
			if dl.OldNo > 0 {
				oldNo = fmt.Sprintf("%d", dl.OldNo)
			}
			if dl.NewNo > 0 {
				newNo = fmt.Sprintf("%d", dl.NewNo)
			}
			c.TextFace(body.X+2, y, oldNo, ui.FsXs, 400, th.TextMute, ui.FaceMono)
			c.TextFace(body.X+32, y, newNo, ui.FsXs, 400, th.TextMute, ui.FaceMono)

			bg := uint32(0)
			fg := th.CodeText
			switch dl.Prefix {
			case '+':
				bg = ui.Blend(th.Surface, th.Success, 0.20)
			case '-':
				bg = ui.Blend(th.Surface, th.Danger, 0.20)
			case '@':
				fg = th.Accent
			}
			if bg != 0 {
				c.FillRect(ui.R(body.X+gutterW, y, body.W-gutterW, lineH), bg)
			}
			// 语法着色：高亮后的 Run 以 dl.Text 为源，逐一绘在 gutter 右侧。
			runs := ui.HighlightLine(c, dl.Text, ui.FsXs, th, 0)
			if dl.Prefix == ' ' || dl.Prefix == '+' || dl.Prefix == '-' {
				c.TextFace(body.X+gutterW+4, y, string(dl.Prefix), ui.FsXs, 400, fg, ui.FaceMono)
			}
			for _, run := range runs {
				col := run.Color
				if dl.Prefix == '@' {
					col = fg
				}
				c.TextFace(body.X+gutterW+4+14+run.X, y, run.Text, ui.FsXs, 400, col, ui.FaceMono)
			}
			y += lineH
		}
		y += 6
	}

	// 底栏在裁剪区之外：必须先 PopClip，否则按钮被 body 裁掉（真机截图抓到过这个 bug）。
	c.PopClip()

	// 底栏：当前选中数 + 操作按钮。
	by := r.Bottom() - 56
	selected := 0
	for i := range a.modal.Hunks {
		if a.modal.HunkAccept[i] {
			selected++
		}
	}
	hint := fmt.Sprintf("共 %d 段，已选 %d 段 · %s", len(a.modal.Hunks), selected, a.modal.Path)
	c.TextFace(r.X+24, by+9, ellipsis(c, hint, ui.FsXs, 400, r.W-260), ui.FsXs, 400, th.TextMute, ui.FaceUI)
	if u.Button("modal.cancel", "取消", ui.R(r.Right()-220, by, 94, 34), BtnGhost) {
		a.modalCancelDiff()
	}
	if u.Button("modal.ok", "应用所选", ui.R(r.Right()-118, by, 94, 34), BtnDanger) {
		a.modalAnswerDiff(false)
	}
}

// paintPlanModal 渲染方案审批弹窗；方案正文直接复用聊天的 markdown 排版管线。
func (a *App) paintPlanModal(c *ui.Canvas, r ui.Rect, u *UI, th *ui.Theme) {
	body := ui.R(r.X+24, r.Y+64, r.W-48, r.H-150)
	c.Clip(body)
	sub := fmt.Sprintf("%d 个步骤 · 批准后才开始写文件", a.modal.Steps)
	c.TextFace(body.X, body.Y, sub, ui.FsXs, 400, th.TextMute, ui.FaceUI)
	lines := ui.LayoutMarkdown(c, ui.ParseMarkdown(a.modal.Plan), body.W, ui.FsSm, th)
	total := ui.LayoutHeight(lines) + 26
	a.modal.ScrollMax = float32(total - body.H)
	if a.modal.ScrollMax < 0 {
		a.modal.ScrollMax = 0
	}
	if a.modal.Scroll > a.modal.ScrollMax {
		a.modal.Scroll = a.modal.ScrollMax
	}
	ui.DrawLines(c, lines, body.X, body.Y+26-int32(a.modal.Scroll), body, th)
	// 同差分弹窗：底栏按钮必须脱离正文裁剪区。
	c.PopClip()

	by := r.Bottom() - 56
	if u.Button("plan.reject", "拒绝", ui.R(r.Right()-330, by, 94, 34), BtnGhost) {
		a.modalAnswerPlan("reject", "")
	}
	if u.Button("plan.modify", "修改", ui.R(r.Right()-224, by, 94, 34), BtnGhost) {
		a.modalAnswerPlan("modify", a.modal.Plan)
	}
	if u.Button("plan.approve", "批准并执行", ui.R(r.Right()-118, by, 108, 34), BtnPrimary) {
		a.modalAnswerPlan("approve", "")
	}
	c.TextFace(r.X+24, by+9, "回车默认拒绝 · 可修改后重新提交", ui.FsXs, 400, th.TextMute, ui.FaceUI)
}

// installShotModal 为 WIN7H_SHOT_MODAL 调试入口准备样例数据，仅用于视觉验收截图。
func (a *App) installShotModal(mode string) {
	switch mode {
	case "diff":
		h1 := core.DiffHunk{Title: "@@ -12,7 +12,9 @@ internal/ui/gfx.go", Lines: []core.DiffLine{
			{OldNo: 12, NewNo: 12, Prefix: ' ', Text: "func (c *Canvas) Flush() {"},
			{OldNo: 13, NewNo: 13, Prefix: '-', Text: "\tc.flushGDI()"},
			{NewNo: 14, Prefix: '+', Text: "\tc.flushCanvas()"},
			{NewNo: 15, Prefix: '+', Text: "\tc.replayText()"},
			{OldNo: 14, NewNo: 16, Prefix: ' ', Text: "}"},
		}}
		h2 := core.DiffHunk{Title: "@@ -48,6 +50,7 @@ internal/ui/gfx.go", Lines: []core.DiffLine{
			{OldNo: 48, NewNo: 50, Prefix: ' ', Text: "func (c *Canvas) FillRect(r Rect, color uint32) {"},
			{OldNo: 49, NewNo: 51, Prefix: '-', Text: "\tc.drawFill(r, color)"},
			{NewNo: 52, Prefix: '+', Text: "\tc.emit(rxFill, r, color) // 走命令流，支持脏区重放"},
			{OldNo: 50, NewNo: 53, Prefix: ' ', Text: "}"},
		}}
		h3 := core.DiffHunk{Title: "@@ -88,3 +91,6 @@ internal/ui/gfx.go", Lines: []core.DiffLine{
			{OldNo: 88, NewNo: 91, Prefix: ' ', Text: "func (c *Canvas) Layer() {"},
			{NewNo: 92, Prefix: '+', Text: "\tc.rx.layer++"},
			{NewNo: 93, Prefix: '+', Text: "\tc.dirty = true"},
			{OldNo: 89, NewNo: 94, Prefix: ' ', Text: "}"},
		}}
		a.modal = &Modal{
			Kind: modalDiff, Title: "应用补丁前确认（逐段审阅）", Path: "internal/ui/gfx.go",
			Hunks: []core.DiffHunk{h1, h2, h3}, AllowPartial: true,
			HunkAccept: map[int]bool{0: true, 1: false, 2: true},
			DiffResp:   make(chan []int, 1),
		}
	case "plan":
		a.modal = &Modal{
			Kind: modalPlan, Title: "方案待确认", Steps: 4,
			Plan: "## 渲染层拆分方案\n\n1. 把 `FillRect` 改为写入命令流，不再直接 GDI 填充。\n" +
				"2. `Flush` 分两趟：先光栅几何，再回放 GDI 文字，保证文字压在色块之上。\n" +
				"3. 增加 `Layer()` 管理覆盖层的 z 序，浮层单独起一层。\n" +
				"4. 补 `WIN7H_RXDUMP=1` 的调试转储，便于核对脏区计算。\n\n" +
				"**风险**：16bpp 下需保留 Bayer 抖动路径；改动集中在 `internal/ui/gfx.go`。",
			PlanResp: make(chan planDecision, 1),
		}
	}
}

// isDiffLine 供测试与调试使用：判断差异行前缀是否可着色。
func isDiffLine(p byte) bool { return p == ' ' || p == '+' || p == '-' || p == '@' }

// trimDiffText 去掉行尾空白，避免差异里出现一整屏无意义的空格。
func trimDiffText(s string) string { return strings.TrimRight(s, " \t\r") }
