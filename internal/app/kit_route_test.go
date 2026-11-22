//go:build windows

package app

import (
	"testing"

	"win7harness/internal/ui"
	"win7harness/internal/ui/kit"
)

// 回归：kit 页顶栏的「← 返回对话」是 classic 按钮，不能被 kit 鼠标路由吞掉。
// 之前 MouseDown/MouseUp 只要 x >= railW 就整块转给 kit，导致所有 kit 页
// 右上角的返回按钮都点不动，只能靠 Esc 退出。
func TestKitPageTopBarClicksGoToClassic(t *testing.T) {
	th := ui.LightTheme()
	a := &App{
		th:    th,
		ui:    NewUI(nil, th),
		kitui: kit.New(nil),
		page:  PageDiag,
		railW: railPanelW,
	}
	back := ui.R(1140, 14, 116, 30)
	a.ui.Hit("diag.back", back) // 模拟上一帧绘制时注册的命中区

	a.MouseDown(back.CX(), back.CY(), 0)
	if id, _ := a.ui.Pressed(); id != "diag.back" {
		t.Fatalf("顶栏返回按钮按下应记录为 classic press，实际 %q", id)
	}
	a.MouseUp(back.CX(), back.CY(), 0)
	if !a.ui.Clicked("diag.back") {
		t.Fatal("顶栏返回按钮抬起后应派发 classic click")
	}
}

// 同时保证正文区仍然归 kit：放行顶栏不能把 kit 的点击抢回来。
func TestKitPageBodyClicksStayInKit(t *testing.T) {
	th := ui.LightTheme()
	k := kit.New(nil)
	a := &App{
		th:    th,
		ui:    NewUI(nil, th),
		kitui: k,
		page:  PageDiag,
		railW: railPanelW,
	}
	// 正文控件必须落在 rail 右侧；rail 变宽后不能再用固定 x=100。
	btn := ui.R(railPanelW+40, 100, 120, 30)
	clicked := false
	k.RegisterHit("diag.tab.checks", btn, func() { clicked = true })

	a.MouseDown(btn.CX(), btn.CY(), 0)
	if id, _ := k.PressInfo(); id != "diag.tab.checks" {
		t.Fatalf("正文按钮按下应记录为 kit press，实际 %q", id)
	}
	if id, _ := a.ui.Pressed(); id != "" {
		t.Fatalf("正文按下不应落到 classic，实际 %q", id)
	}
	a.MouseUp(btn.CX(), btn.CY(), 0)
	if !clicked {
		t.Fatal("正文按钮抬起后应执行 kit onClick")
	}
}
