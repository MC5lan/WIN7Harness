//go:build windows

package app

import (
	"net/http"
	"net/url"
	"time"

	"win7harness/internal/core"
	"win7harness/internal/llm"
	"win7harness/internal/mcp"
	"win7harness/internal/netbridge"
	"win7harness/internal/win32"
)

// 系统生命周期：注销/关机、睡眠/恢复、锁屏/解锁、RDP 断开/重连。
//
// 设计原则：
//   - WM_QUERYENDSESSION 只快速落盘，用户可能取消关机；
//   - WM_ENDSESSION(wParam!=0) 才停止生成/队列/MCP，系统马上会杀进程；
//   - 睡眠/锁屏前 flush，恢复后重探代理并刷新页面数据；
//   - 长任务运行且接电时才阻止睡眠，电池供电不硬撑。

// SettingsChanged 是 ui.SettingsApp 的实现：系统代理变化时立即重读，
// 不用等下次睡眠恢复或重启。
func (a *App) SettingsChanged() {
	a.applyProxy()
	a.forceFull = true
}

func (a *App) applyProxy() {
	llm.ResetProxyCache()
	if a.eng != nil && a.cfg != nil {
		a.eng.RefreshProxy(a.cfg.ProxyMode, a.cfg.ProxyURL)
	}
	// 运行中从“直连/系统代理”改到需要 NTLM 的手动代理时，
	// 本机桥必须此刻启动；否则聊天仍会直连上游并卡在 407。
	if a.cfg == nil || !llm.SystemProxyConfigured() {
		return
	}
	s, err := netbridge.Ensure(llm.ResolveProxyForURL)
	if err != nil || s == nil {
		if err != nil {
			core.Logf("app: 本机代理桥启动失败：%v", err)
		}
		return
	}
	if a.eng != nil {
		a.eng.RefreshProxy("manual", s.URL)
	}
	if tr, ok := http.DefaultTransport.(*http.Transport); ok {
		if pu, err := url.Parse(s.URL); err == nil {
			tr.Proxy = http.ProxyURL(pu)
		}
	}
}

// Busy 供窗口在长任务期间调用 SetThreadExecutionState。
func (a *App) Busy() bool {
	if a.streaming {
		return true
	}
	return a.eng != nil && a.eng.Running()
}

// PrepareForSessionEnd 在 WM_QUERYENDSESSION 时快速落盘，不停止后台队列。
func (a *App) PrepareForSessionEnd() {
	if a.shutdownDone.Load() {
		return
	}
	a.saveConv()
	if a.saver != nil {
		if !a.saver.flush(2 * time.Second) {
			core.Logf("WM_QUERYENDSESSION：会话队列 2 秒内未完全落盘，继续等待系统结束")
		}
	}
	core.Logf("lifecycle: query-end-session 已快速落盘")
}

// Suspend 在系统挂起/锁屏前落盘。恢复后由 Resume 重探网络。
func (a *App) Suspend() {
	if a.shutdownDone.Load() {
		return
	}
	a.saveConv()
	if a.saver != nil {
		a.saver.flush(2 * time.Second)
	}
	core.Logf("lifecycle: suspend 已落盘")
}

// Resume 在系统恢复/解锁/RDP 重连后刷新代理与页面数据。
func (a *App) Resume() {
	if a.shutdownDone.Load() {
		return
	}
	if a.Busy() {
		// 有请求在飞：现在改 transport.Proxy 会和 http.Transport 的并发读竞争，
		// 记脏，等下一轮发送前再换。
		a.proxyDirty = true
	} else {
		a.applyProxy()
	}
	if a.pdata != nil {
		select {
		case a.pdata.wake <- struct{}{}:
		default:
		}
	}
	a.Redraw()
	core.Logf("lifecycle: resume 代理/网络已重探")
}

// SessionChange 处理 WM_WTSSESSION_CHANGE。
func (a *App) SessionChange(event uint32) {
	switch event {
	case win32.WTS_SESSION_LOCK,
		win32.WTS_REMOTE_DISCONNECT,
		win32.WTS_CONSOLE_DISCONNECT:
		a.Suspend()
	case win32.WTS_SESSION_UNLOCK,
		win32.WTS_REMOTE_CONNECT,
		win32.WTS_CONSOLE_CONNECT:
		a.Resume()
	}
}

// Shutdown 在 WM_ENDSESSION 时做最终收尾。只执行一次。
func (a *App) Shutdown() {
	if a.shutdownDone.Swap(true) {
		return
	}
	if a.eng != nil {
		a.eng.Stop()
	}
	a.cancelPendingConfirms()
	a.saveConv()
	if a.saver != nil {
		if !a.saver.stop(2 * time.Second) {
			core.Logf("WM_ENDSESSION：会话队列 2 秒内未写完，系统将终止进程")
		}
	}
	if a.cfg != nil && !core.ConfigReadOnly() {
		_ = a.cfg.Save()
	}
	mcp.Shutdown()
	core.MarkCleanExit(0)
	core.Logf("lifecycle: end-session 已停止生成并落盘")
}
