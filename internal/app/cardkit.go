//go:build windows

package app

import (
	"win7harness/internal/ui"
)

// 功能页卡片的统一规格。
//
// 设置 / 诊断 / 自动化 / 扩展 / 用量这些全屏页曾各写各的卡片：
// 圆角 6/8/10 混用、填充 Surface 与 Card 混用、标题字号与内边距也不一致，
// 看上去每页的"密度"都不一样。这里收敛成唯一一套：
//   - 圆角 8（控件 6、弹窗 10）
//   - 填充 th.Card（主区之上再浮一层，浅色下白卡浮灰主区，深色下深卡浮更深的主区）
//   - 1px 发丝描边 th.BorderSoft
//   - 标题 FsMd 700、距卡片左上 (22,16)，正文起点 y+48
const (
	cardPad    = int32(22)
	cardTitleY = int32(16)
	CardBodyY  = int32(48) // 卡片内正文相对卡片顶边的偏移
)

// cardFrame 绘制卡片本体与标题，返回卡片矩形。
// title 为空时只画卡片本体。
func (a *App) cardFrame(c *ui.Canvas, r ui.Rect, title string) {
	th := a.th
	c.Round(r, 8, th.Card)
	c.RoundStroke(r, 8, th.BorderSoft)
	if title != "" {
		c.TextFace(r.X+cardPad, r.Y+cardTitleY, title, ui.FsMd, 700, th.Text, ui.FaceUI)
	}
}

// paintPageHeader 统一所有二级页面的顶栏：品牌标识 + 页面名 + 一句说明 +
// 明确的「返回对话」按钮。过去每个页面各写一遍，标题位置、按钮宽度都不一致，
// 用户切页时会觉得“每页都是另一套软件”。v1.40.1 收敛成这一处。
func (a *App) paintPageHeader(u *UI, backID, title, subtitle string) bool {
	c := u.c
	th := a.th
	W := a.winW
	c.FillRect(ui.R(a.railW, 0, W-a.railW, 56), ui.Blend(th.Bg, th.Surface, 0.6))
	c.HLine(a.railW, 55, W-a.railW, th.BorderSoft)
	// 侧栏已经有品牌标识，页头不再重复画 logo（用户明确要求删掉这个图标）。
	c.TextFace(a.railW+24, 14, title, ui.FsLg, 700, th.Text, ui.FaceUI)
	if subtitle != "" {
		x := a.railW + 24 + c.MeasureFace(title, ui.FsLg, 700, ui.FaceUI) + 14
		c.TextFace(x, 20, subtitle, ui.FsXs, 400, th.TextMute, ui.FaceUI)
	}
	return u.Button(backID, "返回对话", ui.R(W-140, 14, 116, 30), BtnGhost)
}
