//go:build windows && 386

package ui

import "win7harness/internal/win32"

// 386 的 VARIANT 以 16 字节按值压栈，OLE 代理首轮 get_accName 的 BSTR
// 输出在当前 Go syscall callback 下不稳定。这里回退标准窗口级 IAccessible，
// 富文本/按钮树只在 amd64 启用，避免为了辅助功能引入崩溃/空读。
var msaaSupported = false

func lresultForApp(win32.HWND, uintptr, AccessibleApp) (uintptr, bool) {
	return 0, false
}
