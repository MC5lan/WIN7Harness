//go:build windows

package ui

import (
	"strings"

	"win7harness/internal/win32"
)

// Theme 定义一套完整配色。深浅色主题通过切换实例实现，
// 所有绘制代码只引用 Theme 字段，不硬编码颜色。
type Theme struct {
	Name string

	Bg         uint32 // 窗口底
	Surface    uint32 // 主面板
	Surface2   uint32 // 侧栏
	Card       uint32 // 卡片/气泡
	CardHover  uint32
	Border     uint32
	BorderSoft uint32

	Text     uint32
	TextDim  uint32
	TextMute uint32
	TextInv  uint32

	Accent     uint32
	AccentSoft uint32
	AccentText uint32

	Danger  uint32
	Warn    uint32
	Success uint32

	CodeBg     uint32
	CodeText   uint32
	CodeBorder uint32
	// CodeBar 是代码块左侧那条强调竖条的颜色。
	CodeBar uint32

	UserBubble uint32
	AIBubble   uint32

	InputBg     uint32
	InputBorder uint32
	InputFocus  uint32
	SelectBg    uint32

	ScrollThumb uint32
	Shadow      uint32
}

// themeFromTokens 把令牌表映射成 Theme 字段。
//
// 这里刻意不写任何字面色值：Theme 只是 Tokens 的一个视图，
// 改配色只改 tokens.go 一处，界面不会出现"某块还是旧颜色"。
func themeFromTokens(name string, tk Tokens) *Theme {
	return &Theme{
		Name:    name,
		Bg:      tk.BGCanvas,
		Surface: tk.BGSurface,
		// 侧栏用侧栏专用底色（暖纸浅色：比主区略深的米灰，拉出两层）
		Surface2:   tk.BGSidebar,
		Card:       tk.BGSurfaceRaise,
		CardHover:  tk.BGHover,
		Border:     tk.BorderStrong,
		BorderSoft: tk.BorderHairline,

		Text:     tk.TextPrimary,
		TextDim:  tk.TextSecondary,
		TextMute: tk.TextTertiary,
		// 注意：令牌里的 TextInverse 是"强调色底上的深色文字"，
		// 而 Theme.TextInv 在既有代码里是"深底上的反白"，两者语义不同。
		// P2 保持反白语义不破坏现有对比度，P3 再做语义拆分。
		TextInv: 0xFFFFFF,

		Accent:     tk.AccentBase,
		AccentSoft: tk.AccentSubtle,
		// AccentText 用在 AccentSoft（浅奶油/深绿/深棕）底上的文字，
		// 必须用正文色而不是强调色：浅色主题下 AccentHover 本身是浅珊瑚，
		// 配浅奶油底会变成浅底浅字（预设芯片选中态曾踩过这个坑）。
		AccentText: tk.TextPrimary,

		Danger:  tk.StateDanger,
		Warn:    tk.StateWarn,
		Success: tk.StateSuccess,

		CodeBg:     tk.BGSurfaceSunken,
		CodeText:   tk.TextPrimary,
		CodeBorder: tk.BorderHairline,
		CodeBar:    Blend(tk.BorderStrong, tk.AccentBase, 0.35),

		UserBubble: tk.BGSurfaceSunken,
		AIBubble:   tk.BGSurface,

		InputBg:     tk.BGSurfaceSunken,
		InputBorder: tk.BorderStrong,
		InputFocus:  tk.FocusRing,
		SelectBg:    tk.AccentSubtle,

		ScrollThumb: tk.BorderStrong,
		Shadow:      tk.BGCanvas,
	}
}

// DarkTheme 深色主题：Quiet Instrument 的默认盘（低饱和蓝黑 + 单一强调色）。
func DarkTheme() *Theme { return themeFromTokens("dark", DarkTokens()) }

// LightTheme 浅色主题：与深色同构，底层最浅、层越高越白。
func LightTheme() *Theme { return themeFromTokens("light", LightTokens()) }

// HighContrastTheme 高对比：纯黑底、纯白字、亮黄强调，对比度按 WCAG AAA。
func HighContrastTheme() *Theme { return themeFromTokens("hc", HighContrastTokens()) }

// SystemHighContrast 暴露系统辅助功能状态，供设置页提示。
func SystemHighContrast() bool { return win32.HighContrastActive() }

// ResolveTheme 返回最终生效主题：系统高对比开启时强制 hc，
// 否则按用户配置的 dark/light/hc。这样用户不必在系统切换高对比后
// 再手动进设置——WM_SETTINGCHANGE 会触发重新解析。
func ResolveTheme(pref string) *Theme {
	if SystemHighContrast() {
		return HighContrastTheme()
	}
	return ThemeByName(pref)
}

// ThemeByName 按名称取主题。高对比额外接受 hc / high-contrast 两种写法，
// 用户手改配置文件时两种直觉写法都能生效。
func ThemeByName(name string) *Theme {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "light":
		return LightTheme()
	case "hc", "high-contrast", "highcontrast", "contrast":
		return HighContrastTheme()
	}
	return DarkTheme()
}

// 字体族：Win7 自带微软雅黑与 Consolas，无需随包分发字体。
// 这些必须是 var：启动时按目标机实际可用字体做一次自动回退
// （Win7 没有 "Microsoft YaHei UI"，只有 "Microsoft YaHei"）。
var (
	FaceUI    = "Microsoft YaHei UI"
	FaceMono  = "Consolas"
	FaceEmoji = "Segoe UI Symbol"
	// FaceDisplay 是品牌艺术字（引导页产品名）的展示字体，Segoe UI Black 为几何粗体。
	// 目标机缺失时 InitFonts 回退到 FaceUI，并改用粗体字重。
	FaceDisplay   = "Segoe UI Black"
	WeightDisplay = int32(400)
)

// 字号（逻辑像素）
// five档：11 提示/说明 → 12 次要正文 → 13 正文 → 16 卡片/页面标题 → 20 大数字。
// 不要再把标题压到 15：与正文只差 2px，层级分不出来。
const (
	FsXs   = 11
	FsSm   = 12
	FsBase = 13
	FsMd   = 13
	FsLg   = 16
	FsXl   = 20
	FsH1   = 26
)
