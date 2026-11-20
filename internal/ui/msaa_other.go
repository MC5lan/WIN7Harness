//go:build windows && !amd64 && !386

package ui

import "win7harness/internal/win32"

var msaaSupported = false

func lresultForApp(win32.HWND, uintptr, AccessibleApp) (uintptr, bool) {
	return 0, false
}
