//go:build windows

package win32

import (
	"sync"
	"syscall"
	"unsafe"
)

// ClassName 主窗口类名（唯一即可）。
const ClassName = "Win7HarnessMainWnd"

// AppNameForError 错误提示标题。
const AppNameForError = "WIN7 Harness"

// MINMAXINFO 窗口尺寸限制结构。
type MINMAXINFO struct {
	PtReserved     POINT
	PtMaxSize      POINT
	PtMaxPosition  POINT
	PtMinTrackSize POINT
	PtMaxTrackSize POINT
}

// TRACKMOUSEEVENT 鼠标离开跟踪。
type TRACKMOUSEEVENT struct {
	CbSize      uint32
	DwFlags     uint32
	HwndTrack   HWND
	DwHoverTime uint32
}

var (
	classMu          sync.Mutex
	classAtom        uint16
	procKeep         WndProcFunc
	procAddr         uintptr
	pTrackMouseEvent = dUser32.NewProc("TrackMouseEvent")
)

// RegisterClassEnsure 幂等注册窗口类；fn 会被保存在包级变量中防止 GC 回收。
func RegisterClassEnsure(inst uintptr, fn WndProcFunc) {
	classMu.Lock()
	defer classMu.Unlock()
	if classReady() {
		return
	}
	procKeep = fn
	procAddr = syscall.NewCallback(func(h, msg, w, l uintptr) uintptr {
		return procKeep(HWND(h), uint32(msg), w, l)
	})

	cls, _ := syscall.UTF16PtrFromString(ClassName)
	wc := WNDCLASSEX{
		Style:         CS_HREDRAW | CS_VREDRAW | CS_DBLCLKS,
		LpfnWndProc:   procAddr,
		HInstance:     HINSTANCE(inst),
		HCursor:       LoadCursor(IDC_ARROW),
		HbrBackground: 0,
		LpszClassName: cls,
	}
	classAtom = RegisterClass(&wc)
	classDone = classAtom != 0
}

var classDone bool

func classReady() bool { return classDone }

// TrackMouseLeave 请求 WM_MOUSELEAVE 通知。
func TrackMouseLeave(h HWND) {
	tme := TRACKMOUSEEVENT{CbSize: uint32(unsafe.Sizeof(TRACKMOUSEEVENT{})), DwFlags: 2, HwndTrack: h}
	_, _, _ = pcall(pTrackMouseEvent, ptr(unsafe.Pointer(&tme)))
}
