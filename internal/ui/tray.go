//go:build windows

package ui

import (
	"win7harness/internal/win32"
)

// 托盘与桌面通知的界面层封装。
//
// 单独放一个文件而不是塞进 window.go，是因为这部分逻辑与绘制无关：
// 它只关心"窗口句柄 + 一个回调"，出问题时可以整块摘掉不影响界面。

const (
	swHide    = 0
	swRestore = 9
)

// EnableTray 挂上托盘图标。失败（例如被系统策略禁托盘）返回 false，
// 调用方应当静默继续 —— 通知能力是锦上添花，不能成为启动的硬依赖。
func (w *Window) EnableTray(tip string) bool {
	if w.hwnd == 0 {
		return false
	}
	if ok := win32.TrayAdd(w.hwnd, w.hicon, tip); !ok {
		return false
	}
	w.trayOn = true
	return true
}

// SetTrayTip 更新悬停文字（例如显示当前是否在跑任务）。
func (w *Window) SetTrayTip(tip string) {
	if !w.trayOn {
		return
	}
	win32.TraySetTip(w.hwnd, tip)
}

// Notify 弹气泡通知。isErr 用红色错误图标，其余用信息图标。
// 没有托盘（trayOn=false）时退化为无操作，不报错。
func (w *Window) Notify(title, text string, isErr bool) bool {
	if !w.trayOn {
		return false
	}
	level := uint32(win32.NIIF_INFO)
	if isErr {
		level = win32.NIIF_ERROR
	}
	return win32.TrayBalloon(w.hwnd, title, text, level)
}

// SetTrayClick 设置托盘左键单击回调。
func (w *Window) SetTrayClick(fn func()) { w.onTrayClick = fn }

// trayEvent 处理托盘回调：左键回到前台，气泡被点时也回到前台。
func (w *Window) trayEvent(lp uintptr) {
	ev := uint32(lp) & 0xFFFF
	switch ev {
	case win32.WM_TRAYICON_LCLICK, win32.NIN_BALLOONUSERCLICK, win32.WM_TRAYICON_RCLICK:
		win32.ShowWindow(w.hwnd, swRestore)
		win32.ShowWindow(w.hwnd, 5) // SW_SHOW
		win32.SetForeground(w.hwnd)
		if w.onTrayClick != nil {
			w.onTrayClick()
		}
		w.forceFull = true
		w.Invalidate()
	}
}

// hideToTray 把主窗口收进托盘（/tray 命令与关闭时用）。
func (w *Window) hideToTray() bool {
	if !w.trayOn {
		return false
	}
	win32.ShowWindow(w.hwnd, swHide)
	return true
}

// showFromTray 从托盘恢复窗口，供外部命令调用。
func (w *Window) showFromTray() {
	win32.ShowWindow(w.hwnd, swRestore)
	win32.ShowWindow(w.hwnd, 5)
	win32.SetForeground(w.hwnd)
	w.forceFull = true
	w.Invalidate()
}

// removeTray 退出前摘图标，避免残影。
func (w *Window) removeTray() {
	if w.trayOn {
		win32.TrayRemove(w.hwnd)
		w.trayOn = false
	}
}
