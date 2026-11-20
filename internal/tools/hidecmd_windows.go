//go:build windows && (amd64 || 386)

package tools

import (
	"os/exec"
	"syscall"
)

// hideCmdWindow 隐藏控制台子进程的窗口（GUI 子系统直接跑不隐藏会闪黑框）。
func hideCmdWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
}
