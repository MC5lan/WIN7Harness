//go:build windows

package ui

// 框选截图（PrintScreen）。
//
// 实现要点：
//  1. 先把整个虚拟桌面抓进内存并"冻结"显示，这样拖拽时画面不会因为背后的
//     窗口刷新而抖动（也不是真透明，胜在简单可控、Win7 上不需要 DWM 支持）；
//  2. 遮罩窗口是顶层无边框弹出窗，自绘选区，松手即裁剪；
//  3. 热键走 WH_KEYBOARD_LL：PrintScreen 无法用 RegisterHotKey 注册；
//  4. 全过程同步跑在 UI 线程的嵌套消息循环里，截图结束时一次性把结果回调出去。

import (
	"sync"
	"syscall"
	"unsafe"

	"win7harness/internal/core"
	"win7harness/internal/win32"
)

const (
	snipMsg = 0x8000 + 0x52 // WM_APP + 0x52，主窗口收到即开始截图

	wsPopup        = 0x80000000
	wsExTopmost    = 0x00000008
	wsExToolWindow = 0x00000080
	swShow         = 5
	crossCursor    = 32515
	swpNoActivate  = 0x0010
)

// SnipDone 接收裁剪后的 32bpp DIB（自上而下）。
type SnipDone func(dib []byte, w, h int)

var (
	snipOnce   sync.Once
	snipClass  = "Win7HarnessSnip"
	snipProcFn uintptr

	// 单例状态：同一时刻只可能有一次截图，且全程在 UI 线程。
	snip *snipState
)

type snipState struct {
	dib   []byte
	w, h  int32
	ox    int32 // 虚拟屏幕原点（多显示器时可能为负）
	oy    int32
	drag  bool
	x0    int32
	y0    int32
	x1    int32
	y1    int32
	done  bool
	abort bool
}

// EnableSnip 安装 PrintScreen 钩子；按下后回调 onDone。
// 注意：这里只登记回调，真正的 SetWindowsHookEx 要等主循环跑起来后的
// 第一个定时器再装。低级键盘钩子会在安装线程上接收全局按键，窗口还没
// 显示时安装它没有任何收益，反而让 ShowWindow 与钩子初始化挤在一起。
func (w *Window) EnableSnip(onDone SnipDone) {
	w.onSnip = onDone
	w.snipDeferred = true
}

// DisableSnip 卸载钩子。
func (w *Window) DisableSnip() { win32.RemoveSnipHotkey() }

// SnipNow 主动开始一次框选截图（工具栏按钮入口）。返回是否成功拿到选区。
func SnipInteractive(owner win32.HWND) ([]byte, int, int, bool) {
	dib, w, h, err := win32.CaptureVirtualScreen()
	if err != nil || len(dib) == 0 {
		return nil, 0, 0, false
	}
	vs := win32.VirtualScreen()
	st := &snipState{dib: dib, w: int32(w), h: int32(h), ox: vs.Left, oy: vs.Top}
	snip = st
	defer func() { snip = nil }()

	registerSnipClass()
	hwnd := win32.CreateWindow(wsExTopmost|wsExToolWindow, snipClass, "截图",
		wsPopup, vs.Left, vs.Top, int32(w), int32(h), 0, 0, win32.HINSTANCE(win32.ModuleHandle()), 0)
	if hwnd == 0 {
		return nil, 0, 0, false
	}
	win32.ShowWindow(hwnd, swShow)
	win32.SetForeground(hwnd)
	win32.SetFocus(hwnd)
	win32.SetCursor(win32.LoadCursor(crossCursor))
	win32.UpdateWindow(hwnd)

	var msg win32.MSG
	for !st.done {
		if !win32.GetMessage(&msg) {
			break
		}
		win32.TranslateMessage(&msg)
		win32.DispatchMessage(&msg)
	}
	win32.DestroyWindow(hwnd)
	win32.SetCursor(0)

	if st.abort || !st.done {
		return nil, 0, 0, false
	}
	r := core.NormalizeRect(int(st.x0), int(st.y0), int(st.x1), int(st.y1))
	if r.W < 4 || r.H < 4 { // 误触：松手时几乎没移动，按取消处理
		return nil, 0, 0, false
	}
	out, err := core.CropDIB32(st.dib, r)
	if err != nil {
		return nil, 0, 0, false
	}
	// 顺手写进剪贴板，符合"截完就能 Ctrl+V"的使用习惯。
	win32.SetClipboardDIB(out)
	return out, r.W, r.H, true
}

func registerSnipClass() {
	snipOnce.Do(func() {
		snipProcFn = win32.NewWndProc(snipProc)
		cls, _ := syscall.UTF16PtrFromString(snipClass)
		wc := win32.WNDCLASSEX{
			CbSize:        uint32(unsafe.Sizeof(win32.WNDCLASSEX{})),
			LpfnWndProc:   snipProcFn,
			HInstance:     win32.HINSTANCE(win32.ModuleHandle()),
			HCursor:       win32.LoadCursor(crossCursor),
			HbrBackground: win32.CreateSolidBrush(0x00000000),
			LpszClassName: cls,
		}
		win32.RegisterClass(&wc)
	})
}

func snipProc(h win32.HWND, msg uint32, wp, lp uintptr) uintptr {
	st := snip
	switch msg {
	case win32.WM_ERASEBKGND:
		return 1
	case win32.WM_PAINT:
		dc, ps := win32.BeginPaint(h)
		if st != nil {
			drawSnip(dc, st)
		}
		win32.EndPaint(h, &ps)
		return 0
	case win32.WM_SETCURSOR:
		win32.SetCursor(win32.LoadCursor(crossCursor))
		return 1
	case win32.WM_LBUTTONDOWN:
		if st == nil {
			return 0
		}
		st.drag = true
		st.x0, st.x1 = int32(int16(lp&0xFFFF)), int32(int16(lp&0xFFFF))
		st.y0, st.y1 = int32(int16((lp>>16)&0xFFFF)), int32(int16((lp>>16)&0xFFFF))
		win32.SetCapture(h)
		win32.Invalidate(h)
		return 0
	case win32.WM_MOUSEMOVE:
		if st != nil && st.drag {
			st.x1 = int32(int16(lp & 0xFFFF))
			st.y1 = int32(int16((lp >> 16) & 0xFFFF))
			win32.Invalidate(h)
		}
		return 0
	case win32.WM_LBUTTONUP:
		if st != nil && st.drag {
			st.x1 = int32(int16(lp & 0xFFFF))
			st.y1 = int32(int16((lp >> 16) & 0xFFFF))
			st.drag = false
			st.done = true
			win32.ReleaseCapture()
			win32.PostQuitMessage(0)
		}
		return 0
	case win32.WM_RBUTTONDOWN, win32.WM_KEYDOWN:
		if msg == win32.WM_KEYDOWN && wp != 0x1B { // 只认 Esc
			return win32.DefWindowProc(h, msg, wp, lp)
		}
		if st != nil {
			st.abort = true
			st.done = true
		}
		win32.ReleaseCapture()
		win32.PostQuitMessage(0)
		return 0
	}
	return win32.DefWindowProc(h, msg, wp, lp)
}

// drawSnip 画冻结画面 + 选区高亮。
func drawSnip(dc win32.HDC, st *snipState) {
	if st.w <= 0 || st.h <= 0 {
		return
	}
	// 一次性把 DIB 铺满窗口（源是自上而下，按行倒序拷入 bottom-up 的 DIB section）。
	mem := win32.CreateCompatibleDC(dc)
	if mem == 0 {
		return
	}
	bmp, bits := win32.CreateDIBSection(mem, st.w, st.h)
	if bmp == 0 || bits == nil {
		win32.DeleteDC(mem)
		return
	}
	old := win32.SelectObject(mem, uintptr(bmp))
	stride := int(st.w) * 4
	raw := unsafe.Slice((*byte)(bits), stride*int(st.h))
	src := st.dib[40:]
	for y := 0; y < int(st.h); y++ {
		copy(raw[y*stride:(y+1)*stride], src[(int(st.h)-1-y)*stride:(int(st.h)-y)*stride])
	}

	// 未选区压暗：不透明深灰，代价最低，视觉上也足够交代"这块不截"。
	dim := win32.CreateSolidBrush(0x00323232)
	if st.drag || st.done {
		r := core.NormalizeRect(int(st.x0), int(st.y0), int(st.x1), int(st.y1))
		l, t := int32(r.X), int32(r.Y)
		rr, bb := int32(r.X+r.W), int32(r.Y+r.H)
		for _, band := range [][4]int32{
			{0, 0, st.w, t},
			{0, bb, st.w, st.h},
			{0, t, l, bb},
			{rr, t, st.w, bb},
		} {
			if band[2] <= band[0] || band[3] <= band[1] {
				continue
			}
			rect := win32.RECT{Left: band[0], Top: band[1], Right: band[2], Bottom: band[3]}
			win32.FillRect(dc, rect, dim)
		}
		// 选区内显示原始亮画面
		win32.BitBlt(dc, l, t, int32(r.W), int32(r.H), mem, l, t, win32.SRCCOPY)
		// 白框
		pen := win32.CreatePen(0, 2, 0x00FFFFFF)
		po := win32.SelectObject(dc, uintptr(pen))
		win32.SelectObject(dc, win32.GetStockObject(5)) // NULL_BRUSH
		win32.Rectangle(dc, win32.RECT{Left: l, Top: t, Right: rr - 1, Bottom: bb - 1})
		win32.SelectObject(dc, po)
		win32.DeleteObject(uintptr(pen))
		drawSnipHUD(dc, r)
	} else {
		win32.BitBlt(dc, 0, 0, st.w, st.h, mem, 0, 0, win32.SRCCOPY)
		win32.FillRect(dc, win32.RECT{Left: 0, Top: 0, Right: st.w, Bottom: st.h}, dim)
		drawSnipHint(dc, st)
	}

	win32.SelectObject(mem, old)
	win32.DeleteObject(uintptr(bmp))
	win32.DeleteDC(mem)
}

// drawSnipHUD 在选区左上角显示尺寸。
func drawSnipHUD(dc win32.HDC, r core.DIBRect) {
	txt := itoa(int32(r.W)) + " x " + itoa(int32(r.H))
	font := win32.CreateFont(-14, 400, false, "Segoe UI")
	of := win32.SelectObject(dc, uintptr(font))
	bg := win32.CreateSolidBrush(0x00000000)
	pad := int32(6)
	w := win32.TextWidth(dc, txt) + pad*2
	boxY := int32(r.Y) - 22
	if boxY < 0 {
		boxY = int32(r.Y) + 4
	}
	box := win32.RECT{Left: int32(r.X), Top: boxY, Right: int32(r.X) + w, Bottom: boxY + 18}
	win32.FillRect(dc, box, bg)
	win32.SetBkMode(dc, 1)
	win32.SetTextColor(dc, 0x00FFFFFF)
	win32.TextOut(dc, int32(r.X)+pad, boxY+2, txt)
	win32.SelectObject(dc, of)
	win32.DeleteObject(uintptr(font))
	win32.DeleteObject(uintptr(bg))
}

// drawSnipHint 首次进入时的操作提示。
func drawSnipHint(dc win32.HDC, st *snipState) {
	txt := "拖动鼠标框选区域  ·  Esc / 右键 取消"
	font := win32.CreateFont(-18, 500, false, "Microsoft YaHei")
	of := win32.SelectObject(dc, uintptr(font))
	win32.SetBkMode(dc, 1)
	win32.SetTextColor(dc, 0x00FFFFFF)
	w := win32.TextWidth(dc, txt)
	x := (st.w - w) / 2
	win32.TextOut(dc, x, st.h/2-20, txt)
	win32.SelectObject(dc, of)
	win32.DeleteObject(uintptr(font))
}
