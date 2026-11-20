package app

// 面板布局的纯计算：不依赖 App、不碰 HWND/DC，因此可以直接单测。
//
// 为什么单独抽出来：抽屉/分栏的"是否挤压主区"是这套界面最容易回归的地方
// （历史上抽屉一打开，主区就被压窄，消息换行全变、滚动位置也乱）。
// 把计算与绘制分开后，可以用坐标断言把"打开抽屉时主区宽度不变"钉死。

// panelInput 是布局输入。
type panelInput struct {
	WinW, WinH int32

	ShowSide, ShowInsp bool  // 用户想不想显示
	SideUser, InspUser int32 // 用户拖出来的宽度

	// 展开动画进度 0..1。首帧直接给 1，避免启动时侧栏滑入抖动。
	SideA, InspA float32
}

// panelGeom 是布局输出。Occ 是"占位宽度"：抽屉态为 0，不参与排版。
type panelGeom struct {
	RailW   int32
	SideW   int32 // 当前可视宽度（含动画）
	InspW   int32
	SideOcc int32
	InspOcc int32
	ChatW   int32
	LayW    int32 // 排版基准宽度（取目标宽，动画期间不逐帧重排）

	SideDrawer bool
	InspDrawer bool
}

// easeOut 复用 app.go 里的实现（动画缓动只有一处定义）。

// clampPanelW 把面板宽夹到 [lo,hi]，并保证不超过窗口的 46%。
func clampPanelW(w, lo, hi, winW int32) int32 {
	if lim := winW * 46 / 100; lim < hi {
		hi = lim
	}
	if hi < lo {
		hi = lo
	}
	if w < lo {
		w = lo
	}
	if w > hi {
		w = hi
	}
	return w
}

// drawerFit 决定某个面板要不要降级为浮层抽屉：
// 窗口装不下「rail + 面板 + 最小会话区」时，面板改浮层覆盖，主区因此不被压扁。
// 两个抽屉同时开且都装不下时，检查器让位（先保住侧栏，因为会话列表更常用）。
func drawerFit(winW, railW, sideT, inspT int32, showSide, showInsp bool) (bool, bool) {
	side, insp := false, false
	if showSide && winW < railW+sideT+minChatW {
		side = true
	}
	if showInsp && winW < railW+inspT+minChatW {
		insp = true
	}
	if showSide && showInsp && !side && !insp && winW < railW+sideT+inspT+minChatW {
		insp = true
	}
	if side && insp {
		insp = false
	}
	return side, insp
}

// computePanelGeom 是 layout() 的纯计算部分。
func computePanelGeom(in panelInput) panelGeom {
	sideUser, inspUser := in.SideUser, in.InspUser
	if sideUser <= 0 {
		sideUser = defSideW
	}
	if inspUser <= 0 {
		inspUser = defInspW
	}

	railW := railPanelW
	sideT := clampPanelW(sideUser, minSideW, maxSideW, in.WinW)
	inspT := clampPanelW(inspUser, minInspW, maxInspW, in.WinW)

	sideDrawer, inspDrawer := drawerFit(in.WinW, railW, sideT, inspT, in.ShowSide, in.ShowInsp)

	sideW := int32(float32(sideT)*easeOut(in.SideA) + 0.5)
	inspW := int32(float32(inspT)*easeOut(in.InspA) + 0.5)

	// 抽屉是浮层：可视宽度照画，但占位为 0，主区不重排。
	sideOcc, inspOcc := sideW, inspW
	if sideDrawer {
		sideOcc = 0
	}
	if inspDrawer {
		inspOcc = 0
	}

	// 排版基准取目标宽：动画期间只做可视裁剪，避免逐帧全量重排。
	fSide, fInsp := int32(0), int32(0)
	if in.ShowSide && !sideDrawer {
		fSide = sideT
	}
	if in.ShowInsp && !inspDrawer {
		fInsp = inspT
	}
	layW := in.WinW - railW - fSide - fInsp
	if layW < 320 {
		layW = 320
	}

	// 窄窗保护：先压检查器再压侧栏，保证会话区不被挤到 0。
	if in.WinW >= railW+minChatW {
		over := sideOcc + inspOcc + minChatW - (in.WinW - railW)
		if over > 0 {
			cut := over
			if cut > inspOcc {
				cut = inspOcc
			}
			inspW -= cut
			inspOcc -= cut
			over -= cut
			if over > 0 {
				if over > sideOcc {
					over = sideOcc
				}
				sideW -= over
				sideOcc -= over
			}
		}
	}

	chatW := in.WinW - railW - sideOcc - inspOcc
	if chatW < 320 {
		chatW = 320
	}

	return panelGeom{
		RailW: railW, SideW: sideW, InspW: inspW,
		SideOcc: sideOcc, InspOcc: inspOcc,
		ChatW: chatW, LayW: layW,
		SideDrawer: sideDrawer, InspDrawer: inspDrawer,
	}
}

// ComposeChatW 给单测用：在给定窗口宽与显隐状态下，主区（会话区）最终宽度。
// 导出是为了让包外断言也能写，避免只能靠肉眼截图判断是否被挤压。
func ComposeChatW(winW int32, showSide, showInsp bool, sideUser, inspUser int32) int32 {
	return computePanelGeom(panelInput{
		WinW: winW, WinH: 800,
		ShowSide: showSide, ShowInsp: showInsp,
		SideUser: sideUser, InspUser: inspUser,
		SideA: 1, InspA: 1,
	}).ChatW
}
