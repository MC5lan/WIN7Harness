//go:build windows && amd64

package tools

// 64 位下 Go 的字段对齐与 Win32 头文件一致，无需额外填充。
type jobExtendedLimit struct {
	BasicLimitInformation jobBasicLimit
	IoInfo                ioCounters
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}
