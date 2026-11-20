//go:build !windows

package lsp

import "os/exec"

func hideWindowPlatform(cmd *exec.Cmd) {}

func killTreePlatform(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
