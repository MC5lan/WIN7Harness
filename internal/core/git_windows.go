//go:build windows

package core

import (
	"os/exec"
	"syscall"
)

// hideWindow 避免执行 git 时闪出黑色控制台窗口。
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}
