//go:build windows && (amd64 || 386)

package evolve

import (
	"os/exec"
	"strconv"
)

// killProcessTree 用 taskkill 收掉 go test 派生的整棵进程树。
// 不能只依赖 exec.CommandContext：它只杀直接子进程，test.exe 会活下来。
func killProcessTree(pid int) {
	if pid <= 0 {
		return
	}
	_ = exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/T", "/F").Run()
}
