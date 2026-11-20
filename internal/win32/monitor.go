//go:build windows

package win32

import "unsafe"

// Win7 也支持 MonitorFromRect/GetMonitorInfoW；这里不用 Win8+ 的
// GetDpiForMonitor，只解决“多显示器重启后窗口落在屏幕外”的常见问题。
const monitorDefaultToNearest = 2

var (
	pMonitorFromRect   = dUser32.NewProc("MonitorFromRect")
	pMonitorFromWindow = dUser32.NewProc("MonitorFromWindow")
	pGetMonitorInfoW   = dUser32.NewProc("GetMonitorInfoW")
	pGetWindowRect     = dUser32.NewProc("GetWindowRect")
)

type monitorInfo struct {
	cbSize    uint32
	rcMonitor RECT
	rcWork    RECT
	dwFlags   uint32
}

func monitorWork(h uintptr) (RECT, bool) {
	if h == 0 {
		return RECT{}, false
	}
	var mi monitorInfo
	mi.cbSize = uint32(unsafe.Sizeof(mi))
	r, _, _ := pcall(pGetMonitorInfoW, h, uintptr(unsafe.Pointer(&mi)))
	if r == 0 {
		return RECT{}, false
	}
	return mi.rcWork, true
}

// MonitorWorkAreaForWindow 返回窗口当前所在显示器的工作区（不含任务栏）。
func MonitorWorkAreaForWindow(h HWND) (RECT, bool) {
	if h == 0 {
		return RECT{}, false
	}
	hm, _, _ := pcall(pMonitorFromWindow, uintptr(h), monitorDefaultToNearest)
	return monitorWork(hm)
}

// MonitorWorkAreaAt 返回屏幕点所在显示器的工作区。
func MonitorWorkAreaAt(x, y int32) (RECT, bool) {
	r := RECT{Left: x, Top: y, Right: x + 1, Bottom: y + 1}
	hm, _, _ := pcall(pMonitorFromRect, uintptr(unsafe.Pointer(&r)), monitorDefaultToNearest)
	return monitorWork(hm)
}

// ClampWindowToWorkArea 在显示器插拔/分辨率变化后把窗口拉回可见工作区。
// 只移动/缩小超出部分，不改变最大化状态；返回是否实际调整。
func ClampWindowToWorkArea(h HWND) bool {
	if h == 0 {
		return false
	}
	var r RECT
	ok, _, _ := pcall(pGetWindowRect, uintptr(h), uintptr(unsafe.Pointer(&r)))
	if ok == 0 {
		return false
	}
	work, okWork := MonitorWorkAreaForWindow(h)
	if !okWork {
		return false
	}
	ww, wh := r.Width(), r.Height()
	if ww > work.Width() {
		ww = work.Width()
	}
	if wh > work.Height() {
		wh = work.Height()
	}
	x, y := r.Left, r.Top
	if x < work.Left {
		x = work.Left
	}
	if y < work.Top {
		y = work.Top
	}
	if x+ww > work.Right {
		x = work.Right - ww
	}
	if y+wh > work.Bottom {
		y = work.Bottom - wh
	}
	if x == r.Left && y == r.Top && ww == r.Width() && wh == r.Height() {
		return false
	}
	SetWindowPos(h, 0, x, y, ww, wh, 0x0004|0x0010) // SWP_NOZORDER|SWP_NOACTIVATE
	return true
}
