//go:build !windows

package core

import "syscall"

// hiddenProcAttr 非 Windows 平台（开发机自测）没有 HideWindow，直接返回空属性。
func hiddenProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{}
}
