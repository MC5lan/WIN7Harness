//go:build windows

package win32

import (
	"syscall"
	"unsafe"
)

// UTF16PtrToString 读取以 NUL 结尾的 UTF-16 字符串，最多 max 个字符。
// 用于 WM_SETTINGCHANGE 的 lParam（如 "WininetSettings"）。
// 指针为 0 或 max<=0 时返回空串；不负责校验任意指针是否可读。
func UTF16PtrToString(p uintptr, max int) string {
	if p == 0 || max <= 0 {
		return ""
	}
	buf := make([]uint16, 0, max)
	for i := 0; i < max; i++ {
		v := *(*uint16)(unsafe.Pointer(p + uintptr(i)*unsafe.Sizeof(uint16(0))))
		if v == 0 {
			break
		}
		buf = append(buf, v)
	}
	return syscall.UTF16ToString(buf)
}
