//go:build windows

package win32

import "testing"

func TestTaskbarProgressDoesNotPanic(t *testing.T) {
	CoInit()
	defer CoUninit()
	hwnd, _, _ := pcall(pGetDesktopWindow)
	if hwnd == 0 {
		t.Skip("桌面窗口不可用")
	}
	SetTaskbarProgress(HWND(hwnd), TBPF_INDETERMINATE)
	SetTaskbarProgress(HWND(hwnd), TBPF_NOPROGRESS)
}
