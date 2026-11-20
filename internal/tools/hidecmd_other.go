//go:build !windows

package tools

import "os/exec"

func hideCmdWindow(cmd *exec.Cmd) {}
