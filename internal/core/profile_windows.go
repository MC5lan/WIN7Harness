//go:build windows

package core

import (
	"syscall"
	"unsafe"
)

// memoryStatusEx 结构体与 kernel32 句柄都已在 checks_windows.go 里声明过，
// v1.29 这里不再重复定义（重复声明会让 windows 目标直接编译失败）。

// totalMemoryKB 返回物理内存（KB）。失败返回 false，上层按最保守档处理。
func totalMemoryKB() (uint64, bool) {
	if err := procGlobalMemoryStatusEx.Find(); err != nil {
		return 0, false
	}
	var ms memoryStatusEx
	ms.Length = uint32(unsafe.Sizeof(ms))
	r, _, _ := syscall.Syscall(procGlobalMemoryStatusEx.Addr(), 1,
		uintptr(unsafe.Pointer(&ms)), 0, 0)
	if r == 0 {
		return 0, false
	}
	return ms.TotalPhys / 1024, true
}
