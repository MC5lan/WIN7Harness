//go:build windows

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"win7harness/internal/core"
	"win7harness/internal/ui"
	"win7harness/internal/win32"
)

// 本文件由 app.go 按职责拆分（T07），仅移动代码，未改行为。
// 绘制：主框架、聊天区、侧栏、输入区、检查器与通用绘制辅助。

// drawerDim 在抽屉外侧压一层半透明遮罩，把"浮层层级"变成看得见的事实。
// 只压被抽屉盖住的那块会话区：rail、内嵌侧栏、内嵌检查器都不压，
// 它们是"还能点的"区域，压暗会误导成不可用。
func drawerDim(c *ui.Canvas, th *ui.Theme, x, w, h int32) {
	if w <= 0 {
		return
	}
	c.FillRectAlpha(ui.R(x, 0, w, h), th.Tokens().BGOverlay, overlayAlpha)
}

// drawerShade 在抽屉边缘外侧画一条渐变阴影，用最少的绘制交代浮层层级。
// left 为 true 时阴影画在 x 左侧（检查器从右缘滑出用），否则画在右侧。
//
// 用逐列 alpha 而不是"和底色预混"：预混只对纯色底成立，一旦下面是会话正文
// （白卡片、代码块），预混出来的灰条会变成一条明显的脏带。
func drawerShade(c *ui.Canvas, x, y, h int32, left bool) {
	const w = 14
	for i := int32(0); i < w; i++ {
		t := float32(w-i) / float32(w) // 靠边最浓，往外衰减
		col := x + i
		if left {
			col = x - w + i
		}
		c.FillRectAlpha(ui.R(col, y, 1, h), 0x000000, 0.30*t*t)
	}
}

// paintHandles 画两条可拖拽分隔条：静止时只是一条细线，
// 悬停或拖拽时变成强调色竖条，明确告诉用户这里能拉伸。
func (a *App) paintHandles(c *ui.Canvas, H int32) {
	mark := func(x int32, id int) {
		if a.dragMode == id || (a.dragMode == 0 && a.hbHover == id) {
			c.FillRect(ui.R(x-1, 0, 3, H), ui.Alpha(a.th.Bg, a.th.Accent, 0.8))
			// 抓手：拖拽时在两处显示短横，像真实的拖拽手柄
			if a.dragMode == id {
				c.FillRect(ui.R(x-2, H/2-14, 5, 28), a.th.Accent)
			}
			return
		}
		c.VLine(x, 0, H, a.th.BorderSoft)
	}
	if a.showSide && a.sideW > 8 {
		mark(a.railW+a.sideW-1, 1)
	}
	if a.showInsp && a.inspW > 8 {
		mark(a.winW-a.inspW, 2)
	}
}

// Paint 绘制一帧。
func (a *App) Paint(c *ui.Canvas) {
	a.beat()
	if a.w != nil {
		state := uint32(win32.TBPF_NOPROGRESS)
		if a.streaming || (a.eng != nil && a.eng.Running()) {
			state = win32.TBPF_INDETERMINATE
		}
		win32.SetTaskbarProgress(a.w.Hwnd(), state)
	}
	if a.perf != nil && a.perf.on {
		t0 := time.Now()
		defer func() { a.observePaint(time.Since(t0)) }()
	}
	if a.ui == nil {
		a.ui = NewUI(c, a.th)
		// 表单初值只在 UI 建立时填一次：过去 initForms 从未被调用，
		// 引导页四个框全是空的；顺便给引导页一个初始焦点，打开即可打字。
		a.initForms()
		if a.page == PageOnboard {
			a.ui.SetFocus("ob.base")
		}
	}
	a.maybePromptPortableUnlock()
	a.ui.c = c
	a.ui.th = a.th
	// primitive/kit 组件从全局 currentTokens 取文字色，而背景来自 a.th。
	// 主题切换后若两者分叉，就会出现"浅底白字"的整页故障。每帧开头统一注入，
	// 让令牌只有 a.th 一个真值来源（原来只在 gallery 分支调用，业务页漏了）。
	ui.SetTokens(a.th.Tokens())
	a.phase += 0.03
	a.ui.Begin(a.mx, a.my, a.phase)
	// 页面/页签切换：命令流会整块换掉，但背景 Clear 命令完全相同，
	// 脏区算法不会重绘"新页面没有画到的旧像素"。这里强制一次全帧。
	key := fmt.Sprintf("%d/%d/%d/%d/%d", a.page, a.autoTab, a.extTab, a.useTab, a.diag.tab)
	if key != a.uiKey {
		a.uiKey = key
		a.forceFull = true
		// 每次进入设置页都从当前配置重铺表单：避免上次未保存的编辑
		// 或者“会话里刚换过 provider”让设置页显示旧数据。
		if a.page == PageSettings {
			a.initForms()
			a.settingsScrl = 0
		}
		if perfProfOn {
			core.Logf("[page] %s", a.pageName())
		}
	}
	if a.forceFull {
		// 面板刚换了形态：这一帧按全屏重放，抹掉上一形态留下的像素碎片。
		a.forceFull = false
		c.InvalidateFrame()
	}

	if a.winW == 0 {
		a.winW, a.winH = c.W(), c.H()
		a.layout()
	}

	c.Clear(a.th.Bg)
	// 组件矩阵页：WIN7H_GALLERY=1 时只画组件与状态，不进业务路径。
	// 用途是把"视觉验收"变成可截图的静态画面（改令牌后逐格核对）。
	if os.Getenv("WIN7H_GALLERY") == "1" {
		ui.DrawGallery(c, a.winW, a.winH)
		return
	}
	// 视觉验收：WIN7H_SHOT_MODAL=diff|plan 用样例数据挂一个模态，
	// 不必接真实 LLM 就能截图核对差分弹窗/方案弹窗的排版（与 GALLERY 同属调试入口）。
	if mode := os.Getenv("WIN7H_SHOT_MODAL"); mode != "" && a.modal == nil {
		a.installShotModal(mode)
	}
	switch a.page {
	case PageOnboard:
		a.paintOnboard(c)
	case PageSettings:
		a.paintSettings(c)
	case PageDiag:
		a.paintDiag(c)
	case PageCompare:
		a.paintCompare(c)
	case PageAuto:
		a.paintAuto(c)
	case PageExt:
		a.paintExt(c)
	case PageUsage:
		a.paintUsage(c)
	case PageSessions:
		a.paintSessionsPage(c)
	case PageWorkspace:
		a.paintWorkspacePage(c)
	case PageCosts:
		a.paintCostsPage(c)
	case PageNotices:
		a.paintNotices(c)
	default:
		a.paintChat(c)
	}
	// 浮层各起一层：canvas 后端的几何与文本是两趟画的，不分层的话
	// 浮层底色压不住下层正文的文字（详见 Canvas.Layer 的注释）。
	if a.palOpen {
		c.Layer()
		a.paintPalette(c)
	}
	if a.histOpen {
		c.Layer()
		a.paintHistory(c)
	}
	if a.menu != nil {
		c.Layer()
		a.paintMenu(c)
	}
	if a.modal != nil {
		c.Layer()
		a.paintModal(c)
	}
	if a.qrVisible() {
		c.Layer()
		a.paintQR(c)
	}
	c.Layer()
	a.paintToast(c)
}

func (a *App) paintChat(c *ui.Canvas) {
	th := a.th
	u := a.ui
	W, H := a.winW, a.winH
	topH := a.topH()

	// 背景分区
	c.FillRect(ui.R(0, 0, W, H), th.Bg)

	// 侧栏背景（用实际宽度判定，折叠动画期间仍绘制）
	if a.sideW > 0 {
		a.paintSideBG(c, H)
	}
	// 主区：抽屉模式下不占位，起点不含抽屉宽度
	mx := a.railW + a.sideOcc
	c.FillRect(ui.R(mx, 0, a.chatW, H), th.Bg)
	// 右侧抽屉浮在会话区上：顶栏按钮与输入区让开被遮挡的部分，避免被压住点不到
	rightGap := int32(0)
	if a.inspDrawer && a.inspW > 8 {
		rightGap = a.inspW
	}
	cw := a.chatW - rightGap
	// 顶栏用整条主区宽度：右侧抽屉覆盖它也无所谓（抽屉自己有标题与关闭钮），
	// 反而按 cw 排版会把一整排按钮挤到侧栏上，和标题叠字。
	topW := a.chatW

	a.paintChatHeader(c, u, mx, topW, topH, H)
	a.paintChatBody(c, u, mx, cw, topH, H)
}

// paintChatHeader 绘制顶栏、会话标题、状态与操作按钮。
func (a *App) paintChatHeader(c *ui.Canvas, u *UI, mx, topW, topH, H int32) {
	th := a.th
	// 顶栏
	c.FillRect(ui.R(mx, 0, a.chatW, topH), ui.Blend(th.Bg, th.Surface, 0.6))
	c.HLine(mx, topH-1, a.chatW, th.Border)

	// 会话标题
	conv := a.currentConv()
	title := "新对话"
	if conv != nil && conv.Title != "" {
		title = conv.Title
	}
	c.TextFace(mx+20, 14, title, ui.FsMd, 700, th.Text, ui.FaceUI)

	// 状态：呼吸点 + 状态文本
	st := "就绪"
	color := th.Success
	if a.streaming {
		st = "生成中"
		color = th.Accent
	}
	tw := c.MeasureFace(title, ui.FsMd, 700, ui.FaceUI)
	dotX := mx + 30 + tw
	g := 0.5 + 0.5*float32((1+sin(a.phase*2.4))/2)
	c.Dot(dotX, 23, 3, ui.Alpha(th.Bg, color, g))
	c.TextFace(dotX+9, 15, st, ui.FsXs, 400, th.TextMute, ui.FaceUI)
	// 用量摘要常驻顶栏：预算与 token 不该只有发 /status 才看得见。
	if a.eng != nil {
		sw := c.MeasureFace(st, ui.FsXs, 400, ui.FaceUI)
		if line := a.usageSummary(); line != "" {
			ux := dotX + 9 + sw + 10
			limit := mx + topW - 380
			if ux < limit {
				c.TextFace(ux, 15, ellipsis(c, "· "+line, ui.FsXs, 400, limit-ux), ui.FsXs, 400, th.TextMute, ui.FaceUI)
			}
		}
	}

	// 顶栏右侧按钮。
	// 间距收紧到 36，并且全部用矢量图标——原来其中一个按钮写的是汉字"搜"，
	// 和其余图标混排时字重/基线都不一样，一排看过去就是"没对齐"。
	by := int32(11)
	bw := int32(34)
	x := mx + topW - 46
	if u.IconButton("act.insp", ui.R(x, by, bw, 28), "ico:panel", a.showInsp) {
		a.toggleInspector()
	}
	x -= 36
	// 主题按钮不再显示高亮：它是"切一下"，不是"当前选中"，
	// 一直亮着会让人以为这是一组单选按钮。
	if u.IconButton("act.theme", ui.R(x, by, bw, 28), "ico:theme", false) {
		a.toggleTheme()
	}
	x -= 36
	if u.IconButton("act.find", ui.R(x, by, bw, 28), "ico:search", false) {
		a.openPaletteWith("/find ")
	}
	x -= 36
	if u.IconButton("act.new", ui.R(x, by, bw, 28), "ico:plus", false) {
		a.editReset()
		a.newConv()
	}
	// 图标组与工作区之间加一条竖线：不加的话"目录名 + 五个图标"挤成一片，
	// 看不出哪个是"当前上下文"、哪个是"操作"。
	c.VLine(x-12, by+6, 16, th.BorderSoft)

	// 工作区入口：显示本会话实际生效的目录，点开就地切换
	wcR := ui.R(x-158, by+1, 148, 26)
	hoverW := u.Hit("act.workdir", wcR)
	if hoverW {
		c.Round(wcR, 6, ui.Blend(th.Bg, th.Surface, 0.9))
	}
	{
		wd := a.convWorkDir()
		name := "未设置工作区"
		if wd != "" {
			name = filepath.Base(wd)
			if name == "" || name == "." || name == string(filepath.Separator) {
				name = wd
			}
		}
		drawIcon(c, "folder", ui.R(wcR.X+8, wcR.Y+5, 15, 16), th.TextMute)
		tw2 := c.MeasureFace(name, ui.FsXs, 400, ui.FaceUI)
		label := ellipsis(c, name, ui.FsXs, 400, 148-50)
		c.TextFace(wcR.X+28, wcR.Y+6, label, ui.FsXs, 400, th.TextDim, ui.FaceUI)
		_ = tw2
		if a.sessionDirPinned() {
			c.Dot(wcR.X+28+c.MeasureFace(label, ui.FsXs, 400, ui.FaceUI)+7, wcR.Y+12, 2, th.Accent)
		}
		drawIcon(c, "caret-down", ui.R(wcR.Right()-20, wcR.Y+6, 14, 14), th.TextMute)
	}
	if u.Clicked("act.workdir") {
		a.editReset()
		a.openDirMenu(wcR, false)
	}
}

// paintChatBody 绘制消息区、输入区、检查器、rail 与抽屉浮层。
func (a *App) paintChatBody(c *ui.Canvas, u *UI, mx, cw, topH, H int32) {
	th := a.th
	// Rail 挪到最后一层画（见下方调用处）。

	// 会话列表：内嵌态现在画（占位、和主区并排）；
	// 抽屉态挪到最后一层画——浮层必须压在会话正文之上，否则正文会穿透到抽屉里。
	if a.sideW > 0 && !a.sideDrawer {
		a.paintConvList(u, H, topH)
	}

	// 消息区
	chatTop := topH
	inputH := a.inputHeight()
	botGap := int32(12)
	if a.dense() {
		botGap = 8
	}
	chatBot := H - inputH - botGap
	// 底部多留 18px：末行内容不贴着输入框边缘，避免长消息被裁成半行。
	clip := ui.R(mx, chatTop, cw, chatBot-chatTop-18)
	// 每次进入都走增量排版：内容没变的消息整段复用，只有流式尾条会真正重排。
	a.layoutSession(c)
	// 排版完成才知道目标消息的纵向位置，跳转在这里消费
	if a.jumpMsg >= 0 {
		if y := a.msgTopOffset(a.jumpMsg); y >= 0 {
			a.scroll = float32(y) - 24
			if a.scroll < 0 {
				a.scroll = 0
			}
			if max := float32(a.contentH - clip.H); max > 0 && a.scroll > max {
				a.scroll = max
			}
		}
		a.jumpMsg = -1
	}
	// 流式结束会改变最后一条消息的高度（Markdown 收尾、工具卡/操作条出现）。
	// 事件处理器当时用的是上一帧 contentH，可能把 scroll 设到新内容高度之外，
	// 结果整屏只剩背景；这里按本帧排版结果统一夹紧，并让 autoScroll 真正到底。
	a.scroll = clampChatScroll(a.scroll, float32(a.contentH), float32(clip.H), a.autoScroll)
	a.drawSession(u, clip)

	// 滚动条
	if a.contentH > clip.H {
		u.Scrollbar(ui.R(mx+cw-8, clip.Y+4, 6, clip.H-8), a.contentH, clip.H, int32(a.scroll))
	}

	// 输入区
	a.paintInput(u, mx, chatBot, cw, H)

	// Inspector：内嵌时贴会话区右侧（先画，让抽屉态可以盖住它）
	if a.inspW > 0 && !a.inspDrawer {
		a.paintInspector(u, mx+cw, a.inspW, H, topH)
	}

	// 分隔条在这里画：它属于"底图"，
	// 放到浮层里会把图层的回写范围拉成"从分隔条到窗口右缘"的一条大矩形，
	// 于是浮层回写时把遮罩区的文字全擦掉（浮层只该擦自己盖住的那块）。
	// 左侧栏放最后画。
	//
	// 它是最靠左、最常被鼠标扫过的控件区，而 hover 会给每个图标多画一条圆角底
	// ——命令数一变，脏区算法就从那条命令一路算到末尾，等于整屏重绘（实测 54 帧里
	// 8 帧全屏、峰值 60ms）。把它挪到最后，差异落在命令流末端，前缀能覆盖绝大部分，
	// 脏区就只剩 rail 那一小块。rail 的区域不与任何内容重叠，z 序放最后是安全的。
	a.paintRail(u, H)

	a.paintHandles(c, H)

	// 抽屉浮层：遮罩 → 阴影 → 面板本体，三步都在最后一层。
	if a.sideW > 0 && a.sideDrawer {
		c.Layer()
		x0 := a.railW + a.sideW
		if x1 := mx + cw; x1 > x0 {
			drawerDim(c, th, x0, x1-x0, H)
		}
		drawerShade(c, a.railW+a.sideW, 0, H, false)
		// 抽屉是浮层：必须先铺自己的底，否则会透出下层正文。
		a.paintSideBG(c, H)
		a.paintConvList(u, H, topH)
		// 抽屉态没有标题栏可点，必须给一个显眼的关闭钮，
		// 否则用户只能靠点空白区猜（而空白区点下去还可能是别的控件）。
		a.ui.IconButton("drw.side.close", ui.R(a.railW+a.sideW-30, 8, 22, 22), "ico:close", false)
	}
	if a.inspW > 0 && a.inspDrawer {
		c.Layer()
		ix := a.winW - a.inspW
		if w := ix - mx; w > 0 {
			drawerDim(c, th, mx, w, H)
		}
		drawerShade(c, ix, 0, H, true)
		a.paintInspector(u, ix, a.inspW, H, topH)
	}
}

// paintSideBG 铺侧栏底色与右缘分隔线。
// 内嵌态在画正文之前铺一次；抽屉态要在浮层层再铺一次（正文会盖掉前面那层）。
func (a *App) paintSideBG(c *ui.Canvas, H int32) {
	if a.sideW <= 0 {
		return
	}
	c.FillRect(ui.R(a.railW, 0, a.sideW, H), a.th.Surface2)
	c.VLine(a.railW+a.sideW-1, 0, H, a.th.Border)
}

// railItem 画一个暖色胶囊 rail 按钮：图标悬停带暖色底，选中态最暖，
// 和 Claude/Raycast 的 rail 一致——外层一颗小圆角方块，而不是裸图标。
func railItem(c *ui.Canvas, u *UI, id, glyph string, x, y int32, active bool) bool {
	th := u.th
	r := ui.R(x, y, 32, 32)
	hover := u.Hit(id, r)
	_ = hover
	hvt := u.hoverT(id)
	bg := uint32(0)
	if active {
		bg = ui.Blend(th.Card, th.AccentSoft, 0.85)
	} else if hvt > 0 {
		bg = ui.Blend(th.Card, th.CardHover, hvt*0.9)
	}
	if bg != 0 {
		c.Round(r, 8, bg)
	}
	if active {
		c.RoundStroke(r, 8, ui.Blend(th.BorderSoft, th.Accent, 0.35))
	}
	kind := glyph
	fg := th.TextDim
	if active {
		fg = th.Accent
	} else if hvt > 0 {
		fg = th.Text
	}
	if ik, ok := iconKind(kind); ok {
		drawIcon(c, ik, ui.R(r.CX()-8, r.CY()-8, 16, 16), fg)
	}
	if u.focus == id {
		c.RoundStroke(r.Inset(-2), 10, th.InputFocus)
	}
	return u.Clicked(id)
}

func (a *App) paintRail(u *UI, H int32) {
	c := u.c
	th := a.th
	c.FillRect(ui.R(0, 0, a.railW, H), th.Surface2)
	c.VLine(a.railW-1, 0, H, th.BorderSoft)

	// Logo（warm 珊瑚）
	DrawLogo(c, 15, 12, 26, th.Accent, th.Surface2)

	// 用暖色胶囊 rail 按钮替代裸图标
	y := int32(58)
	step := int32(40)
	if railItem(c, u, "rail.new", "ico:plus", 12, y, false) {
		a.page = PageChat
		a.newConv()
	}
	y += step
	if railItem(c, u, "rail.side", "ico:menu", 12, y, a.showSide) {
		a.page = PageChat
		a.toggleDrawer()
	}
	y += step
	if railItem(c, u, "rail.diag", "ico:check", 12, y, a.page == PageDiag) {
		a.page = PageDiag
		if len(a.diag.results) == 0 {
			a.runChecksAsync()
		}
	}
	y += step
	if railItem(c, u, "rail.auto", "ico:clock", 12, y, a.page == PageAuto) {
		a.page = PageAuto
	}
	y += step
	if railItem(c, u, "rail.ext", "ico:grid", 12, y, a.page == PageExt) {
		a.page = PageExt
	}
	y += step
	if railItem(c, u, "rail.use", "ico:chart", 12, y, a.page == PageUsage) {
		a.page = PageUsage
	}
	y += step
	if railItem(c, u, "rail.sessions", "ico:star", 12, y, a.page == PageSessions) {
		a.page = PageSessions
		a.sessDirty = true
	}
	y += step
	if railItem(c, u, "rail.workspace", "ico:panel", 12, y, a.page == PageWorkspace) {
		a.page = PageWorkspace
		a.invalidateWorkspace()
	}
	y += step
	if railItem(c, u, "rail.costs", "ico:coin", 12, y, a.page == PageCosts) {
		a.page = PageCosts
	}
	y += step
	if railItem(c, u, "rail.notices", "ico:bell", 12, y, a.page == PageNotices) {
		a.page = PageNotices
	}

	// 左下角：设置入口固定在 rail 底部，和常见桌面应用一致；
	// 上面的业务页再多，设置的位置也不会随列表长度漂移。
	gearY := H - 92
	if gearY < y+8 {
		gearY = y + 8
	}
	if gearY > 64 {
		c.HLine(12, gearY-12, a.railW-24, th.BorderSoft)
	}
	if railItem(c, u, "rail.settings", "ico:gear", 12, gearY, a.page == PageSettings) {
		a.page = PageSettings
		a.settingsScrl = 0
	}
	// 左下角整块都是设置热区：齿轮只有 32px，老人机/触摸屏/高 DPI 下
	// 精确点中小图标并不容易；版本号本身不承载其他动作，可以一起让给设置。
	u.Hit("rail.settings", ui.R(8, H-170, 40, H-30-(H-170)))

	// 底部版本号
	c.TextFace(16, H-30, "v"+core.Version, ui.FsXs, 400, th.TextMute, ui.FaceUI)
}

func (a *App) paintConvList(u *UI, H, topH int32) {
	c := u.c
	th := a.th
	x := a.railW
	// 头部
	c.TextFace(x+16, 14, "会话", ui.FsSm, 700, th.TextDim, ui.FaceUI)
	btn := ui.R(x+a.sideW-82, 8, 70, 24)
	if u.Button("conv.new", "+ 新建", btn, BtnGhost) {
		a.editReset()
		a.newConv()
	}

	// 搜索框：标题与正文都能搜，输入即过滤
	q := u.TextField("conv.q", ui.R(x+8, topH, a.sideW-16, 30), "", "搜索标题或内容…")
	if u.Clicked("conv.q") {
		a.editReset()
		u.SetFocus("conv.q")
	}
	matches := a.searchConvs(q)
	rowH, rowGap := int32(52), int32(56)
	if a.dense() {
		rowH, rowGap = 46, 50
	}
	// 折叠动画收尾段宽度过窄时只留底色，避免文字被挤压变形
	if a.sideW < 64 {
		a.convListH = 0
		return
	}
	y := topH + 34

	listClip := ui.R(x, topH+30, a.sideW, H-topH-70)
	c.Clip(listClip)
	defer c.PopClip()

	if len(matches) == 0 {
		c.TextFace(x+16, topH+52, "没有匹配的会话", ui.FsSm, 400, th.TextMute, ui.FaceUI)
		a.convListH = 0
		return
	}

	for _, m := range matches {
		i := m.idx
		cv := a.convs[i]
		r := ui.R(x+8, y-int32(a.convScrl), a.sideW-16, rowH)
		if r.Y > listClip.Bottom() || r.Bottom() < listClip.Y {
			y += rowGap
			continue
		}
		hover := u.Hit(fmt.Sprintf("conv:%d", i), r)
		bg := uint32(0)
		if i == a.active {
			bg = th.SelectBg
		} else if hover {
			bg = th.CardHover
		}
		if bg != 0 {
			c.Round(r, 8, bg)
		}
		if i == a.active {
			c.Round(ui.R(r.X+2, r.Y+12, 4, r.H-24), 2, th.Accent)
		}

		if i == a.editingID {
			// 就地改名：输入框直接嵌在标题位置，回车提交、Esc 取消
			name := u.TextField("conv.ren", ui.R(r.X+8, r.Y+10, r.W-56, 30), "", "会话名称")
			a.convRename = name
			c.TextFace(r.X+12, r.Y+42, "回车保存 · Esc 取消", ui.FsXs, 400, th.TextMute, ui.FaceUI)
			y += 56
			continue
		}

		t := cv.Title
		if t == "" {
			t = "新对话"
		}
		titleW := r.W - 24
		if hover {
			// 只有鼠标悬停才露出置顶/改名/删除：常态与选中态保持清爽，
			// 选中已经由左侧色条和底色表达，不需要再挂三个图标。
			titleW = r.W - 92
			pinGlyph := "ico:star-off"
			if cv.Pinned {
				pinGlyph = "ico:star"
			}
			if u.IconButton(fmt.Sprintf("conv.pin:%d", i), ui.R(r.Right()-72, r.Y+16, 20, 20), pinGlyph, cv.Pinned) {
			}
			if u.IconButton(fmt.Sprintf("conv.ren:%d", i), ui.R(r.Right()-48, r.Y+16, 20, 20), "ico:edit", false) {
			}
			if u.IconButton(fmt.Sprintf("conv.del:%d", i), ui.R(r.Right()-24, r.Y+16, 20, 20), "ico:close", false) {
			}
		}
		c.TextFace(r.X+12, r.Y+10, ellipsis(c, t, ui.FsBase, 500, titleW), ui.FsBase, 500, th.Text, ui.FaceUI)
		count := len(cv.Msgs)
		if !cv.Loaded {
			count = cv.MsgN
		}
		sub := fmt.Sprintf("%d 条消息 · %s", count, humanWhen(cv.At))
		if m.sip != "" {
			sub = ellipsis(c, m.sip, ui.FsXs, 400, r.W-24)
		}
		c.TextFace(r.X+12, r.Y+30, sub, ui.FsXs, 400, th.TextMute, ui.FaceUI)
		y += 56
	}
	a.convListH = y - (topH + 38)
}

func ellipsis(c *ui.Canvas, s string, size, weight, maxW int32) string {
	if c.MeasureFace(s, size, weight, ui.FaceUI) <= maxW {
		return s
	}
	rs := []rune(s)
	for len(rs) > 1 {
		rs = rs[:len(rs)-1]
		if c.MeasureFace(string(rs)+"…", size, weight, ui.FaceUI) <= maxW {
			return string(rs) + "…"
		}
	}
	return string(rs)
}

func humanWhen(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "刚刚"
	case d < time.Hour:
		return fmt.Sprintf("%d 分钟前", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d 小时前", int(d.Hours()))
	}
	return t.Format("01-02")
}

func (a *App) paintInput(u *UI, x, y, w, H int32) { a.paintInputBox(u, x, y, w, H) }

func (a *App) paintInspector(u *UI, x, w, H, topH int32) {
	c := u.c
	th := a.th
	c.FillRect(ui.R(x, 0, w, H), th.Surface)
	c.VLine(x, 0, H, th.BorderSoft)
	if w < 120 {
		return
	}

	// 检查器以浮层抽屉出现时，标题行右端给一个关闭钮。
	if a.inspDrawer {
		a.ui.IconButton("drw.insp.close", ui.R(x+w-30, 8, 22, 22), "ico:close", false)
	}
	c.TextFace(x+16, 16, "工作区", ui.FsSm, 700, th.TextDim, ui.FaceUI)
	wd := a.convWorkDir()
	if wd == "" {
		wd = core.ExeDir()
	}
	c.TextFace(x+16, 38, ellipsis(c, wd, ui.FsXs, 400, w-32), ui.FsXs, 400, th.TextMute, ui.FaceMono)

	yy := int32(70)
	if a.dense() {
		yy = 56
	}
	c.HLine(x+16, yy-10, w-32, th.BorderSoft)

	// 上下文预算：把“模型还能装多少”常驻在侧栏；详情交给 /context。
	if a.eng != nil {
		msgs, _, histTok, window, limit := a.eng.ContextInfo()
		toolN, _ := a.eng.ToolDefsStats()
		c.TextFace(x+16, yy, "上下文", ui.FsSm, 700, th.TextDim, ui.FaceUI)
		yy += 20
		barW := w - 32
		if barW > 8 {
			used := histTok
			if used > limit {
				used = limit
			}
			c.Round(ui.R(x+16, yy, barW, 6), 3, ui.Blend(th.Surface, th.Border, 0.6))
			if used > 0 {
				den := limit
				if den < 1 {
					den = 1
				}
				fw := int32(float32(barW) * float32(used) / float32(den))
				if fw < 2 {
					fw = 2
				}
				col := th.Accent
				if used*10 >= limit*8 {
					col = th.Warn
				}
				c.Round(ui.R(x+16, yy, fw, 6), 3, col)
			}
			yy += 14
		}
		c.TextFace(x+16, yy, ellipsis(c,
			fmt.Sprintf("历史 %d 条 · 约 %s / %s token · 工具 %d 个", msgs, humanTokens(histTok), humanTokens(limit), toolN),
			ui.FsXs, 400, barW), ui.FsXs, 400, th.TextMute, ui.FaceUI)
		yy += 18
		c.TextFace(x+16, yy, ellipsis(c,
			fmt.Sprintf("窗口 %s · 压缩 %d 次 · /context 看分段", humanTokens(window), a.eng.CompactCount()),
			ui.FsXs, 400, barW), ui.FsXs, 400, th.TextMute, ui.FaceUI)
		yy += 18
		if sk := a.eng.ActiveSkill(); sk != "" {
			c.TextFace(x+16, yy, "技能 "+ellipsis(c, sk, ui.FsXs, 400, barW-40), ui.FsXs, 400, th.Accent, ui.FaceUI)
			yy += 18
		}
		yy += 4
		c.HLine(x+16, yy-8, w-32, th.BorderSoft)
	}
	yy = a.paintTimeline(u, x, w, H, yy)

	// 耗时归因：把 /stats 的核心结论常驻在侧栏，不用每次手打命令。
	if a.eng != nil {
		if modelMs, toolMs := a.eng.ModelToolMs(); modelMs+toolMs > 0 {
			c.TextFace(x+16, yy, "耗时归因", ui.FsSm, 700, th.TextDim, ui.FaceUI)
			yy += 22
			barW := w - 32
			total := float32(modelMs + toolMs)
			mw := int32(float32(barW) * float32(modelMs) / total)
			c.Round(ui.R(x+16, yy, barW, 6), 3, ui.Blend(th.Surface, th.Border, 0.6))
			if mw > 0 {
				c.Round(ui.R(x+16, yy, mw, 6), 3, th.Accent)
			}
			if barW-mw > 0 {
				c.Round(ui.R(x+16+mw, yy, barW-mw, 6), 3, ui.Blend(th.Surface, th.TextMute, 0.55))
			}
			yy += 14
			c.TextFace(x+16, yy, fmt.Sprintf("模型 %.1fs · 工具 %.1fs", float64(modelMs)/1000, float64(toolMs)/1000),
				ui.FsXs, 400, th.TextMute, ui.FaceUI)
			yy += 18
			if tp := a.eng.Throughput(); tp != "" && !strings.HasPrefix(tp, "尚未") {
				c.TextFace(x+16, yy, ellipsis(c, tp, ui.FsXs, 400, barW), ui.FsXs, 400, th.TextMute, ui.FaceUI)
				yy += 18
			}
			if stats := a.eng.StatItems(3); len(stats) > 0 {
				s := stats[0]
				line := fmt.Sprintf("最慢 %s ×%d · %s", ToolTitle(s.Name), s.Calls, durText(s.Total))
				c.TextFace(x+16, yy, ellipsis(c, line, ui.FsXs, 400, barW), ui.FsXs, 400, th.TextMute, ui.FaceUI)
				yy += 18
			}
			yy += 6
			c.HLine(x+16, yy-8, w-32, th.BorderSoft)
		}
	}

	yy = a.paintTaskSide(u, x, w, H, yy)

	step := int32(24)
	if a.dense() {
		step = 21
	}
	c.TextFace(x+16, yy, "本次任务", ui.FsSm, 700, th.TextDim, ui.FaceUI)
	yy += step

	// 最近的工具调用
	var tools []*ToolCard
	for i := len(a.msgs) - 1; i >= 0 && len(tools) < 12; i-- {
		for j := len(a.msgs[i].Tools) - 1; j >= 0 && len(tools) < 12; j-- {
			tools = append([]*ToolCard{a.msgs[i].Tools[j]}, tools...)
		}
	}
	if len(tools) == 0 {
		c.TextFace(x+16, yy, "暂无工具调用", ui.FsXs, 400, th.TextMute, ui.FaceUI)
	}
	for _, t := range tools {
		if yy > H-40 {
			break
		}
		col := th.TextMute
		if !t.Done {
			col = th.Warn
		} else if t.Err {
			col = th.Danger
		} else {
			col = th.Success
		}
		c.Dot(x+20, yy+7, 3, col)
		c.TextFace(x+30, yy, ellipsis(c, ToolTitle(t.Name), ui.FsXs, 400, w-50), ui.FsXs, 400, th.TextDim, ui.FaceUI)
		yy += 22
		s := ArgSummary(t.Name, t.Args)
		if s != "" {
			c.TextFace(x+30, yy-4, ellipsis(c, s, ui.FsXs, 400, w-60), ui.FsXs, 400, th.TextMute, ui.FaceMono)
			yy += 16
		}
	}
}
