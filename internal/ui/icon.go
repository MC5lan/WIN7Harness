//go:build windows

package ui

import (
	_ "embed"
	"os"
	"path/filepath"

	"win7harness/internal/core"
	"win7harness/internal/win32"
)

// 品牌图标随二进制内嵌，运行期落盘一次再由 LoadImage 载入。
// 内嵌而非外挂，是为了保证"拷一个 exe 就能跑"的零依赖承诺。
//
//go:embed icon.ico
var iconBytes []byte

// applyIcon 把内嵌图标挂到窗口标题栏与任务栏，并返回 16px 图标句柄供托盘复用。
// 任何一步失败都静默降级为系统默认图标，不影响启动。
func applyIcon(h win32.HWND) win32.HICON {
	if len(iconBytes) == 0 {
		return 0
	}
	path := filepath.Join(os.TempDir(), "win7harness-app.ico")
	if err := core.WriteFileAtomic(path, iconBytes, 0o600); err != nil {
		return 0
	}
	small := win32.LoadIconFile(path, 16, 16)
	big := win32.LoadIconFile(path, 32, 32)
	if small == 0 {
		small = win32.LoadIconFile(path, 0, 0)
	}
	if big == 0 {
		big = small
	}
	win32.SetWindowIcon(h, small, big)
	win32.SetClassIcon(h, small, big)
	return small
}
