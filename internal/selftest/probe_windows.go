//go:build windows

package selftest

import (
	"fmt"

	"win7harness/internal/ui"
	"win7harness/internal/win32"
)

// casePlatformProbe 探测真实渲染环境：屏幕色深、DPI 缩放、界面字体。
//
// 这条用例把"16 位色偏色""高 DPI 字体被裁"这类只在真机上出现的问题，
// 从口头猜测变成报告里的实测数据：用户跑一次自检，开发者就能看到
// 对方是什么色深、什么缩放、用的哪个字体。
func casePlatformProbe(e *env) (Status, string, string) {
	ui.InitFonts()

	hdc := win32.GetDC(0)
	if hdc == 0 {
		return StatusSkip, "取不到屏幕 DC，跳过渲染探测", ""
	}
	const (
		bitsPixel  = 12 // BITSPIXEL
		logPixelsX = 88
		horzRes    = 8
	)
	bpp := win32.GetDeviceCaps(hdc, bitsPixel)
	dpiX := win32.GetDeviceCaps(hdc, logPixelsX)
	width := win32.GetDeviceCaps(hdc, horzRes)
	win32.ReleaseDC(0, hdc)

	if bpp <= 0 {
		return StatusSkip, "GetDeviceCaps 未返回有效色深，跳过", ""
	}
	if dpiX <= 0 || width <= 0 {
		return StatusSkip, "GetDeviceCaps 返回无效 DPI/宽度，跳过", ""
	}
	scale := float64(dpiX) / 96.0
	detail := fmt.Sprintf("色深 %d 位，DPI %d（缩放 %.0f%%），屏幕宽 %d，界面字体 %s",
		bpp, dpiX, scale*100, width, ui.FaceUI)

	switch {
	case bpp < 15:
		return StatusFail, detail + "；色深过低，界面会明显失真",
			"右键桌面 → 屏幕分辨率 → 高级设置，把颜色改为「真彩色 32 位」"
	case bpp < 24:
		// 16 位色（老显卡、远程桌面、精简驱动）下角色贴图会走 GDI 色深转换，
		// 可能出现色阶。功能不受影响，只记录以便对照用户反馈。
		return StatusPass, detail + "；16 位色下界面贴图由 GDI 转换，若出现明显偏色请回传本报告",
			""
	}
	if ui.FaceUI == "SimSun" {
		return StatusPass, detail + "；系统缺少雅黑，已回退宋体（不影响使用）", ""
	}
	if scale >= 1.5 && scale != 2.0 && scale != 1.25 && scale != 1.75 {
		return StatusPass, detail + "；非标准缩放比例，界面按整数倍近似", ""
	}
	return StatusPass, detail, ""
}
