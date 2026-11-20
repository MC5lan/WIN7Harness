//go:build windows

package ui

import (
	"syscall"
	"unsafe"

	"win7harness/internal/win32"
)

var procGetTextFaceW = syscall.NewLazyDLL("gdi32.dll").NewProc("GetTextFaceW")

// InitFonts 在目标机上探测字体可用性，做一次回退。
// Win7 只有 "Microsoft YaHei"（无 UI 后缀），若不做回退，GDI 会替换成宋体，
// 界面观感会明显掉档。
func InitFonts() {
	if !fontAvailable("Microsoft YaHei UI") {
		if fontAvailable("Microsoft YaHei") {
			FaceUI = "Microsoft YaHei"
		} else {
			FaceUI = "SimSun"
		}
	}
	if !fontAvailable("Consolas") {
		if fontAvailable("Courier New") {
			FaceMono = "Courier New"
		} else {
			FaceMono = "Lucida Console"
		}
	}
	if !fontAvailable("Segoe UI Symbol") {
		FaceEmoji = FaceUI
	}
}

// fontAvailable 通过创建字体并读回实际字体名判断。
func fontAvailable(face string) bool {
	hdc := win32.CreateCompatibleDC(0)
	if hdc == 0 {
		return false
	}
	defer win32.DeleteDC(hdc)
	f := win32.CreateFont(14, 400, false, face)
	if f == 0 {
		return false
	}
	old := win32.SelectObject(hdc, uintptr(f))
	buf := make([]uint16, 64)
	n, _, _ := procGetTextFaceW.Call(uintptr(hdc), 64, uintptr(unsafe.Pointer(&buf[0])))
	win32.SelectObject(hdc, old)
	win32.DeleteObject(uintptr(f))
	if n == 0 {
		return false
	}
	got := syscall.UTF16ToString(buf[:n])
	return equalFold(got, face)
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}
