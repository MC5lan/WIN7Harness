//go:build windows

package app

import (
	"fmt"
	"time"
	"win7harness/internal/core"
)

// 本文件由 app.go 按职责拆分（T07），仅移动代码，未改行为。
// 布局：尺寸计算、面板拖拽、抽屉与动画。

// dense 低高度窗口（1280x720 一档）启用紧凑纵向间距。
func (a *App) dense() bool { return a.winH > 0 && a.winH < 800 }

// topH 返回顶栏高度。
func (a *App) topH() int32 {
	if a.dense() {
		return 40
	}
	return 46
}

func (a *App) layout() {
	ts, ti := float32(0), float32(0)
	if a.showSide {
		ts = 1
	}
	if a.showInsp {
		ti = 1
	}
	if !a.animInit { // 首帧直接就位，避免启动时侧栏滑入造成抖动
		a.animInit = true
		a.sideA, a.inspA = ts, ti
	}
	// 纯计算下沉到 layout_geom.go，便于用坐标断言验证"抽屉不挤压主区"。
	g := computePanelGeom(panelInput{
		WinW: a.winW, WinH: a.winH,
		ShowSide: a.showSide, ShowInsp: a.showInsp,
		SideUser: a.sideWUser, InspUser: a.inspWUser,
		SideA: a.sideA, InspA: a.inspA,
	})
	a.railW = g.RailW
	a.sideW = g.SideW
	a.inspW = g.InspW
	a.sideOcc = g.SideOcc
	a.inspOcc = g.InspOcc
	a.chatW = g.ChatW
	a.layW = g.LayW
	// 抽屉开合会整块挪动面板：拓扑变了就下一帧整屏重绘，别指望局部脏区能擦干净。
	if a.sideDrawer != g.SideDrawer || a.inspDrawer != g.InspDrawer {
		a.forceFull = true
	}
	a.sideDrawer = g.SideDrawer
	a.inspDrawer = g.InspDrawer
	a.chatH = a.winH
	a.logLayout(g)
}

// logLayout 只在布局结果发生变化时写一行，避免每帧刷屏。
func (a *App) logLayout(g panelGeom) {
	if !layoutLogOn {
		return
	}
	key := fmt.Sprintf("%d/%d|%d|%d|%d|%d|%d|%v%v", a.winW, a.winH, g.SideW, g.SideOcc,
		g.InspW, g.InspOcc, g.ChatW, g.SideDrawer, g.InspDrawer)
	if key == a.dbgLayout {
		return
	}
	a.dbgLayout = key
	core.Logf("布局: 窗口 %dx%d rail=%d 侧栏 %d(占位 %d, 抽屉 %v) 检查器 %d(占位 %d, 抽屉 %v) 主区 %d 排版宽 %d",
		a.winW, a.winH, g.RailW, g.SideW, g.SideOcc, g.SideDrawer,
		g.InspW, g.InspOcc, g.InspDrawer, g.ChatW, g.LayW)
}

// handleAt 返回鼠标所在的分隔条：0 无 / 1 侧栏 / 2 检查器。
func (a *App) handleAt(x, y int32) int {
	if a.page != PageChat || a.modal != nil || a.palOpen || a.menu != nil {
		return 0
	}
	if x < 0 || y < 0 || y > a.winH {
		return 0
	}
	if a.showSide && a.sideW > 8 && x > a.railW {
		if bx := a.railW + a.sideW; x >= bx-handleGrab && x <= bx+handleGrab {
			return 1
		}
	}
	if a.showInsp && a.inspW > 8 {
		if bx := a.winW - a.inspW; x >= bx-handleGrab && x <= bx+handleGrab {
			return 2
		}
	}
	return 0
}

// beginDrag 开始拖拽分隔条；400ms 内在同一条上再按一次视为双击，复位默认宽度。
func (a *App) beginDrag(h int, x int32) {
	now := time.Now()
	if a.hbHover == h && !a.hbAt.IsZero() && now.Sub(a.hbAt) < 400*time.Millisecond {
		if h == 1 {
			a.sideWUser = defSideW
		} else {
			a.inspWUser = defInspW
		}
		a.hbAt = time.Time{}
		a.dragMode = 0
		a.layout()
		a.savePanelW()
		return
	}
	a.dragMode = h
	a.dragX0 = x
	if h == 1 {
		a.dragW0 = a.sideWUser
	} else {
		a.dragW0 = a.inspWUser
	}
	a.hbAt = now
	a.hbHover = h
}

// dragPanel 按鼠标位移实时改面板宽度，边界由 clampPanelW 兜住。
func (a *App) dragPanel(x int32) {
	d := x - a.dragX0
	if a.dragMode == 1 {
		a.sideWUser = clampPanelW(a.dragW0+d, minSideW, maxSideW, a.winW)
	} else if a.dragMode == 2 {
		a.inspWUser = clampPanelW(a.dragW0-d, minInspW, maxInspW, a.winW)
	} else {
		return
	}
	a.layout()
}

// savePanelW 把拖拽结果写进配置，下次启动保持同样宽度。
func (a *App) savePanelW() {
	if a.cfg == nil {
		return
	}
	if int32(a.cfg.SideW) == a.sideWUser && int32(a.cfg.InspW) == a.inspWUser {
		return
	}
	a.cfg.SideW = int(a.sideWUser)
	a.cfg.InspW = int(a.inspWUser)
	_ = a.cfg.Save()
}

// toggleDrawer 展开/收起侧栏；占位还是浮层由 layout 的自适应判定决定。
func (a *App) toggleDrawer() {
	a.showSide = !a.showSide
	a.layout()
}

// toggleInspector 展开/收起检查器，同样交给自适应判定。
func (a *App) toggleInspector() {
	a.showInsp = !a.showInsp
	a.layout()
	if a.cfg != nil && a.cfg.ShowInsp != a.showInsp {
		a.cfg.ShowInsp = a.showInsp
		_ = a.cfg.Save()
	}
}

// animStep 推进侧栏/检查器折叠动画，返回本帧是否有变化。
func (a *App) animStep() bool {
	moved := false
	step := func(cur *float32, target float32) {
		d := target - *cur
		if d == 0 {
			return
		}
		ad := d
		if ad < 0 {
			ad = -ad
		}
		if ad < 0.02 { // 收尾吸附，避免亚像素区间来回抖动
			*cur = target
		} else {
			*cur += d * 0.34
		}
		moved = true
	}
	ts, ti := float32(0), float32(0)
	if a.showSide {
		ts = 1
	}
	if a.showInsp {
		ti = 1
	}
	step(&a.sideA, ts)
	step(&a.inspA, ti)
	return moved
}
