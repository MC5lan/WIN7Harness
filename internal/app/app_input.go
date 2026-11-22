//go:build windows

package app

import (
	"fmt"
	"os"
	"strings"
	"time"
	"win7harness/internal/core"
	"win7harness/internal/ui/kit"
	"win7harness/internal/win32"
)

// 本文件由 app.go 按职责拆分（T07），仅移动代码，未改行为。
// 输入：鼠标/键盘/滚轮消息与点击分发。

func (a *App) MouseMove(x, y int32) {
	a.beat()
	a.mx, a.my = x, y
	// kit 页面：只同步坐标给 kit Ctx（hover 依赖它）
	if isKitPage(a.page) {
		if k := a.kitui; k != nil {
			k.Mouse(x, y)
		}
		return
	}
	if a.dragMode != 0 {
		a.dragPanel(x)
		return
	}
	// 首帧 Paint 之前 a.ui 还没建立；WM_MOUSEMOVE 可能先到。
	if a.ui == nil {
		return
	}
	// 主输入区按下后拖动＝拖选，而不是只改悬停态。
	if id, btn := a.ui.Pressed(); id == "input" && btn == 0 {
		if a.inputSel < 0 {
			a.inputSel = a.inputCur
		}
		a.inputCur = a.inputHitRune(x, y)
		return
	}
	// 悬停态随时刷新，供分隔条高亮与左右箭头光标使用
	a.hbHover = a.handleAt(x, y)
}

func (a *App) MouseDown(x, y int32, button int) {
	a.beat()
	if a.ui == nil {
		return
	}
	// kit 页面正文区走 kit 命中；rail 与顶栏（返回按钮）仍走 classic，
	// 否则「← 返回对话」永远收不到按下。
	if a.kitOwnsPoint(x, y) {
		if k := a.kitui; k != nil {
			k.Mouse(x, y)
			k.MouseDown(x, y, button)
		}
		return
	}
	// 记下按下时命中的控件：抬起必须落在同一个控件上、且是左键才派发。
	a.ui.Press(a.ui.HitAt(x, y), button)
	if a.palOpen {
		return
	}
	if a.menu != nil && !a.menuRect.Contains(x, y) {
		// 点浮层外面即收起；抬起时也要吞掉，不能穿透到下层控件。
		a.closeMenu()
		a.suppressClick = true
		return
	}
	if a.modal != nil {
		return
	}
	// 点击输入区即聚焦
	if a.page == PageChat {
		// 分隔条优先命中：贴边点击不该被下面的按钮抢走
		if h := a.handleAt(x, y); h != 0 {
			a.beginDrag(h, x)
			return
		}
		// 抽屉展开时点空白处即收起，符合抽屉的直觉
		if a.sideDrawer && a.sideW > 8 && x > a.railW+a.sideW {
			a.showSide = false
			a.hbHover = 0
			a.layout()
			return
		}
		if a.inspDrawer && a.inspW > 8 && x < a.winW-a.inspW {
			a.showInsp = false
			a.hbHover = 0
			a.layout()
			return
		}
		// 先命中附件缩略图：点一下移除
		for i, rc := range a.chipRects {
			if rc.Contains(x, y) {
				a.removePendingImage(i)
				a.toast("已移除图片", false)
				return
			}
		}
		h := a.inputHeight()
		if y > a.winH-h-8 && x > a.railW+a.sideW {
			a.ui.SetFocus("input")
			if a.ui.HitAt(x, y) == "input" {
				a.inputCur = a.inputHitRune(x, y)
				a.inputSel = -1
			}
		}
	}
}

func (a *App) MouseUp(x, y int32, button int) {
	a.beat()
	if a.ui == nil {
		return
	}
	// kit 页面正文区走 kit 命中；rail 与顶栏交给 classic。
	if a.kitOwnsPoint(x, y) {
		if k := a.kitui; k != nil {
			k.Mouse(x, y)
			k.MouseUp(x, y, button)
		}
		return
	}
	press, pbtn := a.ui.Pressed()
	a.ui.ClearPress()
	if a.dragMode != 0 { // 松手落盘，下次启动保持同一宽度
		a.dragMode = 0
		a.savePanelW()
		return
	}
	if a.suppressClick {
		a.suppressClick = false
		return
	}
	// 右键/中键不派发动作；按下与抬起必须落在同一控件上。
	if button != 0 || pbtn != 0 {
		return
	}
	if a.qrVisible() {
		// 二维码面板是"看一眼就走"的浮层，点哪儿都关，不设关闭按钮。
		a.closeQR()
		return
	}
	if a.palOpen {
		a.palClick(x, y)
		return
	}
	if a.histOpen {
		a.histClick(x, y)
		return
	}
	if a.menu != nil {
		a.menuClick(x, y)
		return
	}
	if a.modal != nil {
		a.modalClick(x, y)
		return
	}
	id := a.ui.HandleClick(x, y)
	if id == "" || (press != "" && press != id) {
		return
	}
	a.dispatchClick(id)
}

func (a *App) dispatchClick(id string) {
	if os.Getenv("WIN7H_TRACE_CLICK") != "" {
		core.Logf("[click] %s page=%d", id, a.page)
	}
	switch {
	case strings.HasPrefix(id, "chip:"):
		idx := 0
		fmt.Sscanf(id, "chip:%d", &idx)
		chips := []string{
			"帮我看看这个目录的项目结构",
			"用 Go 写一个命令行待办工具并编译",
			"分析这里的报错并修复",
			"给这个项目补一份 README",
		}
		if idx >= 0 && idx < len(chips) {
			a.input = []rune(chips[idx])
			a.inputCur = len(a.input)
			a.send()
		}
	case strings.HasPrefix(id, "conv:"):
		idx := 0
		fmt.Sscanf(id, "conv:%d", &idx)
		if idx >= 0 && idx < len(a.convs) {
			if cur := a.currentConv(); cur != nil {
				cur.Scroll = a.scroll
			}
			a.saveConv()
			a.active = idx
			a.hlMsg = -1
			a.loadConv()
			// 侧栏现在在所有页面都可见：从设置里点会话应回到对话页。
			a.page = PageChat
		}
	case strings.HasPrefix(id, "hit:"):
		var mi, hi int
		fmt.Sscanf(id, "hit:%d:%d", &mi, &hi)
		if mi < len(a.msgs) && hi < len(a.msgs[mi].Hits) {
			a.openHitView(a.msgs[mi].Hits[hi])
		}
	case strings.HasPrefix(id, "path:"):
		idx := 0
		fmt.Sscanf(id, "path:%d", &idx)
		if idx >= 0 && idx < len(a.msgLinks) {
			link := a.msgLinks[idx]
			a.openPathAtLine(link.Abs, link.From)
		}
	case id == "drw.side.close":
		a.showSide = false
		a.hbHover = 0
		a.layout()
	case id == "drw.insp.close":
		a.showInsp = false
		a.hbHover = 0
		a.layout()
	case strings.HasPrefix(id, "tool:"):
		var mi, ti int
		fmt.Sscanf(id, "tool:%d:%d", &mi, &ti)
		if mi < len(a.msgs) && ti < len(a.msgs[mi].Tools) {
			a.msgs[mi].Tools[ti].Expanded = !a.msgs[mi].Tools[ti].Expanded
			a.ver++
		}
	case strings.HasPrefix(id, "conv.pin:"):
		idx := 0
		fmt.Sscanf(id, "conv.pin:%d", &idx)
		a.togglePin(idx)
	case strings.HasPrefix(id, "conv.ren:"):
		idx := 0
		fmt.Sscanf(id, "conv.ren:%d", &idx)
		a.startRename(idx)
	case strings.HasPrefix(id, "conv.del:"):
		idx := 0
		fmt.Sscanf(id, "conv.del:%d", &idx)
		a.requestDeleteConv(idx)
	case id == "input.img":
		// 一个按钮吃两种来源：剪贴板有图就贴，没有就弹选图框
		if !a.pasteClipboardImage() {
			a.pickImages()
		}
	case id == "input.snip":
		a.startSnip()
	case id == "input.cmd":
		a.openPaletteWith("/")
	case strings.HasPrefix(id, "msgact.copy:"):
		idx := 0
		fmt.Sscanf(id, "msgact.copy:%d", &idx)
		a.copyMsg(idx)
	case strings.HasPrefix(id, "msgact.retry:"):
		idx := 0
		fmt.Sscanf(id, "msgact.retry:%d", &idx)
		a.retryFrom(idx)
	case strings.HasPrefix(id, "msgact.edit:"):
		idx := 0
		fmt.Sscanf(id, "msgact.edit:%d", &idx)
		a.editMsg(idx)
	case strings.HasPrefix(id, "toolcopy:"):
		var mi, ti int
		fmt.Sscanf(id, "toolcopy:%d:%d", &mi, &ti)
		a.copyToolOutput(mi, ti)
	case strings.HasPrefix(id, "codeact.copy:"):
		var mi, bi int
		fmt.Sscanf(id, "codeact.copy:%d:%d", &mi, &bi)
		a.copyCodeBlock(mi, bi)
	case strings.HasPrefix(id, "codeact.save:"):
		var mi, bi int
		fmt.Sscanf(id, "codeact.save:%d:%d", &mi, &bi)
		a.saveCodeBlock(mi, bi)
	}
}

func (a *App) MouseWheel(x, y int32, delta int32) {
	a.beat()
	if a.perf != nil && a.perf.on {
		t0 := time.Now()
		defer func() { a.observeWheel(time.Since(t0)) }()
	}
	// 原始增量换算成整格：高精度滚轮的小增量会先积累，攒够一格再动。
	n := a.wheelNotches(delta)
	if n == 0 {
		return
	}
	step := -n // 向下滚为正

	// 富差分弹窗内容可能超出一屏，滚轮在这里独立滚动（单位是像素）。
	if a.modal != nil && a.modal.Kind == modalDiff {
		a.modal.Scroll += float32(step) * 54
		if a.modal.Scroll < 0 {
			a.modal.Scroll = 0
		}
		if a.modal.ScrollMax > 0 && a.modal.Scroll > a.modal.ScrollMax {
			a.modal.Scroll = a.modal.ScrollMax
		}
		a.Redraw()
		return
	}
	if a.modal != nil && a.modal.Kind == modalPlan {
		a.modal.Scroll += float32(step) * 54
		if a.modal.Scroll < 0 {
			a.modal.Scroll = 0
		}
		if a.modal.ScrollMax > 0 && a.modal.Scroll > a.modal.ScrollMax {
			a.modal.Scroll = a.modal.ScrollMax
		}
		a.Redraw()
		return
	}
	// kit 页面：先确认指针在内容区，避免在 rail / 顶栏上滚轮误滚整页。
	if isKitPage(a.page) {
		if !a.kitOwnsPoint(x, y) {
			return
		}
		if k := a.kitui; k != nil {
			for _, key := range a.kitScrollKeys() {
				kit.ScrollBy(k, key, step*64)
			}
		}
		return
	}
	if a.page == PageDiag {
		if y < 60 {
			return
		}
		view := float32(a.winH) - 116 - 16
		max := float32(a.diag.height) - view
		if max < 0 {
			max = 0
		}
		a.diag.scroll += float32(step) * 64
		if a.diag.scroll < 0 {
			a.diag.scroll = 0
		}
		if a.diag.scroll > max {
			a.diag.scroll = max
		}
		return
	}
	if a.page == PageSettings {
		if y < 108 {
			return
		}
		max := float32(a.settingsH) - (float32(a.winH) - 144)
		if max < 0 {
			max = 0
		}
		a.settingsScrl += float32(step) * 64
		if a.settingsScrl < 0 {
			a.settingsScrl = 0
		}
		if a.settingsScrl > max {
			a.settingsScrl = max
		}
		return
	}
	if a.page != PageChat {
		return
	}
	if a.modal != nil {
		return
	}
	if y < 46 {
		return
	}
	// 输入卡内有溢出内容时，滚轮优先滚动输入（否则滚动对话）
	if a.inputRect.W > 0 && y >= a.inputRect.Y {
		line := step
		lines := a.wrapInput()
		vis := a.inputVisibleLines()
		if len(lines) > vis {
			scroll := a.inputScroll + line
			if scroll < 0 {
				scroll = 0
			}
			if max := int32(len(lines) - vis); scroll > max {
				scroll = max
			}
			a.inputScroll = scroll
			a.inputPinScroll = true
			return
		}
	}
	// 侧栏会话列表滚动（搜索过滤后条目变少，限位按实际内容算）
	if a.sideW > 0 && x < a.railW+a.sideW {
		a.convScrl += float32(step) * 56
		if a.convScrl < 0 {
			a.convScrl = 0
		}
		max := float32(a.convListH) - (float32(a.winH) - 116)
		if max < 0 {
			max = 0
		}
		if a.convScrl > max {
			a.convScrl = max
		}
		return
	}
	a.scroll += float32(step) * 64
	if a.scroll < 0 {
		a.scroll = 0
	}
	max := float32(a.contentH) - (float32(a.winH) - 46 - float32(a.inputHeight()) - 12)
	if max < 0 {
		max = 0
	}
	if a.scroll > max {
		a.scroll = max
	}
	a.autoScroll = a.scroll >= max-4
}

// kitScrollKeys 返回当前页面真正需要滚动的 Scroll 容器 key。
// 滚轮与 PageUp/PageDown/Home/End 共用这一份映射，避免两处漏配。
func (a *App) kitScrollKeys() []string {
	switch a.page {
	case PageSessions:
		return []string{"s:main"}
	case PageWorkspace:
		return []string{"w:main"}
	case PageCosts:
		return []string{"cost:main"}
	case PageCompare:
		return []string{"cmp:当前会话", "cmp:对照会话"}
	case PageAuto:
		switch a.autoTab {
		case autoTabQueue:
			return []string{"auto:queue"}
		case autoTabSched:
			return []string{"auto:sched"}
		case autoTabBg:
			return []string{"auto:bg"}
		default:
			return []string{"auto:watch"}
		}
	case PageUsage:
		return []string{"use:main"}
	case PageExt:
		switch a.extTab {
		case extTabDevice:
			return []string{"ext:device"}
		case extTabRemote:
			return []string{"ext:remote"}
		case extTabAgents:
			return []string{"ext:agents"}
		case extTabSkills:
			return []string{"ext:skills"}
		case extTabMCP:
			return []string{"ext:mcp"}
		case extTabMem:
			return []string{"ext:mem"}
		}
	case PageNotices:
		return []string{"notice:main"}
	case PageDiag:
		switch a.diag.tab {
		case diagTabChecks:
			return []string{"diag:checks"}
		case diagTabLogs:
			return []string{"diag:logs"}
		case diagTabEndToEnd:
			return []string{"diag:e2e"}
		case diagTabEval:
			return []string{"diag:eval"}
		case diagTabCrash:
			return []string{"diag:crash"}
		}
	}
	return nil
}

func (a *App) KeyDown(vk int32) bool {
	a.beat()
	if a.ui == nil {
		return false
	}
	if a.modal != nil && a.modal.Kind == modalPassword {
		return a.passwordKey(vk)
	}
	if isKitPage(a.page) {
		return a.keyKit(vk)
	}
	if handled, res := a.keyOverlays(vk); handled {
		return res
	}
	ctrl := win32.GetKeyState(win32.VK_CONTROL)
	shift := win32.GetKeyState(win32.VK_SHIFT)
	alt := win32.GetKeyState(win32.VK_MENU)
	if a.keyShortcuts(vk, ctrl, shift, alt) {
		return true
	}
	return a.keyNav(vk, ctrl, shift, alt)
}

// keyKit 处理 kit 页面的按键（焦点、翻页、输入）。
func (a *App) keyKit(vk int32) bool {
	if isKitPage(a.page) {
		if vk == win32.VK_ESCAPE {
			a.page = PageChat
			return true
		}
		// Tab / Shift+Tab：在 kit 控件（按钮/输入框）之间循环焦点
		if vk == win32.VK_TAB {
			if k := a.kitui; k != nil {
				k.MoveFocus(win32.GetKeyState(win32.VK_SHIFT))
			}
			return true
		}
		// 所有 kit 页：PageUp/PageDown 翻页，Home/End 到顶/到底。
		// 并列查看页额外支持 ↑↓ 微调两栏。
		if k := a.kitui; k != nil {
			keys := a.kitScrollKeys()
			switch vk {
			case win32.VK_PRIOR:
				for _, key := range keys {
					kit.ScrollPage(k, key, -1)
				}
				return true
			case win32.VK_NEXT:
				for _, key := range keys {
					kit.ScrollPage(k, key, 1)
				}
				return true
			case win32.VK_HOME:
				for _, key := range keys {
					kit.ScrollHome(k, key)
				}
				return true
			case win32.VK_END:
				for _, key := range keys {
					kit.ScrollEnd(k, key)
				}
				return true
			case win32.VK_UP, win32.VK_DOWN:
				if a.page == PageCompare {
					delta := int32(40)
					if vk == win32.VK_UP {
						delta = -40
					}
					for _, key := range keys {
						kit.ScrollBy(k, key, delta)
					}
					return true
				}
			}
		}
		if k := a.kitui; k != nil {
			if foc := k.Focus(); foc != "" {
				switch vk {
				case win32.VK_BACK:
					kit.InputKey(k, foc, 0, true)
					return true
				case win32.VK_RETURN:
					// Enter：激活聚焦的按钮（ kit 按钮/输入框都注册了命中）
					k.DispatchClick(foc)
					return true
				}
			}
		}
		return vk == win32.VK_RETURN || vk == win32.VK_ESCAPE
	}
	return false
}

// keyOverlays 处理改名、二维码、命令面板、历史、菜单、弹窗与补全。
// 第一个返回值表示事件是否被这一层接管，第二个返回值是最终结果。
func (a *App) keyOverlays(vk int32) (bool, bool) {
	if a.editingID >= 0 {
		switch vk {
		case win32.VK_ESCAPE:
			a.commitRename(false)
			return true, true
		case win32.VK_RETURN:
			a.commitRename(true)
			return true, true
		}
		if a.fieldKey(vk) {
			return true, true
		}
		return true, true // 改名期间吞掉其余按键，避免误触发发送/滚动
	}
	if a.qrVisible() {
		switch vk {
		case win32.VK_ESCAPE, win32.VK_RETURN, win32.VK_SPACE:
			a.closeQR()
		}
		return true, true
	}
	if a.palOpen {
		return true, a.palKey(vk)
	}
	if a.histOpen {
		return true, a.histKey(vk)
	}
	if a.menu != nil {
		return true, a.menuKey(vk)
	}
	if a.modal != nil {
		// 富差分与方案弹窗的默认键都是"安全侧"：Esc/回车默认取消或拒绝，
		// 必须 Tab 切到执行按钮后再回车，避免长任务里无脑回车误批。
		if a.modal.Kind == modalDiff {
			switch vk {
			case win32.VK_ESCAPE:
				a.modalCancelDiff()
			case win32.VK_TAB, win32.VK_LEFT, win32.VK_RIGHT:
				a.modalOKFocused = !a.modalOKFocused
			case win32.VK_RETURN, win32.VK_SPACE:
				if a.modalOKFocused {
					a.modalAnswerDiff(false)
				} else {
					a.modalCancelDiff()
				}
			}
			return true, true
		}
		if a.modal.Kind == modalPlan {
			switch vk {
			case win32.VK_ESCAPE:
				a.modalAnswerPlan("reject", "")
			case win32.VK_TAB, win32.VK_LEFT, win32.VK_RIGHT:
				a.modalOKFocused = !a.modalOKFocused
			case 'M', 'm':
				a.modalAnswerPlan("modify", a.modal.Plan)
			case win32.VK_RETURN, win32.VK_SPACE:
				if a.modalOKFocused {
					a.modalAnswerPlan("approve", "")
				} else {
					a.modalAnswerPlan("reject", "")
				}
			}
			return true, true
		}
		switch vk {
		case win32.VK_ESCAPE:
			a.modalAnswer(false)
			return true, true
		case win32.VK_TAB, win32.VK_LEFT, win32.VK_RIGHT:
			a.modalOKFocused = !a.modalOKFocused
			return true, true
		case win32.VK_RETURN, win32.VK_SPACE:
			a.modalAnswer(a.modalOKFocused)
			return true, true
		}
		return true, true
	}

	if len(a.comp) > 0 && a.ui.Focus() == "input" && a.page == PageChat {
		switch vk {
		case win32.VK_ESCAPE:
			a.comp = nil
			return true, true
		case win32.VK_UP:
			a.compIdx--
			if a.compIdx < 0 {
				a.compIdx = len(a.comp) - 1
			}
			return true, true
		case win32.VK_DOWN:
			a.compIdx++
			if a.compIdx >= len(a.comp) {
				a.compIdx = 0
			}
			return true, true
		case win32.VK_TAB:
			if a.acceptComplete() {
				return true, true
			}
		case win32.VK_RETURN:
			if a.acceptComplete() {
				return true, true
			}
		}
	}
	return false, false
}

// keyShortcuts 处理组合键与非聊天输入框的编辑键。
func (a *App) keyShortcuts(vk int32, ctrl, shift, alt bool) bool {
	// Ctrl+, 打开设置（0xBC = VK_OEM_COMMA）。桌面应用通用快捷键，
	// 也给“设置入口在左下角”提供一条不依赖鼠标的路径。
	if ctrl && a.cfg != nil && a.cfg.Onboarded && vk == 0xBC {
		a.page = PageSettings
		a.settingsScrl = 0
		return true
	}

	// 组合键优先于输入框编辑：Ctrl+R 即使焦点在输入框也要打开历史搜索。
	if a.keyMatches("historySearch", vk, ctrl, shift, alt) {
		a.openHistory()
		return true
	}

	// Tab / Shift+Tab 在表单与按钮之间循环焦点；补全候选打开时 Tab 仍归补全。
	if vk == win32.VK_TAB && !(len(a.comp) > 0 && a.ui.Focus() == "input" && a.page == PageChat) {
		a.moveFocus(shift)
		return true
	}

	// 侧栏搜索框等非聊天输入框的编辑键交给字段自己处理。
	if a.ui.Focus() != "input" && a.ui.Focus() != "" && a.fieldKey(vk) {
		return true
	}

	// 聊天主输入：选区、剪贴、撤销、跨行光标统一在这里处理。
	if a.ui.Focus() == "input" && a.page == PageChat && a.editKey(vk, ctrl, shift) {
		return true
	}

	if a.keyMatches("palette", vk, ctrl, shift, alt) {
		a.openPalette()
		return true
	}
	if a.keyMatches("sidebar", vk, ctrl, shift, alt) {
		a.toggleDrawer()
		return true
	}
	if a.keyMatches("inspector", vk, ctrl, shift, alt) {
		a.toggleInspector()
		return true
	}
	if a.keyMatches("toggleFold", vk, ctrl, shift, alt) {
		a.cfg.NoFoldTools = !a.cfg.NoFoldTools
		a.foldOpen = nil
		_ = a.cfg.Save()
		a.layout()
		if a.cfg.NoFoldTools {
			a.toast("工具卡：逐条显示", false)
		} else {
			a.toast("工具卡：按组折叠", false)
		}
		return true
	}
	if a.keyMatches("find", vk, ctrl, shift, alt) {
		if a.ui.Focus() == "conv.q" {
			a.ui.SetFocus("input")
		}
		a.openPaletteWith("/find ")
		return true
	}
	if a.ui.Focus() == "conv.q" && (vk == win32.VK_RETURN || vk == win32.VK_ESCAPE) {
		if vk == win32.VK_ESCAPE {
			a.ui.SetField("conv.q", "")
		}
		a.ui.SetFocus("input")
		return true
	}
	return false
}

// keyNav 处理聊天页最终按键：复制、粘贴、新建、历史与翻页。
func (a *App) keyNav(vk int32, ctrl, shift, alt bool) bool {
	switch vk {
	case win32.VK_ESCAPE:
		if a.streaming {
			a.stopGeneration()
			return true
		}
		return false
	case win32.VK_RETURN:
		if a.page != PageChat {
			a.pageDefaultAction()
			return true
		}
		if ctrl || !a.cfg.SendOnEnter || shift {
			a.insertInput("\n")
			return true
		}
		a.send()
		return true
	case win32.VK_C:
		if ctrl {
			if lo, hi := a.selRange(); lo >= 0 {
				win32.SetClipboardText(string(a.input[lo:hi]))
				return true
			}
			txt := string(a.input)
			if txt != "" {
				win32.SetClipboardText(txt)
				return true
			}
			if n := len(a.msgs); n > 0 && a.msgs[n-1].Role == "assistant" {
				win32.SetClipboardText(a.msgs[n-1].Text)
				a.toast("已复制最后一条回复", false)
				return true
			}
		}
	case win32.VK_V:
		if ctrl {
			if a.pasteClipboardImage() {
				return true
			}
			if txt := win32.GetClipboardText(); txt != "" {
				a.insertInput(txt)
			}
			return true
		}
	case 0x4E:
		if ctrl {
			a.newConv()
			return true
		}
	case 0x52: // Ctrl+R 的兜底：组合键解析失败时仍按默认打开历史
		if ctrl && !shift && !alt {
			a.openHistory()
			return true
		}
	case win32.VK_PRIOR:
		a.scroll -= 300
		if a.scroll < 0 {
			a.scroll = 0
		}
		return true
	case win32.VK_NEXT:
		a.scroll += 300
		max := float32(a.contentH) - (float32(a.winH) - 46 - float32(a.inputHeight()) - 12)
		if a.scroll > max {
			a.scroll = max
		}
		return true
	}
	return false
}
