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
	a.presetIdx = a.cfg.ActiveProvider
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

func (a *App) paintOnboard(c *ui.Canvas) {
	th := a.th
	u := a.ui
	W, H := a.winW, a.winH

	// 背景渐变：左侧品牌区带一层极淡的强调色，避免整页发白。
	c.Gradient(ui.R(0, 0, W, H), ui.Blend(th.Bg, th.Accent, 0.12), th.Bg, true)

	leftW := W / 2
	if leftW > 640 {
		leftW = 640
	}

	// 左侧品牌区
	cb := ui.R(48, 48, leftW-96, H-96)
	DrawLogo(c, cb.X, cb.Y+8, 44, th.Accent, th.Bg)
	c.TextFace(cb.X+58, cb.Y+8, "WIN7 Harness", ui.FsH1, 700, th.Text, ui.FaceUI)
	c.TextFace(cb.X+58, cb.Y+44, "零依赖的本机编程智能体", ui.FsBase, 400, th.TextDim, ui.FaceUI)

	feats := [][2]string{
		{"完全离线原生渲染", "自绘 UI 引擎，不依赖 Edge / WebView2 / 任何运行时，Win7 SP1 直接跑"},
		{"只认 OpenAI 兼容协议", "填一个 Base URL、Key 和模型 ID 即可；不绑定厂商，也不内置任何模型名"},
		{"真·本机操作能力", "读写文件、搜索代码、执行编译命令，进程 Job Object 管控，随时可中断"},
		{"为老机器优化", "单文件 EXE，386 / amd64 双版本，内存占用低，32 位系统同样可用"},
	}
	y := cb.Y + 110
	for i, f := range feats {
		if y+64 > cb.Bottom() {
			break
		}
		r := ui.R(cb.X, y, cb.W, 58)
		c.Round(r, 8, ui.Blend(th.Surface, th.Accent, 0.05))
		c.RoundStroke(r, 8, th.BorderSoft)
		// 序号徽标
		c.Round(ui.R(r.X+14, r.Y+16, 26, 26), 8, ui.Blend(th.Accent, th.Bg, 0.75))
		c.TextFace(r.X+22, r.Y+20, fmt.Sprintf("%d", i+1), ui.FsSm, 700, th.Accent, ui.FaceUI)
		c.TextFace(r.X+52, r.Y+10, f[0], ui.FsBase, 700, th.Text, ui.FaceUI)
		// 描述是「内容说明」而不是占位符：用 TextDim（次级文字），
		// 深色底上 TextMute 的对比度不足，小字会糊成一片。
		c.TextFace(r.X+52, r.Y+32, ellipsis(c, f[1], ui.FsXs, 400, r.W-70), ui.FsXs, 400, th.TextDim, ui.FaceUI)
		y += 68
	}

	// 右侧配置卡片
	cw := int32(520)
	if W-leftW < 560 {
		cw = W - leftW - 48
	}
	card := ui.R(leftW+24, 48, cw, H-96)
	if card.Bottom() > H-24 {
		card.H = H - 96
	}
	c.Round(card, 8, th.Card)
	// 外圈浅描边 + 内圈实线：给白卡片一点浮起感，不靠阴影滤镜。
	c.RoundStroke(card.Inset(-2), 10, th.BorderSoft)
	c.RoundStroke(card, 8, th.Border)

	x := card.X + 28
	iw := card.W - 56
	yy := card.Y + 24

	title := "连接模型"
	c.TextFace(x, yy, title, ui.FsLg, 700, th.Text, ui.FaceUI)
	stepX := x + c.MeasureFace(title, ui.FsLg, 700, ui.FaceUI) + 14
	c.TextFace(stepX, yy+6, "第 1 步 / 共 1 步", ui.FsXs, 400, th.TextMute, ui.FaceUI)
	yy += 34
	c.TextFace(x, yy, "选择一个接入方式，或直接填任意 OpenAI 兼容地址。", ui.FsSm, 400, th.TextDim, ui.FaceUI)
	yy += 26

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

	// 底部按钮
	by := card.Bottom() - 56
	startBtn := ui.R(card.Right()-160, by, 132, 38)
	if u.Button("ob.start", "开始使用", startBtn, BtnPrimary) {
		a.queueUI(a.finishOnboard)
	}
	if a.testing {
		c.TextFace(card.Right()-260, by+10, "测试中…", ui.FsXs, 400, th.TextMute, ui.FaceUI)
	}
	c.TextFace(x, by+10, "配置保存在本机，不会上传", ui.FsXs, 400, th.TextMute, ui.FaceUI)

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
	if a.presetIdx >= 0 && a.presetIdx < len(a.cfg.Providers) {
		a.cfg.ActiveProvider = a.presetIdx
	}
	base := strings.TrimSpace(a.ui.Field("set.base", "").String())
	key := strings.TrimSpace(a.ui.Field("set.key", "").String())
	model := strings.TrimSpace(a.ui.Field("set.model", "").String())
	dir := strings.TrimSpace(a.ui.Field("set.dir", "").String())
	temp := a.ui.Field("set.temp", "").String()
	mt := a.ui.Field("set.maxtok", "").String()

	if base == "" || model == "" {
		a.toast("接口地址与模型不能为空", true)
		return
	}
	if key != "" && !a.ensurePortablePassword(func() { a.saveSettings() }) {
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
			a.ui.SetFocus("set.dir")
			return
		}
		a.cfg.WorkDir = dir
	}
	fmt.Sscanf(temp, "%f", &a.cfg.Temperature)
	fmt.Sscanf(mt, "%d", &a.cfg.MaxTokens)
	a.cfg.ProxyURL = strings.TrimSpace(a.ui.Field("set.proxyurl", "").String())
	a.cfg.TLSExtraCA = strings.TrimSpace(a.ui.Field("set.tlsca", "").String())
	if a.cfg.ProxyMode == "manual" && a.cfg.ProxyURL == "" {
		a.toast("已选手动代理，请填写代理地址", true)
		return
	}
	fmt.Sscanf(strings.TrimSpace(a.ui.Field("set.maxcost", "").String()), "%f", &a.cfg.MaxCost)
	fmt.Sscanf(strings.TrimSpace(a.ui.Field("set.budget", "").String()), "%d", &a.cfg.BudgetTokens)
	fmt.Sscanf(strings.TrimSpace(a.ui.Field("set.rounds", "").String()), "%d", &a.cfg.MaxToolRounds)
	fmt.Sscanf(strings.TrimSpace(a.ui.Field("set.ctxwin", "").String()), "%d", &a.cfg.ContextWindow)
	fmt.Sscanf(strings.TrimSpace(a.ui.Field("set.subto", "").String()), "%d", &a.cfg.SubAgentTimeoutSec)
	fmt.Sscanf(strings.TrimSpace(a.ui.Field("set.pricein", "").String()), "%f", &a.cfg.PriceIn)
	fmt.Sscanf(strings.TrimSpace(a.ui.Field("set.priceout", "").String()), "%f", &a.cfg.PriceOut)
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
	a.rebuildEngine()
	a.toast("设置已保存", false)
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
