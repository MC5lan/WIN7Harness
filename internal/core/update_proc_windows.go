//go:build windows

package core

import "syscall"

// hiddenProcAttr Windows 下隐藏黑框窗口。
func hiddenProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{HideWindow: true}
}
