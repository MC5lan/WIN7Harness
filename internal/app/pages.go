//go:build windows

package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"win7harness/internal/core"
	"win7harness/internal/llm"
	"win7harness/internal/ui"
	"win7harness/internal/win32"
)

type testResult struct {
	msg string
	ok  bool
}

// modelListResult 是后台 GET /models 的结果。base 用于只把结果写回
// 发起请求时对应的端点，避免用户中途改了地址导致串台。
type modelListResult struct {
	base        string
	models      []string
	err         error
	fromOnboard bool
}

// initForms 用当前配置填充表单。
func (a *App) initForms() {
	if a.ui == nil {
		return
	}
	p := a.cfg.Provider()
	a.ui.SetField("ob.base", p.BaseURL)
	a.ui.SetField("ob.key", p.APIKey)
	a.ui.SetField("ob.model", a.cfg.Model)
	a.ui.SetField("ob.dir", a.cfg.WorkDir)
	a.ui.SetField("set.base", p.BaseURL)
	a.ui.SetField("set.key", p.APIKey)
	a.ui.SetField("set.model", a.cfg.Model)
	a.ui.SetField("set.dir", a.cfg.WorkDir)
	a.ui.SetField("set.temp", fmt.Sprintf("%.1f", a.cfg.Temperature))
	a.ui.SetField("set.maxtok", fmt.Sprintf("%d", a.cfg.MaxTokens))
	a.ui.SetField("set.maxcost", fmt.Sprintf("%g", a.cfg.MaxCost))
	a.ui.SetField("set.budget", fmt.Sprintf("%d", a.cfg.BudgetTokens))
	a.ui.SetField("set.rounds", fmt.Sprintf("%d", a.cfg.MaxToolRounds))
	a.ui.SetField("set.ctxwin", fmt.Sprintf("%d", a.cfg.ContextWindow))
	a.ui.SetField("set.subto", fmt.Sprintf("%d", a.cfg.SubAgentTimeoutSec))
	a.ui.SetField("set.pricein", fmt.Sprintf("%g", a.cfg.PriceIn))
	a.ui.SetField("set.priceout", fmt.Sprintf("%g", a.cfg.PriceOut))
	a.ui.SetField("set.sysprompt", a.cfg.SystemPrompt)
	a.ui.SetField("set.toolprofile", a.cfg.ToolProfile)
	a.ui.SetField("set.updrepo", a.cfg.UpdateRepo)
	a.ui.SetField("set.url", a.cfg.UpdateURL)
	a.presetIdx = a.cfg.ActiveProvider
	a.settingsMarkSaved()
}

func (a *App) applyPreset(i int) {
	if i < 0 || i >= len(a.cfg.Providers) {
		return
	}
	a.presetIdx = i
	p := a.cfg.Providers[i]
	a.ui.SetField("ob.base", p.BaseURL)
	a.ui.SetField("ob.model", p.Model)
	if p.NoAuth {
		a.ui.SetField("ob.key", "")
	} else if p.APIKey == "" {
		a.ui.SetField("ob.key", "")
	}
	a.ui.SetFocus("ob.key")
}

/* ---------------- 引导页 ---------------- */

// onbGo 切换引导页步骤。进入第 2 步时焦点交给接口地址；回到第 1 步时清空焦点，
// 免得看不见的表单框还在接收键入。
func (a *App) onbGo(step int) {
	a.onbStep = step
	if a.ui != nil {
		if step == 1 {
			a.ui.SetFocus("ob.base")
		} else {
			a.ui.SetFocus("")
		}
	}
	a.Redraw()
}

func (a *App) paintOnboard(c *ui.Canvas) {
	th := a.th
	W, H := a.winW, a.winH

	// 背景渐变：顶部带一层极淡的强调色，避免整页发白。
	c.Gradient(ui.R(0, 0, W, H), ui.Blend(th.Bg, th.Accent, 0.12), th.Bg, true)
	if a.onbStep == 0 {
		a.paintOnboardWelcome(c)
		return
	}

	// 第 2 步：居中的连接配置卡片。
	cw := int32(560)
	if W-96 < cw {
		cw = W - 96
	}
	ch := H - 64
	if ch > 600 {
		ch = 600
	}
	card := ui.R((W-cw)/2, (H-ch)/2, cw, ch)
	c.Round(card, 12, th.Card)
	// 外圈浅描边 + 内圈实线：给卡片一点浮起感，不靠阴影滤镜。
	c.RoundStroke(card.Inset(-2), 14, th.BorderSoft)
	c.RoundStroke(card, 12, th.Border)
	a.paintOnboardForm(c, card)
}

// paintOnboardWelcome 绘制第 1 步：居中的品牌标识（图标 + 艺术字名称）、一句话定位与「下一步」。
func (a *App) paintOnboardWelcome(c *ui.Canvas) {
	th := a.th
	u := a.ui
	W, H := a.winW, a.winH
	cx := W / 2

	const iconSz, titleSz, chipH = int32(104), int32(42), int32(28)
	const block = iconSz + 14 + titleSz + 16 + 22 + 26 + chipH + 40 + 44
	top := (H-block)/2 - 24

	// 图标底色取渐变在图标中心处的颜色，让透明圆角与背景无缝衔接。
	bgAt := ui.Blend(ui.Blend(th.Bg, th.Accent, 0.12), th.Bg, float32(top+iconSz/2)/float32(H))
	paintBrandMark(c, th, ui.R(cx-iconSz/2, top, iconSz, iconSz), bgAt)

	y := top + iconSz + 14
	paintBrandTitle(c, th, cx, y, "WIN7 Harness", titleSz)

	y += titleSz + 16
	tag := "零依赖的本机编程智能体"
	c.TextFace(cx-c.MeasureFace(tag, ui.FsLg, 400, ui.FaceUI)/2, y, tag, ui.FsLg, 400, th.TextDim, ui.FaceUI)

	y += 22 + 26
	chips := []string{"完全离线原生渲染", "只认 OpenAI 兼容协议", "为老机器优化"}
	widths := make([]int32, len(chips))
	var total int32
	for i, s := range chips {
		widths[i] = c.MeasureFace(s, ui.FsSm, 500, ui.FaceUI) + 28
		total += widths[i] + 10
	}
	x := cx - (total-10)/2
	for i, s := range chips {
		r := ui.R(x, y, widths[i], chipH)
		c.Round(r, chipH/2, th.Card)
		c.RoundStroke(r, chipH/2, th.BorderSoft)
		c.TextFace(r.X+14, ui.TextTop(c, r, ui.FsSm), s, ui.FsSm, 500, th.TextDim, ui.FaceUI)
		x += widths[i] + 10
	}

	y += chipH + 40
	if u.Button("ob.next", "下一步", ui.R(cx-100, y, 200, 44), BtnPrimary) {
		a.onbGo(1)
	}

	// 步骤指示：当前页是实心胶囊，下一页是小圆点。
	dy := H - 56
	c.Round(ui.R(cx-19, dy, 22, 8), 4, th.Accent)
	c.Round(ui.R(cx+11, dy, 8, 8), 4, th.Border)
}

// paintBrandTitle 绘制品牌艺术字：逐字从强调色渐变到正文色，底下压一层强调色投影。
func paintBrandTitle(c *ui.Canvas, th *ui.Theme, cx, y int32, s string, size int32) {
	face, weight := ui.FaceDisplay, ui.WeightDisplay
	w := c.MeasureFace(s, size, weight, face)
	x0 := cx - w/2
	c.TextFace(x0, y+4, s, size, weight, ui.Blend(th.Bg, th.Accent, 0.35), face)
	x := x0
	for _, r := range s {
		g := string(r)
		adv := c.MeasureFace(g, size, weight, face)
		t := float32(x+adv/2-x0) / float32(w)
		c.TextFace(x, y, g, size, weight, ui.Blend(th.Accent, th.Text, t), face)
		x += adv
	}
}

// paintBrandMark 绘制品牌图标：ico 位图压在 bg 上；取不到时退回矢量标识。
func paintBrandMark(c *ui.Canvas, th *ui.Theme, r ui.Rect, bg uint32) {
	if img := ui.AppIcon(c.Px(r.W), bg); img != nil {
		c.DrawImg(img, r)
		return
	}
	DrawLogo(c, r.X, r.Y, r.W, th.Accent, th.Bg)
}

// paintOnboardForm 绘制首次启动页右侧连接配置卡片。
func (a *App) paintOnboardForm(c *ui.Canvas, card ui.Rect) {
	th := a.th
	x := card.X + 28
	iw := card.W - 56
	yy := card.Y + 26

	paintBrandMark(c, th, ui.R(x, yy-2, 28, 28), th.Card)
	tx := x + 40
	title := "连接模型"
	c.TextFace(tx, yy, title, ui.FsLg, 700, th.Text, ui.FaceUI)
	stepX := tx + c.MeasureFace(title, ui.FsLg, 700, ui.FaceUI) + 14
	c.TextFace(stepX, yy+6, "第 2 步 / 共 2 步", ui.FsXs, 400, th.TextMute, ui.FaceUI)
	yy += 40
	c.TextFace(x, yy, "选择一个接入方式，或直接填任意 OpenAI 兼容地址。", ui.FsSm, 400, th.TextDim, ui.FaceUI)
	yy += 26
	yy = a.paintOnboardPresets(c, x, iw, yy)
	yy = a.paintOnboardFields(c, x, iw, yy)
	yy = a.paintOnboardWorkDir(c, x, iw, yy)
	a.paintOnboardFooter(c, card, x, iw, yy)

	// 键盘：Tab 切焦点
	_ = win32.VK_TAB
}

// paintOnboardPresets 绘制接入方式 chips，返回下一个 y。
func (a *App) paintOnboardPresets(c *ui.Canvas, x, iw, yy int32) int32 {
	th := a.th
	u := a.ui
	// 预设 chips
	chipH := int32(30)
	cx := x
	row := int32(0)
	for i, p := range a.cfg.Providers {
		name := p.Name
		w := c.MeasureFace(name, ui.FsXs, 500, ui.FaceUI) + 22
		if cx+w > x+iw {
			cx = x
			row++
			yy += chipH + 12
		}
		if row > 2 {
			break
		}
		cr := ui.R(cx, yy, w, chipH)
		cid := fmt.Sprintf("preset:%d", i)
		hover := u.Hit(cid, cr)
		if u.Clicked(cid) {
			a.applyPreset(i)
		}
		sel := i == a.presetIdx
		bg, txt, bd := th.Surface, th.TextDim, th.Border
		if sel {
			bg, txt, bd = th.AccentSoft, th.AccentText, th.Accent
		} else if hover {
			bg, txt = th.CardHover, th.Text
		}
		c.Round(cr, cr.H/2, bg)
		c.RoundStroke(cr, cr.H/2, bd)
		tw := c.MeasureFace(name, ui.FsXs, 500, ui.FaceUI)
		c.TextFace(cr.CX()-tw/2, cr.CY()-7, name, ui.FsXs, 500, txt, ui.FaceUI)
		cx += w + 8
	}
	yy += chipH + 20
	return yy
}

// paintOnboardFields 绘制 Base URL / API Key / 模型 ID，返回下一个 y。
func (a *App) paintOnboardFields(c *ui.Canvas, x, iw, yy int32) int32 {
	th := a.th
	u := a.ui
	// Base URL
	c.TextFace(x, yy, "接口地址 Base URL", ui.FsXs, 500, th.TextDim, ui.FaceUI)
	yy += 20
	u.TextField("ob.base", ui.R(x, yy, iw, 36), "", "https://你的端点/v1")
	yy += 52

	// API Key
	c.TextFace(x, yy, "API Key", ui.FsXs, 500, th.TextDim, ui.FaceUI)
	hintKey := "本地模型可留空"
	if p := a.cfg.Providers[a.safePreset()]; p.NoAuth {
		hintKey = "该端点通常无需 Key"
	} else if p.ID == "opencode-go" {
		hintKey = "OpenCode Go 的 Key 来自 opencode.ai/console；DeepSeek Key 不能填这里"
	} else if p.ID == "custom" {
		hintKey = "DeepSeek 等：Base URL 填对应 /v1，Key 只适用于该服务商"
	}
	c.TextFace(x+c.MeasureFace("API Key", ui.FsXs, 500, ui.FaceUI)+14, yy,
		ellipsis(c, hintKey, ui.FsXs, 400, iw-120), ui.FsXs, 400, th.TextMute, ui.FaceUI)
	yy += 20
	keyField := u.Field("ob.key", "")
	keyField.Masked = true
	u.TextField("ob.key", ui.R(x, yy, iw-96, 36), "", "sk-...")
	if u.Button("ob.eye", "显示", ui.R(x+iw-88, yy, 48, 36), BtnGhost) {
		keyField.Masked = !keyField.Masked
	}
	yy += 52

	// 模型 ID：不内置任何名字，用户自己填或从 /models 拉取。
	lw := c.MeasureFace("模型 ID", ui.FsXs, 500, ui.FaceUI)
	c.TextFace(x, yy, "模型 ID", ui.FsXs, 500, th.TextDim, ui.FaceUI)
	c.TextFace(x+lw+14, yy,
		ellipsis(c, "可手填，也可拉取端点列表（只读元数据，不消耗对话 token）", ui.FsXs, 400, iw-lw-20),
		ui.FsXs, 400, th.TextMute, ui.FaceUI)
	yy += 20
	modelW := iw - 154
	if modelW < 140 {
		modelW = 140
	}
	u.TextField("ob.model", ui.R(x, yy, modelW, 36), "", "模型 ID")
	if u.Button("ob.models.fetch", "拉取", ui.R(x+modelW+8, yy, 68, 36), BtnGhost) {
		a.fetchModels(u.Field("ob.base", "").String(), u.Field("ob.key", "").String(), true)
	}
	if u.Button("ob.models.pick", "选择", ui.R(x+modelW+82, yy, 68, 36), BtnGhost) {
		a.openFetchedModelMenu(ui.R(x+modelW+82, yy, 68, 36), u.Field("ob.base", "").String(), true)
	}
	yy += 42
	if a.modelsMsg != "" {
		col := th.TextMute
		if a.modelsOK != nil {
			if *a.modelsOK {
				col = th.Success
			} else {
				col = th.Danger
			}
		}
		c.TextFace(x, yy, ellipsis(c, a.modelsMsg, ui.FsXs, 400, iw), ui.FsXs, 400, col, ui.FaceUI)
		yy += 22
	}
	return yy
}

// paintOnboardWorkDir 绘制默认工作目录，返回下一个 y。
func (a *App) paintOnboardWorkDir(c *ui.Canvas, x, iw, yy int32) int32 {
	th := a.th
	u := a.ui
	// 工作目录
	c.TextFace(x, yy, "默认工作目录", ui.FsXs, 500, th.TextDim, ui.FaceUI)
	c.TextFace(x+c.MeasureFace("默认工作目录", ui.FsXs, 500, ui.FaceUI)+14, yy, "新会话默认目录，可在顶栏单独切换", ui.FsXs, 400, th.TextMute, ui.FaceUI)
	yy += 20
	u.TextField("ob.dir", ui.R(x, yy, iw-140, 36), "", `C:\Users\你\WIN7Harness-Workspace`)
	if u.Button("ob.browse", "选择…", ui.R(x+iw-132, yy, 66, 36), BtnGhost) {
		a.queueUI(func() {
			var (
				d  string
				ok bool
			)
			a.beginNativeDialog()
			d, ok = win32.BrowseForFolder(0, "选择默认工作目录", u.Field("ob.dir", "").String())
			a.endNativeDialog()
			if ok {
				u.SetField("ob.dir", d)
			}
		})
	}
	if u.Button("ob.mkdir", "创建", ui.R(x+iw-62, yy, 62, 36), BtnGhost) {
		a.queueUI(func() {
			dir := u.Field("ob.dir", "").String()
			if err := core.EnsureWorkDir(dir); err != nil {
				a.toast("目录创建失败："+err.Error(), true)
			} else {
				a.toast("目录已就绪", false)
			}
		})
	}
	yy += 52
	return yy
}

// paintOnboardFooter 绘制测试结果与开始按钮。
func (a *App) paintOnboardFooter(c *ui.Canvas, card ui.Rect, x, iw, yy int32) {
	th := a.th
	u := a.ui
	// 测试结果
	if a.testMsg != "" {
		col := th.TextMute
		if a.testOK != nil {
			if *a.testOK {
				col = th.Success
			} else {
				col = th.Danger
			}
		}
		c.TextFace(x, yy, ellipsis(c, a.testMsg, ui.FsXs, 400, iw), ui.FsXs, 400, col, ui.FaceUI)
		yy += 22
	}

	// 底部：分隔线 + 返回 / 开始
	by := card.Bottom() - 64
	c.HLine(x, by-16, iw, th.BorderSoft)
	if u.Button("ob.back", "上一步", ui.R(x, by, 96, 38), BtnGhost) {
		a.onbGo(0)
	}
	startBtn := ui.R(x+iw-132, by, 132, 38)
	if u.Button("ob.start", "开始使用", startBtn, BtnPrimary) {
		a.queueUI(a.finishOnboard)
	}
	note := "配置保存在本机，不会上传"
	if a.testing {
		note = "测试中…"
	}
	c.TextFace(startBtn.X-16-c.MeasureFace(note, ui.FsXs, 400, ui.FaceUI), ui.TextTop(c, startBtn, ui.FsXs),
		note, ui.FsXs, 400, th.TextMute, ui.FaceUI)

	// 键盘：Tab 切焦点
	_ = win32.VK_TAB
}

func (a *App) safePreset() int {
	if a.presetIdx < 0 || a.presetIdx >= len(a.cfg.Providers) {
		return 0
	}
	return a.presetIdx
}

// testConnection 异步测试端点连通性。
func (a *App) testConnection(base, key, model string) {
	if a.testing {
		return
	}
	a.testing = true
	a.testMsg = "正在连接（会发一条最短对话）…"
	a.testOK = nil
	core.SafeGo("app/pages.go:299", func() {
		c := llm.New(base, key, model, 60*time.Second)
		ctx, cancel := context.WithTimeout(context.Background(), 65*time.Second)
		defer cancel()
		msg, err := c.Ping(ctx)
		if err == nil {
			if cerr := c.CheckChat(ctx); cerr != nil {
				err = fmt.Errorf("连接成功，但最小对话失败：%w", cerr)
			} else {
				msg += "；最小对话通过"
			}
		}
		res := testResult{msg: msg, ok: err == nil}
		if err != nil {
			res.msg = "连接失败：" + err.Error()
		}
		a.testCh <- res
	})
}

// fetchModels 后台拉取端点 /models。只读元数据，不产生对话 token；
// 失败时把原因留在设置页/引导页，不弹窗打断填写。
func (a *App) fetchModels(base, key string, fromOnboard bool) {
	base = core.NormalizeBaseURL(base)
	if base == "" {
		a.toast("请先填写接口地址", true)
		return
	}
	if a.fetchingModels {
		return
	}
	a.fetchingModels = true
	a.modelsMsg = "正在拉取模型列表…"
	a.modelsOK = nil
	core.SafeGo("app/pages.go:fetchModels", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		c := llm.New(base, key, "", 20*time.Second)
		models, err := c.ListModels(ctx)
		a.modelsCh <- modelListResult{base: base, models: models, err: err, fromOnboard: fromOnboard}
	})
}

// modelsForBase 优先返回本次运行中动态拉到的模型；其次返回配置里已保存的列表。
func (a *App) modelsForBase(base string) []string {
	key := core.NormalizeBaseURL(base)
	if m, ok := a.modelsCache[key]; ok {
		return m
	}
	for _, p := range a.cfg.Providers {
		if strings.EqualFold(core.NormalizeBaseURL(p.BaseURL), key) && len(p.Models) > 0 {
			return p.Models
		}
	}
	return nil
}

// applyModelList 把后台结果写回缓存与对应 provider。
func (a *App) applyModelList(res modelListResult) {
	a.fetchingModels = false
	if res.err != nil {
		ok := false
		a.modelsOK = &ok
		a.modelsMsg = res.err.Error()
		a.Redraw()
		return
	}
	ok := true
	a.modelsOK = &ok
	a.modelsCache[core.NormalizeBaseURL(res.base)] = append([]string{}, res.models...)
	a.modelMenuBase = ""
	a.modelMenuPage = 0
	a.modelsMsg = fmt.Sprintf("已获取 %d 个模型，点「选择模型」填入", len(res.models))
	// 同步到地址相同的 provider，保存设置时一并落盘，会话模型菜单随后可用。
	for i := range a.cfg.Providers {
		if strings.EqualFold(core.NormalizeBaseURL(a.cfg.Providers[i].BaseURL), core.NormalizeBaseURL(res.base)) {
			a.cfg.Providers[i].Models = append([]string{}, res.models...)
		}
	}
	a.Redraw()
}

// openFetchedModelMenu 打开“已拉取模型”浮层；选中后写入引导页或设置页的模型框。
func (a *App) openFetchedModelMenu(anchor ui.Rect, base string, fromOnboard bool) {
	models := a.modelsForBase(base)
	if len(models) == 0 {
		a.toast("还没有模型列表，先点「拉取模型」", true)
		return
	}
	// 32 个模型一屏放不下，菜单本身也不支持滚动：按 12 个一页分页，
	// 避免后面的模型永远选不到。
	key := core.NormalizeBaseURL(base)
	if key != a.modelMenuBase {
		a.modelMenuBase = key
		a.modelMenuPage = 0
	}
	const pageSize = 12
	if a.modelMenuPage < 0 {
		a.modelMenuPage = 0
	}
	start := a.modelMenuPage * pageSize
	if start >= len(models) {
		start = 0
		a.modelMenuPage = 0
	}
	end := start + pageSize
	if end > len(models) {
		end = len(models)
	}
	items := make([]popupItem, 0, pageSize+2)
	cur := a.ui.Field("set.model", "").String()
	if fromOnboard {
		cur = a.ui.Field("ob.model", "").String()
	}
	for _, m := range models[start:end] {
		m := m
		items = append(items, popupItem{
			Text:  m,
			Check: m == cur,
			Run: func() {
				a.closeMenu()
				if fromOnboard {
					a.ui.SetField("ob.model", m)
					a.ui.SetFocus("ob.key")
				} else {
					a.ui.SetField("set.model", m)
				}
				a.Redraw()
			},
		})
	}
	if start > 0 {
		items = append(items, popupItem{
			Text: "上一页",
			Hint: fmt.Sprintf("%d/%d", a.modelMenuPage, (len(models)+pageSize-1)/pageSize),
			Run: func() {
				a.closeMenu()
				a.modelMenuPage--
				a.openFetchedModelMenu(anchor, base, fromOnboard)
			},
		})
	}
	if end < len(models) {
		items = append(items, popupItem{
			Text: "下一页",
			Hint: fmt.Sprintf("%d/%d", a.modelMenuPage+2, (len(models)+pageSize-1)/pageSize),
			Run: func() {
				a.closeMenu()
				a.modelMenuPage++
				a.openFetchedModelMenu(anchor, base, fromOnboard)
			},
		})
	}
	a.menu = &popupMenu{Title: fmt.Sprintf("可用模型（%d 个 · 来自 /models）", len(models)), Items: items, Anchor: anchor, Up: true}
	a.menuIdx = 0
	for i, it := range items {
		if it.Check {
			a.menuIdx = i
		}
	}
	a.Redraw()
}

// finishOnboard 保存配置并进入主界面。
func (a *App) finishOnboard() {
	// 引导页的 chips 只改表单；保存时必须把当前选中的接入方式设为 active，
	// 否则用户选了 OpenCode Go、最后却写进了默认 provider。
	a.cfg.ActiveProvider = a.safePreset()
	base := strings.TrimSpace(a.ui.Field("ob.base", "").String())
	key := strings.TrimSpace(a.ui.Field("ob.key", "").String())
	model := strings.TrimSpace(a.ui.Field("ob.model", "").String())
	dir := strings.TrimSpace(a.ui.Field("ob.dir", "").String())

	if base == "" {
		a.toast("请填写接口地址", true)
		a.ui.SetFocus("ob.base")
		return
	}
	if model == "" {
		a.toast("请填写模型名称", true)
		a.ui.SetFocus("ob.model")
		return
	}
	if key != "" && !a.ensurePortablePassword(func() { a.finishOnboard() }) {
		return
	}
	p := a.cfg.Provider()
	p.BaseURL = core.NormalizeBaseURL(base)
	p.APIKey = key
	p.Model = core.NormalizeModelID(p.BaseURL, model)
	if fetched := a.modelsForBase(p.BaseURL); len(fetched) > 0 {
		p.Models = append([]string{}, fetched...)
	}
	a.cfg.Providers[a.cfg.ActiveProvider] = p
	a.cfg.Model = p.Model
	if dir != "" {
		if err := core.ValidateWorkDir(dir); err != nil {
			a.toast(err.Error(), true)
			a.ui.SetFocus("ob.dir")
			return
		}
		a.cfg.WorkDir = dir
	}
	if err := core.EnsureDirs(a.cfg); err != nil {
		a.toast("目录创建失败："+err.Error(), true)
		return
	}
	a.cfg.Onboarded = true
	if err := a.cfg.Save(); err != nil {
		a.toast("配置保存失败："+err.Error(), true)
		return
	}
	a.rebuildEngine()
	a.page = PageChat
	a.toast("配置完成，正在后台自检环境", false)
	// 首次配置完成后自动跑一次体检，异常项用提示条告知，不打断使用
	a.runChecksNotify(true)
}

/* ---------------- 设置页 ---------------- */

func (a *App) saveSettings() {
	if a.streaming || (a.eng != nil && a.eng.Running()) {
		a.toast("生成中不能保存设置：先停止或等本轮结束", true)
		return
	}
	if field, msg := a.validateSettingsFields(); field != "" {
		a.toast(msg, true)
		a.ui.SetFocus(field)
		return
	}
	base := strings.TrimSpace(a.ui.Field("set.base", "").String())
	key := strings.TrimSpace(a.ui.Field("set.key", "").String())
	model := strings.TrimSpace(a.ui.Field("set.model", "").String())
	dir := strings.TrimSpace(a.ui.Field("set.dir", "").String())
	temp := a.ui.Field("set.temp", "").String()
	mt := a.ui.Field("set.maxtok", "").String()

	// 模型配置不完整时仍然保存外观/预算/网络等其它设置；
	// 以前这里直接 return，用户只改主题或窗口也会被拦住，看起来像“设置没用”。
	incompleteModel := base == "" || model == ""
	if !incompleteModel {
		if a.presetIdx >= 0 && a.presetIdx < len(a.cfg.Providers) {
			a.cfg.ActiveProvider = a.presetIdx
		}
		if key != "" && !a.ensurePortablePassword(func() { a.saveSettings() }) {
			return
		}
		p := a.cfg.Provider()
		p.BaseURL = core.NormalizeBaseURL(base)
		p.APIKey = key
		if key == "" && p.KeyLoaded {
			// 用户明确清空了一个本次已成功解密的 Key：连密文一起删，
			// 否则 forDisk 跳过空 Key，旧密文会在下次启动“复活”。
			p.APIKeyEnc = ""
			p.KeyLoaded = false
		}
		if key != "" {
			p.KeyLoaded = true
		}
		p.Model = core.NormalizeModelID(p.BaseURL, model)
		if fetched := a.modelsForBase(p.BaseURL); len(fetched) > 0 {
			p.Models = append([]string{}, fetched...)
		}
		a.cfg.Providers[a.cfg.ActiveProvider] = p
		a.cfg.Model = p.Model
	}
	if dir != "" {
		if err := core.ValidateWorkDir(dir); err != nil {
			a.toast(err.Error(), true)
			a.ui.SetFocus("set.dir")
			return
		}
		a.cfg.WorkDir = dir
	}
	fmt.Sscanf(temp, "%f", &a.cfg.Temperature)
	fmt.Sscanf(mt, "%d", &a.cfg.MaxTokens)
	a.cfg.ProxyURL = strings.TrimSpace(a.ui.Field("set.proxyurl", "").String())
	a.cfg.TLSExtraCA = strings.TrimSpace(a.ui.Field("set.tlsca", "").String())
	if repo := strings.TrimSpace(a.ui.Field("set.updrepo", "").String()); repo != "" {
		a.cfg.UpdateRepo = repo
	}
	if url := strings.TrimSpace(a.ui.Field("set.url", "").String()); url != "" {
		a.cfg.UpdateURL = url
	}
	if a.cfg.ProxyMode == "manual" && a.cfg.ProxyURL == "" {
		a.toast("已选手动代理，请填写代理地址", true)
		return
	}
	fmt.Sscanf(strings.TrimSpace(a.ui.Field("set.maxcost", "").String()), "%f", &a.cfg.MaxCost)
	fmt.Sscanf(strings.TrimSpace(a.ui.Field("set.budget", "").String()), "%d", &a.cfg.BudgetTokens)
	fmt.Sscanf(strings.TrimSpace(a.ui.Field("set.rounds", "").String()), "%d", &a.cfg.MaxToolRounds)
	if n, ok := parseWindowSize(strings.TrimSpace(a.ui.Field("set.ctxwin", "").String())); ok {
		a.cfg.ContextWindow = n
	}
	fmt.Sscanf(strings.TrimSpace(a.ui.Field("set.subto", "").String()), "%d", &a.cfg.SubAgentTimeoutSec)
	fmt.Sscanf(strings.TrimSpace(a.ui.Field("set.pricein", "").String()), "%f", &a.cfg.PriceIn)
	fmt.Sscanf(strings.TrimSpace(a.ui.Field("set.priceout", "").String()), "%f", &a.cfg.PriceOut)
	a.cfg.SystemPrompt = strings.TrimSpace(a.ui.Field("set.sysprompt", "").String())
	if len([]rune(a.cfg.SystemPrompt)) > 4000 {
		a.cfg.SystemPrompt = string([]rune(a.cfg.SystemPrompt)[:4000])
	}
	a.cfg.ToolProfile = strings.ToLower(strings.TrimSpace(a.ui.Field("set.toolprofile", "").String()))
	switch a.cfg.ToolProfile {
	case "auto", "core", "full", "plan":
	default:
		a.cfg.ToolProfile = "auto"
	}
	if a.cfg.PriceIn < 0 {
		a.cfg.PriceIn = 0
	}
	if a.cfg.PriceOut < 0 {
		a.cfg.PriceOut = 0
	}
	if a.cfg.MaxToolRounds < 0 {
		a.cfg.MaxToolRounds = 0
	}
	if a.cfg.ContextWindow < 0 {
		a.cfg.ContextWindow = 0
	}
	if a.cfg.SubAgentTimeoutSec < 0 {
		a.cfg.SubAgentTimeoutSec = 0
	}
	if a.cfg.MaxCost < 0 {
		a.cfg.MaxCost = 0
	}
	if a.cfg.BudgetTokens < 0 {
		a.cfg.BudgetTokens = 0
	}
	if err := core.EnsureDirs(a.cfg); err != nil {
		a.toast("目录创建失败："+err.Error(), true)
		return
	}
	if err := a.cfg.Save(); err != nil {
		a.toast("保存失败："+err.Error(), true)
		return
	}
	if a.cfg.MaxSubTasks <= 0 {
		a.cfg.MaxSubTasks = 3
	}
	if a.cfg.MaxSubTasks > 8 {
		a.cfg.MaxSubTasks = 8
	}
	if err := a.cfg.Save(); err != nil {
		a.toast("保存失败："+err.Error(), true)
		return
	}
	// 保存后重建引擎，但必须把当前会话的结构化历史带过去；
	// 否则用户只是改了个温度/窗口，回到对话发现模型“失忆”了。
	if cv := a.currentConv(); cv != nil {
		a.syncEngineTranscript(cv)
	}
	a.rebuildEngine()
	a.restoreHistory()
	if incompleteModel {
		a.toast("设置已保存；模型地址或模型 ID 不完整，未更新模型", true)
	} else {
		a.toast("设置已保存", false)
	}
	a.settingsMarkSaved()
	a.page = PageChat
}

// paintScrollHint 在内容区右侧绘制竖向滚动条（仅内容超出可视区时出现）。
func (a *App) paintScrollHint(c *ui.Canvas, view ui.Rect) {
	if a.settingsH <= view.H {
		return
	}
	a.ui.Scrollbar(ui.R(view.X+view.W-12, view.Y+4, 6, view.H-8), a.settingsH, view.H-8, int32(a.settingsScrl))
}

// addHook 从设置页表单读取一条钩子并落盘。
func (a *App) addHook() {
	ev := strings.TrimSpace(a.ui.Field("hook.event", "").String())
	match := strings.TrimSpace(a.ui.Field("hook.match", "").String())
	cmd := strings.TrimSpace(a.ui.Field("hook.cmd", "").String())
	if cmd == "" {
		a.toast("命令不能为空", true)
		return
	}
	if ev == "" {
		ev = "post_tool"
	}
	switch ev {
	case "pre_tool", "post_tool", "turn_end":
	default:
		a.toast("事件只能是 pre_tool / post_tool / turn_end", true)
		return
	}
	if match == "" {
		match = "*"
	}
	if len(a.cfg.Hooks) >= 12 {
		a.toast("钩子数量已达上限 12 条", true)
		return
	}
	a.cfg.Hooks = append(a.cfg.Hooks, core.Hook{Event: ev, Match: match, Command: cmd, TimeoutSec: 30})
	if err := a.cfg.Save(); err != nil {
		a.toast("保存失败："+err.Error(), true)
		return
	}
	a.ui.SetField("hook.cmd", "")
	a.ui.SetField("hook.match", "")
	a.toast("钩子已添加，下次工具调用即生效", false)
}
