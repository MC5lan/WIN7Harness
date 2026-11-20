package ui

// 组件矩阵页：WIN7H_GALLERY=1 时启动即可看到全部组件 × 状态。
//
// 存在的意义是把"视觉验收"变成可截图的事实：改一次令牌或组件，
// 打开这一页就能逐格比对，不用在真实界面里到处点着找边界情况。
// 它不进业务路径，也不读取任何配置。

// GalleryRows 是矩阵页里用到的状态顺序，验收时按这个顺序逐格核对。
var GalleryRows = []struct {
	Name  string
	State State
}{
	{"default", StateDefault},
	{"hover", StateHover},
	{"active", StateActive},
	{"focus", StateFocus},
	{"disabled", StateDisabled},
}

// galleryButtons 是四类按钮变体，按 DESIGN.md 第 4 节。
var galleryButtons = []struct {
	Name string
	Kind ComponentKind
}{
	{"primary", KindButtonPrimary},
	{"secondary", KindButtonSecondary},
	{"ghost", KindButtonGhost},
	{"danger", KindButtonDanger},
}

// DrawGallery 按 1280×800 基准排版组件矩阵；窗口更小时自动换列。
func DrawGallery(c *Canvas, w, h int32) {
	tk := CurrentTokens()
	SetTokens(tk)

	c.FillRect(Rect{X: 0, Y: 0, W: w, H: h}, tk.BGCanvas)

	pad := int32(Scaled(Space6))
	titleSize := int32(Scaled(FSH1))
	bodySize := int32(Scaled(FSBody))
	smallSize := int32(Scaled(FSBodySm))

	c.TextFace(pad, pad-int32(Scaled(Space1)), "Quiet Instrument · 组件矩阵", titleSize, 700, tk.TextPrimary, FaceUI)
	c.TextFace(pad, pad+int32(Scaled(FSH1))+int32(Scaled(Space2)), "WIN7H_GALLERY=1 · 逐格核对状态与令牌", smallSize, 400, tk.TextSecondary, FaceUI)

	y := pad + int32(Scaled(FSH1)) + int32(Scaled(Space8)) + int32(Scaled(Space3))

	// ── 按钮：4 变体 × 5 状态 ──
	c.TextFace(pad, y, "Button", bodySize, 700, tk.TextSecondary, FaceUI)
	y += int32(Scaled(Space5))
	btnW := int32(Scaled(120))
	btnH := int32(Scaled(ControlH))
	gap := int32(Scaled(Space3))
	for _, vb := range galleryButtons {
		// 行首标签
		c.TextFace(pad, y+btnH/2-int32(Scaled(FSCaption))/2, vb.Name, smallSize, 400, tk.TextTertiary, FaceUI)
		x := pad + int32(Scaled(84))
		for _, sr := range GalleryRows {
			if x+btnW > w-pad {
				break
			}
			DrawButton(c, Rect{X: x, Y: y, W: btnW, H: btnH}, "按钮", vb.Kind, sr.State)
			x += btnW + gap
		}
		y += btnH + int32(Scaled(Space2))
	}
	// 状态名（列头，画在最后一行的上边）
	{
		x := pad + int32(Scaled(84))
		hy := y - int32(Scaled(Space2)) - int32(Scaled(Space4))
		_ = hy
		for _, sr := range GalleryRows {
			if x+btnW > w-pad {
				break
			}
			c.TextFace(x, y, sr.Name, smallSize, 400, tk.TextTertiary, FaceUI)
			x += btnW + gap
		}
		y += int32(Scaled(Space5))
	}

	// ── 输入类 ──
	c.TextFace(pad, y, "Input / Select / Textarea", bodySize, 700, tk.TextSecondary, FaceUI)
	y += int32(Scaled(Space5))
	fieldW := int32(Scaled(240))
	DrawInput(c, Rect{X: pad, Y: y, W: fieldW, H: int32(Scaled(ControlH))}, "", "placeholder 文本", StateDefault)
	DrawInput(c, Rect{X: pad + fieldW + gap, Y: y, W: fieldW, H: int32(Scaled(ControlH))}, "已填内容", "", StateFocus)
	DrawInput(c, Rect{X: pad + (fieldW+gap)*2, Y: y, W: fieldW, H: int32(Scaled(ControlH))}, "禁用态", "", StateDisabled)
	DrawSelect(c, Rect{X: pad + (fieldW+gap)*3, Y: y, W: fieldW, H: int32(Scaled(ControlH))}, "model-id", StateHover)
	y += int32(Scaled(ControlH)) + int32(Scaled(Space2))
	DrawTextarea(c, Rect{X: pad, Y: y, W: fieldW*2 + gap, H: int32(Scaled(TextareaMinH))},
		[]string{"第一行文本", "第二行：示例内容", "第三行：观察行高与截断"}, StateFocus)
	y += int32(Scaled(TextareaMinH)) + int32(Scaled(Space5))

	// ── 开关 / 页签 / 徽标 / 列表 ──
	c.TextFace(pad, y, "Toggle / Tab / Badge / ListItem", bodySize, 700, tk.TextSecondary, FaceUI)
	y += int32(Scaled(Space5))
	DrawToggle(c, Rect{X: pad, Y: y, W: int32(Scaled(40)), H: int32(Scaled(24))}, true, StateSelected)
	DrawToggle(c, Rect{X: pad + int32(Scaled(56)), Y: y, W: int32(Scaled(40)), H: int32(Scaled(24))}, false, StateDefault)
	tx := pad + int32(Scaled(120))
	for i, name := range []string{"概览", "会话", "工具"} {
		DrawTab(c, Rect{X: tx, Y: y - int32(Scaled(6)), W: int32(Scaled(72)), H: int32(Scaled(34))}, name, i == 0, StateSelected)
		tx += int32(Scaled(76))
	}
	bx := tx + int32(Scaled(Space3))
	DrawBadge(c, Rect{X: bx, Y: y, W: int32(Scaled(56)), H: int32(Scaled(20))}, "默认", KindBadge)
	DrawBadge(c, Rect{X: bx + int32(Scaled(64)), Y: y, W: int32(Scaled(56)), H: int32(Scaled(20))}, "通过", KindBadgeSuccess)
	DrawBadge(c, Rect{X: bx + int32(Scaled(128)), Y: y, W: int32(Scaled(56)), H: int32(Scaled(20))}, "待确认", KindBadgeWarn)
	DrawBadge(c, Rect{X: bx + int32(Scaled(192)), Y: y, W: int32(Scaled(56)), H: int32(Scaled(20))}, "失败", KindBadgeDanger)
	y += int32(Scaled(Space6))
	listW := int32(Scaled(320))
	DrawListItem(c, Rect{X: pad, Y: y, W: listW, H: int32(Scaled(ListRowH))}, "默认行", "12:04", false, StateDefault)
	DrawListItem(c, Rect{X: pad, Y: y + int32(Scaled(ListRowH)), W: listW, H: int32(Scaled(ListRowH))}, "悬停行", "12:05", false, StateHover)
	DrawListItem(c, Rect{X: pad, Y: y + int32(Scaled(ListRowH))*2, W: listW, H: int32(Scaled(ListRowH))}, "选中行", "12:06", true, StateSelected)
	// 右侧：卡片 + 抽屉缩略
	cardX := pad + listW + int32(Scaled(Space5))
	DrawCard(c, Rect{X: cardX, Y: y - int32(Scaled(Space2)), W: int32(Scaled(280)), H: int32(Scaled(96))}, "卡片标题")
	DrawDrawer(c, Rect{X: cardX + int32(Scaled(296)), Y: y - int32(Scaled(Space2)), W: int32(Scaled(200)), H: int32(Scaled(140))}, "抽屉")
	y += int32(Scaled(ListRowH))*3 + int32(Scaled(Space6))

	// ── 表格（右侧留白用来放通知/提示组，避免纵向溢出） ──
	tableTopY := y
	c.TextFace(pad, y, "Table", bodySize, 700, tk.TextSecondary, FaceUI)
	y += int32(Scaled(Space5))
	cols := []TableCol{
		{Title: "工具", W: int32(Scaled(180))},
		{Title: "次数", W: int32(Scaled(72)), Right: true},
		{Title: "P95", W: int32(Scaled(96)), Right: true},
		{Title: "状态", W: int32(Scaled(120))},
	}
	tableW := int32(0)
	for _, col := range cols {
		tableW += col.W + int32(Scaled(Space2))
	}
	DrawTableHeader(c, Rect{X: pad, Y: y, W: tableW, H: int32(Scaled(32))}, cols)
	y += int32(Scaled(32))
	rows := [][]string{
		{"write_file", "6", "3785ms", "成功"},
		{"apply_patch", "3", "5028ms", "1 次失败"},
		{"run_command", "17", "4051ms", "成功"},
	}
	for i, row := range rows {
		DrawTableRow(c, Rect{X: pad, Y: y, W: tableW, H: int32(Scaled(ListRowH))}, row, cols, i%2 == 1, false)
		y += int32(Scaled(ListRowH))
	}
	y += int32(Scaled(Space5))

	// ── 通知 / 提示 / 分隔条 / 滚动条 ──
	// 放在表格右侧的空白带：纵向继续堆会越过状态栏（800 高窗口装不下）。
	tx0 := pad + tableW + int32(Scaled(Space5))
	c.TextFace(tx0, tableTopY, "Toast / Tooltip / Splitter", bodySize, 700, tk.TextSecondary, FaceUI)
	ty := tableTopY + int32(Scaled(Space5)) + int32(Scaled(Space1))
	toastH := int32(Scaled(36))
	toastW := int32(Scaled(240))
	DrawToast(c, Rect{X: tx0, Y: ty, W: toastW, H: toastH}, "回答已完成", KindToastSuccess)
	DrawToast(c, Rect{X: tx0, Y: ty + toastH + int32(Scaled(Space2)), W: toastW, H: toastH}, "需要授权：write_file", KindToastWarn)
	DrawToast(c, Rect{X: tx0, Y: ty + (toastH+int32(Scaled(Space2)))*2, W: toastW, H: toastH}, "写入失败：磁盘不足", KindToastDanger)
	// 提示气泡放到分隔条右侧同一水平带：再往下会压到状态栏。
	DrawTooltip(c, Rect{X: tx0 + int32(Scaled(276)), Y: ty, W: int32(Scaled(156)), H: int32(Scaled(26))}, "提示气泡 12px")
	DrawSplitter(c, Rect{X: tx0 + int32(Scaled(220)), Y: ty, W: int32(Scaled(8)), H: int32(Scaled(120))}, true)
	DrawScrollbar(c, Rect{X: tx0 + int32(Scaled(250)), Y: ty, W: int32(Scaled(8)), H: int32(Scaled(120))}, ty, int32(Scaled(40)), true)

	// ── 状态栏（贴底） ──
	DrawStatusBar(c, Rect{X: 0, Y: h - int32(Scaled(StatusH)), W: w, H: int32(Scaled(StatusH))},
		"组件矩阵 · 全部状态可截", "Quiet Instrument")
}
