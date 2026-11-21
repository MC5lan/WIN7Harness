//go:build windows

package app

import (
	"fmt"
	"runtime"
	"strconv"
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
	{"model", "模型"},
	{"look", "外观"},
	{"perm", "权限"},
	{"network", "网络"},
	{"budget", "预算"},
	{"advanced", "高级"},
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
	DrawLogo(c, a.railW+24, 16, 24, th.Accent, th.Bg)
	c.TextFace(a.railW+56, 15, "设置", ui.FsLg, 700, th.Text, ui.FaceUI)
	c.TextFace(a.railW+56+c.MeasureFace("设置", ui.FsLg, 700, ui.FaceUI)+14, 20,
		"模型、外观、权限、网络与预算集中在这里 · Ctrl+, 打开", ui.FsXs, 400, th.TextMute, ui.FaceUI)
	if u.Button("set.save.top", "保存", ui.R(W-206, 14, 84, 30), BtnPrimary) {
		a.queueUI(a.saveSettings)
	}
	if u.Button("set.back", "← 返回对话", ui.R(W-114, 14, 102, 30), BtnGhost) {
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

	// 顶部页签：与内容同宽，选中态用暖珊瑚底 + 下划线。
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
			c.Round(r, 8, th.AccentSoft)
			c.RoundStroke(r, 8, ui.Blend(th.BorderSoft, th.Accent, 0.35))
			c.FillRect(ui.R(r.X+14, r.Bottom()-2, r.W-28, 2), th.Accent)
		} else if hvt > 0 {
			c.Round(r, 8, ui.Blend(th.Card, th.CardHover, hvt*0.8))
		}
		col := th.TextDim
		if active {
			col = th.AccentText
		} else if hover {
			col = th.Text
		}
		fw := c.MeasureFace(t.label, ui.FsSm, 700, ui.FaceUI)
		c.TextFace(r.CX()-fw/2, r.Y+9, t.label, ui.FsSm, 700, col, ui.FaceUI)
		if u.Clicked("set.tab."+t.key) && !active {
			a.settingsTab = i
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
		a.settingsAdvancedTab(p)
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
	// 顶栏最后注册，吸收滚动内容溢出到顶栏区域的点击。
	a.ui.Hit("set.top", ui.R(a.railW, 0, W-a.railW, 56))
}

// applySettingsPreset 只切换接入方式，不替用户猜模型名。
func (a *App) applySettingsPreset(i int) {
	if i < 0 || i >= len(a.cfg.Providers) {
		return
	}
	a.presetIdx = i
	p := a.cfg.Providers[i]
	a.ui.SetField("set.base", p.BaseURL)
	if p.NoAuth {
		a.ui.SetField("set.key", "")
	}
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
	if u.Button("set.eye", "显示", ui.R(ix+iw-92, iy, 44, 34), BtnGhost) {
		f.Masked = !f.Masked
	}
	iy += 44
	a.settingsModelBottom(c, u, ix, iw, iy)
}

// settingsModelBottom 绘制便携密钥状态、模型 ID、工作目录与测试按钮。
func (a *App) settingsModelBottom(c *ui.Canvas, u *UI, ix, iw, iy int32) {
	th := a.th
	// 便携模式：U 盘密码状态。没解锁时给一个入口，不强迫用户去猜。
	if a.runMode == "便携" {
		if core.PortableSecretsLocked() {
			c.TextFace(ix, iy, ellipsis(c, "U 盘密钥未解锁；调用模型前需要输入 U 盘密码", ui.FsXs, 400, iw-90),
				ui.FsXs, 400, th.Danger, ui.FaceUI)
			if u.Button("set.pwd.unlock", "解锁", ui.R(ix+iw-72, iy-5, 64, 28), BtnGhost) {
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
	if u.Button("set.models.fetch", "拉取", ui.R(ix+modelW+8, iy, 68, 34), BtnGhost) {
		a.fetchModels(u.Field("set.base", "").String(), u.Field("set.key", "").String(), false)
	}
	if u.Button("set.models.pick", "选择", ui.R(ix+modelW+82, iy, 68, 34), BtnGhost) {
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
	if u.Button("set.browsedir", "选择…", ui.R(ix+iw-148, iy, 72, 34), BtnGhost) {
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
	if u.Button("set.opendir", "打开", ui.R(ix+iw-72, iy, 72, 34), BtnGhost) {
		win32.ShellOpen(u.Field("set.dir", "").String())
	}
	iy += 48

	if u.Button("set.test", "测试连接", ui.R(ix, iy, 96, 32), BtnSubtle) {
		a.testConnection(strings.TrimSpace(u.Field("set.base", "").String()),
			strings.TrimSpace(u.Field("set.key", "").String()), strings.TrimSpace(u.Field("set.model", "").String()))
	}
	if u.Button("set.doctor", "诊断", ui.R(ix+104, iy, 64, 32), BtnGhost) {
		a.cmdDoctorForm()
	}
	c.TextFace(ix+180, iy+8, ellipsis(c, "测试会发一条最短对话（约几个 token）；诊断会测 /models、对话和工具", ui.FsXs, 400, iw-180), ui.FsXs, 400, th.TextMute, ui.FaceUI)
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

	verifyOn := strings.EqualFold(a.cfg.VerifyMode, "auto")
	c.TextFace(ix, iy+4, "自动验证（改完跑测试）", ui.FsSm, 400, th.Text, ui.FaceUI)
	if v := u.ToggleHit("set.verify", ui.R(ix, iy-4, 500, 28), ui.R(ix+170, iy, 38, 20), verifyOn); v != verifyOn {
		if v {
			a.cfg.VerifyMode = "auto"
		} else {
			a.cfg.VerifyMode = "off"
		}
		if a.eng != nil {
			a.eng.VerifyMode = a.cfg.VerifyMode
		}
		if err := a.cfg.Save(); err != nil {
			a.toast("保存失败："+err.Error(), true)
		}
	}
	c.TextFace(ix+220, iy+4, "模型准备收尾时自动构建+测试；失败把摘要回灌继续修（最多 3 轮）", ui.FsXs, 400, th.TextMute, ui.FaceUI)
	iy += 38

	c.TextFace(ix, iy+4, "子任务并行上限", ui.FsSm, 400, th.Text, ui.FaceUI)
	if u.Button("set.st-", "−", ui.R(ix+120, iy, 26, 24), BtnGhost) {
		if a.cfg.MaxSubTasks > 1 {
			a.cfg.MaxSubTasks--
		}
	}
	c.TextFace(ix+160, iy+5, strconv.Itoa(a.cfg.MaxSubTasks), ui.FsSm, 700, th.Text, ui.FaceMono)
	if u.Button("set.st+", "+", ui.R(ix+186, iy, 26, 24), BtnGhost) {
		if a.cfg.MaxSubTasks < 8 {
			a.cfg.MaxSubTasks++
		}
	}
	c.TextFace(ix+222, iy+4, "1~8，task 批量派发的并发数", ui.FsXs, 400, th.TextMute, ui.FaceUI)
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
		"auto": "自动执行：非高危操作直接放行，高危命令仍逐次确认",
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
			a.cfg.ProxyMode = opt.mode
			a.rebuildEngine()
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
	u.TextField("set.tlsca", ui.R(ix, iy, iw-70, 32), "", `C:\certs\corp-root.pem`)
	if u.Button("set.tlsca-pick", "浏览", ui.R(ix+iw-62, iy, 62, 32), BtnGhost) {
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
}

func (a *App) settingsBudgetTab(p *settingsPane) {
	c, u := p.c, p.u
	th := a.th
	r := p.card("预算与限制", 330)
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
	c.TextFace(ix+332, iy+24, "窗口 0 = 按 128k 估；超时 0 = 1200 秒", ui.FsXs, 400, th.TextMute, ui.FaceUI)
	iy += 64

	c.TextFace(ix, iy, "自定义单价（美元 / 百万 token）", ui.FsXs, 500, th.TextDim, ui.FaceUI)
	c.TextFace(ix+c.MeasureFace("自定义单价（美元 / 百万 token）", ui.FsXs, 500, ui.FaceUI)+14, iy,
		"不内置价目表；填 0 用保守默认价估算", ui.FsXs, 400, th.TextMute, ui.FaceUI)
	iy += 18
	c.TextFace(ix, iy+9, "输入", ui.FsXs, 500, th.TextDim, ui.FaceUI)
	u.TextField("set.pricein", ui.R(ix+40, iy, 90, 32), "", "0")
	c.TextFace(ix+144, iy+9, "输出", ui.FsXs, 500, th.TextDim, ui.FaceUI)
	u.TextField("set.priceout", ui.R(ix+184, iy, 90, 32), "", "0")
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
	if u.Button("set.diag", "自检与诊断 >", ui.R(ix+iw-132, iy, 132, 32), BtnGhost) {
		a.page = PageDiag
		if len(a.diag.results) == 0 {
			a.runChecksAsync()
		}
	}
}

func (a *App) settingsAdvancedTab(p *settingsPane) {
	c, u := p.c, p.u
	th := a.th
	iw := p.cw - 18 - 44

	hks := a.cfg.Hooks
	hooksH := int32(150) + int32(len(hks))*34
	if len(hks) == 0 {
		hooksH = 190
	}
	r := p.card("工具钩子（hooks）", hooksH)
	ix := r.X + 22
	iy := r.Y + 48
	if len(hks) == 0 {
		c.TextFace(ix, iy, "还没有钩子。钩子能在工具执行前后自动跑本地命令，例如改完文件自动 gofmt。", ui.FsXs, 400, th.TextMute, ui.FaceUI)
		iy += 26
	}
	for i := range hks {
		h := hks[i]
		row := ui.R(ix, iy, iw, 30)
		c.Round(row, 6, ui.Blend(th.Surface, th.Bg, 0.55))
		c.TextFace(row.X+10, row.Y+7, h.Event+" · "+h.Match, ui.FsXs, 700, th.Text, ui.FaceMono)
		cm := h.Command
		if len([]rune(cm)) > 46 {
			cm = string([]rune(cm)[:45]) + "…"
		}
		c.TextFace(row.X+210, row.Y+7, cm, ui.FsXs, 400, th.TextDim, ui.FaceMono)
		if u.Button("hook.del:"+strconv.Itoa(i), "删除", ui.R(row.Right()-58, row.Y+3, 52, 24), BtnSubtle) {
			a.cfg.Hooks = append(a.cfg.Hooks[:i], a.cfg.Hooks[i+1:]...)
			if err := a.cfg.Save(); err != nil {
				a.toast("保存失败："+err.Error(), true)
			} else {
				a.toast("已删除钩子", false)
			}
			a.Redraw()
		}
		iy += 34
	}
	c.TextFace(ix, iy, "新增：事件 / 工具匹配 / 命令", ui.FsXs, 500, th.TextDim, ui.FaceUI)
	iy += 18
	u.TextField("hook.event", ui.R(ix, iy, 118, 32), "", "post_tool")
	u.TextField("hook.match", ui.R(ix+126, iy, 152, 32), "", "write_file,edit_file")
	u.TextField("hook.cmd", ui.R(ix+286, iy, iw-286-84, 32), "", "gofmt -w .")
	if u.Button("hook.add", "添加", ui.R(ix+iw-76, iy, 76, 32), BtnGhost) {
		a.addHook()
	}
	iy += 40
	c.TextFace(ix, iy, "事件限 pre_tool / post_tool / turn_end；pre_tool 以非零退出码结束会阻止本次调用，超时默认 30 秒。", ui.FsXs, 400, th.TextMute, ui.FaceUI)

	// 生成参数：不常用，但不能因为“设置页变小”就把能力删掉。
	rGen := p.card("生成参数", 120)
	ixn := rGen.X + 22
	iyn := rGen.Y + 50
	c.TextFace(ixn, iyn, "温度", ui.FsXs, 500, th.TextDim, ui.FaceUI)
	u.TextField("set.temp", ui.R(ixn, iyn+18, 80, 32), "", "0.3")
	c.TextFace(ixn+100, iyn, "最大输出 token", ui.FsXs, 500, th.TextDim, ui.FaceUI)
	u.TextField("set.maxtok", ui.R(ixn+100, iyn+18, 100, 32), "", "8192")
	c.TextFace(ixn+218, iyn+4, "温度 0 更稳定，写代码建议 0.2~0.4；最大输出按端点上限填。", ui.FsXs, 400, th.TextMute, ui.FaceUI)

	gitCardH := int32(160)
	if iw < 600 {
		gitCardH = 222
	}
	rGit := p.card("Git 身份（提交作者）", gitCardH)
	ixg := rGit.X + 22
	yyg := rGit.Y + 50
	git := a.uiData()
	gName, gEmail, gTemp := git.gitName, git.gitEmail, git.gitTemp
	if git.gitAt.IsZero() {
		c.TextFace(ixg, yyg, "正在读取 Git 身份…", ui.FsXs, 400, th.TextMute, ui.FaceUI)
	} else {
		if a.ui.Field("set.gitname", "").String() == "" && gName != "" {
			a.ui.SetField("set.gitname", gName)
			a.ui.SetField("set.gitmail", gEmail)
		}
		note := "当前：" + gName + " <" + gEmail + ">"
		if gTemp {
			note += "（临时身份，提交历史里会显示成这个名字）"
		}
		c.TextFace(ixg, yyg, ellipsis(c, note, ui.FsXs, 400, iw), ui.FsXs, 400, th.TextMute, ui.FaceUI)
	}
	yyg += 22
	if iw >= 600 {
		c.TextFace(ixg, yyg, "名字", ui.FsXs, 500, th.TextDim, ui.FaceUI)
		u.TextField("set.gitname", ui.R(ixg, yyg+18, 180, 32), "", "你的名字")
		c.TextFace(ixg+196, yyg, "邮箱", ui.FsXs, 500, th.TextDim, ui.FaceUI)
		u.TextField("set.gitmail", ui.R(ixg+196, yyg+18, 220, 32), "", "you@example.com")
		if u.Button("set.gitsave", "写入 git 全局配置", ui.R(ixg+432, yyg+18, 160, 32), BtnGhost) {
			name := strings.TrimSpace(u.Field("set.gitname", "").String())
			mail := strings.TrimSpace(u.Field("set.gitmail", "").String())
			if err := core.SetGitIdentity(name, mail); err != nil {
				a.toast(err.Error(), true)
			} else {
				a.toast("已写入 git 全局身份："+name+" <"+mail+">", false)
			}
			a.Redraw()
		}
	} else {
		c.TextFace(ixg, yyg, "名字", ui.FsXs, 500, th.TextDim, ui.FaceUI)
		u.TextField("set.gitname", ui.R(ixg, yyg+18, iw, 32), "", "你的名字")
		yyg += 58
		c.TextFace(ixg, yyg, "邮箱", ui.FsXs, 500, th.TextDim, ui.FaceUI)
		u.TextField("set.gitmail", ui.R(ixg, yyg+18, iw, 32), "", "you@example.com")
		if u.Button("set.gitsave", "写入 git 全局配置", ui.R(ixg, yyg+58, 160, 32), BtnGhost) {
			name := strings.TrimSpace(u.Field("set.gitname", "").String())
			mail := strings.TrimSpace(u.Field("set.gitmail", "").String())
			if err := core.SetGitIdentity(name, mail); err != nil {
				a.toast(err.Error(), true)
			} else {
				a.toast("已写入 git 全局身份："+name+" <"+mail+">", false)
			}
			a.Redraw()
		}
	}

	rAbout := p.card("关于", 120)
	ixa := rAbout.X + 22
	iya := rAbout.Y + 48
	c.TextFace(ixa, iya, "WIN7 Harness · Win7 SP1 原生自绘 UI · Go 1.20.14 · 单文件 EXE", ui.FsXs, 500, th.TextDim, ui.FaceUI)
	c.TextFace(ixa, iya+22, "只兼容 OpenAI Chat Completions 协议；模型名从端点动态获取，不随应用内置。", ui.FsXs, 400, th.TextMute, ui.FaceUI)
	c.TextFace(ixa, iya+44, "OpenCode Go / Go Plus 使用 "+core.OpenCodeGoBaseURL+"，模型 ID 粘贴 opencode-go/xxx 也会自动去掉前缀。", ui.FsXs, 400, th.TextMute, ui.FaceUI)
}
