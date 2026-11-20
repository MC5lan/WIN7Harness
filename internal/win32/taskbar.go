//go:build windows

package win32

import (
	"sync"
	"syscall"
	"unsafe"
)

// ITaskbarList3（Win7+）只用来显示“正在生成”的蓝色不确定进度。
// COM 创建失败（经典主题/远程会话/被策略禁用）时静默降级，不影响主界面。
const (
	TBPF_NOPROGRESS      = 0
	TBPF_INDETERMINATE   = 1
	CLSCTX_INPROC_SERVER = 1
)

var (
	taskbarMu    sync.Mutex
	taskbarOnce  sync.Once
	taskbarPtr   uintptr
	taskbarHWND  HWND
	taskbarState uint32 = ^uint32(0)
	pCoCreate           = dOle32.NewProc("CoCreateInstance")
	taskbarCLSID        = guid{Data1: 0x56FDF344, Data2: 0xFD6D, Data3: 0x11D0, Data4: [8]byte{0x95, 0x8A, 0x00, 0x60, 0x97, 0xC9, 0xA0, 0x90}}
	taskbarIID          = guid{Data1: 0xEA1AFB91, Data2: 0x9E28, Data3: 0x4B86, Data4: [8]byte{0x90, 0xE9, 0x9E, 0x9F, 0x8A, 0x5E, 0xEA, 0x84}}
)

func taskbarInit() {
	taskbarOnce.Do(func() {
		var p uintptr
		hr, _, _ := pcall(pCoCreate,
			uintptr(unsafe.Pointer(&taskbarCLSID)),
			0,
			CLSCTX_INPROC_SERVER,
			uintptr(unsafe.Pointer(&taskbarIID)),
			uintptr(unsafe.Pointer(&p)),
		)
		if int32(hr) < 0 || p == 0 {
			return
		}
		vtbl := *(*uintptr)(unsafe.Pointer(p))
		hrInit := *(*uintptr)(unsafe.Pointer(vtbl + 3*unsafe.Sizeof(uintptr(0))))
		rh, _, _ := syscall.Syscall(hrInit, 1, p, 0, 0)
		if int32(rh) < 0 {
			return
		}
		taskbarPtr = p
	})
}

// SetTaskbarProgress 在任务栏按钮上显示/清除不确定进度。
// state 0=NOPROGRESS, 1=INDETERMINATE。
func SetTaskbarProgress(h HWND, state uint32) {
	if h == 0 {
		return
	}
	taskbarInit()
	taskbarMu.Lock()
	defer taskbarMu.Unlock()
	if taskbarPtr == 0 {
		return
	}
	if taskbarHWND == h && taskbarState == state {
		return
	}
	if state != TBPF_NOPROGRESS {
		state = TBPF_INDETERMINATE
	}
	vtbl := *(*uintptr)(unsafe.Pointer(taskbarPtr))
	fn := *(*uintptr)(unsafe.Pointer(vtbl + 10*unsafe.Sizeof(uintptr(0))))
	_, _, _ = syscall.Syscall(fn, 3, taskbarPtr, uintptr(h), uintptr(state))
	taskbarHWND, taskbarState = h, state
}
