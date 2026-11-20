//go:build windows && (amd64 || 386)

package tools

import (
	"syscall"
	"testing"
)

// requireChineseOEM 只让“中文控制台输出”用例在中文代码页（936）上跑。
//
// 英文 Win7 的 OEM 代码页是 437/850，cmd.exe 在把命令行写进管道时
// 就会把中文变成 ?；这不是 decodeConsoleText 能恢复的，也不是产品 bug。
// 这类路径在英文系统上应使用 list_dir / read_file（Go 直接读 Unicode 文件名），
// 不要用 dir/where 探测。
func requireChineseOEM(t *testing.T) {
	t.Helper()
	k := syscall.NewLazyDLL("kernel32.dll")
	p := k.NewProc("GetOEMCP")
	r, _, _ := p.Call()
	if int(r) != 936 {
		t.Skipf("当前 OEM 代码页 %d 不是 936（中文 Win7），中文控制台输出用例跳过；请用 list_dir/read_file 读中文路径", r)
	}
}
