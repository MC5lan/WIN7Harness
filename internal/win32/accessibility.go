//go:build windows

package win32

import (
	"syscall"
	"unsafe"
)

// 最小 MSAA 暴露：自定义绘制的窗口没有子控件，但至少要让屏幕阅读器
// 通过 WM_GETOBJECT 拿到窗口级 IAccessible（角色/标题），而不是完全空白。
// 更细的文本/按钮级 IAccessible 需要自建 COM 对象，见路线图 P1 的剩余项。
const (
	WM_GETOBJECT = 0x003D
	objIDClient  = 0xFFFFFFFC
)

type guid struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

var iidIAccessible = guid{
	Data1: 0x618736E0,
	Data2: 0x3C3D,
	Data3: 0x11CF,
	Data4: [8]byte{0x81, 0x0C, 0x00, 0xAA, 0x00, 0x38, 0x9B, 0x71},
}

var (
	dOleacc              = syscall.NewLazyDLL("oleacc.dll")
	pCreateStdAccessible = dOleacc.NewProc("CreateStdAccessibleObject")
	pLresultFromObject   = dOleacc.NewProc("LresultFromObject")
	pObjectFromLresult   = dOleacc.NewProc("ObjectFromLresult")
)

// LresultForObject 把自定义 IAccessible 交给 OLE 编组，供 WM_GETOBJECT 返回。
func LresultForObject(punk, wParam uintptr) (uintptr, bool) {
	if punk == 0 {
		return 0, false
	}
	res, _, _ := pcall(pLresultFromObject, uintptr(unsafe.Pointer(&iidIAccessible)), wParam, punk)
	if res == 0 {
		return 0, false
	}
	return res, true
}

// ObjectFromLresult 是测试/宿主端辅助：把 WM_GETOBJECT 的 LRESULT 还原成 IAccessible。
func ObjectFromLresult(lresult, wParam uintptr) (uintptr, bool) {
	if lresult == 0 {
		return 0, false
	}
	var pacc uintptr
	hr, _, _ := pcall(pObjectFromLresult, lresult, uintptr(unsafe.Pointer(&iidIAccessible)), wParam, uintptr(unsafe.Pointer(&pacc)))
	if int32(hr) < 0 || pacc == 0 {
		return 0, false
	}
	return pacc, true
}

// StdAccessibleLresult 为 WM_GETOBJECT/OBJID_CLIENT 返回标准窗口级 IAccessible。
// 返回 ok=false 时调用方应走 DefWindowProc。
func StdAccessibleLresult(hwnd, wParam, lParam uintptr) (uintptr, bool) {
	if int32(lParam) != -4 {
		return 0, false
	}
	var pacc uintptr
	hr, _, _ := pcall(pCreateStdAccessible, hwnd, uintptr(objIDClient), uintptr(unsafe.Pointer(&iidIAccessible)), uintptr(unsafe.Pointer(&pacc)))
	if int32(hr) < 0 || pacc == 0 {
		return 0, false
	}
	defer comRelease(pacc)
	res, _, _ := pcall(pLresultFromObject, uintptr(unsafe.Pointer(&iidIAccessible)), wParam, pacc)
	if res == 0 {
		return 0, false
	}
	return res, true
}

func comRelease(p uintptr) {
	if p == 0 {
		return
	}
	vtbl := *(*uintptr)(unsafe.Pointer(p))
	ptrSize := unsafe.Sizeof(uintptr(0))
	release := *(*uintptr)(unsafe.Pointer(vtbl + 2*ptrSize))
	_, _, _ = syscall.Syscall(release, 1, p, 0, 0)
}
