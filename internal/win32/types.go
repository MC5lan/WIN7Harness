//go:build windows

package win32

import "syscall"

// 句柄统一用 uintptr，避免 386/amd64 转换差异。
type (
	HWND      uintptr
	HDC       uintptr
	HINSTANCE uintptr
	HICON     uintptr
	HCURSOR   uintptr
	HBRUSH    uintptr
	HPEN      uintptr
	HFONT     uintptr
	HBITMAP   uintptr
	HMENU     uintptr
	HGDIOBJ   uintptr
	ATOM      uint16
)

// POINT 坐标。
type POINT struct {
	X, Y int32
}

// SIZE 尺寸。
type SIZE struct {
	CX, CY int32
}

// RECT 矩形。
type RECT struct {
	Left, Top, Right, Bottom int32
}

func (r RECT) Width() int32  { return r.Right - r.Left }
func (r RECT) Height() int32 { return r.Bottom - r.Top }

// WNDCLASSEX 窗口类。
type WNDCLASSEX struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     HINSTANCE
	HIcon         HICON
	HCursor       HCURSOR
	HbrBackground HBRUSH
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       HICON
}

// MSG 消息。
type MSG struct {
	Hwnd    HWND
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      POINT
}

// PAINTSTRUCT 绘制结构。
type PAINTSTRUCT struct {
	Hdc         HDC
	FErase      int32
	RcPaint     RECT
	FRestore    int32
	FIncUpdate  int32
	RgbReserved [32]byte
}

// BITMAPINFOHEADER 位图头。
type BITMAPINFOHEADER struct {
	BiSize          uint32
	BiWidth         int32
	BiHeight        int32
	BiPlanes        uint16
	BiBitCount      uint16
	BiCompression   uint32
	BiSizeImage     uint32
	BiXPelsPerMeter int32
	BiYPelsPerMeter int32
	BiClrUsed       uint32
	BiClrImportant  uint32
}

// BITMAPINFO 位图信息（仅支持 32bpp BI_RGB，无需调色板）。
type BITMAPINFO struct {
	Header BITMAPINFOHEADER
	Colors [1]uint32
}

// LOGFONTW 字体描述。
type LOGFONT struct {
	LfHeight         int32
	LfWidth          int32
	LfEscapement     int32
	LfOrientation    int32
	LfWeight         int32
	LfItalic         byte
	LfUnderline      byte
	LfStrikeOut      byte
	LfCharSet        byte
	LfOutPrecision   byte
	LfClipPrecision  byte
	LfQuality        byte
	LfPitchAndFamily byte
	LfFaceName       [32]uint16
}

// BLENDFUNCTION AlphaBlend 参数。
type BLENDFUNCTION struct {
	BlendOp             byte
	BlendFlags          byte
	SourceConstantAlpha byte
	AlphaFormat         byte
}

// TRIVERTEX 渐变端点。
type TRIVERTEX struct {
	X, Y        int32
	Red, Green  uint16
	Blue, Alpha uint16
}

// GRADIENTRECT 渐变矩形索引。
type GRADIENTRECT struct {
	UpperLeft, LowerRight uint32
}

// 常用常量。
const (
	WS_OVERLAPPED   = 0x00000000
	WS_CAPTION      = 0x00C00000
	WS_SYSMENU      = 0x00080000
	WS_THICKFRAME   = 0x00040000
	WS_MINIMIZEBOX  = 0x00020000
	WS_MAXIMIZEBOX  = 0x00010000
	WS_CLIPCHILDREN = 0x02000000
	WS_VISIBLE      = 0x10000000

	WS_OVERLAPPEDWINDOW = WS_OVERLAPPED | WS_CAPTION | WS_SYSMENU | WS_THICKFRAME | WS_MINIMIZEBOX | WS_MAXIMIZEBOX

	CS_VREDRAW         = 0x0001
	CS_HREDRAW         = 0x0002
	CS_DBLCLKS         = 0x0008
	CS_OWNDC           = 0x0020
	CS_BYTEALIGNWINDOW = 0x2000

	WM_CREATE               = 0x0001
	WM_DESTROY              = 0x0002
	WM_MOVE                 = 0x0003
	WM_SIZE                 = 0x0005
	WM_ACTIVATE             = 0x0006
	WM_SHOWWINDOW           = 0x0018
	WM_SETFOCUS             = 0x0007
	WM_KILLFOCUS            = 0x0008
	WM_PAINT                = 0x000F
	WM_CLOSE                = 0x0010
	WM_QUERYENDSESSION      = 0x0011
	WM_ENDSESSION           = 0x0016
	WM_POWERBROADCAST       = 0x0218
	WM_WTSSESSION_CHANGE    = 0x02B1
	WM_QUIT                 = 0x0012
	WM_ERASEBKGND           = 0x0014
	WM_KEYDOWN              = 0x0100
	WM_KEYUP                = 0x0101
	WM_CHAR                 = 0x0102
	WM_SYSKEYDOWN           = 0x0104
	WM_IME_SETCONTEXT       = 0x0281
	WM_IME_STARTCOMPOSITION = 0x010D
	WM_IME_COMPOSITION      = 0x010F
	WM_IME_ENDCOMPOSITION   = 0x010E
	WM_TIMER                = 0x0113
	WM_MOUSEMOVE            = 0x0200
	WM_LBUTTONDOWN          = 0x0201
	WM_LBUTTONUP            = 0x0202
	WM_LBUTTONDBLCLK        = 0x0203
	WM_RBUTTONDOWN          = 0x0204
	WM_RBUTTONUP            = 0x0205
	WM_MOUSEWHEEL           = 0x020A
	WM_MOUSELEAVE           = 0x02A3
	WM_GETMINMAXINFO        = 0x0024
	WM_SETTINGCHANGE        = 0x001A
	WM_THEMECHANGED         = 0x031A
	WM_DISPLAYCHANGE        = 0x007E
	WM_SETCURSOR            = 0x0020
	WM_NCHITTEST            = 0x0084
	WM_DPICHANGED           = 0x02E0

	SW_HIDE        = 0
	SW_SHOWNORMAL  = 1
	SW_SHOW        = 5
	SW_SHOWDEFAULT = 10

	SWP_NOSIZE       = 0x0001
	SWP_NOMOVE       = 0x0002
	SWP_NOZORDER     = 0x0004
	SWP_NOACTIVATE   = 0x0010
	SWP_FRAMECHANGED = 0x0020
	SWP_SHOWWINDOW   = 0x0040

	SW_MAXIMIZE = 3
	SW_MINIMIZE = 6
	SW_RESTORE  = 9

	IDC_ARROW   = 32512
	IDC_IBEAM   = 32513
	IDC_HAND    = 32649
	IDC_SIZEWE  = 32644
	IDC_SIZEALL = 32646

	SM_CXSCREEN     = 0
	SM_CYSCREEN     = 1
	SM_CXFULLSCREEN = 16
	SM_CYFULLSCREEN = 17

	COLOR_WINDOW = 5

	FW_NORMAL   = 400
	FW_MEDIUM   = 500
	FW_SEMIBOLD = 600
	FW_BOLD     = 700

	DEFAULT_CHARSET     = 1
	CLEARTYPE_QUALITY   = 5
	ANTIALIASED_QUALITY = 4
	DEFAULT_PITCH       = 0
	FF_DONTCARE         = 0

	TRANSPARENT = 1
	OPAQUE      = 2

	PS_SOLID = 0

	SRCCOPY    = 0x00CC0020
	CAPTUREBLT = 0x40000000

	AC_SRC_OVER  = 0x00
	AC_SRC_ALPHA = 0x01

	GRADIENT_FILL_RECT_H = 0
	GRADIENT_FILL_RECT_V = 1

	CF_UNICODETEXT = 13
	GMEM_MOVEABLE  = 0x0002

	DT_LEFT         = 0x00000000
	DT_CENTER       = 0x00000001
	DT_RIGHT        = 0x00000002
	DT_VCENTER      = 0x00000004
	DT_WORDBREAK    = 0x00000010
	DT_SINGLELINE   = 0x00000020
	DT_NOPREFIX     = 0x00000800
	DT_END_ELLIPSIS = 0x00008000
	DT_CALCRECT     = 0x00000400
	DT_TOP          = 0x00000000
	DT_BOTTOM       = 0x00000008

	MB_OK              = 0x00000000
	MB_ICONINFORMATION = 0x00000040
	MB_ICONERROR       = 0x00000010
	MB_ICONWARNING     = 0x00000030

	VK_RETURN  = 0x0D
	VK_BACK    = 0x08
	VK_TAB     = 0x09
	VK_ESCAPE  = 0x1B
	VK_DELETE  = 0x2E
	VK_LEFT    = 0x25
	VK_UP      = 0x26
	VK_RIGHT   = 0x27
	VK_DOWN    = 0x28
	VK_HOME    = 0x24
	VK_END     = 0x23
	VK_SHIFT   = 0x10
	VK_CONTROL = 0x11
	VK_MENU    = 0x12
	VK_SPACE   = 0x20
	VK_PRIOR   = 0x21
	VK_NEXT    = 0x22
	VK_A       = 0x41
	VK_C       = 0x43
	VK_V       = 0x56
	VK_X       = 0x58
	VK_Z       = 0x5A

	GWLP_USERDATA = ^uintptr(0) - 20 // -21
)

// RGB 生成 GDI 颜色值。
func RGB(r, g, b uint32) uint32 { return r | g<<8 | b<<16 }

// LOWORD / HIWORD 辅助。
func LOWORD(v uintptr) int32      { return int32(v & 0xFFFF) }
func HIWORD(v uintptr) int32      { return int32((v >> 16) & 0xFFFF) }
func MAKELONG(lo, hi int32) int32 { return int32(uint32(uint16(lo)) | uint32(uint16(hi))<<16) }

var _ = syscall.Handle(0)
