//go:build windows

package win32

import (
	"syscall"
	"unsafe"
)

// 极简注册表读取：只读，不写、不建键。用于读系统代理与 IE 设置。
var (
	dAdvapi32 = syscall.NewLazyDLL("advapi32.dll")

	pRegOpenKeyExW             = dAdvapi32.NewProc("RegOpenKeyExW")
	pRegQueryValueExW          = dAdvapi32.NewProc("RegQueryValueExW")
	pRegCloseKey               = dAdvapi32.NewProc("RegCloseKey")
	pExpandEnvironmentStringsW = dKernel32.NewProc("ExpandEnvironmentStringsW")
)

const (
	HKEY_CURRENT_USER  = 0x80000001
	HKEY_LOCAL_MACHINE = 0x80000002

	KEY_READ = 0x20019

	REG_SZ        = 1
	REG_EXPAND_SZ = 2
	REG_DWORD     = 4
)

// RegGetString 读一个字符串值。第二个返回值表示是否存在。
// 先查大小再读：超过 512 字符的值不再静默失败；
// REG_EXPAND_SZ 展开环境变量（企业代理常写 %USERPROFILE% 之类）。
func RegGetString(root uintptr, subkey, name string) (string, bool) {
	h, ok := regOpen(root, subkey)
	if !ok {
		return "", false
	}
	defer pcall(pRegCloseKey, h)

	namep, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return "", false
	}
	// 先查类型与字节大小
	var typ uint32
	var size uint32
	r, _, _ := pcall(pRegQueryValueExW, h, uintptr(unsafe.Pointer(namep)), 0,
		uintptr(unsafe.Pointer(&typ)), 0, uintptr(unsafe.Pointer(&size)))
	if typ != REG_SZ && typ != REG_EXPAND_SZ {
		return "", false
	}
	if r != 0 {
		return "", false
	}
	if size == 0 {
		// 空字符串是合法值（存在但为空）
		return "", true
	}
	buf := make([]uint16, int(size)/2+2)
	r, _, _ = pcall(pRegQueryValueExW, h, uintptr(unsafe.Pointer(namep)), 0,
		uintptr(unsafe.Pointer(&typ)), uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if r != 0 {
		return "", false
	}
	n := int(size) / 2
	for n > 0 && buf[n-1] == 0 {
		n--
	}
	s := syscall.UTF16ToString(buf[:n])
	if typ == REG_EXPAND_SZ {
		if expanded := expandEnv(s); expanded != "" {
			s = expanded
		}
	}
	return s, true
}

// expandEnv 展开 %VAR% 形式的环境变量；失败返回原串。
func expandEnv(s string) string {
	src, err := syscall.UTF16PtrFromString(s)
	if err != nil {
		return ""
	}
	needed, _, _ := pcall(pExpandEnvironmentStringsW, uintptr(unsafe.Pointer(src)), 0, 0)
	if needed == 0 {
		return s
	}
	dst := make([]uint16, needed)
	r, _, _ := pcall(pExpandEnvironmentStringsW, uintptr(unsafe.Pointer(src)),
		uintptr(unsafe.Pointer(&dst[0])), needed)
	if r == 0 {
		return s
	}
	n := int(needed)
	for n > 0 && dst[n-1] == 0 {
		n--
	}
	return syscall.UTF16ToString(dst[:n])
}

// RegGetDWORD 读一个 32 位整数值。
func RegGetDWORD(root uintptr, subkey, name string) (uint32, bool) {
	h, ok := regOpen(root, subkey)
	if !ok {
		return 0, false
	}
	defer pcall(pRegCloseKey, h)

	namep, _ := syscall.UTF16PtrFromString(name)
	var typ uint32
	var val uint32
	var size uint32 = 4
	r, _, _ := pcall(pRegQueryValueExW, h, uintptr(unsafe.Pointer(namep)), 0,
		uintptr(unsafe.Pointer(&typ)), uintptr(unsafe.Pointer(&val)), uintptr(unsafe.Pointer(&size)))
	if r != 0 || typ != REG_DWORD {
		return 0, false
	}
	return val, true
}

func regOpen(root uintptr, subkey string) (uintptr, bool) {
	sub, err := syscall.UTF16PtrFromString(subkey)
	if err != nil {
		return 0, false
	}
	var h uintptr
	r, _, _ := pcall(pRegOpenKeyExW, root, uintptr(unsafe.Pointer(sub)), 0, KEY_READ,
		uintptr(unsafe.Pointer(&h)))
	if r != 0 {
		return 0, false
	}
	return h, true
}
