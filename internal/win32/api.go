//go:build windows

package win32

import (
	"syscall"
	"time"
	"unsafe"
)

var (
	dUser32   = syscall.NewLazyDLL("user32.dll")
	dGdi32    = syscall.NewLazyDLL("gdi32.dll")
	dKernel32 = syscall.NewLazyDLL("kernel32.dll")
	dMsimg32  = syscall.NewLazyDLL("msimg32.dll")
	dShell32  = syscall.NewLazyDLL("shell32.dll")
	dOle32    = syscall.NewLazyDLL("ole32.dll")
	dComdlg32 = syscall.NewLazyDLL("comdlg32.dll")

	pRegisterClassExW            = dUser32.NewProc("RegisterClassExW")
	pCreateWindowExW             = dUser32.NewProc("CreateWindowExW")
	pDefWindowProcW              = dUser32.NewProc("DefWindowProcW")
	pDestroyWindow               = dUser32.NewProc("DestroyWindow")
	pShowWindow                  = dUser32.NewProc("ShowWindow")
	pShowWindowAsync             = dUser32.NewProc("ShowWindowAsync")
	pIsWindowVisible             = dUser32.NewProc("IsWindowVisible")
	pPeekMessageW                = dUser32.NewProc("PeekMessageW")
	pMsgWaitForMultipleObjectsEx = dUser32.NewProc("MsgWaitForMultipleObjectsEx")
	pUpdateWindow                = dUser32.NewProc("UpdateWindow")
	pGetMessageW                 = dUser32.NewProc("GetMessageW")
	pTranslateMessage            = dUser32.NewProc("TranslateMessage")
	pDispatchMessageW            = dUser32.NewProc("DispatchMessageW")
	pPostQuitMessage             = dUser32.NewProc("PostQuitMessage")
	pPostMessageW                = dUser32.NewProc("PostMessageW")
	pSendMessageW                = dUser32.NewProc("SendMessageW")
	pInvalidateRect              = dUser32.NewProc("InvalidateRect")
	pBeginPaint                  = dUser32.NewProc("BeginPaint")
	pEndPaint                    = dUser32.NewProc("EndPaint")
	pGetClientRect               = dUser32.NewProc("GetClientRect")
	pSetTimer                    = dUser32.NewProc("SetTimer")
	pKillTimer                   = dUser32.NewProc("KillTimer")
	pLoadCursorW                 = dUser32.NewProc("LoadCursorW")
	pSetCursor                   = dUser32.NewProc("SetCursor")
	pSetCapture                  = dUser32.NewProc("SetCapture")
	pReleaseCapture              = dUser32.NewProc("ReleaseCapture")
	pGetSystemMetrics            = dUser32.NewProc("GetSystemMetrics")
	pMessageBoxW                 = dUser32.NewProc("MessageBoxW")
	pSetWindowTextW              = dUser32.NewProc("SetWindowTextW")
	pGetWindowTextW              = dUser32.NewProc("GetWindowTextW")
	pSetWindowPos                = dUser32.NewProc("SetWindowPos")
	pGetDC                       = dUser32.NewProc("GetDC")
	pReleaseDC                   = dUser32.NewProc("ReleaseDC")
	pOpenClipboard               = dUser32.NewProc("OpenClipboard")
	pCloseClipboard              = dUser32.NewProc("CloseClipboard")
	pEmptyClipboard              = dUser32.NewProc("EmptyClipboard")
	pSetClipboardData            = dUser32.NewProc("SetClipboardData")
	pGetClipboardData            = dUser32.NewProc("GetClipboardData")
	pIsClipboardFormatAvailable  = dUser32.NewProc("IsClipboardFormatAvailable")
	pSetProcessDPIAware          = dUser32.NewProc("SetProcessDPIAware")
	pGetKeyState                 = dUser32.NewProc("GetKeyState")
	pGetAsyncKeyState            = dUser32.NewProc("GetAsyncKeyState")
	pScreenToClient              = dUser32.NewProc("ScreenToClient")
	pClientToScreen              = dUser32.NewProc("ClientToScreen")
	pAdjustWindowRectEx          = dUser32.NewProc("AdjustWindowRectEx")
	pSetForegroundWindow         = dUser32.NewProc("SetForegroundWindow")
	pGetForegroundWindow         = dUser32.NewProc("GetForegroundWindow")
	pGetFocus                    = dUser32.NewProc("GetFocus")
	pSetFocus                    = dUser32.NewProc("SetFocus")
	pSetWindowLongPtrW           = longProc("SetWindowLongW", "SetWindowLongPtrW")
	pGetWindowLongPtrW           = longProc("GetWindowLongW", "GetWindowLongPtrW")
	pShowScrollBar               = dUser32.NewProc("ShowScrollBar")
	pLoadImageW                  = dUser32.NewProc("LoadImageW")
	pSendMessageTimeoutW         = dUser32.NewProc("SendMessageTimeoutW")
	pSetClassLongPtrW            = longProc("SetClassLongW", "SetClassLongPtrW")

	pCreateCompatibleDC    = dGdi32.NewProc("CreateCompatibleDC")
	pCreateDIBSection      = dGdi32.NewProc("CreateDIBSection")
	pSelectObject          = dGdi32.NewProc("SelectObject")
	pDeleteObject          = dGdi32.NewProc("DeleteObject")
	pDeleteDC              = dGdi32.NewProc("DeleteDC")
	pBitBlt                = dGdi32.NewProc("BitBlt")
	pStretchBlt            = dGdi32.NewProc("StretchBlt")
	pCreateFontW           = dGdi32.NewProc("CreateFontW")
	pSetBkMode             = dGdi32.NewProc("SetBkMode")
	pSetTextColor          = dGdi32.NewProc("SetTextColor")
	pSetBkColor            = dGdi32.NewProc("SetBkColor")
	pTextOutW              = dGdi32.NewProc("TextOutW")
	pDrawTextW             = dUser32.NewProc("DrawTextW")
	pGetTextExtentPoint32W = dGdi32.NewProc("GetTextExtentPoint32W")
	pGetTextMetricsW       = dGdi32.NewProc("GetTextMetricsW")
	pCreateSolidBrush      = dGdi32.NewProc("CreateSolidBrush")
	pFillRect              = dUser32.NewProc("FillRect")
	pRoundRect             = dGdi32.NewProc("RoundRect")
	pRectangle             = dGdi32.NewProc("Rectangle")
	pEllipse               = dGdi32.NewProc("Ellipse")
	pCreatePen             = dGdi32.NewProc("CreatePen")
	pMoveToEx              = dGdi32.NewProc("MoveToEx")
	pLineTo                = dGdi32.NewProc("LineTo")
	pSetPixel              = dGdi32.NewProc("SetPixel")
	pGetDeviceCaps         = dGdi32.NewProc("GetDeviceCaps")
	pCreateRectRgn         = dGdi32.NewProc("CreateRectRgn")
	pSelectClipRgn         = dGdi32.NewProc("SelectClipRgn")
	pGetStockObject        = dGdi32.NewProc("GetStockObject")
	pSetROP2               = dGdi32.NewProc("SetROP2")

	pGradientFill = dMsimg32.NewProc("GradientFill")
	pAlphaBlend   = dMsimg32.NewProc("AlphaBlend")

	pGetModuleHandleW   = dKernel32.NewProc("GetModuleHandleW")
	pGlobalAlloc        = dKernel32.NewProc("GlobalAlloc")
	pGlobalLock         = dKernel32.NewProc("GlobalLock")
	pGlobalUnlock       = dKernel32.NewProc("GlobalUnlock")
	pGlobalFree         = dKernel32.NewProc("GlobalFree")
	pOutputDebugStringW = dKernel32.NewProc("OutputDebugStringW")

	pShellExecuteW = dShell32.NewProc("ShellExecuteW")

	pCoInitializeEx = dOle32.NewProc("CoInitializeEx")
	pCoUninitialize = dOle32.NewProc("CoUninitialize")
)

func ptr(p unsafe.Pointer) uintptr { return uintptr(p) }

// InitDPI 声明 DPI 感知，避免高分屏下界面模糊。
func InitDPI() {
	_, _, _ = pcall(pSetProcessDPIAware)
}

// CoInit 初始化 COM（STA），供 ShellExecute 与剪贴板使用。
func CoInit() {
	const COINIT_APARTMENTTHREADED = 0x2
	_, _, _ = pcall(pCoInitializeEx, 0, COINIT_APARTMENTTHREADED)
}

// CoUninit 反初始化 COM。
func CoUninit() { _, _, _ = pcall(pCoUninitialize) }

// ModuleHandle 返回当前模块句柄。
func ModuleHandle() uintptr {
	h, _, _ := pcall(pGetModuleHandleW, 0)
	return h
}

// RegisterClass 注册窗口类，返回 ATOM（0 表示失败）。
func RegisterClass(wc *WNDCLASSEX) uint16 {
	wc.CbSize = uint32(unsafe.Sizeof(*wc))
	a, _, _ := pcall(pRegisterClassExW, ptr(unsafe.Pointer(wc)))
	return uint16(a)
}

// CreateWindow 创建窗口。
func CreateWindow(exStyle uint32, class, title string, style uint32, x, y, w, h int32,
	parent HMENU, menu HMENU, inst HINSTANCE, param uintptr) HWND {

	cls, _ := syscall.UTF16PtrFromString(class)
	ttl, _ := syscall.UTF16PtrFromString(title)
	r, _, _ := pcall(pCreateWindowExW,
		uintptr(exStyle), uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(ttl)),
		uintptr(style), uintptr(x), uintptr(y), uintptr(w), uintptr(h),
		uintptr(parent), uintptr(menu), uintptr(inst), param)
	return HWND(r)
}

// DefWindowProc 默认消息处理。
func DefWindowProc(h HWND, msg uint32, w, l uintptr) uintptr {
	r, _, _ := pcall(pDefWindowProcW, uintptr(h), uintptr(msg), w, l)
	return r
}

// WndProcFunc 窗口过程签名。
type WndProcFunc func(h HWND, msg uint32, w, l uintptr) uintptr

// NewWndProc 把 Go 函数转成回调地址。
func NewWndProc(fn WndProcFunc) uintptr {
	return syscall.NewCallback(func(h, msg, w, l uintptr) uintptr {
		return fn(HWND(h), uint32(msg), w, l)
	})
}

func ShowWindow(h HWND, cmd int32) { _, _, _ = pcall(pShowWindow, uintptr(h), uintptr(cmd)) }

// ShowWindowAsync 只投递显示请求、不等待窗口线程处理。
// ShowWindow 是同步的：窗口显示期间会与外壳/任务栏交互，Windows 上偶发
// 卡住数十秒（我们自己的看门狗抓到过 ShowWindow 内部 30s+）。启动路径改用
// 异步版本，主循环先跑起来，显示请求随后在消息循环里落地。
func ShowWindowAsync(h HWND, cmd int32) bool {
	r, _, _ := pcall(pShowWindowAsync, uintptr(h), uintptr(cmd))
	return r != 0
}

// IsWindowVisible 返回窗口当前是否带 WS_VISIBLE。
func IsWindowVisible(h HWND) bool {
	r, _, _ := pcall(pIsWindowVisible, uintptr(h))
	return r != 0
}
func UpdateWindow(h HWND)       { _, _, _ = pcall(pUpdateWindow, uintptr(h)) }
func DestroyWindow(h HWND) bool { r, _, _ := pcall(pDestroyWindow, uintptr(h)); return r != 0 }
func Invalidate(h HWND)         { _, _, _ = pcall(pInvalidateRect, uintptr(h), 0, 0) }

// InvalidateRect 只失效指定客户区矩形（物理像素），供脏矩形局部刷新使用。
func InvalidateRect(h HWND, r *RECT, erase bool) {
	e := uintptr(0)
	if erase {
		e = 1
	}
	_, _, _ = pcall(pInvalidateRect, uintptr(h), ptr(unsafe.Pointer(r)), e)
}
func SetWindowText(h HWND, s string) {
	p, _ := syscall.UTF16PtrFromString(s)
	_, _, _ = pcall(pSetWindowTextW, uintptr(h), ptr(unsafe.Pointer(p)))
}
func PostQuitMessage(code int32) { _, _, _ = pcall(pPostQuitMessage, uintptr(code)) }
func PostMessage(h HWND, msg uint32, w, l uintptr) {
	_, _, _ = pcall(pPostMessageW, uintptr(h), uintptr(msg), w, l)
}
func SendMessage(h HWND, msg uint32, w, l uintptr) uintptr {
	r, _, _ := pcall(pSendMessageW, uintptr(h), uintptr(msg), w, l)
	return r
}

// GetMessage 阻塞取消息，返回 false 表示退出循环。
func GetMessage(m *MSG) bool {
	r, _, _ := pcall(pGetMessageW, ptr(unsafe.Pointer(m)), 0, 0, 0)
	return r > 0 // 0 = WM_QUIT，^0 = 错误
}

// PM_REMOVE 是 PeekMessage 的移除标志。
const PM_REMOVE = 0x0001

// PeekMessage 非阻塞取一条消息；没有消息时返回 false。
func PeekMessage(m *MSG, remove bool) bool {
	flag := uintptr(0)
	if remove {
		flag = PM_REMOVE
	}
	r, _, _ := pcall(pPeekMessageW, ptr(unsafe.Pointer(m)), 0, 0, 0, flag)
	return r != 0
}

// MsgWait 的队列标志与唤醒条件。
const (
	QS_ALLINPUT         = 0x04FF
	MWMO_INPUTAVAILABLE = 0x0004
)

// WaitMessageTimeout 等到队列里有消息或超时。
// 比 PeekMessage + Sleep(8ms) 的空转省电得多，也不会丢消息：
// MWMO_INPUTAVAILABLE 保证“已经有消息但还没被取走”时立即返回。
func WaitMessageTimeout(d time.Duration) {
	ms := uint32(d / time.Millisecond)
	if ms == 0 {
		ms = 1
	}
	r, _, err := pcall(pMsgWaitForMultipleObjectsEx, 0, 0, uintptr(ms),
		QS_ALLINPUT, MWMO_INPUTAVAILABLE)
	if err != nil || r == 0xFFFFFFFF {
		// 极老/受限系统拿不到这个导出函数或等待失败时退回 Sleep，至少不忙等。
		time.Sleep(d)
	}
}

func TranslateMessage(m *MSG) { _, _, _ = pcall(pTranslateMessage, ptr(unsafe.Pointer(m))) }
func DispatchMessage(m *MSG)  { _, _, _ = pcall(pDispatchMessageW, ptr(unsafe.Pointer(m))) }

// BeginPaint 开始绘制。
func BeginPaint(h HWND) (HDC, PAINTSTRUCT) {
	var ps PAINTSTRUCT
	r, _, _ := pcall(pBeginPaint, uintptr(h), ptr(unsafe.Pointer(&ps)))
	return HDC(r), ps
}

// EndPaint 结束绘制。
func EndPaint(h HWND, ps *PAINTSTRUCT) {
	_, _, _ = pcall(pEndPaint, uintptr(h), ptr(unsafe.Pointer(ps)))
}

// ClientRect 客户区尺寸。
func ClientRect(h HWND) RECT {
	var r RECT
	_, _, _ = pcall(pGetClientRect, uintptr(h), ptr(unsafe.Pointer(&r)))
	return r
}

func SetTimer(h HWND, id uintptr, ms uint32) uintptr {
	r, _, _ := pcall(pSetTimer, uintptr(h), id, uintptr(ms), 0)
	return r
}

func KillTimer(h HWND, id uintptr) { _, _, _ = pcall(pKillTimer, uintptr(h), id) }

func LoadCursor(id int32) HCURSOR {
	r, _, _ := pcall(pLoadCursorW, 0, uintptr(id))
	return HCURSOR(r)
}

func SetCursor(c HCURSOR) { _, _, _ = pcall(pSetCursor, uintptr(c)) }
func SetCapture(h HWND)   { _, _, _ = pcall(pSetCapture, uintptr(h)) }
func ReleaseCapture()     { _, _, _ = pcall(pReleaseCapture) }

func GetSystemMetrics(idx int32) int32 {
	r, _, _ := pcall(pGetSystemMetrics, uintptr(idx))
	return int32(r)
}

// MsgBox 弹出原生消息框（仅错误兜底用）。
func MsgBox(owner HWND, text, title string, flags uint32) int32 {
	t, _ := syscall.UTF16PtrFromString(text)
	c, _ := syscall.UTF16PtrFromString(title)
	r, _, _ := pcall(pMessageBoxW, uintptr(owner), ptr(unsafe.Pointer(t)), ptr(unsafe.Pointer(c)), uintptr(flags))
	return int32(r)
}

func SetWindowPos(h HWND, after HWND, x, y, w, hh int32, flags uint32) {
	_, _, _ = pcall(pSetWindowPos, uintptr(h), uintptr(after), uintptr(x), uintptr(y), uintptr(w), uintptr(hh), uintptr(flags))
}

// AdjustWindowRect 计算带边框窗口尺寸。
func AdjustWindowRect(r *RECT, style uint32, menu bool) {
	m := uintptr(0)
	if menu {
		m = 1
	}
	_, _, _ = pcall(pAdjustWindowRectEx, ptr(unsafe.Pointer(r)), uintptr(style), m, 0)
}

func GetKeyState(vk int) bool { r, _, _ := pcall(pGetKeyState, uintptr(vk)); return r&0x8000 != 0 }
func GetAsyncKeyState(vk int) bool {
	r, _, _ := pcall(pGetAsyncKeyState, uintptr(vk))
	return r&0x8000 != 0
}

// ---------------------- 剪贴板 ----------------------

// SetClipboardText 写入剪贴板（CF_UNICODETEXT）。
func SetClipboardText(s string) bool {
	if r, _, _ := pcall(pOpenClipboard, 0); r == 0 {
		return false
	}
	defer pcall(pCloseClipboard)
	_, _, _ = pcall(pEmptyClipboard)
	utf, err := syscall.UTF16FromString(s)
	if err != nil {
		return false
	}
	n := len(utf) * 2
	h, _, _ := pcall(pGlobalAlloc, GMEM_MOVEABLE, uintptr(n))
	if h == 0 {
		return false
	}
	lp, _, _ := pcall(pGlobalLock, h)
	if lp == 0 {
		pcall(pGlobalFree, h)
		return false
	}
	dst := unsafe.Slice((*uint16)(unsafe.Pointer(lp)), len(utf))
	copy(dst, utf)
	pcall(pGlobalUnlock, h)
	r, _, _ := pcall(pSetClipboardData, CF_UNICODETEXT, h)
	return r != 0
}

// GetClipboardText 读取剪贴板文本。
func GetClipboardText() string {
	if r, _, _ := pcall(pOpenClipboard, 0); r == 0 {
		return ""
	}
	defer pcall(pCloseClipboard)
	r, _, _ := pcall(pGetClipboardData, CF_UNICODETEXT)
	if r == 0 {
		return ""
	}
	lp, _, _ := pcall(pGlobalLock, r)
	if lp == 0 {
		return ""
	}
	defer pcall(pGlobalUnlock, r)
	// 找到 NUL 结尾
	var n int
	p := (*uint16)(unsafe.Pointer(lp))
	for {
		ch := *(*uint16)(unsafe.Pointer(uintptr(unsafe.Pointer(p)) + uintptr(n*2)))
		if ch == 0 || n > 1<<20 {
			break
		}
		n++
	}
	if n == 0 {
		return ""
	}
	buf := unsafe.Slice(p, n)
	return syscall.UTF16ToString(buf)
}

// ---------------------- GDI ----------------------

func CreateCompatibleDC(hdc HDC) HDC {
	r, _, _ := pcall(pCreateCompatibleDC, uintptr(hdc))
	return HDC(r)
}

// CreateDIBSection 创建 32bpp 位图，返回位图句柄与像素首地址。
func CreateDIBSection(hdc HDC, w, h int32) (HBITMAP, unsafe.Pointer) {
	bi := BITMAPINFO{}
	bi.Header.BiSize = uint32(unsafe.Sizeof(bi.Header))
	bi.Header.BiWidth = w
	bi.Header.BiHeight = -h // 自上而下
	bi.Header.BiPlanes = 1
	bi.Header.BiBitCount = 32
	bi.Header.BiCompression = 0
	var bits unsafe.Pointer
	r, _, _ := pcall(pCreateDIBSection, uintptr(hdc), ptr(unsafe.Pointer(&bi)), 0,
		uintptr(unsafe.Pointer(&bits)), 0, 0)
	return HBITMAP(r), bits
}

func SelectObject(dc HDC, obj uintptr) uintptr {
	r, _, _ := pcall(pSelectObject, uintptr(dc), obj)
	return r
}

func DeleteObject(obj uintptr) { _, _, _ = pcall(pDeleteObject, obj) }
func DeleteDC(dc HDC)          { _, _, _ = pcall(pDeleteDC, uintptr(dc)) }

func BitBlt(dst HDC, x, y, w, h int32, src HDC, sx, sy int32, rop uint32) {
	_, _, _ = pcall(pBitBlt, uintptr(dst), uintptr(x), uintptr(y), uintptr(w), uintptr(h),
		uintptr(src), uintptr(sx), uintptr(sy), uintptr(rop))
}

func StretchBlt(dst HDC, x, y, w, h int32, src HDC, sx, sy, sw, sh int32, rop uint32) {
	_, _, _ = pcall(pStretchBlt, uintptr(dst), uintptr(x), uintptr(y), uintptr(w), uintptr(h),
		uintptr(src), uintptr(sx), uintptr(sy), uintptr(sw), uintptr(sh), uintptr(rop))
}

// CreateFont 创建字体，height 为正表示字高（像素）。
func CreateFont(height int32, weight int32, italic bool, face string) HFONT {
	lf := LOGFONT{}
	lf.LfHeight = -height
	lf.LfWeight = weight
	if italic {
		lf.LfItalic = 1
	}
	lf.LfCharSet = DEFAULT_CHARSET
	lf.LfQuality = CLEARTYPE_QUALITY
	lf.LfPitchAndFamily = DEFAULT_PITCH | FF_DONTCARE
	name := syscall.StringToUTF16(face)
	for i := 0; i < len(name) && i < 31; i++ {
		lf.LfFaceName[i] = name[i]
	}
	r, _, _ := pcall(pCreateFontW,
		uintptr(lf.LfHeight), 0, 0, 0, uintptr(lf.LfWeight),
		uintptr(lf.LfItalic), 0, 0, uintptr(lf.LfCharSet), 0, 0,
		uintptr(lf.LfQuality), uintptr(lf.LfPitchAndFamily),
		ptr(unsafe.Pointer(&lf.LfFaceName[0])))
	return HFONT(r)
}

func SetBkMode(dc HDC, mode int32)  { _, _, _ = pcall(pSetBkMode, uintptr(dc), uintptr(mode)) }
func SetTextColor(dc HDC, c uint32) { _, _, _ = pcall(pSetTextColor, uintptr(dc), uintptr(c)) }
func SetBkColor(dc HDC, c uint32)   { _, _, _ = pcall(pSetBkColor, uintptr(dc), uintptr(c)) }
func SetROP2(dc HDC, rop int32)     { _, _, _ = pcall(pSetROP2, uintptr(dc), uintptr(rop)) }

// TextOut 输出文本。
func TextOut(dc HDC, x, y int32, s string) {
	if s == "" {
		return
	}
	p, err := syscall.UTF16FromString(s)
	if err != nil {
		return
	}
	_, _, _ = pcall(pTextOutW, uintptr(dc), uintptr(x), uintptr(y), ptr(unsafe.Pointer(&p[0])), uintptr(len(p)-1))
}

// DrawText 富文本绘制，flags 为 DT_* 组合。
func DrawText(dc HDC, s string, r *RECT, flags uint32) int32 {
	if s == "" {
		return 0
	}
	p, err := syscall.UTF16FromString(s)
	if err != nil {
		return 0
	}
	res, _, _ := pcall(pDrawTextW, uintptr(dc), ptr(unsafe.Pointer(&p[0])), uintptr(len(p)-1),
		ptr(unsafe.Pointer(r)), uintptr(flags))
	return int32(res)
}

// TextWidth 计算文本像素宽度。
func TextWidth(dc HDC, s string) int32 {
	if s == "" {
		return 0
	}
	p, err := syscall.UTF16FromString(s)
	if err != nil {
		return 0
	}
	var sz SIZE
	_, _, _ = pcall(pGetTextExtentPoint32W, uintptr(dc), ptr(unsafe.Pointer(&p[0])), uintptr(len(p)-1), ptr(unsafe.Pointer(&sz)))
	return sz.CX
}

// TextHeight 计算文本行高。
func TextHeight(dc HDC) int32 {
	// 用 "国Ag" 估算，兼容中英文混合
	var sz SIZE
	s := syscall.StringToUTF16("国Ag")
	_, _, _ = pcall(pGetTextExtentPoint32W, uintptr(dc), ptr(unsafe.Pointer(&s[0])), uintptr(len(s)-1), ptr(unsafe.Pointer(&sz)))
	return sz.CY
}

func CreateSolidBrush(c uint32) HBRUSH {
	r, _, _ := pcall(pCreateSolidBrush, uintptr(c))
	return HBRUSH(r)
}

func FillRect(dc HDC, r RECT, br HBRUSH) {
	_, _, _ = pcall(pFillRect, uintptr(dc), ptr(unsafe.Pointer(&r)), uintptr(br))
}

func RoundRect(dc HDC, r RECT, radius int32) {
	_, _, _ = pcall(pRoundRect, uintptr(dc), uintptr(r.Left), uintptr(r.Top), uintptr(r.Right), uintptr(r.Bottom),
		uintptr(radius*2), uintptr(radius*2))
}

func Rectangle(dc HDC, r RECT) {
	_, _, _ = pcall(pRectangle, uintptr(dc), uintptr(r.Left), uintptr(r.Top), uintptr(r.Right), uintptr(r.Bottom))
}

func Ellipse(dc HDC, r RECT) {
	_, _, _ = pcall(pEllipse, uintptr(dc), uintptr(r.Left), uintptr(r.Top), uintptr(r.Right), uintptr(r.Bottom))
}

func CreatePen(style int32, width int32, c uint32) HPEN {
	r, _, _ := pcall(pCreatePen, uintptr(style), uintptr(width), uintptr(c))
	return HPEN(r)
}

// Line 画线。
func Line(dc HDC, x1, y1, x2, y2 int32) {
	_, _, _ = pcall(pMoveToEx, uintptr(dc), uintptr(x1), uintptr(y1), 0)
	_, _, _ = pcall(pLineTo, uintptr(dc), uintptr(x2), uintptr(y2))
}

func GetDeviceCaps(dc HDC, idx int32) int32 {
	r, _, _ := pcall(pGetDeviceCaps, uintptr(dc), uintptr(idx))
	return int32(r)
}

func CreateRectRgn(l, t, r, b int32) uintptr {
	h, _, _ := pcall(pCreateRectRgn, uintptr(l), uintptr(t), uintptr(r), uintptr(b))
	return h
}

func SelectClipRgn(dc HDC, rgn uintptr) { _, _, _ = pcall(pSelectClipRgn, uintptr(dc), rgn) }

// GradientFillRect 垂直/水平渐变填充。
func GradientFillRect(dc HDC, r RECT, c1, c2 uint32, vertical bool) {
	verts := [2]TRIVERTEX{
		{r.Left, r.Top, uint16(c1&0xFF) << 8, uint16((c1>>8)&0xFF) << 8, uint16((c1>>16)&0xFF) << 8, 0xFF00},
		{r.Right, r.Bottom, uint16(c2&0xFF) << 8, uint16((c2>>8)&0xFF) << 8, uint16((c2>>16)&0xFF) << 8, 0xFF00},
	}
	mode := uint32(GRADIENT_FILL_RECT_H)
	if vertical {
		mode = GRADIENT_FILL_RECT_V
	}
	gr := [1]GRADIENTRECT{{0, 1}}
	_, _, _ = pcall(pGradientFill, uintptr(dc), ptr(unsafe.Pointer(&verts[0])), 2,
		ptr(unsafe.Pointer(&gr[0])), 1, uintptr(mode))
}

// AlphaBlend 半透明合成。
func AlphaBlend(dst HDC, x, y, w, h int32, src HDC, sx, sy, sw, sh int32, alpha byte) {
	bf := BLENDFUNCTION{BlendOp: AC_SRC_OVER, BlendFlags: 0, SourceConstantAlpha: alpha, AlphaFormat: 0}
	_, _, _ = pcall(pAlphaBlend, uintptr(dst), uintptr(x), uintptr(y), uintptr(w), uintptr(h),
		uintptr(src), uintptr(sx), uintptr(sy), uintptr(sw), uintptr(sh), ptr(unsafe.Pointer(&bf)))
}

// ShellOpen 用系统默认程序打开文件或网址。
func ShellOpen(path string) bool {
	op, _ := syscall.UTF16PtrFromString("open")
	f, _ := syscall.UTF16PtrFromString(path)
	r, _, _ := pcall(pShellExecuteW, 0, ptr(unsafe.Pointer(op)), ptr(unsafe.Pointer(f)), 0, 0, SW_SHOWNORMAL)
	return r > 32
}

// GetDC 取得窗口 DC。
func GetDC(h HWND) HDC {
	r, _, _ := pcall(pGetDC, uintptr(h))
	return HDC(r)
}

func ReleaseDC(h HWND, dc HDC) { _, _, _ = pcall(pReleaseDC, uintptr(h), uintptr(dc)) }

// SetFocus 设置焦点。
func SetFocus(h HWND) { _, _, _ = pcall(pSetFocus, uintptr(h)) }

// SetForeground 激活窗口。
func SetForeground(h HWND) { _, _, _ = pcall(pSetForegroundWindow, uintptr(h)) }

// ScreenToClientPoint 屏幕坐标转客户区。
func ScreenToClientPoint(h HWND, p *POINT) {
	_, _, _ = pcall(pScreenToClient, uintptr(h), ptr(unsafe.Pointer(p)))
}

// ClientToScreenPoint 客户端坐标转屏幕坐标（可访问对象 accLocation 用）。
func ClientToScreenPoint(h HWND, p *POINT) {
	_, _, _ = pcall(pClientToScreen, uintptr(h), ptr(unsafe.Pointer(p)))
}

// GetStockObject 取系统预置 GDI 对象（NULL_BRUSH=5, NULL_PEN=8）。
func GetStockObject(kind int32) uintptr {
	r, _, _ := pcall(pGetStockObject, uintptr(kind))
	return r
}

var _ = pGetFocus
var _ = pGetTextMetricsW
var _ = pSetPixel
var _ = pSetBkColor
var _ = pSendMessageTimeoutW
var _ = pGetWindowTextW
var _ = pSetWindowLongPtrW
var _ = pGetWindowLongPtrW
var _ = pShowScrollBar
var _ = pClientToScreen
var _ = pGetAsyncKeyState
var _ = pIsClipboardFormatAvailable
var _ = pOutputDebugStringW

// ---------- 图标 ----------

// WM_SETICON / ICON_* 常量。
const (
	WM_SETICON = 0x0080
	ICON_SMALL = 0
	ICON_BIG   = 1

	IMAGE_ICON      = 1
	LR_LOADFROMFILE = 0x00000010
	LR_DEFAULTSIZE  = 0x00000040
	GCLP_HICON      = -14
	GCLP_HICONSM    = -34
)

// LoadIconFile 从 .ico 文件加载图标；w/h 传 0 表示使用系统默认尺寸。
// 失败返回 0（调用方应降级为默认图标，不要 panic）。
func LoadIconFile(path string, w, h int32) HICON {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0
	}
	flags := uintptr(LR_LOADFROMFILE)
	if w == 0 && h == 0 {
		flags |= LR_DEFAULTSIZE
	}
	r, _, _ := pcall(pLoadImageW, 0, uintptr(unsafe.Pointer(p)), IMAGE_ICON,
		uintptr(w), uintptr(h), flags)
	return HICON(r)
}

// LoadDefaultIcon 取系统默认应用图标（IDI_APPLICATION）。
// 托盘必须用有效 HICON：传 0 给 Shell_NotifyIconW 在部分 Win7 上会静默失败。
func LoadDefaultIcon() HICON {
	r, _, _ := pcall(pLoadImageW, 0, uintptr(32512), IMAGE_ICON, 16, 16, 0)
	return HICON(r)
}

// SetWindowIcon 把图标挂到窗口（小图标=标题栏，大图标=任务栏/Alt-Tab）。
func SetWindowIcon(h HWND, small, big HICON) {
	if small != 0 {
		_, _, _ = pcall(pSendMessageW, uintptr(h), WM_SETICON, ICON_SMALL, uintptr(small))
	}
	if big != 0 {
		_, _, _ = pcall(pSendMessageW, uintptr(h), WM_SETICON, ICON_BIG, uintptr(big))
	}
}

// SetClassIcon 设置窗口类图标，影响后续新窗口与该进程的任务栏分组图标。
func SetClassIcon(h HWND, small, big HICON) {
	// 索引是负数，必须先落到变量再转 uintptr（Go 不允许负常量直接转）。
	var idxBig, idxSmall int = GCLP_HICON, GCLP_HICONSM
	if big != 0 {
		_, _, _ = pcall(pSetClassLongPtrW, uintptr(h), uintptr(idxBig), uintptr(big))
	}
	if small != 0 {
		_, _, _ = pcall(pSetClassLongPtrW, uintptr(h), uintptr(idxSmall), uintptr(small))
	}
}

// ForegroundWindow 返回当前前台窗口句柄。
// v1.28 用它判断"窗口是不是被挡住了"：只有人看不到的时候才值得弹气泡，
// 否则每轮回答都弹一次，托盘通知就成了噪音。
func ForegroundWindow() HWND {
	r, _, _ := pcall(pGetForegroundWindow)
	return HWND(r)
}
