//go:build windows

package app

// 会话级环境：模型与工作区。
//
// 设计取向：
//   - 全局配置里的模型/工作目录降级为「默认值」，新会话跟随默认；
//   - 单个会话可以钉住自己的模型与工作区，随会话一起落盘，重开不丢；
//   - 换模型必须连带换端点，否则会把 A 家的模型名发到 B 家的地址上；
//   - 引擎按会话重建，切会话/切模型/切工作区都走 rebuildEngine。

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"win7harness/internal/core"
	"win7harness/internal/llm"
	"win7harness/internal/ui"
	"win7harness/internal/win32"
)

// providerModel 是模型选择列表里的一行：端点 + 模型名。
type providerModel struct {
	Prov  string // 端点 ID
	PName string // 端点显示名
	Model string // 模型名
}

// convProvider 解析当前会话实际使用的端点：会话指定优先，否则全局活动端点。
func (a *App) convProvider() core.Provider {
	if cv := a.currentConv(); cv != nil {
		if p, ok := a.cfg.ProviderByID(cv.Prov); ok {
			return p
		}
	}
	return a.cfg.Provider()
}

// convModel 解析当前会话实际使用的模型名。
func (a *App) convModel() string {
	if cv := a.currentConv(); cv != nil && strings.TrimSpace(cv.Model) != "" {
		return strings.TrimSpace(cv.Model)
	}
	if m := strings.TrimSpace(a.cfg.Model); m != "" {
		return m
	}
	return strings.TrimSpace(a.convProvider().Model)
}

// convWorkDir 解析当前会话实际使用的工作区。
func (a *App) convWorkDir() string {
	if cv := a.currentConv(); cv != nil && strings.TrimSpace(cv.WorkDir) != "" {
		return cv.WorkDir
	}
	return a.cfg.WorkDir
}

// sessionModelPinned 当前会话是否钉住了模型。
func (a *App) sessionModelPinned() bool {
	cv := a.currentConv()
	return cv != nil && strings.TrimSpace(cv.Model) != ""
}

// sessionDirPinned 当前会话是否钉住了工作区。
func (a *App) sessionDirPinned() bool {
	cv := a.currentConv()
	return cv != nil && strings.TrimSpace(cv.WorkDir) != ""
}

// restoreHistory 把当前会话的文本历史回灌进引擎：切模型/切工作区后上下文不丢。
func (a *App) restoreHistory() {
	if a.eng == nil {
		return
	}
	cv := a.currentConv()
	if cv == nil {
		return
	}
	a.eng.Reset()
	for _, m := range cv.Msgs {
		if m.Role == "user" || (m.Role == "assistant" && m.Text != "") {
			text := m.Text
			if len(m.Images) > 0 {
				// 历史图片不重复上送（每张都带会迅速吃满上下文），只在文本里标注
				text += fmt.Sprintf("\n（此处原有 %d 张图片，历史上下文已省略）", len(m.Images))
			}
			a.eng.Messages = append(a.eng.Messages, llm.Message{Role: m.Role, Content: text})
		}
	}
}

// setSessionModel 把当前会话钉到某个端点/模型；prov 与 model 同为空表示恢复跟随默认。
func (a *App) setSessionModel(prov, model string) {
	cv := a.currentConv()
	if cv == nil {
		return
	}
	if a.streaming {
		a.toast("生成中不能切模型，先停止", true)
		return
	}
	cv.Prov = prov
	cv.Model = model
	a.saveConv()
	a.rebuildEngine()
	a.restoreHistory()
	if model == "" {
		a.sysNote("已恢复跟随默认模型：" + a.convModel())
		a.toast("已跟随默认模型", false)
	} else {
		a.sysNote("本会话模型已切换为 " + model + "\n只影响当前会话，新会话仍用默认模型。")
		a.toast("已切换模型："+model, false)
	}
	a.Redraw()
}

// setSessionDir 把当前会话钉到某个工作区；dir 为空表示恢复跟随默认。
func (a *App) setSessionDir(dir string) {
	cv := a.currentConv()
	if cv == nil {
		return
	}
	if a.streaming {
		a.toast("生成中不能切工作区，先停止", true)
		return
	}
	dir = strings.TrimSpace(dir)
	if dir != "" {
		if err := core.ValidateWorkDir(dir); err != nil {
			a.toast(err.Error(), true)
			return
		}
		if abs, err := filepath.Abs(dir); err == nil {
			dir = abs
		}
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			a.toast("目录不存在："+dir, true)
			return
		}
	}
	cv.WorkDir = dir
	a.saveConv()
	if dir != "" {
		a.cfg.TouchRecentDir(dir)
		_ = a.cfg.Save()
	}
	a.rebuildEngine()
	a.restoreHistory()
	if dir == "" {
		a.sysNote("已恢复跟随默认工作目录：" + a.convWorkDir())
		a.toast("已跟随默认工作目录", false)
	} else {
		a.sysNote("本会话工作区已切换为：" + dir + "\n只影响当前会话，工具读写与 @ 引用都在此目录下解析。")
		a.toast("工作区已切换", false)
	}
	a.Redraw()
}

// pickSessionDir 弹系统目录选择框，选完即切本会话工作区。
func (a *App) pickSessionDir() {
	a.closeMenu()
	var owner win32.HWND
	if a.w != nil {
		owner = a.w.Hwnd()
	}
	a.beginNativeDialog()
	dir, ok := win32.BrowseForFolder(owner, "选择本会话的工作区目录", a.convWorkDir())
	a.endNativeDialog()
	if ok {
		a.setSessionDir(dir)
	}
}

// discoverModels 在端点支持的模型清单为空时，临时问一次 /models。
// 只用于补齐下拉框，失败就静默——离线端点没有 /models 是常态。
func (a *App) discoverModels(p core.Provider) []string {
	if len(p.Models) > 0 {
		return p.Models
	}
	if m := strings.TrimSpace(p.Model); m != "" {
		return []string{m}
	}
	return nil
}

/* ---------------- 模型 / 工作区 选择浮层 ---------------- */

// popupMenu 通用下拉浮层：标题 + 若干行 + 行内回调。
type popupMenu struct {
	Title  string
	Items  []popupItem
	Anchor ui.Rect
	Up     bool // true 表示在锚点上方展开（输入区按钮用）
}

type popupItem struct {
	Text    string
	Hint    string
	Check   bool // 当前选中项打勾
	Divider bool // 仅画一条分隔线
	Run     func()
}

// modelChoices 汇总所有端点的模型，供会话内切换。
// 本地端点（Ollama 等）也列出来——离线环境恰恰最需要切模型。
func (a *App) modelChoices() []providerModel {
	var out []providerModel
	for _, p := range a.cfg.Providers {
		if strings.TrimSpace(p.BaseURL) == "" {
			continue
		}
		// 只列用户真正配过 API Key 的服务商：没填 Key 的端点不该出现在
		// 会话模型菜单里，点进去也只会鉴权失败。本地端点（NoAuth）例外，
		// 它们本来就不需要 Key。
		if strings.TrimSpace(p.APIKey) == "" && !p.NoAuth {
			continue
		}
		name := p.Name
		if name == "" {
			name = p.ID
		}
		for _, m := range a.discoverModels(p) {
			if strings.TrimSpace(m) == "" {
				continue
			}
			out = append(out, providerModel{Prov: p.ID, PName: name, Model: m})
		}
	}
	return out
}

// openModelMenu 打开模型选择浮层。模型可能来自 32 项的 OpenCode Go，
// 菜单不支持滚动，按 12 个一页分页，否则后面的模型永远点不到。
func (a *App) openModelMenu(anchor ui.Rect, up bool) {
	cur := a.convModel()
	curProv := a.convProvider().ID
	choices := a.modelChoices()
	if len(choices) == 0 {
		a.toast("还没有可用模型，请先在设置里配置服务商", true)
		return
	}
	if a.modelMenuBase != "chat" {
		a.modelMenuBase = "chat"
		a.modelMenuPage = 0
	}
	const pageSize = 12
	if a.modelMenuPage < 0 {
		a.modelMenuPage = 0
	}
	start := a.modelMenuPage * pageSize
	if start >= len(choices) {
		start = 0
		a.modelMenuPage = 0
	}
	end := start + pageSize
	if end > len(choices) {
		end = len(choices)
	}
	var items []popupItem
	for _, c := range choices[start:end] {
		c := c
		items = append(items, popupItem{
			Text:  c.Model,
			Hint:  c.PName,
			Check: c.Model == cur && c.Prov == curProv,
			Run:   func() { a.closeMenu(); a.setSessionModel(c.Prov, c.Model) },
		})
	}
	if start > 0 {
		items = append(items, popupItem{
			Text: "上一页",
			Hint: fmt.Sprintf("%d/%d", a.modelMenuPage, (len(choices)+pageSize-1)/pageSize),
			Run: func() {
				a.closeMenu()
				a.modelMenuPage--
				a.openModelMenu(anchor, up)
			},
		})
	}
	if end < len(choices) {
		items = append(items, popupItem{
			Text: "下一页",
			Hint: fmt.Sprintf("%d/%d", a.modelMenuPage+2, (len(choices)+pageSize-1)/pageSize),
			Run: func() {
				a.closeMenu()
				a.modelMenuPage++
				a.openModelMenu(anchor, up)
			},
		})
	}
	items = append(items, popupItem{Divider: true})
	items = append(items, popupItem{
		Text:  "跟随默认模型",
		Hint:  a.defaultModelLabel(),
		Check: !a.sessionModelPinned(),
		Run:   func() { a.closeMenu(); a.setSessionModel("", "") },
	})
	a.menu = &popupMenu{Title: "本会话模型", Items: items, Anchor: anchor, Up: up}
	a.menuIdx = 0
	for i, it := range items {
		if it.Check {
			a.menuIdx = i
		}
	}
	a.Redraw()
}

// openDirMenu 打开工作区选择浮层。
func (a *App) openDirMenu(anchor ui.Rect, up bool) {
	items := []popupItem{{
		Text: "选择文件夹…",
		Hint: "系统目录选择框",
		Run:  func() { a.pickSessionDir() },
	}}
	cur := a.convWorkDir()
	for _, d := range a.cfg.RecentDirs {
		d := d
		if strings.EqualFold(d, cur) {
			continue
		}
		items = append(items, popupItem{
			Text: filepath.Base(d),
			Hint: d,
			Run:  func() { a.closeMenu(); a.setSessionDir(d) },
		})
	}
	items = append(items, popupItem{Divider: true})
	items = append(items, popupItem{
		Text:  "跟随默认工作目录",
		Hint:  a.cfg.WorkDir,
		Check: !a.sessionDirPinned(),
		Run:   func() { a.closeMenu(); a.setSessionDir("") },
	})
	a.menu = &popupMenu{Title: "本会话工作区", Items: items, Anchor: anchor, Up: up}
	a.menuIdx = 0
	for i, it := range items {
		if it.Check {
			a.menuIdx = i
		}
	}
	a.Redraw()
}

func (a *App) defaultModelLabel() string {
	p := a.cfg.Provider()
	m := strings.TrimSpace(a.cfg.Model)
	if m == "" {
		m = p.Model
	}
	if m == "" {
		m = "未设置模型"
	}
	if p.Name != "" {
		return p.Name + " · " + m
	}
	return m
}

func (a *App) closeMenu() {
	if a.menu == nil {
		return
	}
	a.menu = nil
	a.menuIdx = 0
	a.Redraw()
}

// menuKey 上下选、回车执行、Esc 收起。
func (a *App) menuKey(vk int32) bool {
	m := a.menu
	if m == nil {
		return true
	}
	switch vk {
	case win32.VK_ESCAPE:
		a.closeMenu()
	case win32.VK_UP:
		a.menuStep(-1)
	case win32.VK_DOWN:
		a.menuStep(1)
	case win32.VK_RETURN:
		if a.menuIdx >= 0 && a.menuIdx < len(m.Items) {
			it := m.Items[a.menuIdx]
			if !it.Divider && it.Run != nil {
				it.Run()
			}
		}
	}
	return true
}

// menuStep 上下移动选中项，跳过分隔线。
func (a *App) menuStep(d int) {
	if a.menu == nil {
		return
	}
	n := len(a.menu.Items)
	if n == 0 {
		return
	}
	for i := 0; i < n; i++ {
		a.menuIdx = (a.menuIdx + d + n) % n
		if !a.menu.Items[a.menuIdx].Divider {
			break
		}
	}
	a.Redraw()
}

// menuClick 命中行即执行；点空白处收起。
func (a *App) menuClick(x, y int32) {
	id := a.ui.HandleClick(x, y)
	if strings.HasPrefix(id, "menu:") {
		if i, err := strconv.Atoi(id[5:]); err == nil && a.menu != nil && i >= 0 && i < len(a.menu.Items) {
			it := a.menu.Items[i]
			if !it.Divider && it.Run != nil {
				it.Run()
			}
		}
		return
	}
	if !a.menuRect.Contains(x, y) {
		a.closeMenu()
	}
}

// menuGeom 计算浮层尺寸：宽度按最长行自适应，高度超限则截断行数。
func (a *App) menuGeom(c *ui.Canvas, m *popupMenu) (ui.Rect, []int32, int32) {
	W, H := a.winW, a.winH
	const padX = int32(14)
	header := int32(32)
	footer := int32(40)
	rowH := int32(34)

	w := int32(320)
	for _, it := range m.Items {
		if it.Divider {
			continue
		}
		tw := c.MeasureFace(it.Text, ui.FsBase, 500, ui.FaceUI)
		hw := c.MeasureFace(it.Hint, ui.FsXs, 400, ui.FaceUI)
		if need := tw + hw + padX*2 + 34; need > w {
			w = need
		}
	}
	if w > W-80 {
		w = W - 80
	}
	// 逐行算高度，超出可视区就截断
	maxH := H - 140
	offs := make([]int32, 0, len(m.Items))
	// total 只累计标题与各列表项；页脚在最后统一追加。
	// 过去把 footer 加在开头，最后一项会直接压到页脚提示上。
	total := header
	for _, it := range m.Items {
		h := rowH
		if it.Divider {
			h = 9
		}
		if total+h+footer > maxH {
			break
		}
		offs = append(offs, total)
		total += h
	}
	total += footer
	h := total

	x := m.Anchor.Right() - w
	if x < 12 {
		x = 12
	}
	if x+w > W-12 {
		x = W - 12 - w
	}
	y := m.Anchor.Y - h - 6
	if !m.Up {
		y = m.Anchor.Bottom() + 6
	}
	if y < 12 {
		y = 12
	}
	if y+h > H-12 {
		y = H - 12 - h
	}
	return ui.R(x, y, w, h), offs, rowH
}

// paintMenu 绘制下拉浮层。
func (a *App) paintMenu(c *ui.Canvas) {
	th := a.th
	u := a.ui
	m := a.menu
	if m == nil {
		return
	}
	r, offs, rowH := a.menuGeom(c, m)
	a.menuRect = r

	c.Round(ui.R(r.X+1, r.Y+3, r.W, r.H), 8, ui.Blend(th.Bg, 0x000000, 0.14))
	u.Panel(r, th.Surface, th.Border, 10)
	c.TextFace(r.X+14, r.Y+9, m.Title, ui.FsXs, 700, th.TextMute, ui.FaceUI)
	c.HLine(r.X+14, r.Y+31, r.W-28, th.BorderSoft)

	for i, it := range m.Items {
		if i >= len(offs) {
			break
		}
		yy := r.Y + offs[i]
		if it.Divider {
			c.HLine(r.X+14, yy+4, r.W-28, th.BorderSoft)
			continue
		}
		rowR := ui.R(r.X+6, yy, r.W-12, rowH)
		hover := u.Hit(fmt.Sprintf("menu:%d", i), rowR)
		if i == a.menuIdx || hover {
			c.Round(rowR, 6, ui.Blend(th.Surface, th.Accent, 0.14))
		}
		tx := r.X + 32
		rightLimit := r.Right() - 14
		// 先给右侧说明留出固定区域，再把左侧标题裁到剩余宽度：
		// 过去两者各按整行宽度排版，长说明会直接压在标题上。
		hintW := int32(0)
		hintText := ""
		if it.Hint != "" {
			maxHint := r.W / 2
			hw := c.MeasureFace(it.Hint, ui.FsXs, 400, ui.FaceUI)
			if hw > maxHint {
				hw = maxHint
			}
			if hw > 40 {
				hintW = hw
				hintText = ellipsis(c, it.Hint, ui.FsXs, 400, hw)
			}
		}
		textMax := rightLimit - tx - 14 - hintW
		if textMax < 40 {
			textMax = 40
		}
		c.TextFace(tx, yy+8, ellipsis(c, it.Text, ui.FsBase, 500, textMax), ui.FsBase, 500, th.Text, ui.FaceUI)
		if hintText != "" {
			c.TextFace(rightLimit-hintW, yy+10, hintText, ui.FsXs, 400, th.TextMute, ui.FaceUI)
		}
		if it.Check {
			drawIcon(c, "check", ui.R(r.X+12, yy+9, 14, 16), th.Accent)
		}
	}
	c.HLine(r.X+14, r.Bottom()-29, r.W-28, th.BorderSoft)
	c.TextFace(r.X+14, r.Bottom()-22, "↑↓ 选择 · Enter 确认 · Esc 收起", ui.FsXs, 400, th.TextMute, ui.FaceUI)
}

/* ---------------- /model 与 /dir 命令 ---------------- */

// cmdRouter 处理 /model router [on|off]：
//
//	/model router        查看当前路由状态
//	/model router on     开启：压缩与只读子代理走快速模型
//	/model router off    关闭：全部走主模型
func (a *App) cmdRouter(arg string) {
	switch strings.ToLower(strings.TrimSpace(arg)) {
	case "":
		line := a.engRouterSummary()
		hint := "\n\n开启后：上下文压缩、只读子代理走快速模型，主对话不变。\n" +
			"用法：`/model router on` · `/model router off` · `/model fast <模型名或序号>`"
		if a.cfg.FastModel == "" {
			hint += "\n⚠ 还没指定快速模型，先用 `/model fast 序号` 选一个，否则打开也不生效。"
		}
		a.sysNote("**模型路由**\n" + line + hint)
	case "on", "开", "开启":
		a.cfg.RouterOn = true
		_ = a.cfg.Save()
		a.applyRouter()
		a.sysNote(a.engRouterSummary() + "\n\n省钱效果：`/cost` 里能看到轻任务单独的开销，以及换成主模型要多花多少。")
	case "off", "关", "关闭":
		a.cfg.RouterOn = false
		_ = a.cfg.Save()
		a.applyRouter()
		a.sysNote("模型路由已关闭，全部任务使用主模型 " + dashIfEmpty(a.cfg.Model))
	default:
		a.toast("用法：/model router [on|off]", true)
	}
}

// cmdFast 处理 /model fast <模型名或序号>：指定轻任务使用的模型。
func (a *App) cmdFast(arg string) {
	arg = strings.TrimSpace(arg)
	choices := a.modelChoices()
	if len(choices) == 0 {
		a.sysNote("没有可用模型。请先到「设置」页选择服务商并填写地址与 Key。")
		return
	}
	if arg == "" {
		var sb strings.Builder
		sb.WriteString("**选择轻任务模型**（当前：" + dashIfEmpty(a.cfg.FastModel) + "）\n")
		for i, c := range choices {
			mark := "  "
			if c.Model == a.cfg.FastModel {
				mark = "→ "
			}
			fmt.Fprintf(&sb, "%s%d. `%s` — %s\n", mark, i+1, c.Model, c.PName)
		}
		sb.WriteString("\n用法：`/model fast 2` 或 `/model fast 模型名`")
		a.sysNote(sb.String())
		return
	}
	pick := ""
	if n, err := strconv.Atoi(arg); err == nil {
		if n < 1 || n > len(choices) {
			a.toast(fmt.Sprintf("序号超出范围（1-%d）", len(choices)), true)
			return
		}
		pick = choices[n-1].Model
	} else {
		for _, c := range choices {
			if strings.EqualFold(c.Model, arg) {
				pick = c.Model
				break
			}
		}
		if pick == "" {
			for _, c := range choices {
				if strings.Contains(strings.ToLower(c.Model), strings.ToLower(arg)) {
					pick = c.Model
					break
				}
			}
		}
	}
	if pick == "" {
		a.toast("没有匹配的模型："+arg, true)
		return
	}
	a.cfg.FastModel = pick
	if !a.cfg.RouterOn {
		a.cfg.RouterOn = true
	}
	_ = a.cfg.Save()
	a.applyRouter()
	a.sysNote(a.engRouterSummary() + "\n\n已同时打开路由。关掉用 `/model router off`。")
}

// engRouterSummary 返回引擎的路由摘要；引擎还没建好时给配置层的话术。
func (a *App) engRouterSummary() string {
	if a.eng != nil {
		return a.eng.RouterSummary()
	}
	if !a.cfg.RouterOn {
		return "模型路由：关闭（全部使用主模型 " + dashIfEmpty(a.cfg.Model) + "）"
	}
	return "模型路由：开启｜轻任务 " + dashIfEmpty(a.cfg.FastModel)
}

// applyRouter 把配置里的路由设置同步到引擎。
func (a *App) applyRouter() {
	if a.eng == nil {
		return
	}
	a.eng.SetRouter(a.cfg.RouterOn, a.cfg.FastModel)
	a.eng.SubTaskMaxChars = a.cfg.SubTaskMaxChars
}

// cmdModel 处理 /model：
//
//	/model            列出所有端点下的模型，标出当前使用的
//	/model 3          按序号切
//	/model gpt-4o     按模型名切（重名时取第一个可用端点）
//	/model default    恢复跟随全局默认
func (a *App) cmdModel(rest string) {
	rest = strings.TrimSpace(rest)
	// router 子命令在前：它管的是"哪类任务用哪个模型"，与"切本会话模型"是两件事，
	// 混在序号选择里容易误操作。
	if strings.HasPrefix(strings.ToLower(rest), "router") {
		a.cmdRouter(strings.TrimSpace(rest[len("router"):]))
		return
	}
	if strings.HasPrefix(strings.ToLower(rest), "fast") {
		a.cmdFast(strings.TrimSpace(rest[len("fast"):]))
		return
	}
	choices := a.modelChoices()
	if len(choices) == 0 {
		a.sysNote("没有可用模型。请先到「设置」页选择服务商并填写地址与 Key。")
		return
	}
	curProv := a.convProvider().ID
	cur := a.convModel()

	if rest == "" {
		var sb strings.Builder
		sb.WriteString("**模型清单**（仅当前会话生效，序号可直接 `/model 2` 切）\n")
		for i, c := range choices {
			mark := "  "
			if c.Model == cur && c.Prov == curProv {
				mark = "→ "
			}
			pin := ""
			if a.sessionModelPinned() && c.Model == cur && c.Prov == curProv {
				pin = " · 本会话"
			}
			fmt.Fprintf(&sb, "%s%d. `%s` — %s%s\n", mark, i+1, c.Model, c.PName, pin)
		}
		fmt.Fprintf(&sb, "\n当前生效：`%s`（端点 %s）\n", cur, a.convProvider().Name)
		if a.sessionModelPinned() {
			sb.WriteString("本会话已钉住模型；`/model default` 恢复跟随默认。\n")
		} else {
			sb.WriteString("当前跟随默认模型；`/model 序号` 可只改本会话。\n")
		}
		a.sysNote(sb.String())
		return
	}

	if strings.EqualFold(rest, "default") || rest == "默认" {
		a.setSessionModel("", "")
		return
	}

	// 序号优先：允许 1-based
	if n, err := strconv.Atoi(rest); err == nil {
		if n < 1 || n > len(choices) {
			a.toast(fmt.Sprintf("序号超出范围（1-%d）", len(choices)), true)
			return
		}
		c := choices[n-1]
		a.setSessionModel(c.Prov, c.Model)
		return
	}

	// 名字匹配：先精确，再忽略大小写包含
	var hit *providerModel
	for i := range choices {
		if choices[i].Model == rest {
			hit = &choices[i]
			break
		}
	}
	if hit == nil {
		r := strings.ToLower(rest)
		for i := range choices {
			if strings.Contains(strings.ToLower(choices[i].Model), r) {
				hit = &choices[i]
				break
			}
		}
	}
	if hit == nil {
		a.toast("没有匹配的模型："+rest+"，输入 /model 查看清单", true)
		return
	}
	a.setSessionModel(hit.Prov, hit.Model)
}

// cmdWorkDir 处理 /dir：
//
//	/dir              显示当前工作区
//	/dir C:\proj      切换本会话工作区
//	/dir pick         弹系统目录选择框
//	/dir default      恢复跟随全局默认
func (a *App) cmdWorkDir(rest string) {
	rest = strings.TrimSpace(rest)
	if rest == "" {
		var sb strings.Builder
		sb.WriteString("**工作区**\n")
		fmt.Fprintf(&sb, "- 本会话：`%s`\n", a.convWorkDir())
		fmt.Fprintf(&sb, "- 默认目录：`%s`\n", a.cfg.WorkDir)
		if a.sessionDirPinned() {
			sb.WriteString("- 状态：本会话已指定目录，`/dir default` 恢复跟随默认\n")
		} else {
			sb.WriteString("- 状态：跟随默认目录，`/dir 路径` 可只改本会话\n")
		}
		if len(a.cfg.RecentDirs) > 0 {
			sb.WriteString("\n最近使用：\n")
			for i, d := range a.cfg.RecentDirs {
				if i >= 5 {
					break
				}
				fmt.Fprintf(&sb, "- `%s`\n", d)
			}
		}
		sb.WriteString("\n提示：`/dir pick` 弹系统目录选择框；顶栏文件夹按钮同效。")
		a.sysNote(sb.String())
		return
	}
	if strings.EqualFold(rest, "pick") || rest == "选择" {
		a.pickSessionDir()
		return
	}
	if strings.EqualFold(rest, "default") || rest == "默认" {
		a.setSessionDir("")
		return
	}
	// 去掉可能带的引号，方便从资源管理器复制路径
	rest = strings.Trim(rest, "\"'")
	a.setSessionDir(rest)
}

// dashIfEmpty 空值给个占位话术，避免界面上出现「模型：」后面什么都没有。
func dashIfEmpty(s string) string {
	if strings.TrimSpace(s) == "" {
		return "（未设置）"
	}
	return s
}
