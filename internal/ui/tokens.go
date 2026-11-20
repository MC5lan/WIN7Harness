// ==== 暖纸设计系统令牌 ====
// Claude Desktop 风格：米色纸底 + 珊瑚强调 + 温暖灰度。深色同构炭灰底。
// 本文件是视觉单一真值来源：颜色/字号/间距/圆角/控件尺寸全在此定义，
// 绘制层只引用 Scaled() 与当前主题，不写死字面值。
package ui

import "math"

// Tokens 是一套完整的界面令牌（暖纸设计系统）。
// 字段同构于原令牌表，新增 BGSidebar（侧栏专用底）。
type Tokens struct {
	BGCanvas        uint32 // 窗口底（侧栏区）
	BGSurface       uint32 // 主内容区
	BGSurfaceRaise  uint32 // 卡片/气泡
	BGSurfaceSunken uint32 // 代码块/输入内槽
	BGHover         uint32 // 行/项悬停
	BGActive        uint32 // 选中态背景
	BGOverlay       uint32 // 遮罩底色
	BGSidebar       uint32 // 侧栏专用底

	BorderHairline  uint32
	BorderStrong    uint32
	BorderHighlight uint32

	TextPrimary   uint32
	TextSecondary uint32
	TextTertiary  uint32
	TextInverse   uint32

	AccentBase    uint32 // 珊瑚
	AccentHover   uint32
	AccentPressed uint32
	AccentSubtle  uint32
	AccentText    uint32

	StateSuccess uint32
	StateWarn    uint32
	StateDanger  uint32
	StateInfo    uint32
	FocusRing    uint32

	ShadowSoft   uint32
	ShadowCard   uint32
	HighlightTop uint32
}

func LightTokens() Tokens {
	return Tokens{
		// 暖纸：窗口底 #F5F0EB，主区 #FAF8F5，卡片 #FFFFFF，内槽 #EDEAE6
		BGCanvas:        0x00EBF0F5, // #F5F0EB
		BGSurface:       0x00F5F8FA, // #FAF8F5
		BGSurfaceRaise:  0x00FFFFFF, // #FFFFFF
		BGSurfaceSunken: 0x00E6EAED, // #EDEAE6
		BGHover:         0x00E0E5E8, // #E8E5E0
		BGActive:        0x00CFD6DA, // #DAD6CF
		BGOverlay:       0x001A120C, // #0C121A
		BGSidebar:       0x00E6EAED, // #EDEAE6

		BorderHairline:  0x00CCD2D6, // #D6D2CC
		BorderStrong:    0x00ACB3B8, // #B8B3AC
		BorderHighlight: 0x00FFFFFF, // #FFFFFF

		TextPrimary:   0x0020252A, // #2A2520
		TextSecondary: 0x005E646B, // #6B645E
		TextTertiary:  0x00949AA0, // #A09A94
		TextInverse:   0x00FFFFFF, // #FFFFFF

		// 珊瑚 #D97757 → hover #E8875B → pressed #C86642
		AccentBase:    0x005777D9, // #D97757
		AccentHover:   0x005B87E8, // #E8875B
		AccentPressed: 0x004266C8, // #C86642
		AccentSubtle:  0x00E9F4FD, // #FDF4E9
		AccentText:    0x00FFFFFF, // #FFFFFF

		StateSuccess: 0x003B7A3B, // #3B7A3B
		StateWarn:    0x001070B0, // #B07010
		StateDanger:  0x003232B8, // #B83232
		StateInfo:    0x000D6BB0, // #B06B0D
		FocusRing:    0x005777D9, // 珊瑚

		ShadowSoft:   0x00211413, // #131421
		ShadowCard:   0x003F2F1C, // #1C2F3F
		HighlightTop: 0x00FFFFFF,
	}
}

func DarkTokens() Tokens {
	return Tokens{
		// 暖炭：窗口底 #1F1D1B，主区 #262422，卡片 #2E2C29，内槽 #242220
		BGCanvas:        0x001B1D1F, // #1F1D1B
		BGSurface:       0x00222426, // #262422
		BGSurfaceRaise:  0x00292C2E, // #2E2C29
		BGSurfaceSunken: 0x00202224, // #242220
		BGHover:         0x00292C2E, // #2E2C29
		BGActive:        0x003A3835, // #383633?  hmm
		BGOverlay:       0x00070605, // #050607
		BGSidebar:       0x001B1D1F, // #1F1D1B

		BorderHairline:  0x0035383A, // #3A3835
		BorderStrong:    0x00504D4A, // #4A4D50
		BorderHighlight: 0x00403D3A, // #3A3D40

		TextPrimary:   0x00E0E6EA, // #EAE6E0
		TextSecondary: 0x009CA4A8, // #A8A49C
		TextTertiary:  0x00666D70, // #706D66
		TextInverse:   0x001A1816, // #16181A

		// 珊瑚 #E8875B → hover #E8956B → pressed #C86642
		AccentBase:    0x005B87E8, // #E8875B
		AccentHover:   0x006B95E8, // #E8956B
		AccentPressed: 0x004266C8, // #C86642
		AccentSubtle:  0x00303A2E, // #2E3A30
		AccentText:    0x001B1816, // #16181B

		StateSuccess: 0x004A8B4A, // #4A8B4A
		StateWarn:    0x002088D0, // #D08820
		StateDanger:  0x004040D0, // #D04040
		StateInfo:    0x00A87A30, // #307AA8
		FocusRing:    0x005B87E8, // 珊瑚

		ShadowSoft:   0x00070605,
		ShadowCard:   0x0012100C,
		HighlightTop: 0x003A3835,
	}
}

func HighContrastTokens() Tokens {
	return Tokens{
		BGCanvas:        0x00000000,
		BGSurface:       0x00000000,
		BGSurfaceRaise:  0x00121212,
		BGSurfaceSunken: 0x000A0A0A,
		BGHover:         0x001E1E1E,
		BGActive:        0x002A2A2A,
		BGOverlay:       0x00000000,
		BGSidebar:       0x00000000,

		BorderHairline:  0x00FFFFFF,
		BorderStrong:    0x00FFFFFF,
		BorderHighlight: 0x00FFFFFF,

		TextPrimary:   0x00FFFFFF,
		TextSecondary: 0x00E0E0E0,
		TextTertiary:  0x00B8B8B8,
		TextInverse:   0x00000000,

		AccentBase:    0x0000D4FF,
		AccentHover:   0x0040E0FF,
		AccentPressed: 0x0000B0D6,
		AccentSubtle:  0x0000303A,
		AccentText:    0x00000000,

		StateSuccess: 0x0040E060,
		StateWarn:    0x0000D4FF,
		StateDanger:  0x004040FF,
		StateInfo:    0x0080C0FF,
		FocusRing:    0x0000D4FF,

		ShadowSoft:   0x00000000,
		ShadowCard:   0x00000000,
		HighlightTop: 0x00FFFFFF,
	}
}

// TokensFor 按主题名返回令牌。默认走暖纸浅色。
func TokensFor(name string) Tokens {
	switch name {
	case "dark":
		return DarkTokens()
	case "hc", "high-contrast", "highcontrast", "contrast":
		return HighContrastTokens()
	}
	return LightTokens()
}

// 字号阶梯（暖纸设计系统；逻辑像素 @96DPI）
const (
	FSDisplay = 28 // 大展示数字
	FSH1      = 22 // 页面标题
	FSH2      = 17 // 卡片标题
	FSBody    = 14 // 正文（Claude 风格：稍大、可读）
	FSBodySm  = 12 // 辅助文字
	FSCaption = 11 // 最小说明
	FSMono    = 12 // 等宽
)

// 行高
const (
	LHDisplay = 36
	LHH1      = 30
	LHH2      = 24
	LHBody    = 24
	LHBodySm  = 18
	LHCaption = 16
	LHMono    = 18
)

// 间距基数（4 倍数）
const (
	Space1 = 4
	Space2 = 8
	Space3 = 12
	Space4 = 16
	Space5 = 20
	Space6 = 24
	Space7 = 32
	Space8 = 48
)

// 圆角阶梯（暖纸设计：大圆角制造纸面质感）
const (
	RadiusControl = 6
	RadiusCard    = 10
	RadiusDialog  = 12
	RadiusFab     = 999 // 胶囊
	RadiusMax     = 12
)

// 控件与框架尺寸
const (
	ControlH     = 36 // 暖纸控件更高
	ControlHSm   = 28
	TextareaMinH = 80
	ListRowH     = 36
	StatusH      = 28
	RailW        = 56
	SideDrawerW  = 280
	InspectorW   = 340
	MinWindowW   = 960
	MinWindowH   = 600
	HitMin       = 32
)

// FontScale 全局字号缩放（1.0 = 100%）。由配置写入。
var FontScale float32 = 1

// Scaled 把逻辑像素换算为当前缩放下的物理像素。
func Scaled(v int) int {
	if FontScale <= 0 || FontScale == 1 {
		return v
	}
	return int(math.Round(float64(v) * float64(FontScale)))
}

// Tokens 返回当前主题对应的令牌表。
func (t *Theme) Tokens() Tokens {
	if t == nil {
		return DarkTokens()
	}
	return TokensFor(t.Name)
}

// Scaled 在 Theme 上的便捷入口，语义同包级 Scaled。
func (t *Theme) Scaled(v int) int { return Scaled(v) }
