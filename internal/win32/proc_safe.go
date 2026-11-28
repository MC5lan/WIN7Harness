//go:build windows

package win32

import (
	"sort"
	"sync"
	"syscall"
	"unsafe"
)

// 安全调用层。
//
// 背景：syscall.LazyProc.Call 在第一次调用时才 Find，找不到导出函数会直接 panic。
// 一个 API 名字写错了 DLL、或者某个函数在 Win7 上根本不存在，整程序就在启动时
// 弹"遇到未预期的错误"死掉，而且看不出是哪一层的问题。
//
// 这里把所有 LazyProc.Call 换成 pcall：找不到就把缺失清单记下来、返回零值，
// 程序继续跑（对应功能降级），日志里能看到到底缺了哪些。
var missingMu sync.Mutex

var missingProcs = map[string]string{}

func noteMissing(name string, err error) {
	missingMu.Lock()
	if _, ok := missingProcs[name]; !ok {
		missingProcs[name] = err.Error()
	}
	missingMu.Unlock()
	dbg("win32: 缺少导出函数 " + name + ": " + err.Error())
}

// longProc 选择 LongPtr 系列导出函数。
// 32 位 Windows 的 user32 只导出 GetWindowLongW / SetClassLongW，
// 只有 64 位才导出 ...LongPtrW，混用会让 386 包在 32 位系统上启动即崩。
func longProc(a32, a64 string) *syscall.LazyProc {
	if unsafe.Sizeof(uintptr(0)) == 4 {
		return dUser32.NewProc(a32)
	}
	return dUser32.NewProc(a64)
}

// dbg 输出到调试器（DebugView 可见）。绕过 pcall，避免缺失记录时递归。
func dbg(s string) {
	if err := pOutputDebugStringW.Find(); err != nil {
		return
	}
	u, err := syscall.UTF16PtrFromString(s)
	if err != nil {
		return
	}
	pOutputDebugStringW.Call(uintptr(unsafe.Pointer(u)))
}

// pcall 安全调用 LazyProc。
func pcall(p *syscall.LazyProc, a ...uintptr) (uintptr, uintptr, error) {
	if err := p.Find(); err != nil {
		noteMissing(p.Name, err)
		return 0, 0, err
	}
	return p.Call(a...)
}

// MissingProcs 返回本次运行中缺失的 Win32 导出函数清单，用于日志与自检。
func MissingProcs() []string {
	missingMu.Lock()
	defer missingMu.Unlock()
	out := make([]string, 0, len(missingProcs))
	for k := range missingProcs {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// allProcs 是全部已注册的导出函数，供启动自检使用。
// 自检的意义：某些 API 在 Win7 上不存在、或者名字挂错了 DLL，
// 如果等到用户点到那个功能才调用，表现就是"某个按钮按下去没反应"，
// 排查成本极高。启动时一次性 Find 出来写进日志，缺什么一目了然。
var allProcs = []*syscall.LazyProc{
	pAdjustWindowRectEx,
	pAlphaBlend,
	pBeginPaint,
	pBitBlt,
	pCallNextHookEx,
	pClientToScreen,
	pCloseClipboard,
	pCloseHandle,
	pCreateJobObjectW,
	pSetInformationJobObj,
	pAssignProcessToJobObj,
	pTerminateJobObject,
	pExpandEnvironmentStringsW,
	pCoInitializeEx,
	pCoTaskMemFree,
	pCoUninitialize,
	pCreateCompatibleDC,
	pCreateDIBSection,
	pCreateFontW,
	pCreateMutexW,
	pCreatePen,
	pCreateRectRgn,
	pCreateSolidBrush,
	pCreateWindowExW,
	pDefWindowProcW,
	pDeleteDC,
	pDeleteObject,
	pDestroyWindow,
	pDispatchMessageW,
	pDragAccept,
	pDragFinish,
	pDragQueryFile,
	pDrawTextW,
	pEllipse,
	pEmptyClipboard,
	pEndPaint,
	pFillRect,
	pFindWindowW,
	pGetAsyncKeyState,
	pGetClientRect,
	pGetClipboardData,
	pGetCursorPos,
	pGetDC,
	pGetDeviceCaps,
	pGetFocus,
	pGetForegroundWindow,
	pGetKeyState,
	pGetMessageW,
	pGetModuleHandleW,
	pGetOpenFile,
	pGetStockObject,
	pGetSystemMetrics,
	pGetTextExtentPoint32W,
	pGetTextMetricsW,
	pGetWindowTextW,
	pGlobalAlloc,
	pGlobalFree,
	pGlobalLock,
	pGlobalSize,
	pGlobalUnlock,
	pGradientFill,
	pImmGetContext,
	pImmReleaseContext,
	pImmSetCompositionWindow,
	pImmNotifyIME,
	pInvalidateRect,
	pIsClipboardFormatAvailable,
	pIsIconic,
	pKillTimer,
	pLineTo,
	pLoadCursorW,
	pLoadImageW,
	pMessageBoxW,
	pMoveToEx,
	pOpenClipboard,
	pOutputDebugStringW,
	pPostMessageW,
	pPostQuitMessage,
	pRectangle,
	pRegCloseKey,
	pRegOpenKeyExW,
	pRegQueryValueExW,
	pRegisterClassExW,
	pReleaseCapture,
	pReleaseDC,
	pRoundRect,
	pSHBrowseForFolderW,
	pSHGetPathFromIDListW,
	pScreenToClient,
	pSelectClipRgn,
	pSelectObject,
	pSendMessageTimeoutW,
	pSendMessageW,
	pSetBkColor,
	pSetBkMode,
	pSetCapture,
	pSetClipboardData,
	pSetCursor,
	pSetCursorPos,
	pSetFocus,
	pSetForeground,
	pSetForegroundWindow,
	pSetPixel,
	pSetProcessDPIAware,
	pSetROP2,
	pSetTextColor,
	pSetTimer,
	pSetWindowPos,
	pSetWindowTextW,
	pSetWindowsHookEx,
	pShellExecuteW,
	pShowScrollBar,
	pShowWindow,
	pShowWindowAsync,
	pIsWindowVisible,
	pPeekMessageW,
	pMsgWaitForMultipleObjectsEx,
	pStretchBlt,
	pTextOutW,
	pTrackMouseEvent,
	pTranslateMessage,
	pUnhookWindowsHookEx,
	pUpdateWindow,
	procAllocConsole,
	procAttachConsole,
	procGetFileType,
	procGetStdHandle,
	procSetConsoleTitle,
	pWTSRegisterSessionNotification,
	pWTSUnRegisterSessionNotification,
	pSetThreadExecutionState,
	pGetSystemPowerStatus,
	pWinHttpOpen,
	pWinHttpGetProxyForUrl,
	pWinHttpCloseHandle,
	pCertOpenSystemStoreW,
	pCertEnumCertificatesInStore,
	pCertCloseStore,
}

// SelfCheck 逐个解析导出函数，返回无法解析的清单。
func SelfCheck() []string {
	bad := make([]string, 0, 4)
	for _, p := range allProcs {
		if err := p.Find(); err != nil {
			noteMissing(p.Name, err)
			bad = append(bad, p.Name)
		}
	}
	return bad
}

// ProcCount 返回自检覆盖的导出函数数量。
func ProcCount() int { return len(allProcs) }

// SafeCall 供包外对动态获取的 LazyProc 使用（例如 ui 包按名字取的 GDI 扩展函数），
// 语义与包内 pcall 一致：缺失时不 panic。
func SafeCall(p *syscall.LazyProc, a ...uintptr) (uintptr, uintptr, error) {
	return pcall(p, a...)
}
