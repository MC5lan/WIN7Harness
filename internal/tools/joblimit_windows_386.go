//go:build windows && 386

package tools

// Windows 的 JOBOBJECT_EXTENDED_LIMIT_INFORMATION 在 32 位上要求
// IO_COUNTERS 按 8 字节对齐：BasicLimitInformation 占 44 字节，
// 后面必须补 4 字节，总大小才是 112。Go 在 386 上把 uint64 按 4 字节
// 对齐，直接拼会得到 108，SetInformationJobObject 返回 ERROR_BAD_LENGTH。
type jobExtendedLimit struct {
	BasicLimitInformation jobBasicLimit
	_                     [4]byte
	IoInfo                ioCounters
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}
