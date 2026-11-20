//go:build !windows

package mcp

import (
	"fmt"
	"os/exec"
)

// newMCPCommand 非 Windows 平台仅做开发期交叉验证用；
// 行为刻意与 Windows 版对齐（命令白名单校验），但不拒绝引号。
func newMCPCommand(command string, args []string) (*exec.Cmd, error) {
	if command == "" {
		return nil, fmt.Errorf("命令为空")
	}
	return exec.Command(command, args...), nil
}

func hideMCPWindow(cmd *exec.Cmd) {}

func killProcessTreePS(pid int) {}

func assignJob(pid int) (uintptr, bool) { return 0, false }

func terminateJob(job uintptr) bool { return false }

func closeHandle(h uintptr) {}
