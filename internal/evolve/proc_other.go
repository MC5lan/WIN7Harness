//go:build !windows || (!amd64 && !386)

package evolve

import "os"

// killProcessTree 在非 Windows 平台只能杀直接进程；dev-only 工具的主要
// 目标环境是 Win7，Windows 分支会走 taskkill /T。
func killProcessTree(pid int) {
	if pid <= 0 {
		return
	}
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
	}
}
