//go:build !windows

package selftest

import "runtime"

// casePlatformProbe 非 Windows 下无 GDI 环境，仅用于开发期跑通其余用例。
func casePlatformProbe(e *env) (Status, string, string) {
	return StatusSkip, "当前平台 " + runtime.GOOS + " 无 GDI 渲染层，跳过", ""
}
