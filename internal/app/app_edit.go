//go:build windows

package app

import (
	"win7harness/internal/ui/kit"
	"win7harness/internal/win32"
)

// 本文件由 app.go 按职责拆分（T09），仅移动代码，未改行为。
// 职责：聊天输入框的编辑键、焦点移动与字符输入。

// editKey 处理主输入框的编辑键；返回 true 表示已消费。
func (a *App) editKey(vk int32, ctrl, shift bool) bool {
	a.inputPinScroll = false // 编辑即跟随光标
	n := len(a.input)
	lines := a.inputLineBounds()
	lineOf := func(pos int) (int, int, int) {
		for i, l := range lines {
			if pos >= l.start && pos <= l.end {
				return i, l.start, l.end
			}
		}
		l := lines[len(lines)-1]
		return len(lines) - 1, l.start, l.end
	}
	setPos := func(p int) {
		if p < 0 {
			p = 0
		}
		if p > n {
			p = n
		}
		if shift {
			if a.inputSel < 0 {
				a.inputSel = a.inputCur
			}
			a.inputCur = p
		} else {
			a.inputSel = -1
			a.inputCur = p
		}
	}
	switch {
	case ctrl && (vk == 'A' || vk == 'a'):
		a.inputSel, a.inputCur = 0, n
		return true
	case ctrl && (vk == 'Z' || vk == 'z'):
		if len(a.inputUndo) > 0 {
			last := a.inputUndo[len(a.inputUndo)-1]
			a.inputUndo = a.inputUndo[:len(a.inputUndo)-1]
			a.input = append([]rune(nil), last...)
			a.inputCur, a.inputSel = len(a.input), -1
			a.refreshComplete()
		}
		return true
	case ctrl && (vk == 'X' || vk == 'x'):
		if lo, hi := a.selRange(); lo >= 0 {
			win32.SetClipboardText(string(a.input[lo:hi]))
			a.pushInputUndo()
			a.input = append(a.input[:lo], a.input[hi:]...)
			a.inputCur, a.inputSel = lo, -1
			a.refreshComplete()
			return true
		}
		return false
	case ctrl && (vk == 'C' || vk == 'c'):
		if lo, hi := a.selRange(); lo >= 0 {
			win32.SetClipboardText(string(a.input[lo:hi]))
			return true
		}
		return false
	case vk == win32.VK_BACK || vk == win32.VK_DELETE:
		if lo, hi := a.selRange(); lo >= 0 {
			a.pushInputUndo()
			a.input = append(a.input[:lo], a.input[hi:]...)
			a.inputCur, a.inputSel = lo, -1
		} else if vk == win32.VK_BACK && a.inputCur > 0 {
			a.pushInputUndo()
			a.input = append(a.input[:a.inputCur-1], a.input[a.inputCur:]...)
			a.inputCur--
		} else if vk == win32.VK_DELETE && a.inputCur < n {
			a.pushInputUndo()
			a.input = append(a.input[:a.inputCur], a.input[a.inputCur+1:]...)
		}
		a.refreshComplete()
		return true
	case vk == win32.VK_LEFT:
		setPos(a.inputCur - 1)
		return true
	case vk == win32.VK_RIGHT:
		setPos(a.inputCur + 1)
		return true
	case vk == win32.VK_HOME:
		_, st, _ := lineOf(a.inputCur)
		setPos(st)
		return true
	case vk == win32.VK_END:
		_, _, en := lineOf(a.inputCur)
		setPos(en)
		return true
	case vk == win32.VK_UP || vk == win32.VK_DOWN:
		row, st, _ := lineOf(a.inputCur)
		col := a.inputCur - st
		if vk == win32.VK_UP {
			row--
		} else {
			row++
		}
		if row < 0 {
			setPos(0)
			return true
		}
		if row >= len(lines) {
			setPos(n)
			return true
		}
		l := lines[row]
		p := l.start + col
		if p > l.end {
			p = l.end
		}
		setPos(p)
		return true
	}
	return false
}

// moveFocus 在页面内的输入框与默认按钮之间循环焦点。
func (a *App) moveFocus(shift bool) {
	order := []string{"input"}
	switch a.page {
	case PageOnboard:
		order = nil
		if a.onbStep == 1 {
			order = []string{"ob.base", "ob.key", "ob.model", "ob.dir", "ob.back", "ob.start"}
		}
	case PageSettings:
		switch a.settingsTab {
		case 0:
			order = []string{"set.base", "set.key", "set.model", "set.dir"}
		case 3:
			order = []string{"set.proxyurl", "set.tlsca"}
		case 4:
			order = []string{"set.maxcost", "set.budget", "set.rounds", "set.ctxwin", "set.subto", "set.pricein", "set.priceout"}
		case 5:
			order = []string{"hook.event", "hook.match", "hook.cmd", "set.temp", "set.maxtok", "set.gitname", "set.gitmail"}
		default:
			order = nil
		}
	case PageChat:
		order = []string{"conv.q", "input"}
	}
	if len(order) == 0 {
		return
	}
	cur := a.ui.Focus()
	idx := -1
	for i, id := range order {
		if id == cur {
			idx = i
			break
		}
	}
	step := 1
	if shift {
		step = -1
	}
	next := 0
	if idx >= 0 {
		next = (idx + step + len(order)) % len(order)
	} else if shift {
		next = len(order) - 1
	}
	a.ui.SetFocus(order[next])
}

func (a *App) fieldKey(vk int32) bool {
	id := a.ui.Focus()
	if id == "" {
		return false
	}
	f := a.ui.fields[id]
	if f == nil {
		return false
	}
	ctrl := win32.GetKeyState(win32.VK_CONTROL)
	switch vk {
	case win32.VK_BACK:
		f.Backspace()
		return true
	case win32.VK_DELETE:
		f.Delete()
		return true
	case win32.VK_LEFT:
		if f.Cur > 0 {
			f.Cur--
		}
		return true
	case win32.VK_RIGHT:
		if f.Cur < len(f.Text) {
			f.Cur++
		}
		return true
	case win32.VK_HOME:
		f.Cur = 0
		return true
	case win32.VK_END:
		f.Cur = len(f.Text)
		return true
	case win32.VK_V:
		if ctrl {
			if txt := win32.GetClipboardText(); txt != "" {
				f.Insert(txt)
			}
			return true
		}
	}
	return false
}

func (a *App) Char(ch rune) {
	a.beat()
	if a.ui == nil {
		return
	}
	if a.modal != nil && a.modal.Kind == modalPassword {
		if id := a.ui.Focus(); id == "modal.pwd1" || id == "modal.pwd2" {
			if f := a.ui.fields[id]; f != nil && ch >= 32 {
				f.Insert(string(ch))
			}
		}
		return
	}
	a.inputPinScroll = false // 编辑即跟随光标
	// kit 页面：聚焦到 kit 输入框就走 kit 的输入路径
	if isKitPage(a.page) {
		if k := a.kitui; k != nil {
			if foc := k.Focus(); foc != "" {
				kit.InputKey(k, foc, ch, false)
			}
		}
		return
	}
	if a.palOpen {
		a.palType(ch)
		return
	}
	if a.histOpen {
		a.histType(ch)
		return
	}
	if a.menu != nil {
		if ch == 27 {
			a.closeMenu()
		}
		return
	}
	if a.modal != nil {
		return
	}
	if a.page != PageChat {
		f := a.ui.fields[a.ui.Focus()]
		if f != nil {
			f.Insert(string(ch))
		}
		return
	}
	if ch < 32 {
		return
	}
	if id := a.ui.Focus(); id != "input" {
		if f := a.ui.fields[id]; f != nil {
			f.Insert(string(ch))
			return
		}
	}
	a.insertInput(string(ch))
}
