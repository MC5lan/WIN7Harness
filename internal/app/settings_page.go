//go:build windows

package app

import (
	"fmt"
	"runtime"
	"strings"

	"win7harness/internal/core"
	"win7harness/internal/ui"
	"win7harness/internal/win32"
)

// 设置中心：左下角齿轮进入，一个页面内用顶部页签整合全部配置。
// 旧版是“左侧目录 + 七八张纵向长卡”，要滚很久才能找到一项；
// v1.32 收成 6 个页签，每个页签只展示当前主题下的配置，保存仍在顶栏。
var settingsTabs = []struct {
	key   string
	label string
}{
	{"model", "模型与上下文"},
	{"look", "界面与体验"},
	{"perm", "权限与安全"},
	{"network", "网络与更新"},
	{"budget", "预算与成本"},
	{"auto", "自动化"},
	{"tools", "工具与关于"},
}

type settingsPane struct {
	a    *App
	c    *ui.Canvas
	u    *UI
	x    int32
	cw   int32
	y    int32
	body int32
}

func (p *settingsPane) card(title string, h int32) ui.Rect {
	r := ui.R(p.x, p.y, p.cw-18, h)
	p.a.cardFrame(p.c, r, title)
	p.y += h + 14
	return r
}

func (a *App) paintSettings(c *ui.Canvas) {
	th := a.th
	u := a.ui
	W, H := a.winW, a.winH

	c.FillRect(ui.R(0, 0, W, H), th.Bg)
	a.paintRail(u, H)

	// 顶栏
	c.FillRect(ui.R(a.railW, 0, W-a.railW, 56), ui.Blend(th.Bg, th.Surface, 0.6))
	c.HLine(a.railW, 55, W-a.railW, th.BorderSoft)
	// 顶栏兜底命中区必须先登记：UI.HitAt 从后往前找，后登记的按钮才能优先。
	// 之前放在函数末尾，整条顶栏把“保存/返回”按钮吃掉了，点哪都没反应。
	a.ui.Hit("set.top", ui.R(a.railW, 0, W-a.railW, 56))
	c.TextFace(a.railW+24, 15, "设置与工具", ui.FsLg, 700, th.Text, ui.FaceUI)
	c.TextFace(a.railW+24+c.MeasureFace("设置与工具", ui.FsLg, 700, ui.FaceUI)+14, 20,
		"模型、外观、权限、网络与预算集中在这里 · Ctrl+, 打开", ui.FsXs, 400, th.TextMute, ui.FaceUI)
	if label := a.settingsDirtyLabel(); label != "" {
		c.TextFace(W-360, 20, label, ui.FsXs, 500, th.Warn, ui.FaceUI)
	}
	if u.Button("set.save.top", "保存设置", ui.R(W-232, 14, 104, 30), BtnPrimary) {
		a.queueUI(a.saveSettings)
	}
	if u.Button("set.back", "返回对话", ui.R(W-114, 14, 102, 30), BtnGhost) {
		if note := a.settingsDiscardNote(); note != "" {
			a.toast(note, true)
		}
		a.page = PageChat
	}

	// 内容宽度：大屏不铺满，避免表单被拉成一条；小屏贴边。
	avail := W - a.railW - 48
	cw := int32(920)
	if cw > avail {
		cw = avail
	}
	if cw < 520 {
		cw = avail
	}
	x := a.railW + (W-a.railW-cw)/2

	// 顶部页签：与内容同宽，选中态用珊瑚实底，和其它页面的页签保持一致。
	tabY := int32(66)
	tabH := int32(36)
	tw := cw / int32(len(settingsTabs))
	for i, t := range settingsTabs {
		r := ui.R(x+int32(i)*tw, tabY, tw-4, tabH)
		if i == len(settingsTabs)-1 {
			r.W = tw
		}
		active := a.settingsTab == i
		hover := u.Hit("set.tab."+t.key, r)
		hvt := u.hoverT("set.tab." + t.key)
		if active {
			c.Round(r, 8, th.Accent)
		} else if hvt > 0 {
			c.Round(r, 8, ui.Blend(th.Card, th.CardHover, hvt*0.8))
		}
		col := th.TextDim
		if active {
			col = 0xFFFFFF
		} else if hover {
			col = th.Text
		}
		fw := c.MeasureFace(t.label, ui.FsSm, 700, ui.FaceUI)
		c.TextFace(r.CX()-fw/2, r.Y+9, t.label, ui.FsSm, 700, col, ui.FaceUI)
		if u.Clicked("set.tab."+t.key) && !active {
			a.settingsTab = i
			core.Logf("[settings-tab] %d %s", i, t.label)
			a.settingsScrl = 0
			// 页签切换后内容整块替换，必须全帧重绘，避免旧页签的卡片残影。
			a.forceFull = true
			a.Redraw()
		}
	}

	// 内容区
	view := ui.R(x, 110, cw, H-110-32)
	c.Clip(view)
	c.FillRect(view, th.Bg)
	p := &settingsPane{a: a, c: c, u: u, x: x, cw: cw, y: 120 - int32(a.settingsScrl), body: H - 142}
	switch a.settingsTab {
	case 1:
		a.settingsLookTab(p)
	case 2:
		a.settingsPermTab(p)
	case 3:
		a.settingsNetTab(p)
	case 4:
		a.settingsBudgetTab(p)
	case 5:
		a.settingsAutomation(p)
		a.settingsHooks(p)
	case 6:
		a.settingsToolsTab(p)
	default:
		a.settingsModelTab(p)
	}
	a.settingsH = (p.y + 16) - (120 - int32(a.settingsScrl))
	c.PopClip()

	// 底部状态栏
	c.FillRect(ui.R(a.railW, H-32, W-a.railW, 32), th.Bg)
	c.HLine(a.railW, H-33, W-a.railW, th.BorderSoft)
	a.paintScrollHint(c, view)
	runMode := a.runMode
	if runMode == "" {
		runMode = "标准"
	}
	info := fmt.Sprintf("WIN7 Harness v%s · Go %s · %s · %s模式 · 配置：%s",
		core.Version, runtime.Version(), runtime.GOARCH, runMode, core.ConfigPath())
	c.TextFace(a.railW+24, H-26, ellipsis(c, info, ui.FsXs, 400, W-a.railW-48), ui.FsXs, 400, th.TextMute, ui.FaceUI)
	if a.testMsg != "" {
		col := th.TextMute
		if a.testOK != nil {
			if *a.testOK {
				col = th.Success
			} else {
				col = th.Danger
			}
		}
		c.TextFace(a.railW+24, H-48, ellipsis(c, a.testMsg, ui.FsXs, 400, W-a.railW-48), ui.FsXs, 400, col, ui.FaceUI)
	}
}

// applySettingsPreset 只切换接入方式，不替用户猜模型名。
func (a *App) applySettingsPreset(i int) {
	if i < 0 || i >= len(a.cfg.Providers) {
		return
	}
	a.presetIdx = i
	p := a.cfg.Providers[i]
	a.ui.SetField("set.base", p.BaseURL)
	// 每个端点有自己的 Key 和模型：切换接入方式时必须一起换，
	// 否则会把 A 家的 Key 填到 B 家，点保存后直接 401/串号。
	a.ui.SetField("set.key", p.APIKey)
	a.ui.SetField("set.model", p.Model)
	a.modelsMsg = ""
	a.Redraw()
}

func (a *App) settingsModelTab(p *settingsPane) {
	c, u := p.c, p.u
	th := a.th

	// 先算预设 chips 占几行，卡片高度随窗口宽度变化。
	iw := p.cw - 18 - 44
	rows := 1
	cx := int32(0)
	for _, prov := range a.cfg.Providers {
		w := c.MeasureFace(prov.Name, ui.FsXs, 500, ui.FaceUI) + 24
		if cx+w > iw && cx > 0 {
			rows++
			cx = 0
		}
		cx += w + 8
	}
	presetH := int32(rows) * 38
	cardH := int32(46) + presetH + int32(60*4) + 80
	if a.modelsMsg != "" {
		cardH += 24
	}
	r := p.card("模型与端点", cardH)
	ix := r.X + 22
	iy := r.Y + 46

	// 接入方式 chips
	cx = ix
	for i, prov := range a.cfg.Providers {
		name := prov.Name
		w := c.MeasureFace(name, ui.FsXs, 500, ui.FaceUI) + 24
		if cx+w > ix+iw && cx > ix {
			cx = ix
			iy += 38
		}
		cr := ui.R(cx, iy, w, 28)
		cid := fmt.Sprintf("set.preset:%d", i)
		hover := u.Hit(cid, cr)
		if u.Clicked(cid) {
			a.applySettingsPreset(i)
		}
		sel := i == a.presetIdx
		bg, txt, bd := th.Surface, th.TextDim, th.Border
		if sel {
			bg, txt, bd = th.AccentSoft, th.AccentText, th.Accent
		} else if hover {
			bg, txt = th.CardHover, th.Text
		}
		c.Round(cr, 14, bg)
		c.RoundStroke(cr, 14, bd)
		tw := c.MeasureFace(name, ui.FsXs, 500, ui.FaceUI)
		c.TextFace(cr.CX()-tw/2, cr.CY()-7, name, ui.FsXs, 500, txt, ui.FaceUI)
		cx += w + 8
	}
	iy += 38

	// Base URL
	c.TextFace(ix, iy, "接口地址 Base URL", ui.FsXs, 500, th.TextDim, ui.FaceUI)
	iy += 18
	u.TextField("set.base", ui.R(ix, iy, iw, 34), "", "https://你的端点/v1")
	iy += 44

	// API Key
	c.TextFace(ix, iy, "API Key", ui.FsXs, 500, th.TextDim, ui.FaceUI)
	hint := "只保存在本机；本地端点可留空"
	if pidx := a.safePreset(); pidx < len(a.cfg.Providers) && a.cfg.Providers[pidx].ID == "opencode-go" {
		hint = "OpenCode Go 的 Key 来自 opencode.ai/console；DeepSeek Key 不能填这里"
	} else if pidx < len(a.cfg.Providers) && a.cfg.Providers[pidx].ID == "custom" {
		hint = "DeepSeek 等：Base URL 填对应 /v1，Key 只适用于该服务商"
	}
	c.TextFace(ix+c.MeasureFace("API Key", ui.FsXs, 500, ui.FaceUI)+14, iy,
		ellipsis(c, hint, ui.FsXs, 400, iw-110), ui.FsXs, 400, th.TextMute, ui.FaceUI)
	iy += 18
	f := u.Field("set.key", "")
	f.Masked = true
	u.TextField("set.key", ui.R(ix, iy, iw-100, 34), "", "sk-...")
	eyeLabel := "显示密钥"
	if !f.Masked {
		eyeLabel = "隐藏密钥"
	}
	if u.Button("set.eye", eyeLabel, ui.R(ix+iw-92, iy, 64, 34), BtnGhost) {
		f.Masked = !f.Masked
	}
	iy += 44
	a.settingsModelBottom(c, u, ix, iw, iy)
	a.settingsModelExtras(p)
	a.settingsContextPresets(p)
}

// settingsModelBottom 绘制便携密钥状态、模型 ID、工作目录与测试按钮。
func (a *App) settingsModelBottom(c *ui.Canvas, u *UI, ix, iw, iy int32) {
	th := a.th
	// 便携模式：U 盘密码状态。没解锁时给一个入口，不强迫用户去猜。
	if a.runMode == "便携" {
		if core.PortableSecretsLocked() {
			c.TextFace(ix, iy, ellipsis(c, "U 盘密钥未解锁；调用模型前需要输入 U 盘密码", ui.FsXs, 400, iw-90),
				ui.FsXs, 400, th.Danger, ui.FaceUI)
			if u.Button("set.pwd.unlock", "解锁密钥", ui.R(ix+iw-88, iy-5, 80, 28), BtnGhost) {
				a.openUnlockModal(func() {
					a.ui.SetField("set.key", a.cfg.Provider().APIKey)
					a.toast("API Key 已解锁", false)
				})
			}
			iy += 30
		} else if core.PortableSecretsPresent() {
			c.TextFace(ix, iy, ellipsis(c, "已用 U 盘密码保护；换电脑时输入密码即可继续用", ui.FsXs, 400, iw),
				ui.FsXs, 400, th.TextMute, ui.FaceUI)
			iy += 26
		} else {
			c.TextFace(ix, iy, ellipsis(c, "便携模式：首次保存 API Key 时会要求设置 U 盘密码", ui.FsXs, 400, iw),
				ui.FsXs, 400, th.TextMute, ui.FaceUI)
			iy += 26
		}
	}

	// 模型 ID + 动态拉取
	c.TextFace(ix, iy, "模型 ID", ui.FsXs, 500, th.TextDim, ui.FaceUI)
	c.TextFace(ix+c.MeasureFace("模型 ID", ui.FsXs, 500, ui.FaceUI)+14, iy,
		ellipsis(c, "不内置模型名；可手填，也可从端点 /models 拉取（不消耗对话 token）",
			ui.FsXs, 400, iw-100), ui.FsXs, 400, th.TextMute, ui.FaceUI)
	iy += 18
	modelW := iw - 154
	if modelW < 180 {
		modelW = 180
	}
	u.TextField("set.model", ui.R(ix, iy, modelW, 34), "", "模型 ID")
	if u.Button("set.models.fetch", "拉取模型", ui.R(ix+modelW+8, iy, 80, 34), BtnGhost) {
		a.fetchModels(u.Field("set.base", "").String(), u.Field("set.key", "").String(), false)
	}
	if u.Button("set.models.pick", "选择模型", ui.R(ix+modelW+94, iy, 80, 34), BtnGhost) {
		a.openFetchedModelMenu(ui.R(ix+modelW+82, iy, 68, 34), u.Field("set.base", "").String(), false)
	}
	iy += 44
	if a.modelsMsg != "" {
		col := th.TextMute
		if a.modelsOK != nil {
			if *a.modelsOK {
				col = th.Success
			} else {
				col = th.Danger
			}
		}
		c.TextFace(ix, iy, ellipsis(c, a.modelsMsg, ui.FsXs, 400, iw), ui.FsXs, 400, col, ui.FaceUI)
		iy += 24
	}

	// 工作目录
	c.TextFace(ix, iy, "默认工作目录", ui.FsXs, 500, th.TextDim, ui.FaceUI)
	c.TextFace(ix+c.MeasureFace("默认工作目录", ui.FsXs, 500, ui.FaceUI)+14, iy,
		"新会话默认目录，可在顶栏单独切换", ui.FsXs, 400, th.TextMute, ui.FaceUI)
	iy += 18
	u.TextField("set.dir", ui.R(ix, iy, iw-156, 34), "", "")
	if u.Button("set.browsedir", "选择目录", ui.R(ix+iw-168, iy, 80, 34), BtnGhost) {
		a.queueUI(func() {
			var (
				d  string
				ok bool
			)
			a.beginNativeDialog()
			d, ok = win32.BrowseForFolder(0, "选择默认工作目录", u.Field("set.dir", "").String())
			a.endNativeDialog()
			if ok {
				u.SetField("set.dir", d)
				_ = core.EnsureWorkDir(d)
			}
		})
	}
	if u.Button("set.opendir", "打开目录", ui.R(ix+iw-80, iy, 72, 34), BtnGhost) {
		win32.ShellOpen(u.Field("set.dir", "").String())
	}
	iy += 48

	if u.Button("set.test", "测试连接", ui.R(ix, iy, 96, 32), BtnGhost) {
		a.testConnection(strings.TrimSpace(u.Field("set.base", "").String()),
			strings.TrimSpace(u.Field("set.key", "").String()), strings.TrimSpace(u.Field("set.model", "").String()))
	}
	if u.Button("set.doctor", "诊断连接", ui.R(ix+104, iy, 80, 32), BtnGhost) {
		a.cmdDoctorForm()
	}
	c.TextFace(ix+196, iy+8, ellipsis(c, "测试会发一条最短对话（约几个 token）；诊断会测 /models、对话和工具", ui.FsXs, 400, iw-196), ui.FsXs, 400, th.TextMute, ui.FaceUI)
}
func (a *App) settingsLookTab(p *settingsPane) {
	c, u := p.c, p.u
	th := a.th
	r := p.card("外观与交互", 340)
	ix := r.X + 22
	iy := r.Y + 50
	iw := r.W - 44

	c.TextFace(ix, iy+4, "外观主题", ui.FsSm, 400, th.Text, ui.FaceUI)
	tx := ix + 120
	cur := strings.ToLower(strings.TrimSpace(a.cfg.Theme))
	if cur == "" {
		cur = "light"
	}
	if ui.SystemHighContrast() {
		// 系统辅助功能优先级高于应用偏好；按钮上如实显示当前生效值。
		cur = "hc"
	}
	for _, opt := range []struct{ id, label, val string }{
		{"set.theme.light", "浅色", "light"},
		{"set.theme.dark", "深色", "dark"},
		{"set.theme.hc", "高对比", "hc"},
	} {
		kind := BtnGhost
		if cur == opt.val {
			kind = BtnPrimary
		}
		if u.Button(opt.id, opt.label, ui.R(tx, iy, 76, 28), kind) {
			a.cfg.Theme = opt.val
			a.th = ui.ResolveTheme(opt.val)
			ui.SetTokens(a.th.Tokens())
			a.forceFull = true
		}
		tx += 84
	}
	iy += 44

	c.TextFace(ix, iy+4, "对话字号", ui.FsSm, 400, th.Text, ui.FaceUI)
	if u.Button("set.font-", "−", ui.R(ix+120, iy, 30, 26), BtnGhost) {
		if a.cfg.FontScale > 80 {
			a.cfg.FontScale -= 10
		}
		a.layKey = layKey{}
		a.ver++
	}
	c.TextFace(ix+162, iy+5, fmt.Sprintf("%d%%", a.cfg.FontScale), ui.FsSm, 700, th.Text, ui.FaceMono)
	if u.Button("set.font+", "+", ui.R(ix+212, iy, 30, 26), BtnGhost) {
		if a.cfg.FontScale < 200 {
			a.cfg.FontScale += 10
		}
		a.layKey = layKey{}
		a.ver++
	}
	c.TextFace(ix+256, iy+4, "只放大对话正文与代码块，工具栏保持原尺寸", ui.FsXs, 400, th.TextMute, ui.FaceUI)
	iy += 42

	c.TextFace(ix, iy+4, "Enter 键发送", ui.FsSm, 400, th.Text, ui.FaceUI)
	if v := u.ToggleHit("set.enter", ui.R(ix, iy-4, 320, 28), ui.R(ix+120, iy, 38, 20), a.cfg.SendOnEnter); v != a.cfg.SendOnEnter {
		a.cfg.SendOnEnter = v
	}
	iy += 38

	c.TextFace(ix, iy+4, "显示思考过程", ui.FsSm, 400, th.Text, ui.FaceUI)
	if v := u.ToggleHit("set.think", ui.R(ix, iy-4, 320, 28), ui.R(ix+120, iy, 38, 20), a.cfg.ShowThinking); v != a.cfg.ShowThinking {
		a.cfg.ShowThinking = v
		a.showThink = v
		a.ver++
	}
	iy += 38

	gdiOn := strings.EqualFold(a.cfg.RenderMode, "gdi")
	c.TextFace(ix, iy+4, "兼容渲染（GDI）", ui.FsSm, 400, th.Text, ui.FaceUI)
	if v := u.ToggleHit("set.gdi", ui.R(ix, iy-4, 420, 28), ui.R(ix+120, iy, 38, 20), gdiOn); v != gdiOn {
		if v {
			a.cfg.RenderMode = "gdi"
		} else {
			a.cfg.RenderMode = ""
		}
		a.toast("渲染模式已切换，重启程序后生效", false)
	}
	c.TextFace(ix+170, iy+4, "老显卡/16bpp/远程桌面下更稳；默认矢量渲染更细腻", ui.FsXs, 400, th.TextMute, ui.FaceUI)
	iy += 38

	a.settingsAutomation(p)

	_ = iw
}

func (a *App) settingsPermTab(p *settingsPane) {
	c, u := p.c, p.u
	th := a.th
	r := p.card("权限（审批模式）", 180)
	ix := r.X + 22
	iy := r.Y + 50
	iw := r.W - 44
	permCur := ""
	permRules := 0
	if a.eng != nil && a.eng.Policy != nil {
		permCur = a.eng.Policy.Mode
		permRules = len(a.eng.Policy.FlatRules())
	}
	opts := []struct{ id, label, mode string }{
		{"set.perm.auto", "自动执行", "auto"},
		{"set.perm.ask", "每次确认", "ask"},
		{"set.perm.plan", "只读计划", "plan"},
	}
	gap := int32(8)
	bw := (iw - gap*2) / 3
	for i, o := range opts {
		kind := BtnGhost
		if permCur == o.mode {
			kind = BtnPrimary
		}
		if u.Button(o.id, o.label, ui.R(ix+int32(i)*(bw+gap), iy, bw, 32), kind) {
			a.setPermMode(o.mode)
		}
	}
	iy += 42
	modeText := map[string]string{
		"auto": "自动执行：仅低风险操作直接放行；删除/复制/解释器等中危命令仍逐次确认",
		"ask":  "每次确认：写类操作与高危命令都逐次询问",
		"plan": "只读计划：禁止改文件与执行高危命令",
	}[permCur]
	if modeText == "" {
		modeText = "当前模式：" + permCur
	}
	c.TextFace(ix, iy, modeText, ui.FsXs, 500, th.TextDim, ui.FaceUI)
	iy += 22
	c.TextFace(ix, iy, fmt.Sprintf("规则 %d 条 · 高危命令始终确认 · 工作区之外的写入始终询问 · 高级规则用 /perm add 添加", permRules),
		ui.FsXs, 400, th.TextMute, ui.FaceUI)

	r2 := p.card("安全边界（内嵌提示）", 150)
	ix2 := r2.X + 22
	iy2 := r2.Y + 48
	c.TextFace(ix2, iy2, "系统提示已内嵌 Win7 安全边界：不改 C:\\Windows 与 Program Files，不读取/外传密钥与浏览器密码，", ui.FsXs, 400, th.TextDim, ui.FaceUI)
	c.TextFace(ix2, iy2+20, "破坏性操作先说明后果。用户拒绝的操作，模型不会换一种方式偷偷执行。", ui.FsXs, 400, th.TextDim, ui.FaceUI)
	c.TextFace(ix2, iy2+44, "计划模式只允许读取；写入、删除、执行命令会被系统直接拒绝，而不是只写在提示词里。", ui.FsXs, 400, th.TextMute, ui.FaceUI)
	c.TextFace(ix2, iy2+68, "权限规则只是第二道闸：模型侧提示词负责“不主动越界”，程序侧审批负责“越界也做不成”。", ui.FsXs, 400, th.TextMute, ui.FaceUI)
}

func (a *App) settingsNetTab(p *settingsPane) {
	c, u := p.c, p.u
	th := a.th
	r := p.card("网络与证书", 300)
	ix := r.X + 22
	iy := r.Y + 50
	iw := r.W - 44

	c.TextFace(ix, iy+4, "代理", ui.FsSm, 400, th.Text, ui.FaceUI)
	mode := a.cfg.ProxyMode
	if mode == "" {
		mode = "system"
	}
	px := ix + 120
	for _, opt := range []struct{ id, mode, label string }{
		{"set.px-sys", "system", "跟随系统"},
		{"set.px-dir", "direct", "直连"},
		{"set.px-man", "manual", "手动"},
	} {
		kind := BtnGhost
		if mode == opt.mode {
			kind = BtnPrimary
		}
		if u.Button(opt.id, opt.label, ui.R(px, iy, 88, 28), kind) {
			if a.streaming || (a.eng != nil && a.eng.Running()) {
				a.toast("生成中不能切换代理，先停止或等本轮结束", true)
				px += 96
				continue
			}
			a.cfg.ProxyMode = opt.mode
			if cv := a.currentConv(); cv != nil {
				a.syncEngineTranscript(cv)
			}
			a.rebuildEngine()
			a.restoreHistory()
			a.toast("代理方式已切换："+opt.label, false)
		}
		px += 96
	}
	iy += 38
	c.TextFace(ix, iy, a.proxyHint(), ui.FsXs, 400, th.TextMute, ui.FaceUI)
	iy += 22
	u.TextField("set.proxyurl", ui.R(ix, iy, iw, 32), "", "http://127.0.0.1:7890 或 socks=127.0.0.1:1080")
	iy += 48

	c.TextFace(ix, iy, "补充根证书（PEM）", ui.FsXs, 500, th.TextDim, ui.FaceUI)
	c.TextFace(ix+c.MeasureFace("补充根证书（PEM）", ui.FsXs, 500, ui.FaceUI)+14, iy,
		"Win7 根证书库停在 2009；公司代理/杀软 MITM 时把它自己的根 CA 填这里", ui.FsXs, 400, th.TextMute, ui.FaceUI)
	iy += 18
	u.TextField("set.tlsca", ui.R(ix, iy, iw-100, 32), "", `C:\certs\corp-root.pem`)
	if u.Button("set.tlsca-pick", "选择证书", ui.R(ix+iw-92, iy, 84, 32), BtnGhost) {
		a.queueUI(func() {
			a.beginNativeDialog()
			picked := win32.OpenCertDialog(0, "选择根证书")
			a.endNativeDialog()
			if picked != "" {
				a.ui.SetField("set.tlsca", picked)
			}
		})
	}
	iy += 44

	c.TextFace(ix, iy+4, "跳过证书校验", ui.FsSm, 400, th.Text, ui.FaceUI)
	if v := u.ToggleHit("set.tlsinsecure", ui.R(ix, iy-4, 420, 28), ui.R(ix+120, iy, 38, 20), a.cfg.TLSInsecure); v != a.cfg.TLSInsecure {
		a.cfg.TLSInsecure = v
		if v {
			a.toast("已开启跳过证书校验，保存后生效；仅建议排障时临时使用", true)
		}
	}
	c.TextFace(ix+170, iy+3, "仅排障用；默认关闭，避免中间人证书被静默接受", ui.FsXs, 400, th.TextMute, ui.FaceUI)

	// —— 自动更新（GitHub Releases + SHA256SUMS）——
	rUpd := p.card("自动更新", 250)
	ux := rUpd.X + 22
	uy := rUpd.Y + CardBodyY
	c.TextFace(ux, uy, "更新源", ui.FsXs, 500, th.TextDim, ui.FaceUI)
	sources := []struct {
		id, label, val string
	}{
		{"set.updsrc.gh", "GitHub Releases", core.UpdateSourceGitHub},
		{"set.updsrc.mf", "签名清单", core.UpdateSourceManifest},
	}
	px = ux + 80
	for _, s := range sources {
		kind := BtnGhost
		if strings.EqualFold(a.cfg.UpdateSource, s.val) {
			kind = BtnPrimary
		}
		if u.Button(s.id, s.label, ui.R(px, uy-6, 132, 30), kind) {
			a.cfg.UpdateSource = s.val
			a.forceFull = true
			a.Redraw()
		}
		px += 140
	}
	uy += 42
	c.TextFace(ux, uy, "GitHub 仓库", ui.FsXs, 500, th.TextDim, ui.FaceUI)
	u.TextField("set.updrepo", ui.R(ux+100, uy-8, 320, 32), "", core.DefaultUpdateRepo)
	c.TextFace(ux+432, uy+2, "owner/repo；GitHub 只做发现，包必须带 SHA256SUMS.txt", ui.FsXs, 400, th.TextMute, ui.FaceUI)
	uy += 46
	if v := u.ToggleHit("set.updcheck", ui.R(ux, uy-6, 420, 28), ui.R(ux+120, uy, 38, 20), a.cfg.CheckUpdateOnStart); v != a.cfg.CheckUpdateOnStart {
		a.cfg.CheckUpdateOnStart = v
	}
	c.TextFace(ux+170, uy+2, "启动时检查一次更新", ui.FsXs, 400, th.TextMute, ui.FaceUI)
	uy += 34
	if v := u.ToggleHit("set.updauto", ui.R(ux, uy-6, 420, 28), ui.R(ux+120, uy, 38, 20), a.cfg.AutoDownloadUpdate); v != a.cfg.AutoDownloadUpdate {
		a.cfg.AutoDownloadUpdate = v
	}
	c.TextFace(ux+170, uy+2, "发现新版本时自动下载并校验（不会自动替换正在运行的程序）", ui.FsXs, 400, th.TextMute, ui.FaceUI)
	uy += 40
	if u.Button("set.updchecknow", "立即检查更新", ui.R(ux, uy, 120, 32), BtnPrimary) {
		go a.checkUpdateQuiet()
		a.toast("已在后台检查更新，结果会出现在通知里", false)
	}
	if u.Button("set.updapply", "安装已下载更新", ui.R(ux+130, uy, 140, 32), BtnGhost) {
		a.cmdUpdateApply("")
	}
	c.TextFace(ux+284, uy+8, "安装会先暂存，重启后替换；失败自动回滚", ui.FsXs, 400, th.TextMute, ui.FaceUI)
}

func (a *App) settingsBudgetTab(p *settingsPane) {
	c, u := p.c, p.u
	th := a.th
	r := p.card("预算与限制", 420)
	ix := r.X + 22
	iy := r.Y + 50
	iw := r.W - 44

	c.TextFace(ix, iy, "花费上限（美元）", ui.FsXs, 500, th.TextDim, ui.FaceUI)
	u.TextField("set.maxcost", ui.R(ix, iy+18, 110, 32), "", "0")
	c.TextFace(ix+126, iy, "Token 上限", ui.FsXs, 500, th.TextDim, ui.FaceUI)
	u.TextField("set.budget", ui.R(ix+126, iy+18, 120, 32), "", "0")
	c.TextFace(ix+262, iy+4, "0 表示不限制", ui.FsXs, 400, th.TextMute, ui.FaceUI)
	c.TextFace(ix+262, iy+24, "达到上限自动停止调用模型", ui.FsXs, 400, th.TextMute, ui.FaceUI)
	iy += 64

	c.TextFace(ix, iy, "轮数上限", ui.FsXs, 500, th.TextDim, ui.FaceUI)
	u.TextField("set.rounds", ui.R(ix, iy+18, 96, 32), "", "0")
	c.TextFace(ix+108, iy, "上下文窗口", ui.FsXs, 500, th.TextDim, ui.FaceUI)
	u.TextField("set.ctxwin", ui.R(ix+108, iy+18, 104, 32), "", "128000")
	c.TextFace(ix+224, iy, "子代理超时", ui.FsXs, 500, th.TextDim, ui.FaceUI)
	u.TextField("set.subto", ui.R(ix+224, iy+18, 96, 32), "", "1200")
	c.TextFace(ix+332, iy+4, "轮数 0 = 不限制（大工程建议 0 或 300+）", ui.FsXs, 400, th.TextMute, ui.FaceUI)
	c.TextFace(ix+332, iy+24, "窗口 0 = 128k 兜底；超时 0 = 1200 秒", ui.FsXs, 400, th.TextMute, ui.FaceUI)
	iy += 64

	a.settingsContextPresets(p)

	c.TextFace(ix, iy, "自定义单价（美元 / 百万 token）", ui.FsXs, 500, th.TextDim, ui.FaceUI)
	c.TextFace(ix+c.MeasureFace("自定义单价（美元 / 百万 token）", ui.FsXs, 500, ui.FaceUI)+14, iy,
		"不内置价目表；填 0 用保守默认价估算", ui.FsXs, 400, th.TextMute, ui.FaceUI)
	iy += 18
	c.TextFace(ix, iy, "输入", ui.FsXs, 500, th.TextDim, ui.FaceUI)
	c.TextFace(ix+126, iy, "输出", ui.FsXs, 500, th.TextDim, ui.FaceUI)
	iy += 18
	u.TextField("set.pricein", ui.R(ix, iy, 110, 32), "", "0")
	u.TextField("set.priceout", ui.R(ix+126, iy, 120, 32), "", "0")
	iy += 50

	lowLabel := "低内存模式：关"
	if core.LowMemory() {
		lowLabel = "低内存模式：开"
	}
	if u.Button("set.lowmem", lowLabel, ui.R(ix, iy, 150, 32), BtnGhost) {
		on := !core.LowMemory()
		core.SetLowMemory(on)
		a.cfg.LowMemory = on
		if err := a.cfg.Save(); err != nil {
			a.toast("保存失败："+err.Error(), true)
		} else if on {
			a.toast("低内存模式已开启：预算/索引/图片阈值收紧，建议随后重建索引（/index）", false)
		} else {
			a.toast("已回到标准档位", false)
		}
	}
	c.TextFace(ix+160, iy+4, "省内存：@引用 48KB · 工具输出 96KB · 索引 1200 文件 · 图片 1024px/2 张", ui.FsXs, 400, th.TextMute, ui.FaceUI)
	if u.Button("set.diag", "打开自检与诊断", ui.R(ix+iw-152, iy, 144, 32), BtnGhost) {
		a.page = PageDiag
		if len(a.diag.results) == 0 {
			a.runChecksAsync()
		}
	}
}

// settingsToolsTab 收纳过去堆在左侧 rail 上的二级页面。
// v1.39 UI 重做后，左侧只留「对话 / 全部会话 / 设置与工具」，
// 这些低频但重要的页面全部以「图标 + 中文名 + 一句说明」的形式出现在这里，
// 不用再猜一个 32px 图标到底是什么意思。
func (a *App) settingsToolsTab(p *settingsPane) {
	c, u := p.c, p.u
	th := a.th
	type tool struct {
		id, glyph, label, desc string
		page                   Page
	}
	tools := []tool{
		{"tool.diag", "ico:check", "自检与诊断", "配置、工具链、网络连通性", PageDiag},
		{"tool.auto", "ico:clock", "定时任务", "让助手按计划自动执行", PageAuto},
		{"tool.ext", "ico:grid", "技能与扩展", "技能、MCP 连接器与工具", PageExt},
		{"tool.usage", "ico:chart", "用量统计", "token、调用次数与耗时", PageUsage},
		{"tool.workspace", "ico:panel", "工作区", "项目文件与索引状态（侧栏也可进入）", PageWorkspace},
		{"tool.costs", "ico:coin", "费用", "按模型与会话查看花费", PageCosts},
		{"tool.notices", "ico:bell", "通知", "更新与系统提醒", PageNotices},
	}
	cols := 2
	if p.cw >= 760 {
		cols = 3
	}
	rows := (len(tools) + cols - 1) / cols
	cardH := int32(70 + rows*80)
	r := p.card("工具与面板", cardH)
	ix := r.X + cardPad
	iy := r.Y + CardBodyY
	gap := int32(12)
	cw := (r.W - cardPad*2 - gap*int32(cols-1)) / int32(cols)
	for i, t := range tools {
		col, row := int32(i%cols), int32(i/cols)
		cell := ui.R(ix+col*(cw+gap), iy+row*80, cw, 70)
		hover := u.Hit(t.id, cell)
		bg := th.Surface
		if hover {
			bg = ui.Blend(th.Surface, th.AccentSoft, 0.35)
		}
		c.Round(cell, 10, bg)
		c.RoundStroke(cell, 10, ui.Blend(th.BorderSoft, th.Accent, 0.12))
		ib := ui.R(cell.X+12, cell.Y+19, 32, 32)
		c.Round(ib, 8, ui.Blend(th.Card, th.AccentSoft, 0.55))
		if ik, ok := iconKind(t.glyph); ok {
			drawIcon(c, ik, ui.R(ib.X+8, ib.Y+8, 16, 16), th.Accent)
		}
		c.TextFace(cell.X+54, cell.Y+16, t.label, ui.FsSm, 700, th.Text, ui.FaceUI)
		c.TextFace(cell.X+54, cell.Y+39, t.desc, ui.FsXs, 400, th.TextMute, ui.FaceUI)
		if u.Clicked(t.id) {
			a.page = t.page
			if t.page == PageDiag && len(a.diag.results) == 0 {
				a.runChecksAsync()
			}
			if t.page == PageWorkspace {
				a.invalidateWorkspace()
			}
			if t.page == PageSessions {
				a.sessDirty = true
			}
			a.forceFull = true
			a.Redraw()
		}
	}
	c.TextFace(ix, iy+int32(rows)*80+6, "提示：设置里的更改需要点右上角「保存设置」才会写入配置。", ui.FsXs, 400, th.TextMute, ui.FaceUI)
	// Git 身份与关于信息也归到「工具与关于」，不再藏在「高级」里。
	a.settingsGitAbout(p)
}
