//go:build windows

package ui

import (
	"fmt"
	"math"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
	"unsafe"

	"win7harness/internal/core"
	"win7harness/internal/win32"
)

// App 是窗口的应用逻辑接口，由具体界面（引导页/主界面）实现。
type App interface {
	// Paint 每帧绘制，坐标为逻辑像素。
	Paint(c *Canvas)
	// Resize 客户区尺寸变化。
	Resize(w, h int32)
	// MouseMove 鼠标移动。
	MouseMove(x, y int32)
	// MouseDown / MouseUp button: 0 左键, 1 右键。
	MouseDown(x, y int32, button int)
	MouseUp(x, y int32, button int)
	DoubleClick(x, y int32)
	// MouseWheel delta 为正表示向上滚。
	MouseWheel(x, y int32, delta int32)
	// KeyDown 返回 true 表示已处理（阻止默认）。
	KeyDown(vk int32) bool
	// Char 输入字符。
	Char(ch rune)
	// Timer 由 30ms 定时器驱动，用于动画推进；返回 true 表示需要重绘。
	Timer() bool
	// Cursor 返回鼠标指针类型：0 箭头, 1 文本, 2 手型, 3 左右调整。
	Cursor() int
	// Close 请求关闭，返回 false 则取消关闭。
	Close() bool
}

// CaretApp 是可选能力：实现后输入法组字窗口会跟随光标，而不是贴在窗口左上角。
// 返回的是逻辑像素坐标，Window 内部再按 DPI 缩放。
type CaretApp interface {
	CaretPos() (x, y int32, ok bool)
}

// DropFilesApp 是可选能力：实现后窗口可接收从资源管理器拖入的文件。
type DropFilesApp interface {
	DropFiles(paths []string)
}

// SessionEndingApp 在 WM_QUERYENDSESSION 时做“快速落盘”，
// 但不停止后台队列——用户仍可能取消关机。
type SessionEndingApp interface {
	PrepareForSessionEnd()
}

// ShutdownApp 在 WM_ENDSESSION(wParam!=0) 时做最终收尾：
// 停生成、停后台队列、关 MCP/LSP。此时系统马上会杀进程。
type ShutdownApp interface {
	Shutdown()
}

// SuspendApp 在系统挂起前落盘；ResumeApp 在恢复后重探代理/网络。
type SuspendApp interface {
	Suspend()
}
type ResumeApp interface {
	Resume()
}

// ThemeApp 是可选能力：系统高对比/主题变化时刷新配色。
// Win7 改高对比只发 WM_SETTINGCHANGE/WM_THEMECHANGED，不会重启进程。
type ThemeApp interface {
	RefreshTheme()
}

// SettingsApp 是可选能力：WinINET 代理设置变化时刷新客户端/本机桥，
// 不必重启程序（WM_SETTINGCHANGE 的 lParam == "WininetSettings"）。
type SettingsApp interface {
	SettingsChanged()
}

// SessionChangeApp 接收锁屏/解锁/RDP 连接/断开。
type SessionChangeApp interface {
	SessionChange(event uint32)
}

// BusyApp 用于长任务期间阻止系统自动睡眠。
type BusyApp interface {
	Busy() bool
}

// AttachApp 是可选能力：窗口创建后把自身回交给应用。
// 应用借此拿到 HWND，托盘、桌面通知、主动重绘与系统对话框 owner 都依赖它。
type AttachApp interface {
	Attach(*Window)
}

var (
	appMu    sync.Mutex
	appByWnd = map[uintptr]*Window{}

	wndProcKeep win32.WndProcFunc
	registerMu  sync.Mutex
	classReady  bool
)

// Window 封装一个原生窗口 + 自绘表面。
type Window struct {
	hwnd   win32.HWND
	canvas *Canvas
	app    App
	scale  float32

	mouseX, mouseY int32
	tracking       bool
	pendingHigh    uint16
	composing      bool // IME 组字中：键盘事件交给输入法，不进应用

	timerOn bool
	title   string
	shown   bool
	busy    bool

	// 自失效区域：只有“我们主动 Invalidate”的区域才能安全地用 canvas
	// 差分脏区贴屏；如果 Windows 因为窗口被遮挡/最小化恢复而补了无效区，
	// ps.RcPaint 会超出这个范围，此时必须整块重贴。
	invMu          sync.Mutex
	selfInvalid    win32.RECT
	hasSelfInvalid bool
	forceFull      bool

	onSnip SnipDone // PrintScreen 框选完成后的回调
	// snipDeferred 表示回调已登记、热键钩子等主循环启动后再装（见 EnableSnip）。
	snipDeferred bool

	hicon       win32.HICON // 16px 图标句柄，托盘复用
	trayOn      bool
	onTrayClick func()

	// panicCount 连续 panic 次数；窗口回调里未捕获的 panic 会直接杀死进程
	// （Windows GUI 没有控制台），这里兜住，崩三次就干净退出。
	panicCount int
}

// RunWindow 创建窗口并进入消息循环（阻塞，必须在主 goroutine 调用）。
func RunWindow(title string, width, height int32, app App) int {
	// Win32 窗口、消息队列、窗口过程回调都属于创建它的 OS 线程。
	// Go 的 goroutine 默认可以在 OS 线程之间迁移：一旦主 goroutine 在
	// CreateWindow 之后被调度到另一条线程，GetMessage 等的就是另一条线程的
	// 队列，ShowWindow/SetWindowPos 会卡在 user32 里等一个永远不处理的
	// 发送消息。这类故障表现为随机启动卡死/窗口不显示，极难复现。
	// 把窗口线程锁死在当前 OS 线程，是 Win32 GUI 的硬性前提。
	runtime.LockOSThread()
	win32.InitDPI()
	inst := win32.ModuleHandle()

	w := &Window{app: app, title: title}

	win32.RegisterClassEnsure(inst, func(h win32.HWND, msg uint32, wp, lp uintptr) uintptr {
		appMu.Lock()
		win := appByWnd[uintptr(h)]
		appMu.Unlock()
		if win == nil {
			return win32.DefWindowProc(h, msg, wp, lp)
		}
		return win.proc(h, msg, wp, lp)
	})

	style := uint32(win32.WS_OVERLAPPEDWINDOW | win32.WS_CLIPCHILDREN)
	hwnd := win32.CreateWindow(0, win32.ClassName, title, style,
		0, 0, width, height, 0, 0, win32.HINSTANCE(inst), 0)
	if hwnd == 0 {
		win32.MsgBox(0, "窗口创建失败。", win32.AppNameForError, win32.MB_ICONERROR)
		return 1
	}
	core.Logf("[trace] CreateWindow done hwnd=0x%x", uintptr(hwnd))
	w.hwnd = hwnd
	w.hicon = applyIcon(hwnd)
	win32.EnableDropFiles(hwnd)
	if !win32.WTSRegisterSessionNotification(hwnd) {
		core.Logf("WTSRegisterSessionNotification 失败：锁屏/解锁/RDP 事件收不到，会话落盘改由电源消息兜底")
	}
	// 把窗口回交给应用：托盘/通知/主动重绘/对话框 owner 都靠 a.w。
	// 必须在 ShowWindow 之前完成，否则首帧里触发的重绘与托盘初始化拿不到窗口。
	if aa, ok := app.(AttachApp); ok {
		aa.Attach(w)
	}

	// 居中到鼠标所在显示器的工作区；Win7 多显示器下不要再固定用主屏
	// SM_CXFULLSCREEN，否则副屏启动会跑到主屏或屏幕外。
	var rc win32.RECT
	rc.Right = width
	rc.Bottom = height
	win32.AdjustWindowRect(&rc, style, false)
	ww, wh := rc.Width(), rc.Height()
	work, ok := win32.RECT{}, false
	if cx, cy := win32.GetCursorPos(); true {
		work, ok = win32.MonitorWorkAreaAt(cx, cy)
	}
	if !ok {
		sw := win32.GetSystemMetrics(win32.SM_CXFULLSCREEN)
		sh := win32.GetSystemMetrics(win32.SM_CYFULLSCREEN)
		work = win32.RECT{Left: 0, Top: 0, Right: sw, Bottom: sh}
	}
	if ww > work.Width()-20 {
		ww = work.Width() - 40
	}
	if wh > work.Height()-30 {
		wh = work.Height() - 60
	}
	if ww < 320 {
		ww = 320
	}
	if wh < 240 {
		wh = 240
	}
	x := work.Left + (work.Width()-ww)/2
	y := work.Top + (work.Height()-wh)/2
	win32.SetWindowPos(hwnd, 0, x, y, ww, wh, win32.SWP_FRAMECHANGED)
	core.Logf("[trace] SetWindowPos done")

	// 计算 DPI
	w.scale = w.queryScale()

	w.canvas = NewCanvas(hwnd, w.scale)
	core.Logf("[trace] NewCanvas done")
	cr := win32.ClientRect(hwnd)
	w.canvas.Resize(cr.Width(), cr.Height())
	core.Logf("[trace] canvas.Resize done w=%d h=%d", cr.Width(), cr.Height())

	appMu.Lock()
	appByWnd[uintptr(hwnd)] = w
	appMu.Unlock()

	// 显示窗口放到消息循环的第一个节拍里做（见下方 nextTick 分支）。
	// 启动测试中，在消息循环尚未开始时就 ShowWindow/SetWindowPos，偶尔会
	// 与上一个实例的窗口销毁/外壳清理撞车，卡在 user32 内部数十秒。先让
	// 消息循环跑起来，再显示，路径更接近普通 Win32 程序的 WM_CREATE→显示。
	core.Logf("[trace] window created, showing")
	// 线程已 LockOSThread；ShowWindow/SetWindowPos 的发送消息会由同一条
	// 线程稍后的消息循环处理，不会再出现"卡在 user32 等窗口线程"的死锁。
	win32.SetWindowPos(hwnd, 0, 0, 0, 0, 0,
		win32.SWP_SHOWWINDOW|win32.SWP_NOMOVE|win32.SWP_NOSIZE|win32.SWP_NOZORDER|win32.SWP_NOACTIVATE)
	core.Logf("[trace] window shown")
	if !win32.IsWindowVisible(hwnd) {
		// 极端情况下同步显示没有生效（外壳/安全软件），交给消息循环里的
		// 第一次节拍再补一次，不在这里死等。
		core.Logf("!! 窗口同步显示未生效，将在首个节拍重试")
	}
	// 消息循环：有消息就立即处理；空闲时用 MsgWaitForMultipleObjectsEx
	// 等消息或下一节拍，不再 PeekMessage + Sleep(8ms) 每秒空转 100 次。
	// 不依赖 SetTimer：启动测试里它曾“返回成功但 WM_TIMER 永不到达”。
	// 动画/流式保持 33fps；完全静止降到 10fps，进一步省 CPU/电池。
	var msg win32.MSG
	nextTick := time.Now()
	quit := false
	for !quit {
		gotMsg := false
		for win32.PeekMessage(&msg, true) {
			gotMsg = true
			if msg.Message == win32.WM_QUIT {
				quit = true
				break
			}
			win32.TranslateMessage(&msg)
			win32.DispatchMessage(&msg)
		}
		if quit {
			break
		}
		now := time.Now()
		if gotMsg {
			// 输入/窗口消息可能刚开始动画；把下一拍提前到 30ms 内，
			// 但不要因为持续消息流把节拍永久提前。
			if sooner := now.Add(30 * time.Millisecond); sooner.Before(nextTick) {
				nextTick = sooner
			}
		}
		if now.Before(nextTick) {
			wait := nextTick.Sub(now)
			if wait > 250*time.Millisecond {
				wait = 250 * time.Millisecond
			}
			win32.WaitMessageTimeout(wait)
			continue
		}
		if !w.shown {
			w.shown = true
			if !win32.IsWindowVisible(hwnd) {
				win32.SetWindowPos(hwnd, 0, 0, 0, 0, 0,
					win32.SWP_SHOWWINDOW|win32.SWP_NOMOVE|win32.SWP_NOSIZE|win32.SWP_NOZORDER|win32.SWP_NOACTIVATE)
			}
			win32.SetForeground(hwnd)
			win32.UpdateWindow(hwnd)
		}
		if w.snipDeferred {
			w.snipDeferred = false
			if w.onSnip != nil {
				win32.InstallSnipHotkey(w.hwnd, snipMsg)
			}
		}
		need := w.app.Timer()
		if need {
			w.Invalidate()
		}
		// 长任务运行且接电时阻止系统自动睡眠；电池供电时不硬撑，
		// 由 SuspendApp 在挂起前落盘。状态只在变化时调一次 syscall。
		if ba, ok := w.app.(BusyApp); ok {
			busy := ba.Busy()
			if busy != w.busy {
				w.busy = busy
				win32.KeepAwake(busy && win32.OnACPower())
			}
		}
		interval := 100 * time.Millisecond
		if need {
			interval = 30 * time.Millisecond
		}
		nextTick = time.Now().Add(interval)
	}

	appMu.Lock()
	delete(appByWnd, uintptr(hwnd))
	appMu.Unlock()
	if w.canvas != nil {
		w.canvas.Dispose()
	}
	return int(msg.WParam)
}

// ScaleApp 是希望感知 DPI 变化的应用接口。
// 窗口只负责把新的缩放比换算好，具体布局由应用自己重排。
type ScaleApp interface {
	ScaleChanged(scale float32)
}

// queryScale 读取当前窗口所在显示器的 DPI 缩放（96dpi = 1.0）。
// 这一路 GetDeviceCaps 在所有 Windows 版本上都可用，是 Win7 的兜底路径。
func (w *Window) queryScale() float32 {
	// 排障/验收开关：WIN7H_FORCE_DPI=1.25 / 1.5 可在不修改系统缩放的情况下
	// 复现 125%/150% 布局，用于截图与自动化回归。只在显式设置时生效。
	if v := strings.TrimSpace(os.Getenv("WIN7H_FORCE_DPI")); v != "" {
		if f, err := strconv.ParseFloat(v, 32); err == nil && f >= 1 && f <= 3 {
			return float32(f)
		}
	}
	h := w.hwnd
	var dc win32.HDC
	if h != 0 {
		dc = win32.GetDC(h)
	}
	if dc == 0 {
		dc = win32.GetDC(0)
	}
	if dc == 0 {
		return 1.0
	}
	scale := float32(win32.GetDeviceCaps(dc, 88)) / 96.0 // LOGPIXELSX
	win32.ReleaseDC(h, dc)
	if scale < 1.0 {
		scale = 1.0
	}
	if scale > 3.0 { // 极端值多半是查询失败，夹一下避免布局炸掉
		scale = 3.0
	}
	return scale
}

// rescale 在系统 DPI/色深变化后刷新画布与布局。
func (w *Window) rescale() {
	s := w.queryScale()
	changed := s != w.scale
	if changed {
		w.scale = s
		if w.canvas != nil {
			w.canvas.SetScale(s)
		}
	}
	// 色深可能随远程桌面会话切换而变，每帧会读 canvas.bpp，
	// 这里重建一次画布，让新的色深与缩放同时生效。
	if w.canvas != nil {
		w.canvas.Resize(w.canvas.w, w.canvas.h)
	}
	if changed {
		if sa, ok := w.app.(ScaleApp); ok {
			sa.ScaleChanged(s)
		}
	}
	w.Invalidate()
}

// Hwnd 返回窗口句柄。
func (w *Window) Hwnd() win32.HWND { return w.hwnd }

// Invalidate 请求重绘整个客户区。
func (w *Window) Invalidate() {
	if w == nil || w.hwnd == 0 {
		return
	}
	r := win32.ClientRect(w.hwnd)
	w.markSelfInvalid(r)
	win32.Invalidate(w.hwnd)
}

// InvalidateLogicalRect 只请求重绘一块逻辑像素区域（会按 DPI 换算成物理像素）。
// 典型用途：toast、输入框光标等局部变化。
func (w *Window) InvalidateLogicalRect(x, y, cw, ch int32) {
	if w == nil || w.hwnd == 0 {
		return
	}
	if cw <= 0 || ch <= 0 {
		w.Invalidate()
		return
	}
	r := win32.RECT{
		Left:   w.px(x),
		Top:    w.px(y),
		Right:  w.px(x + cw),
		Bottom: w.px(y + ch),
	}
	if r.Right <= r.Left || r.Bottom <= r.Top {
		w.Invalidate()
		return
	}
	w.markSelfInvalid(r)
	win32.InvalidateRect(w.hwnd, &r, false)
}

func (w *Window) markSelfInvalid(r win32.RECT) {
	if r.Width() <= 0 || r.Height() <= 0 {
		return
	}
	w.invMu.Lock()
	if !w.hasSelfInvalid {
		w.selfInvalid = r
		w.hasSelfInvalid = true
	} else {
		w.selfInvalid = unionRect(w.selfInvalid, r)
	}
	w.invMu.Unlock()
}

func (w *Window) takeSelfInvalid() (win32.RECT, bool) {
	w.invMu.Lock()
	defer w.invMu.Unlock()
	r := w.selfInvalid
	ok := w.hasSelfInvalid
	w.selfInvalid = win32.RECT{}
	w.hasSelfInvalid = false
	return r, ok
}

func rectContains(outer, inner win32.RECT) bool {
	if inner.Width() <= 0 || inner.Height() <= 0 {
		return true
	}
	if outer.Width() <= 0 || outer.Height() <= 0 {
		return false
	}
	return inner.Left >= outer.Left && inner.Top >= outer.Top &&
		inner.Right <= outer.Right && inner.Bottom <= outer.Bottom
}

func unionRect(a, b win32.RECT) win32.RECT {
	if a.Width() <= 0 || a.Height() <= 0 {
		return b
	}
	if b.Width() <= 0 || b.Height() <= 0 {
		return a
	}
	if a.Left > b.Left {
		a.Left = b.Left
	}
	if a.Top > b.Top {
		a.Top = b.Top
	}
	if a.Right < b.Right {
		a.Right = b.Right
	}
	if a.Bottom < b.Bottom {
		a.Bottom = b.Bottom
	}
	return a
}

// Canvas 返回当前绘制表面（DPI 变化后重新取值用）。
func (w *Window) Canvas() *Canvas { return w.canvas }

// Scale 返回 DPI 缩放。
func (w *Window) Scale() float32 { return w.scale }

// SetTitle 修改标题。
func (w *Window) SetTitle(t string) {
	w.title = t
	win32.SetWindowText(w.hwnd, t)
}

// Quit 退出消息循环。
func (w *Window) Quit() { win32.PostQuitMessage(0) }

func (w *Window) proc(h win32.HWND, msg uint32, wp, lp uintptr) (ret uintptr) {
	// 窗口回调是 Windows→Go 的入口：任何未捕获的 panic 都会穿过 cgo 边界
	// 直接杀死进程，而且 GUI 子系统没有控制台——main() 的 recover 兜不住，
	// 用户看到的就是"莫名其妙闪退"，连崩溃报告都不会留。
	// 这里兜住：记日志 + 落崩溃报告，然后放行给 Windows 默认处理；
	// 连续崩 3 次就干净退出（并标记退出码），避免无限 panic 循环。
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			core.Logf("PANIC in window proc msg=%d wp=%d lp=%d: %v\n%s", msg, wp, lp, r, stack)
			_, _ = core.WriteCrashReport("wndproc-panic",
				fmt.Sprintf("msg=%d wp=%d lp=%d\npanic: %v\n\n%s", msg, wp, lp, r, stack))
			w.panicCount++
			if w.panicCount > 3 {
				core.MarkCleanExit(2)
				os.Exit(2)
			}
			ret = win32.DefWindowProc(h, msg, wp, lp)
		}
	}()

	switch msg {
	case win32.WM_ERASEBKGND:
		return 1

	case win32.WM_SIZE:
		w.forceFull = true
		cw, chh := int32(lp&0xFFFF), int32((lp>>16)&0xFFFF)
		if w.canvas != nil {
			w.canvas.Resize(cw, chh)
		}
		w.app.Resize(w.canvas.W(), w.canvas.H())
		w.Invalidate()
		return 0

	case win32.WM_ACTIVATE:
		// 从最小化/遮挡恢复时 Windows 可能补一块外部无效区；强制整块重贴。
		w.forceFull = true
		return 0

	case win32.WM_SHOWWINDOW:
		w.forceFull = true
		return 0

	case win32.WM_PAINT:
		dc, ps := win32.BeginPaint(h)
		_ = dc
		if w.canvas != nil {
			if ps.RcPaint.Width() <= 0 || ps.RcPaint.Height() <= 0 {
				w.takeSelfInvalid()
				win32.EndPaint(h, &ps)
				return 0
			}
			w.app.Paint(w.canvas)
			dirty, vector := w.canvas.PrepareFrame()
			flush := ps.RcPaint

			self, hasSelf := w.takeSelfInvalid()
			if w.forceFull {
				// 窗口刚显示/尺寸变化/从遮挡恢复：不能信差分脏区，整块重贴。
				w.forceFull = false
			} else if hasSelf && rectContains(self, ps.RcPaint) {
				// 这次 WM_PAINT 只来自我们自己的 Invalidate；可以用 canvas
				// 算出的真实变化区域，避免全窗口 BitBlt。
				if vector {
					if dirty.Width() <= 0 || dirty.Height() <= 0 {
						flush = win32.RECT{}
					} else if rectContains(ps.RcPaint, dirty) {
						flush = dirty
					} else {
						// 差分区域超出 Windows 无效区（布局/浮层变化）：取并集，
						// 宁可多贴一块，也不能把新内容漏在屏幕外。
						flush = unionRect(ps.RcPaint, dirty)
					}
				}
			}
			if flush.Width() > 0 && flush.Height() > 0 {
				w.canvas.FlushPrepared(flush)
			}
		}
		win32.EndPaint(h, &ps)
		return 0

	case win32.WM_DROPFILES:
		paths := win32.ExtractDroppedFiles(wp)
		if len(paths) > 0 {
			if d, ok := w.app.(DropFilesApp); ok {
				d.DropFiles(paths)
				w.Invalidate()
			}
		}
		return 0

	case snipMsg:
		// PrintScreen 钩子投递过来的：就地开遮罩，截完再回调应用。
		if w.onSnip != nil {
			if dib, sw, sh, ok := SnipInteractive(h); ok {
				w.onSnip(dib, sw, sh)
			}
			w.Invalidate()
		}
		return 0

	case win32.WM_GETOBJECT:
		// 屏幕阅读器/辅助工具请求可访问对象。若应用实现了 AccessibleApp，
		// 返回虚拟 IAccessible 树（对话正文/输入框/按钮）；否则退回窗口级
		// 标准对象，至少保留标题/角色。
		if int32(lp) == -4 { // OBJID_CLIENT
			if aa, ok := w.app.(AccessibleApp); ok {
				if r, ok := lresultForApp(h, wp, aa); ok {
					return r
				}
			}
		}
		if r, ok := win32.StdAccessibleLresult(uintptr(h), wp, lp); ok {
			return r
		}
		return win32.DefWindowProc(h, msg, wp, lp)

	case win32.WM_SETTINGCHANGE, win32.WM_THEMECHANGED, win32.WM_DISPLAYCHANGE:
		// Win7 没有 WM_DPICHANGED（那是 8.1 才有的每显示器 DPI 通知）：
		// 改缩放比例、切换显示器、调分辨率都只发这些消息。
		// 所以这里主动重探一次 DPI，变了就重建换算与字体，做到"不改代码也能跟上系统缩放"。
		w.forceFull = true
		w.rescale()
		if msg == win32.WM_DISPLAYCHANGE || msg == win32.WM_SETTINGCHANGE {
			if win32.ClampWindowToWorkArea(h) {
				w.forceFull = true
			}
		}
		if msg == win32.WM_SETTINGCHANGE && lp != 0 {
			if strings.EqualFold(win32.UTF16PtrToString(lp, 128), "WininetSettings") {
				if sa, ok := w.app.(SettingsApp); ok {
					sa.SettingsChanged()
				}
			}
		}
		if ta, ok := w.app.(ThemeApp); ok {
			ta.RefreshTheme()
		}
		return 0

	case win32.WM_TIMER:
		// 节拍由线程定时器在消息循环里直接处理（见 RunWindow），
		// 窗口定时器分支保留给系统/外部代码，不在这里重复推进。
		return 0

	case win32.WM_MOUSEMOVE:
		x, y := int32(int16(lp&0xFFFF)), int32(int16((lp>>16)&0xFFFF))
		if !w.tracking {
			w.tracking = true
			win32.TrackMouseLeave(h)
		}
		if x != w.mouseX || y != w.mouseY {
			w.mouseX, w.mouseY = x, y
			w.app.MouseMove(w.lx(x), w.ly(y))
			w.Invalidate()
		}
		return 0

	case win32.WM_MOUSELEAVE:
		w.tracking = false
		w.mouseX, w.mouseY = -1000, -1000
		w.app.MouseMove(-1000, -1000)
		w.Invalidate()
		return 0

	case win32.WM_LBUTTONDOWN:
		x, y := int32(int16(lp&0xFFFF)), int32(int16((lp>>16)&0xFFFF))
		win32.SetFocus(h)
		win32.SetCapture(h)
		w.app.MouseDown(w.lx(x), w.ly(y), 0)
		w.Invalidate()
		return 0

	case win32.WM_LBUTTONUP:
		x, y := int32(int16(lp&0xFFFF)), int32(int16((lp>>16)&0xFFFF))
		win32.ReleaseCapture()
		w.app.MouseUp(w.lx(x), w.ly(y), 0)
		w.Invalidate()
		return 0

	case win32.WM_LBUTTONDBLCLK:
		x, y := int32(int16(lp&0xFFFF)), int32(int16((lp>>16)&0xFFFF))
		w.app.DoubleClick(w.lx(x), w.ly(y))
		w.Invalidate()
		return 0

	case win32.WM_RBUTTONDOWN:
		x, y := int32(int16(lp&0xFFFF)), int32(int16((lp>>16)&0xFFFF))
		w.app.MouseDown(w.lx(x), w.ly(y), 1)
		w.Invalidate()
		return 0

	case win32.WM_RBUTTONUP:
		x, y := int32(int16(lp&0xFFFF)), int32(int16((lp>>16)&0xFFFF))
		w.app.MouseUp(w.lx(x), w.ly(y), 1)
		w.Invalidate()
		return 0

	case win32.WM_MOUSEWHEEL:
		// 原样把系统滚轮增量交给应用：高精度滚轮/触控板会发送小于 120 的
		// 增量，若在这里先除 120 再取整会被抹成 0，表现为"滚轮完全没反应"。
		// 节流与整格换算统一由 App 的 wheelNotches 处理。
		delta := int32(int16((wp >> 16) & 0xFFFF))
		pt := win32.POINT{X: int32(int16(lp & 0xFFFF)), Y: int32(int16((lp >> 16) & 0xFFFF))}
		win32.ScreenToClientPoint(h, &pt)
		w.app.MouseWheel(w.lx(pt.X), w.ly(pt.Y), delta)
		w.Invalidate()
		return 0

	case win32.WM_KEYDOWN, win32.WM_SYSKEYDOWN:
		// IME 组字中：键盘事件归输入法（Enter 上屏、退格删拼音、数字选词…），
		// 不能让应用把 Enter 当成“发送”。
		if w.composing {
			return win32.DefWindowProc(h, msg, wp, lp)
		}
		if w.app.KeyDown(int32(wp)) {
			// 按键被消费后立即请求重绘：非字符键（Backspace/Delete/方向键/Tab/Enter）
			// 没有 WM_CHAR 兜底，过去要等 250ms 空闲节拍才上屏（"打字快、删字慢"）。
			w.Invalidate()
			return 0
		}
		return win32.DefWindowProc(h, msg, wp, lp)

	case win32.WM_CHAR:
		// 同上：组字期间的字符事件归输入法（committed 文本会在 END 之后以 WM_CHAR 到达）
		if w.composing {
			return win32.DefWindowProc(h, msg, wp, lp)
		}
		ch := uint16(wp)
		if ch >= 0xD800 && ch <= 0xDBFF {
			w.pendingHigh = ch
			return 0
		}
		if ch >= 0xDC00 && ch <= 0xDFFF && w.pendingHigh != 0 {
			r := utf16.DecodeRune(rune(w.pendingHigh), rune(ch))
			w.pendingHigh = 0
			w.app.Char(r)
			w.Invalidate()
			return 0
		}
		w.pendingHigh = 0
		if ch < 32 || ch == 127 {
			return 0
		}
		w.app.Char(rune(ch))
		w.Invalidate()
		return 0

	case win32.WM_IME_STARTCOMPOSITION, win32.WM_IME_COMPOSITION, 0x010E: // 0x010E = WM_IME_ENDCOMPOSITION
		// 自绘输入框没有子编辑控件：每次组字都把组字窗与候选窗挪到当前光标处。
		// 收尾时也刷新一次，避免窗口残留。
		// 进入/退出组字态：进入后键盘事件全部交给 IME（见 WM_KEYDOWN/WM_CHAR）。
		switch msg {
		case win32.WM_IME_STARTCOMPOSITION:
			w.composing = true
		case 0x010E:
			w.composing = false
		}
		if ca, ok := w.app.(CaretApp); ok {
			if cx, cy, valid := ca.CaretPos(); valid {
				px, py := w.px(cx), w.px(cy)
				win32.SetCompositionPos(h, px, py)
				win32.SetCandidatePos(h, px, py)
			}
		}
		return win32.DefWindowProc(h, msg, wp, lp)

	case win32.WM_QUERYENDSESSION:
		// 只做快速落盘，不停止队列：用户可能取消关机。
		// 返回 TRUE 表示允许系统继续注销/关机。
		if se, ok := w.app.(SessionEndingApp); ok {
			se.PrepareForSessionEnd()
		}
		return 1

	case win32.WM_ENDSESSION:
		// wParam!=0 表示系统真的要在几秒内结束进程，此时才停止队列/子进程。
		if wp != 0 {
			if sa, ok := w.app.(ShutdownApp); ok {
				sa.Shutdown()
			}
			win32.KeepAwake(false)
		}
		return 0

	case win32.WM_POWERBROADCAST:
		switch wp {
		case win32.PBT_APMSUSPEND:
			if sa, ok := w.app.(SuspendApp); ok {
				sa.Suspend()
			}
			win32.KeepAwake(false)
		case win32.PBT_APMRESUMESUSPEND, win32.PBT_APMRESUMEAUTOMATIC:
			if ra, ok := w.app.(ResumeApp); ok {
				ra.Resume()
			}
			w.Invalidate()
		}
		return 1

	case win32.WM_WTSSESSION_CHANGE:
		if sc, ok := w.app.(SessionChangeApp); ok {
			sc.SessionChange(uint32(wp))
		}
		return 0

	case win32.WM_SETCURSOR:
		switch w.app.Cursor() {
		case 1:
			win32.SetCursor(win32.LoadCursor(win32.IDC_IBEAM))
		case 2:
			win32.SetCursor(win32.LoadCursor(win32.IDC_HAND))
		case 3:
			win32.SetCursor(win32.LoadCursor(win32.IDC_SIZEWE))
		default:
			win32.SetCursor(win32.LoadCursor(win32.IDC_ARROW))
		}
		return 1

	case win32.WM_GETMINMAXINFO:
		// 最小窗口 960×600（DESIGN 第 8 节）。再小主区只能被挤压，
		// 换行与滚动全乱；与其在布局层硬扛，不如从系统层面拦住拖拽。
		// 高 DPI/小屏上按物理像素算出的最小值可能大于屏幕，先夹到工作区，
		// 否则 150% 缩放的 1366×768 机器会被强制撑出屏幕。
		mmi := (*win32.MINMAXINFO)(unsafe.Pointer(lp))
		minW := w.canvas.Px(minWindowW)
		minH := w.canvas.Px(minWindowH)
		if sw := win32.GetSystemMetrics(win32.SM_CXFULLSCREEN); sw > 0 && minW > sw-40 {
			minW = sw - 40
		}
		if sh := win32.GetSystemMetrics(win32.SM_CYFULLSCREEN); sh > 0 && minH > sh-80 {
			minH = sh - 80
		}
		mmi.PtMinTrackSize.X = minW
		mmi.PtMinTrackSize.Y = minH
		return 0

	case win32.WM_CLOSE:
		if w.app.Close() {
			win32.DestroyWindow(h)
		}
		return 0

	case win32.WM_DESTROY:
		w.removeTray()
		win32.WTSUnRegisterSessionNotification(h)
		win32.KeepAwake(false)
		win32.PostQuitMessage(0)
		return 0

	case win32.TrayMsg:
		w.trayEvent(lp)
		return 0
	}
	return win32.DefWindowProc(h, msg, wp, lp)
}

// px 逻辑像素转物理像素。
func (w *Window) px(v int32) int32 {
	if w.scale <= 0 {
		return v
	}
	return int32(math.Round(float64(v) * float64(w.scale)))
}

// lx / ly 物理像素转逻辑像素。
func (w *Window) lx(x int32) int32 {
	if w.scale <= 0 {
		return x
	}
	return int32(math.Round(float64(x) / float64(w.scale)))
}

func (w *Window) ly(y int32) int32 {
	if w.scale <= 0 {
		return y
	}
	return int32(math.Round(float64(y) / float64(w.scale)))
}

// 最小窗口尺寸，与 DESIGN.md 第 8 节一致；也与 app 层的 minChatW 配套：
// 960 - 56(rail) - 264(侧栏) = 640，仍高于主区下限。
const (
	minWindowW int32 = 960
	minWindowH int32 = 600
)
