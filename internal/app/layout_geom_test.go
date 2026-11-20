package app

import "testing"

// P3 的核心断言：**抽屉打开时主区宽度不能被改变**。
// 这条曾经是真实的回归——抽屉当分栏做，一打开消息区被压窄，
// 换行全变、滚动位置跳。用坐标断言钉死，不靠肉眼。
func TestDrawerDoesNotSqueezeChat(t *testing.T) {
	// 窗口够窄：侧栏会被降级为浮层抽屉（rail+侧栏+主区 = 56+264+520 = 840，
	// 所以取 800 才真正装不下）。
	winW := int32(800)
	g := computePanelGeom(panelInput{
		WinW: winW, WinH: 700,
		ShowSide: true, ShowInsp: false,
		SideUser: 264, InspUser: 320,
		SideA: 1, InspA: 0, // 检查器收起：动画进度必须与 showInsp 一致
	})
	if !g.SideDrawer {
		t.Fatalf("%d 宽窗口下侧栏应降级为抽屉，实际 sideDrawer=false（chatW=%d）", winW, g.ChatW)
	}
	if g.SideOcc != 0 {
		t.Errorf("抽屉占位宽度应为 0，实际 %d", g.SideOcc)
	}
	// 抽屉态：主区只减去 rail，不减去抽屉宽。
	want := winW - railPanelW
	if g.ChatW != want {
		t.Errorf("抽屉态主区宽度应为 %d（winW-rail），实际 %d", want, g.ChatW)
	}
}

// 反过来：窗口够宽时侧栏是分栏，参与排版，主区应当变窄。
func TestPanelSqueezesOnlyWhenNotDrawer(t *testing.T) {
	winW := int32(1400)
	g := computePanelGeom(panelInput{
		WinW: winW, WinH: 800,
		ShowSide: true, ShowInsp: false,
		SideUser: 264, InspUser: 320,
		SideA: 1, InspA: 0,
	})
	if g.SideDrawer {
		t.Fatalf("1400 宽窗口下侧栏不应降级为抽屉")
	}
	if g.SideOcc <= 0 {
		t.Errorf("分栏态占位宽度应大于 0，实际 %d", g.SideOcc)
	}
	if g.ChatW >= winW-railPanelW {
		t.Errorf("分栏态主区应比「仅 rail」更窄，实际 chatW=%d", g.ChatW)
	}
	// 分栏态：主区 = 窗口 - rail - 侧栏占位 - 检查器占位
	if want := winW - railPanelW - g.SideOcc - g.InspOcc; g.ChatW != want {
		t.Errorf("分栏态主区宽度应为 %d，实际 %d", want, g.ChatW)
	}
}

// 排版基准宽（layW）取目标宽：动画中间态不能让文本重排，
// 否则拖拽/展开时每帧换行，滚动条乱跳。
func TestLayWIgnoresAnimationProgress(t *testing.T) {
	base := panelInput{WinW: 1400, WinH: 800, ShowSide: true, InspUser: 320, SideUser: 264}
	var prev int32 = -1
	for _, p := range []float32{0, 0.25, 0.5, 0.75, 1} {
		in := base
		in.SideA = p
		g := computePanelGeom(in)
		if g.LayW != 1400-railPanelW-264 {
			t.Errorf("SideA=%.2f 时 layW 应为固定值 %d，实际 %d", p, 1400-railPanelW-264, g.LayW)
		}
		if prev >= 0 && g.LayW != prev {
			t.Errorf("SideA=%.2f 时 layW 发生变化（%d → %d），动画期间不应重排", p, prev, g.LayW)
		}
		prev = g.LayW
	}
}

// 主区不得被压到不可读：无论窗口多窄，chatW 都有下限。
func TestChatWFloor(t *testing.T) {
	// 只覆盖 DESIGN 支持的最小窗口 960 以上；更窄的窗口由系统层面
	// 的 WM_GETMINMAXINFO 拦住（见 internal/ui/window.go），不靠布局硬扛。
	for _, w := range []int32{960, 1024, 1100, 1200, 1296, 1600} {
		got := ComposeChatW(w, true, true, 264, 320)
		if got < 320 {
			t.Errorf("窗口 %d 宽时主区被压到 %d（下限 320）", w, got)
		}
		if got > w-railPanelW {
			t.Errorf("窗口 %d 宽时主区 %d 超过可用空间 %d", w, got, w-railPanelW)
		}
	}
}

// 两个抽屉同时开且都装不下时，检查器让位，侧栏优先。
func TestBothPanelsPreferSideDrawer(t *testing.T) {
	g := computePanelGeom(panelInput{
		WinW: 820, WinH: 700,
		ShowSide: true, ShowInsp: true,
		SideUser: 264, InspUser: 320,
		SideA: 1, InspA: 1,
	})
	if g.SideDrawer && g.InspDrawer {
		t.Errorf("两个抽屉不应同时降级（会互相压盖），实际 side=%v insp=%v", g.SideDrawer, g.InspDrawer)
	}
	if !g.SideDrawer && !g.InspDrawer {
		t.Errorf("820 宽窗口装不下两个分栏，至少应有一个降级为抽屉")
	}
}

// 断点：DESIGN 要求 ≥1200 可以并排；这里用默认宽度验证边界行为。
func TestBreakpoint1200(t *testing.T) {
	g := computePanelGeom(panelInput{
		WinW: 1200, WinH: 800,
		ShowSide: true, ShowInsp: true,
		SideUser: 264, InspUser: 320,
		SideA: 1, InspA: 1,
	})
	if g.SideDrawer {
		t.Errorf("1200 宽下侧栏应可并排（rail+侧栏+检查器+主区 = %d）", railPanelW+264+320+520)
	}
	if g.ChatW < 520 {
		t.Errorf("1200 宽下主区应至少 520，实际 %d", g.ChatW)
	}
}

// 面板宽度上限不得超过窗口的 46%（避免一个面板吃掉大半屏）。
func TestPanelWidthCapAt46Percent(t *testing.T) {
	got := clampPanelW(9999, minSideW, maxSideW, 1000)
	if want := int32(460); got != want {
		t.Errorf("1000 宽窗口下面板上限应为 %d（460 与 46%%=460 取小），实际 %d", want, got)
	}
	got = clampPanelW(9999, minSideW, maxSideW, 600)
	if want := int32(276); got != want {
		t.Errorf("600 宽窗口下面板上限应为 276（窗口的 46%%），实际 %d", got)
	}
}
