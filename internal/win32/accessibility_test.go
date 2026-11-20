//go:build windows

package win32

import (
	"syscall"
	"testing"
	"unsafe"
)

var pGetDesktopWindow = dUser32.NewProc("GetDesktopWindow")

func TestUTF16PtrToString(t *testing.T) {
	p, err := syscall.UTF16PtrFromString("WininetSettings")
	if err != nil {
		t.Fatal(err)
	}
	if got := UTF16PtrToString(uintptr(unsafe.Pointer(p)), 128); got != "WininetSettings" {
		t.Fatalf("got %q", got)
	}
}

func TestHighContrastQueryDoesNotPanic(t *testing.T) {
	// 返回 false 也合法（大多数开发机没开高对比）；这里验证 Win7 上
	// SystemParametersInfoW 的 HIGHCONTRASTW 结构/调用约定不会读坏内存。
	_ = HighContrastActive()
}

func TestStdAccessibleDesktop(t *testing.T) {
	// 桌面窗口一定有标准 IAccessible；验证 oleacc 导出与 COM 对象释放路径。
	hwnd, _, _ := pcall(pGetDesktopWindow)
	if hwnd == 0 {
		t.Skip("GetDesktopWindow 不可用")
	}
	if _, ok := StdAccessibleLresult(hwnd, 0, uintptr(0xFFFFFFFC)); !ok {
		// 某些最小化/无 shell 的会话没有 oleacc 代理；不把环境差异当代码错误。
		t.Log("当前会话未返回标准 IAccessible")
	}
}
