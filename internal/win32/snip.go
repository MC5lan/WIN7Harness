//go:build windows

package win32

// 框选截图所需的系统能力：PrintScreen 热键钩子、虚拟屏幕抓取、DIB 写剪贴板。
//
// 几个 Win7 相关的约束，决定了实现必须这么写：
//  1. PrintScreen 是系统保留键，RegisterHotKey 注册不了，只能用低级键盘钩子(WH_KEYBOARD_LL)拦；
//  2. 低级钩子回调必须尽快返回，否则系统会静默摘掉钩子，所以回调里只做 PostMessage；
//  3. 多显示器要用虚拟屏幕坐标(SM_XVIRTUALSCREEN 等)，只抓主屏会漏掉副屏上的内容。

import (
	"errors"
	"syscall"
	"unsafe"
)

var (
	pSetWindowsHookEx    = dUser32.NewProc("SetWindowsHookExW")
	pUnhookWindowsHookEx = dUser32.NewProc("UnhookWindowsHookEx")
	pCallNextHookEx      = dUser32.NewProc("CallNextHookEx")
	pGetCursorPos        = dUser32.NewProc("GetCursorPos")
	pSetCursorPos        = dUser32.NewProc("SetCursorPos")
)

const (
	WH_KEYBOARD_LL = 13
	VK_SNAPSHOT    = 0x2C // PrintScreen

	SM_XVIRTUALSCREEN  = 76
	SM_YVIRTUALSCREEN  = 77
	SM_CXVIRTUALSCREEN = 78
	SM_CYVIRTUALSCREEN = 79
)

// kbdHook 结构对应 KBDLLHOOKSTRUCT。
type kbdHook struct {
	VkCode   uint32
	ScanCode uint32
	Flags    uint32
	Time     uint32
	Extra    uintptr
}

var (
	hookHandle uintptr
	hookFn     uintptr
	hookTarget HWND
	hookMsg    uint32
)

// InstallSnipHotkey 安装 PrintScreen 钩子：按下时向 target 投递 msg。
// 返回是否安装成功；失败时功能静默不可用，不影响其它入口。
func InstallSnipHotkey(target HWND, msg uint32) bool {
	if hookHandle != 0 {
		return true
	}
	hookTarget = target
	hookMsg = msg
	hookFn = syscall.NewCallback(func(code int32, wparam, lparam uintptr) uintptr {
		if code >= 0 && (wparam == WM_KEYDOWN || wparam == WM_SYSKEYDOWN) {
			kb := (*kbdHook)(unsafe.Pointer(lparam))
			if kb != nil && kb.VkCode == VK_SNAPSHOT {
				// 命中：交给主窗口去开遮罩，钩子自身立刻返回，避免被系统判定超时。
				PostMessage(hookTarget, hookMsg, 0, 0)
				return 1 // 吞掉，避免系统再弹"已复制到剪贴板"的默认截图
			}
		}
		r, _, _ := pcall(pCallNextHookEx, 0, uintptr(code), wparam, lparam)
		return r
	})
	h, _, _ := pcall(pSetWindowsHookEx, WH_KEYBOARD_LL, hookFn, 0, 0)
	if h == 0 {
		hookFn = 0
		return false
	}
	hookHandle = h
	return true
}

// RemoveSnipHotkey 卸载钩子（退出程序时调用）。
func RemoveSnipHotkey() {
	if hookHandle != 0 {
		_, _, _ = pcall(pUnhookWindowsHookEx, hookHandle)
		hookHandle = 0
	}
	hookFn = 0
}

// GetCursorPos 取光标屏幕坐标。
func GetCursorPos() (int32, int32) {
	var pt POINT
	_, _, _ = pcall(pGetCursorPos, uintptr(unsafe.Pointer(&pt)))
	return pt.X, pt.Y
}

// SetCursorPos 移动光标。
func SetCursorPos(x, y int32) {
	_, _, _ = pcall(pSetCursorPos, uintptr(x), uintptr(y))
}

// VirtualScreen 返回虚拟桌面（所有显示器合并）的矩形。
func VirtualScreen() RECT {
	x := GetSystemMetrics(SM_XVIRTUALSCREEN)
	y := GetSystemMetrics(SM_YVIRTUALSCREEN)
	w := GetSystemMetrics(SM_CXVIRTUALSCREEN)
	h := GetSystemMetrics(SM_CYVIRTUALSCREEN)
	return RECT{Left: x, Top: y, Right: x + w, Bottom: y + h}
}

// CaptureVirtualScreen 抓取整个虚拟桌面，返回以 BITMAPINFOHEADER 起始的 32bpp DIB
// （自上而下），可直接交给 core.CropDIB32 裁剪。
func CaptureVirtualScreen() ([]byte, int, int, error) {
	vs := VirtualScreen()
	w := vs.Right - vs.Left
	h := vs.Bottom - vs.Top
	if w <= 0 || h <= 0 || w > 20000 || h > 20000 {
		return nil, 0, 0, errors.New("虚拟屏幕尺寸异常")
	}
	screen := GetDC(0)
	if screen == 0 {
		return nil, 0, 0, errors.New("无法取得屏幕 DC")
	}
	defer ReleaseDC(0, screen)

	mem := CreateCompatibleDC(screen)
	if mem == 0 {
		return nil, 0, 0, errors.New("无法创建兼容 DC")
	}
	defer DeleteDC(mem)

	bmp, bits := CreateDIBSection(mem, w, h)
	if bmp == 0 || bits == nil {
		return nil, 0, 0, errors.New("无法创建位图")
	}
	old := SelectObject(mem, uintptr(bmp))
	BitBlt(mem, 0, 0, w, h, screen, vs.Left, vs.Top, SRCCOPY|CAPTUREBLT)
	SelectObject(mem, old)

	stride := int(w) * 4
	size := stride * int(h)
	raw := unsafe.Slice((*byte)(bits), size)

	const hdr = 40
	dib := make([]byte, hdr+size)
	putU32 := func(off int, v uint32) { *(*uint32)(unsafe.Pointer(&dib[off])) = v }
	putU32(0, hdr)                            // biSize
	putU32(4, uint32(w))                      // biWidth
	putU32(8, uint32(uint32(int32(-h))))      // 负数 = 自上而下
	*(*uint16)(unsafe.Pointer(&dib[12])) = 1  // biPlanes
	*(*uint16)(unsafe.Pointer(&dib[14])) = 32 // biBitCount
	putU32(16, 0)                             // BI_RGB
	putU32(20, uint32(size))                  // biSizeImage
	copy(dib[hdr:], raw)

	DeleteObject(uintptr(bmp))
	return dib, int(w), int(h), nil
}

// SetClipboardDIB 把裸 DIB 写入剪贴板（CF_DIB），供"框选后直接 Ctrl+V"使用。
func SetClipboardDIB(dib []byte) bool {
	if len(dib) < 40 {
		return false
	}
	if r, _, _ := pcall(pOpenClipboard, 0); r == 0 {
		return false
	}
	defer pcall(pCloseClipboard)
	pcall(pEmptyClipboard)

	h, _, _ := pcall(pGlobalAlloc, GMEM_MOVEABLE, uintptr(len(dib)))
	if h == 0 {
		return false
	}
	p, _, _ := pcall(pGlobalLock, h)
	if p == 0 {
		return false
	}
	copy(unsafe.Slice((*byte)(unsafe.Pointer(p)), len(dib)), dib)
	pcall(pGlobalUnlock, h)
	if r, _, _ := pcall(pSetClipboardData, CF_DIB, h); r == 0 {
		return false
	}
	// 成功后内存所有权归系统，不要再 GlobalFree。
	return true
}
