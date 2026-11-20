//go:build windows

package win32

import (
	"syscall"
	"unsafe"
)

// 电源/会话生命周期。
//
// Win7 笔记本合盖、RDP 断开、下班注销都很常见；如果窗口不处理这些消息，
// 正在流式输出的会话会丢最后一段，后台进程也可能变成孤儿。
// 这些 API 都是 Vista+ 就有，Win7 可用；仍走 pcall，缺失时只降级不崩。

var (
	dWtsapi32 = syscall.NewLazyDLL("wtsapi32.dll")

	pWTSRegisterSessionNotification   = dWtsapi32.NewProc("WTSRegisterSessionNotification")
	pWTSUnRegisterSessionNotification = dWtsapi32.NewProc("WTSUnRegisterSessionNotification")
	pSetThreadExecutionState          = dKernel32.NewProc("SetThreadExecutionState")
	pGetSystemPowerStatus             = dKernel32.NewProc("GetSystemPowerStatus")
)

const (
	// WTSRegisterSessionNotification flags.
	NOTIFY_FOR_THIS_SESSION = 0

	// WM_WTSSESSION_CHANGE 的 wParam。
	WTS_CONSOLE_CONNECT    = 0x1
	WTS_CONSOLE_DISCONNECT = 0x2
	WTS_REMOTE_CONNECT     = 0x3
	WTS_REMOTE_DISCONNECT  = 0x4
	WTS_SESSION_LOGON      = 0x5
	WTS_SESSION_LOGOFF     = 0x6
	WTS_SESSION_LOCK       = 0x7
	WTS_SESSION_UNLOCK     = 0x8

	// WM_POWERBROADCAST 的 wParam。
	PBT_APMSUSPEND         = 0x0004
	PBT_APMRESUMESUSPEND   = 0x0007
	PBT_APMRESUMEAUTOMATIC = 0x0012

	// SetThreadExecutionState flags.
	ES_SYSTEM_REQUIRED  = 0x00000001
	ES_DISPLAY_REQUIRED = 0x00000002
	ES_CONTINUOUS       = 0x80000000
)

type systemPowerStatus struct {
	ACLineStatus        byte
	BatteryFlag         byte
	BatteryLifePercent  byte
	Reserved1           byte
	BatteryLifeTime     uint32
	BatteryFullLifeTime uint32
}

// WTSRegisterSessionNotification 注册锁屏/解锁/RDP 连接/断开通知。
func WTSRegisterSessionNotification(h HWND) bool {
	r, _, _ := pcall(pWTSRegisterSessionNotification, uintptr(h), NOTIFY_FOR_THIS_SESSION)
	return r != 0
}

// WTSUnRegisterSessionNotification 在窗口销毁前注销，避免消息投递到已释放的 HWND。
func WTSUnRegisterSessionNotification(h HWND) {
	_, _, _ = pcall(pWTSUnRegisterSessionNotification, uintptr(h))
}

// KeepAwake 在长任务运行期间阻止系统自动睡眠；on=false 恢复默认电源策略。
// 只阻止睡眠，不强制亮屏（ES_DISPLAY_REQUIRED 不设），避免笔记本屏幕常亮。
func KeepAwake(on bool) {
	flags := uint32(ES_CONTINUOUS)
	if on {
		flags |= ES_SYSTEM_REQUIRED
	}
	_, _, _ = pcall(pSetThreadExecutionState, uintptr(flags))
}

// OnACPower 返回当前是否接交流电。探测失败时按“接电”处理，
// 宁可让长任务跑完，也不要因为一次查询失败就允许系统睡眠。
func OnACPower() bool {
	var st systemPowerStatus
	ok, _, _ := pcall(pGetSystemPowerStatus, uintptr(unsafe.Pointer(&st)))
	if ok == 0 {
		return true
	}
	return st.ACLineStatus == 1
}
