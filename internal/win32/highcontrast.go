//go:build windows

package win32

import "unsafe"

const (
	// SPI_GETHIGHCONTRAST 查询系统高对比设置；Win7 起结构为 HIGHCONTRASTW。
	SPI_GETHIGHCONTRAST = 0x0042
	HCF_HIGHCONTRASTON  = 0x00000001
)

var pSystemParametersInfoW = dUser32.NewProc("SystemParametersInfoW")

type highContrastW struct {
	cbSize            uint32
	dwFlags           uint32
	lpszDefaultScheme *uint16
}

// HighContrastActive 返回系统“高对比度”辅助功能是否开启。
// 不依赖 Win10 的 UISettings，Win7 也走同一套 SystemParametersInfo。
func HighContrastActive() bool {
	var hc highContrastW
	hc.cbSize = uint32(unsafe.Sizeof(hc))
	r, _, _ := pcall(pSystemParametersInfoW, SPI_GETHIGHCONTRAST, uintptr(hc.cbSize), uintptr(unsafe.Pointer(&hc)), 0)
	return r != 0 && hc.dwFlags&HCF_HIGHCONTRASTON != 0
}
