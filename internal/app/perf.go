//go:build windows

package app

import (
	"fmt"
	"os"
	"runtime/pprof"
	"strings"
	"sync/atomic"
	"time"

	"win7harness/internal/core"
)

// UI 卡顿观测与诊断。
//
// 两类能力：
//  1. 看门狗：窗口消息循环里每帧心跳；若界面线程超过 stallLimit 没有推进，
//     就转储全部 goroutine 栈到 crash/ui-stall-*.txt。Win7 上没有任务管理器
//     能直接看 Go 栈，"界面没反应"过去只能靠猜；有了这份栈，能直接看到是
//     SQLite、杀软、网络还是某个锁把 UI 卡住了。
//  2. 帧成本探针：WIN7H_PROFILE=1 时按页统计 Paint/Timer/滚轮耗时，
//     每 5 秒写一行日志。默认关闭，不影响正常使用。
type perfState struct {
	on bool

	beat      atomic.Int64 // 最近一次 UI 心跳（UnixNano）
	lastDump  atomic.Int64 // 上次卡顿转储时间（UnixNano）
	closing   atomic.Bool
	started   atomic.Bool
	stallSeen atomic.Int64
	// nativeDialog > 0 表示 UI 线程正阻塞在系统模态对话框里（选目录/选图/
	// 选证书）。这时没有心跳是正常的，看门狗必须让路，否则每次浏览目录
	// 都会写一份假的 ui-stall 报告。
	nativeDialog atomic.Int32

	wheelAcc float32

	frames    int
	paintSum  time.Duration
	paintMax  time.Duration
	timerSum  time.Duration
	timerMax  time.Duration
	wheelSum  time.Duration
	wheelMax  time.Duration
	wheels    int
	lastFlush time.Time
}

const (
	// stallLimit 超过这个时间没有心跳就认为界面卡住了。启动/大文件扫描
	// 等一次性操作可能短暂超过 1 秒，但 3 秒仍未回到消息循环就值得留证据。
	stallLimit = 3 * time.Second
	// stallDumpGap 两次转储的最小间隔，避免界面卡五分钟写五分钟的栈。
	stallDumpGap = 30 * time.Second
)

var perfProfOn = strings.ToLower(strings.TrimSpace(os.Getenv("WIN7H_PROFILE"))) == "1"

func newPerfState() *perfState {
	p := &perfState{on: perfProfOn, lastFlush: time.Now()}
	p.beat.Store(time.Now().UnixNano())
	return p
}

// beat 由 UI 线程在每个入口调用。只做一次原子写，开销可以忽略。
func (a *App) beat() {
	if a.perf != nil {
		a.perf.beat.Store(time.Now().UnixNano())
	}
}

// startWatchdog 在窗口创建、应用拿到 HWND 后启动。放在 Attach 而不是 New，
// 是为了避免把"创建窗口/初始化字体"的正常耗时误报成界面卡死。
func (a *App) startWatchdog() {
	if a.perf == nil || !a.perf.started.CompareAndSwap(false, true) {
		return
	}
	a.perf.beat.Store(time.Now().UnixNano())
	core.SafeGo("app/perf.go:watchdog", func() {
		tk := time.NewTicker(time.Second)
		defer tk.Stop()
		for range tk.C {
			if a.perf.closing.Load() {
				return
			}
			if a.perf.nativeDialog.Load() > 0 {
				// 用户正在系统对话框里浏览；这不是卡死，保持心跳。
				a.perf.beat.Store(time.Now().UnixNano())
				continue
			}
			last := a.perf.beat.Load()
			if last == 0 {
				continue
			}
			gap := time.Since(time.Unix(0, last))
			if gap < stallLimit {
				continue
			}
			now := time.Now().UnixNano()
			prev := a.perf.lastDump.Load()
			if prev != 0 && now-prev < int64(stallDumpGap) {
				continue
			}
			a.perf.lastDump.Store(now)
			a.perf.stallSeen.Add(1)
			dumpUIStall(gap)
		}
	})
}

func (a *App) beginNativeDialog() {
	if a.perf != nil {
		a.perf.nativeDialog.Add(1)
	}
}

func (a *App) endNativeDialog() {
	if a.perf != nil {
		a.perf.nativeDialog.Add(-1)
	}
}

// dumpUIStall 写一份带 goroutine 栈的卡顿报告。日志里只留摘要，完整栈进文件。
func dumpUIStall(gap time.Duration) {
	var b strings.Builder
	fmt.Fprintf(&b, "UI 线程 %.1f 秒没有心跳\n时间：%s\n\n", gap.Seconds(), time.Now().Format("2006-01-02 15:04:05"))
	if err := pprof.Lookup("goroutine").WriteTo(&b, 1); err != nil {
		fmt.Fprintf(&b, "goroutine 转储失败：%v\n", err)
	}
	core.Logf("ui-stall: UI thread stalled %.1fs, dumping goroutines", gap.Seconds())
	core.Logf("UI 卡顿：界面线程超过 %.1fs 未响应，已转储 goroutine 栈", gap.Seconds())
	if path, err := core.WriteCrashReport("ui-stall", b.String()); err == nil {
		core.Logf("UI 卡顿报告：%s", path)
	} else {
		core.Logf("UI 卡顿报告写入失败：%v", err)
	}
}

// wheelNotches 把 Windows 的原始滚轮增量（一格 120）换算成整格。
// 高精度滚轮/触控板会发 40、80 甚至更小的增量；余数留在 wheelAcc 里，
// 攒够一格再滚。这样既不会"小增量被取整成 0 完全不动"，也不会让
// 老式滚轮一次跳好几格。
func (a *App) wheelNotches(delta int32) int32 {
	if delta == 0 {
		return 0
	}
	if a.perf == nil {
		// 理论上不会发生；兜底按一格 120 算，避免除零/空指针。
		if delta > 0 {
			return 1
		}
		return -1
	}
	a.perf.wheelAcc += float32(delta) / 120
	n := int32(a.perf.wheelAcc) // 向零截断，正负都成立
	a.perf.wheelAcc -= float32(n)
	return n
}

// observePaint / observeTimer / observeWheel 只在探针开启时计时。
func (a *App) observePaint(d time.Duration) {
	p := a.perf
	if p == nil || !p.on {
		return
	}
	p.frames++
	p.paintSum += d
	if d > p.paintMax {
		p.paintMax = d
	}
	if d > 120*time.Millisecond {
		core.Logf("[perf] 慢帧 page=%s paint=%s", a.pageName(), d.Round(time.Millisecond))
	}
	a.flushPerf(time.Now())
}

func (a *App) observeTimer(d time.Duration) {
	p := a.perf
	if p == nil || !p.on {
		return
	}
	p.timerSum += d
	if d > p.timerMax {
		p.timerMax = d
	}
	if d > 80*time.Millisecond {
		core.Logf("[perf] Timer 慢 page=%s dur=%s", a.pageName(), d.Round(time.Millisecond))
	}
}

func (a *App) observeWheel(d time.Duration) {
	p := a.perf
	if p == nil || !p.on {
		return
	}
	p.wheels++
	p.wheelSum += d
	if d > p.wheelMax {
		p.wheelMax = d
	}
	if d > 40*time.Millisecond {
		core.Logf("[perf] 滚轮处理慢 page=%s dur=%s", a.pageName(), d.Round(time.Millisecond))
	}
}

// flushPerf 每 5 秒按页输出一行聚合结果。用固定文案而不是每帧一行，
// 日志在 Win7 机械盘上也不会成为新的卡顿源。
func (a *App) flushPerf(now time.Time) {
	p := a.perf
	if p == nil || !p.on {
		return
	}
	if p.lastFlush.IsZero() {
		p.lastFlush = now
		return
	}
	if now.Sub(p.lastFlush) < 5*time.Second {
		return
	}
	avg := func(sum time.Duration, n int) time.Duration {
		if n <= 0 {
			return 0
		}
		return sum / time.Duration(n)
	}
	wp := time.Duration(0)
	if p.wheels > 0 {
		wp = p.wheelSum / time.Duration(p.wheels)
	}
	core.Logf("[perf] page=%s frames=%d paint(avg=%s max=%s) timer(avg=%s max=%s) wheel(n=%d avg=%s max=%s)",
		a.pageName(), p.frames,
		avg(p.paintSum, p.frames).Round(time.Microsecond), p.paintMax.Round(time.Microsecond),
		avg(p.timerSum, p.frames).Round(time.Microsecond), p.timerMax.Round(time.Microsecond),
		p.wheels, wp.Round(time.Microsecond), p.wheelMax.Round(time.Microsecond))
	p.frames, p.paintSum, p.paintMax = 0, 0, 0
	p.timerSum, p.timerMax = 0, 0
	p.wheels, p.wheelSum, p.wheelMax = 0, 0, 0
	p.lastFlush = now
}

// pageName 给日志一个稳定的页面名，避免打印 int 常量后还要查代码。
func (a *App) pageName() string {
	switch a.page {
	case PageChat:
		return "chat"
	case PageOnboard:
		return "onboard"
	case PageSettings:
		return "settings"
	case PageDiag:
		return "diag"
	case PageCompare:
		return "compare"
	case PageAuto:
		return "auto"
	case PageExt:
		return "ext"
	case PageUsage:
		return "usage"
	case PageSessions:
		return "sessions"
	case PageWorkspace:
		return "workspace"
	case PageCosts:
		return "costs"
	case PageNotices:
		return "notices"
	}
	return "unknown"
}
