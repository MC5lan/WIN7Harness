//go:build windows

package win32

import (
	"syscall"
	"unsafe"
)

// 单实例：命名互斥量 + 激活既有窗口。
// 两个实例同时跑会互相覆盖 config.json 和会话文件，必须从根上挡住。

const ERROR_ALREADY_EXISTS = 183

var (
	pCreateMutexW  = dKernel32.NewProc("CreateMutexW")
	pCloseHandle   = dKernel32.NewProc("CloseHandle")
	pFindWindowW   = dUser32.NewProc("FindWindowW")
	pIsIconic      = dUser32.NewProc("IsIconic")
	pSetForeground = dUser32.NewProc("SetForegroundWindow")
)

// AcquireSingleInstance 创建（或打开）命名互斥量。
// 返回的 handle 需在进程存活期间保持有效；already 为 true 表示已有实例在跑。
func AcquireSingleInstance(name string) (handle uintptr, already bool) {
	p, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return 0, false
	}
	h, _, errno := pcall(pCreateMutexW, 0, 0, uintptr(unsafe.Pointer(p)))
	if h == 0 {
		return 0, false
	}
	if e, ok := errno.(syscall.Errno); ok && e == ERROR_ALREADY_EXISTS {
		return h, true
	}
	return h, false
}

// CloseHandle 关闭内核对象句柄。
func CloseHandle(h uintptr) {
	if h == 0 {
		return
	}
	_, _, _ = pcall(pCloseHandle, h)
}

// ActivateWindow 按类名（title 为空时不限定标题）查找已存在的窗口，
// 最小化则还原，然后前置。返回是否找到。
func ActivateWindow(className, title string) bool {
	var cp, tp *uint16
	if className != "" {
		cp, _ = syscall.UTF16PtrFromString(className)
	}
	if title != "" {
		tp, _ = syscall.UTF16PtrFromString(title)
	}
	var a, b uintptr
	if cp != nil {
		a = uintptr(unsafe.Pointer(cp))
	}
	if tp != nil {
		b = uintptr(unsafe.Pointer(tp))
	}
	h, _, _ := pcall(pFindWindowW, a, b)
	if h == 0 {
		return false
	}
	if iconic, _, _ := pcall(pIsIconic, h); iconic != 0 {
		ShowWindow(HWND(h), SW_RESTORE)
	}
	_, _, _ = pcall(pSetForeground, h)
	return true
}
