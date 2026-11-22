//go:build windows

package win32

import (
	"syscall"
	"unsafe"
)

// —— 托盘图标与气泡通知 ——
//
// 为什么用托盘而不是弹窗：长任务跑完后用户往往已经切走了，
// 再弹一个模态框只会挡路；托盘气泡是 Win7 上最"不打扰但不丢"的提示方式。
// 而且托盘图标本身给了"程序还在跑"的存在感 —— 关掉主窗口也能从托盘点回来。

const (
	NIM_ADD    = 0x00000000
	NIM_MODIFY = 0x00000001
	NIM_DELETE = 0x00000002

	NIF_MESSAGE = 0x00000001
	NIF_ICON    = 0x00000002
	NIF_TIP     = 0x00000004
	NIF_INFO    = 0x00000010

	NIIF_INFO    = 0x00000001
	NIIF_WARNING = 0x00000002
	NIIF_ERROR   = 0x00000003

	// 托盘回调消息：WM_APP + 0x53（0x52 已被截图钩子占用）
	TrayMsg = 0x8000 + 0x53

	// 托盘回调里的低字事件
	NIN_BALLOONUSERCLICK = 0x0405
	WM_TRAYICON_LCLICK   = 0x0202 // WM_LBUTTONUP
	WM_TRAYICON_RCLICK   = 0x0205 // WM_RBUTTONUP
)

const trayID = 1 // 单实例程序，固定 ID 即可

// NOTIFYICONDATAW（Vista 完整版）。字段顺序必须与 shellapi.h 严格一致，
// cbSize 直接取结构体大小 —— Win7 支持完整版本，不必再算 V1/V2 的老尺寸。
type notifyIconData struct {
	CbSize           uint32
	HWnd             HWND
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            HICON
	SzTip            [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	UTimeout         uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GuidItem         [16]byte
	HBalloonIcon     uintptr
}

var pShellNotifyIconW = dShell32.NewProc("Shell_NotifyIconW")

// copyUTF16 把字符串写进定长 UTF-16 缓冲，超长截断并保留结尾 0。
func copyUTF16(dst []uint16, s string) {
	if len(dst) == 0 {
		return
	}
	u, err := syscall.UTF16FromString(s)
	if err != nil {
		u = []uint16{0}
	}
	n := len(u)
	if n > len(dst) {
		n = len(dst)
		u = u[:n]
		u[n-1] = 0 // 截断时必须自己补 0 结尾
	}
	copy(dst, u)
	for i := n; i < len(dst); i++ {
		dst[i] = 0
	}
}

func shellNotify(action uint32, data *notifyIconData) bool {
	r, _, _ := pcall(pShellNotifyIconW, uintptr(action), uintptr(unsafe.Pointer(data)))
	return r != 0
}

// TrayAdd 添加托盘图标。hicon 为 0 时用系统默认应用图标。
func TrayAdd(hwnd HWND, hicon HICON, tip string) bool {
	if hwnd == 0 {
		return false
	}
	var d notifyIconData
	d.CbSize = uint32(unsafe.Sizeof(d))
	d.HWnd = hwnd
	d.UID = trayID
	d.UFlags = NIF_MESSAGE | NIF_ICON | NIF_TIP
	d.UCallbackMessage = TrayMsg
	if hicon == 0 {
		hicon = LoadDefaultIcon()
	}
	d.HIcon = hicon
	copyUTF16(d.SzTip[:], tip)
	return shellNotify(NIM_ADD, &d)
}

// TraySetTip 更新悬停提示。
func TraySetTip(hwnd HWND, tip string) bool {
	var d notifyIconData
	d.CbSize = uint32(unsafe.Sizeof(d))
	d.HWnd = hwnd
	d.UID = trayID
	d.UFlags = NIF_TIP
	copyUTF16(d.SzTip[:], tip)
	return shellNotify(NIM_MODIFY, &d)
}

// TrayBalloon 弹一个气泡通知。level 取 NIIF_INFO / NIIF_WARNING / NIIF_ERROR。
// Win7 上系统会把气泡排队显示，短时间内大量调用会被折叠，这是系统行为不是缺陷。
func TrayBalloon(hwnd HWND, title, text string, level uint32) bool {
	if hwnd == 0 {
		return false
	}
	var d notifyIconData
	d.CbSize = uint32(unsafe.Sizeof(d))
	d.HWnd = hwnd
	d.UID = trayID
	d.UFlags = NIF_INFO
	d.DwInfoFlags = level
	d.UTimeout = 8000 // 毫秒；Win7 会尊重这个值
	copyUTF16(d.SzInfoTitle[:], title)
	copyUTF16(d.SzInfo[:], text)
	return shellNotify(NIM_MODIFY, &d)
}

// TrayRemove 移除托盘图标。程序退出前必须调，否则图标会残留到用户把鼠标划过去为止。
func TrayRemove(hwnd HWND) bool {
	var d notifyIconData
	d.CbSize = uint32(unsafe.Sizeof(d))
	d.HWnd = hwnd
	d.UID = trayID
	return shellNotify(NIM_DELETE, &d)
}
