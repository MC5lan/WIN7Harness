//go:build windows

package win32

import (
	"syscall"
	"unsafe"
)

// 输入法（IMM32）。自绘界面没有子编辑控件，组字窗口默认会贴到窗口左上角，
// 必须由程序自己在每次组字时告诉系统"光标在哪"。
var (
	dImm32 = syscall.NewLazyDLL("imm32.dll")

	pImmGetContext           = dImm32.NewProc("ImmGetContext")
	pImmReleaseContext       = dImm32.NewProc("ImmReleaseContext")
	pImmSetCompositionWindow = dImm32.NewProc("ImmSetCompositionWindow")
	pImmSetCandidateWindow   = dImm32.NewProc("ImmSetCandidateWindow")
	pImmNotifyIME            = dImm32.NewProc("ImmNotifyIME")
)

const (
	CFS_POINT          = 0x0002
	CFS_FORCE_POSITION = 0x0020
	CFS_CANDIDATEPOS   = 0x0040
	CPS_CANCEL         = 0x0004
)

// COMPOSITIONFORM 组字窗口位置。字段顺序必须与 Win32 头文件一致。
type COMPOSITIONFORM struct {
	Style        uint32
	PtCurrentPos POINT
	RcArea       RECT
}

// SetCompositionPos 把输入法候选/组字窗口定位到客户区 (x, y)（物理像素）。
// 没有输入法上下文（未装 IME）时静默返回。
// CANDIDATEFORM 候选窗口位置。
type CANDIDATEFORM struct {
	Index        uint32
	Style        uint32
	PtCurrentPos POINT
	RcArea       RECT
}

// SetCandidatePos 把候选词窗口也定位到光标处，避免中文输入时候选框飘在左上角。
func SetCandidatePos(h HWND, x, y int32) {
	himc, _, _ := pcall(pImmGetContext, uintptr(h))
	if himc == 0 {
		return
	}
	cf := CANDIDATEFORM{Index: 0, Style: CFS_CANDIDATEPOS}
	cf.PtCurrentPos.X = x
	cf.PtCurrentPos.Y = y
	_, _, _ = pcall(pImmSetCandidateWindow, himc, uintptr(unsafe.Pointer(&cf)))
	_, _, _ = pcall(pImmReleaseContext, uintptr(h), himc)
}

func SetCompositionPos(h HWND, x, y int32) {
	himc, _, _ := pcall(pImmGetContext, uintptr(h))
	if himc == 0 {
		return
	}
	cf := COMPOSITIONFORM{Style: CFS_POINT | CFS_FORCE_POSITION}
	cf.PtCurrentPos.X = x
	cf.PtCurrentPos.Y = y
	_, _, _ = pcall(pImmSetCompositionWindow, himc, uintptr(unsafe.Pointer(&cf)))
	_, _, _ = pcall(pImmReleaseContext, uintptr(h), himc)
}

// CancelComposition 在窗口失焦/停用时取消输入法组字。
// 不取消时，WM_IME_ENDCOMPOSITION 可能永远不到，窗口会卡在 composing=true，
// 后续键盘输入全部被交给 DefWindowProc，表现为“输入框活着但打不了字”。
func CancelComposition(h HWND) {
	himc, _, _ := pcall(pImmGetContext, uintptr(h))
	if himc == 0 {
		return
	}
	_, _, _ = pcall(pImmNotifyIME, himc, CPS_CANCEL, 0, 0)
	_, _, _ = pcall(pImmReleaseContext, uintptr(h), himc)
}
