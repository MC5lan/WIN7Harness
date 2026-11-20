//go:build windows

package win32

import (
	"os"
	"syscall"
	"unsafe"
)

// 命令行模式（-run）用到的控制台支持。
// 程序本身用 -H=windowsgui 链接，默认没有控制台；但脚本调用时
// （cmd 里直接运行、或重定向到文件）需要能把结果写到 stdout。
// 做法：检测标准输出句柄是否可用，不可用就附着到父进程的控制台。

const (
	invalidHandleValue = ^uintptr(0)
	stdOutputHandle    = ^uintptr(10) // -11
	stdErrorHandle     = ^uintptr(11) // -12
	attachParentProc   = ^uintptr(0)  // -1

	fileTypeUnknown = 0
	fileTypeDisk    = 1
	fileTypeChar    = 2
	fileTypePipe    = 3
)

var (
	kernel32            = syscall.NewLazyDLL("kernel32.dll")
	procGetStdHandle    = kernel32.NewProc("GetStdHandle")
	procGetFileType     = kernel32.NewProc("GetFileType")
	procAttachConsole   = kernel32.NewProc("AttachConsole")
	procAllocConsole    = kernel32.NewProc("AllocConsole")
	procSetConsoleTitle = kernel32.NewProc("SetConsoleTitleW")
)

// StdoutUsable 判断标准输出是否被重定向到了文件或管道（脚本调用时常见）。
func StdoutUsable() bool {
	h, _, _ := pcall(procGetStdHandle, stdOutputHandle)
	if h == 0 || h == invalidHandleValue {
		return false
	}
	t, _, _ := pcall(procGetFileType, h)
	return t == fileTypeDisk || t == fileTypePipe || t == fileTypeChar
}

// AttachParentConsole 附着到调用方（cmd / PowerShell）的控制台。
// 成功返回 true；父进程没有控制台时返回 false。
func AttachParentConsole() bool {
	r, _, _ := pcall(procAttachConsole, uintptr(attachParentProc))
	return r != 0
}

// SetupConsole 让 stdout / stderr 变得可写，返回是否需要调用方在使用后关闭句柄。
// 顺序是：标准输出本来就能用 → 直接用；否则尝试附着父控制台；再不行自己开一个。
func SetupConsole(title string) (cleanup func()) {
	if StdoutUsable() {
		return func() {}
	}
	if !AttachParentConsole() {
		pcall(procAllocConsole)
	}
	if title != "" {
		t, err := syscall.UTF16PtrFromString(title)
		if err == nil {
			pcall(procSetConsoleTitle, uintptr(unsafe.Pointer(t)))
		}
	}
	out, err1 := os.OpenFile("CONOUT$", os.O_WRONLY, 0)
	errf, err2 := os.OpenFile("CONOUT$", os.O_WRONLY, 0)
	if err1 != nil {
		return func() {}
	}
	os.Stdout = out
	if err2 == nil {
		os.Stderr = errf
	}
	return func() {
		out.Close()
		if err2 == nil {
			errf.Close()
		}
	}
}
