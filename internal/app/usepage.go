package app

import (
	"fmt"

	"win7harness/internal/core"
	"win7harness/internal/ui"
	"win7harness/internal/ui/kit"
)

// 用量页：本次会话花了多少、花在哪儿、工具慢在哪儿。

const (
	useTabOverview = iota
	useTabCost
	useTabPerf
)

func (a *App) paintUsage(c *ui.Canvas) {
	th := a.th
	u := a.ui
	W, H := a.winW, a.winH

	c.FillRect(ui.R(0, 0, W, H), th.Bg)
	a.paintRail(u, H)

	if a.paintPageHeader(u, "use.back", "用量与性能", "token、调用次数与响应时间") {
		a.page = PageChat
	}

	// kit 内容区
	ui.SetTokens(a.th.Tokens())
	ktx := a.kit()
	ktx.Painter = kit.NewCanvasAdapter(c)
	ktx.BeginFrame()

	tabs := []struct {
		id    string
		label string
		idx   int
	}{
		{"use.tab.o", "概览", useTabOverview},
		{"use.tab.c", "成本分项", useTabCost},
		{"use.tab.p", "工具耗时", useTabPerf},
	}
	tabEls := make([]kit.Element, 0, len(tabs))
	for i, t := range tabs {
		j := i
		id := t.id
		lbl := t.label
		idx := t.idx
		kind := kit.ButtonSecondary
		if a.useTab == idx {
			kind = kit.ButtonPrimary
		}
		tabEls = append(tabEls, kit.Button(id, lbl, kind, func() { a.useTab = tabs[j].idx }))
	}

	var body kit.Element
	if a.eng == nil {
		body = kit.Column(12,
			usageEmpty(ktx, "模型还没配置", "配好模型并聊过一轮之后，这里才有数据。"),
			kit.Row(10, kit.Spacer(),
				kit.Button("use.setup", "去配置模型", kit.ButtonPrimary, func() { a.page = PageSettings }),
				kit.Spacer()),
		)
	} else if a.useTab == useTabCost {
		body = a.usageCostBody(ktx)
	} else if a.useTab == useTabPerf {
		body = a.usagePerfBody(ktx)
	} else {
		body = a.usageOverviewBody(ktx)
	}

	content := kit.Column(10,
		kit.Row(8, tabEls...),
		body,
	)
	area := kit.R(a.railW+16, 66, W-a.railW-32, H-66-8)
	scroll := kit.Scroll("use:main", 0, content)
	scroll.Arrange(ktx, area)
	scroll.Paint(ktx, ktx.Painter)
}

func usageEmpty(ktx *kit.Ctx, title, hint string) kit.Element {
	return kit.Column(8,
		kit.Spacer(),
		kit.Label(title, ui.FsMd, 700, ui.CurrentTokens().TextTertiary),
		kit.Label(hint, ui.FsXs, 400, ui.CurrentTokens().TextTertiary),
	)
}

func (a *App) usageOverviewBody(ktx *kit.Ctx) kit.Element {
	snap := a.eng.CostSnapshot()

	// 主数字：本次会话花费
	header := kit.Column(2,
		kit.Label(core.Money(snap.Cost), ui.FsXl, 700, ui.CurrentTokens().TextPrimary),
		kit.Label(fmt.Sprintf("%d 轮模型调用 · 输入 %s · 输出 %s token",
			snap.Turns, humanTokens(snap.TokIn), humanTokens(snap.TokOut)), ui.FsSm, 400, ui.CurrentTokens().TextSecondary),
	)
	if snap.HitRate > 0 {
		header = kit.Column(2,
			header,
			kit.Label(fmt.Sprintf("提示缓存命中 %.0f%%（最近一次 %.0f%%）", snap.HitRate*100, snap.LastHit*100), ui.FsXs, 400, ui.CurrentTokens().TextTertiary),
		)
	}

	// 预算进度条（如有）
	var budgetRows []kit.Element
	cfg := a.cfg
	if cfg != nil {
		if cfg.MaxCost > 0 {
			budgetRows = append(budgetRows, kit.Column(2,
				kit.Row(6, kit.Label("花费上限", ui.FsXs, 400, ui.CurrentTokens().TextTertiary)),
				kit.Label(core.Money(snap.Cost)+" / "+core.Money(cfg.MaxCost), ui.FsSm, 700, ui.CurrentTokens().TextPrimary),
			))
		}
		if cfg.BudgetTokens > 0 {
			budgetRows = append(budgetRows, kit.Column(2,
				kit.Row(6, kit.Label("Token 上限", ui.FsXs, 400, ui.CurrentTokens().TextTertiary)),
				kit.Label(humanTokens(snap.TokIn+snap.TokOut)+" / "+humanTokens(cfg.BudgetTokens), ui.FsSm, 700, ui.CurrentTokens().TextPrimary),
			))
		}
	}
	if len(budgetRows) == 0 {
		budgetRows = append(budgetRows, kit.Label("未设置预算上限。去设置页「预算与限制」填一个花费上限。", ui.FsXs, 400, ui.CurrentTokens().TextTertiary))
	}
	budgetGroup := kit.Column(6, budgetRows...)

	// 三段分项（主对话 / 快速模型 / 子代理）
	parts := []struct {
		label string
		turns int
		in    int
		out   int
		cost  float64
		sub   string
	}{
		{"主对话", snap.Turns, snap.TokIn, snap.TokOut, snap.Cost, snap.Model},
		{"快速模型", snap.FastTurns, snap.FastTokIn, snap.FastTokOut, snap.FastCost, snap.FastModel},
		{"子代理", snap.SubTurns, snap.SubTokIn, snap.SubTokOut, snap.SubCost, "task 派发"},
	}
	partCards := make([]kit.Element, 0, len(parts))
	for _, p := range parts {
		note := p.sub
		if note == "" {
			note = "未设置"
		}
		partCards = append(partCards, kit.Expanded(kit.Card(10, kit.Column(4,
			kit.Label(p.label, ui.FsXs, 400, ui.CurrentTokens().TextTertiary),
			kit.Label(core.Money(p.cost), ui.FsMd, 700, ui.CurrentTokens().TextPrimary),
			kit.Label(fmt.Sprintf("%d 轮 · %s in / %s out", p.turns, humanTokens(p.in), humanTokens(p.out)), ui.FsXs, 400, ui.CurrentTokens().TextTertiary),
			kit.Label(note, ui.FsXs, 400, ui.CurrentTokens().TextTertiary),
		))))
	}
	grid := kit.Row(12, partCards...)

	return kit.Column(12,
		header,
		kit.Card(12, budgetGroup),
		kit.Label("花在哪儿", ui.FsSm, 700, ui.CurrentTokens().TextSecondary),
		grid,
	)
}

func (a *App) usageCostBody(ktx *kit.Ctx) kit.Element {
	snap := a.eng.CostSnapshot()

	// 表
	headerRow := []string{"项目", "轮次", "输入 token", "输出 token", "费用"}
	rows := [][]string{
		{"主对话", fmt.Sprint(snap.Turns), humanTokens(snap.TokIn), humanTokens(snap.TokOut), core.Money(snap.Cost)},
		{"快速模型", fmt.Sprint(snap.FastTurns), humanTokens(snap.FastTokIn), humanTokens(snap.FastTokOut), core.Money(snap.FastCost)},
		{"子代理", fmt.Sprint(snap.SubTurns), humanTokens(snap.SubTokIn), humanTokens(snap.SubTokOut), core.Money(snap.SubCost)},
		{"合计", fmt.Sprint(snap.Turns + snap.FastTurns + snap.SubTurns),
			humanTokens(snap.TokIn + snap.FastTokIn + snap.SubTokIn),
			humanTokens(snap.TokOut + snap.FastTokOut + snap.SubTokOut),
			core.Money(snap.Cost + snap.FastCost + snap.SubCost)},
	}
	table := usageSimpleTable(headerRow, rows)

	yRows := []kit.Element{
		kit.Label("缓存与路由", ui.FsSm, 700, ui.CurrentTokens().TextSecondary),
		kit.Label(fmt.Sprintf("提示缓存命中 %.0f%%（最近一次 %.0f%%）", snap.HitRate*100, snap.LastHit*100), ui.FsXs, 400, ui.CurrentTokens().TextSecondary),
	}
	if snap.CostPlain > snap.Cost {
		yRows = append(yRows, kit.Label(fmt.Sprintf("缓存为你省下 %s（不命中时约 %s）",
			core.Money(snap.CostPlain-snap.Cost), core.Money(snap.CostPlain)), ui.FsXs, 400, ui.CurrentTokens().TextSecondary))
	}
	if snap.RouterOn {
		yRows = append(yRows, kit.Label(fmt.Sprintf("模型路由：开启（轻任务走 %s，已省 %s）", snap.FastModel, core.Money(snap.SavedByFast)), ui.FsXs, 400, ui.CurrentTokens().TextSecondary))
	} else {
		yRows = append(yRows, kit.Label("模型路由：关闭（/model router 可开启，压缩与只读子代理会走便宜模型）", ui.FsXs, 400, ui.CurrentTokens().TextTertiary))
	}
	yRows = append(yRows, kit.Label("费用按配置里的单价估算（设置页「模型」卡），不是账单；实际以服务商结算为准。", ui.FsXs, 400, ui.CurrentTokens().TextTertiary))

	cacheBlock := kit.Column(6, yRows...)

	return kit.Column(12, table, cacheBlock)
}

func (a *App) usagePerfBody(ktx *kit.Ctx) kit.Element {
	data := a.uiData()
	snap := data.metrics

	headerRow := []string{"工具", "调用", "失败", "P50", "P95", "最慢"}
	rows := [][]string{}
	for i, st := range snap.Stats {
		if i >= 12 {
			break
		}
		rows = append(rows, []string{
			st.Name, fmt.Sprint(st.Count), fmt.Sprint(st.Fail),
			fmt.Sprintf("%dms", st.P50), fmt.Sprintf("%dms", st.P95), fmt.Sprintf("%dms", st.Max),
		})
	}

	var table kit.Element
	if len(rows) == 0 {
		table = kit.Column(6,
			kit.Label("还没有工具耗时数据", ui.FsSm, 700, ui.CurrentTokens().TextSecondary),
			kit.Label("跑几次工具调用之后这里会按 P95 从慢到快列出来。", ui.FsXs, 400, ui.CurrentTokens().TextTertiary),
		)
	} else {
		table = usageSimpleTable(headerRow, rows)
	}

	info := fmt.Sprintf("进程：%d MB · 系统占用 %d MB · 协程 %d · GC %d 次",
		data.heapMB, data.sysMB, data.goroutines, data.gcCycles)
	return kit.Column(8,
		table,
		kit.Label(info, ui.FsXs, 400, ui.CurrentTokens().TextTertiary),
		kit.Label("渲染帧成本：设环境变量 WIN7H_PROFILE=1 启动，日志每 3 秒一行。", ui.FsXs, 400, ui.CurrentTokens().TextTertiary),
	)
}

// usageSimpleTable 画一个等宽对齐的表格（Kit 实现）。
func usageSimpleTable(header []string, rows [][]string) kit.Element {
	lines := []kit.Element{}
	for ri, row := range append([][]string{header}, rows...) {
		cells := []kit.Element{}
		for ci, cell := range row {
			if ci == 0 {
				weight := int32(400)
				col := ui.CurrentTokens().TextPrimary
				if ri == 0 {
					weight = 700
					col = ui.CurrentTokens().TextSecondary
				}
				cells = append(cells, kit.Label(cell, ui.FsXs, weight, col))
				continue
			}
			weight := int32(400)
			col := ui.CurrentTokens().TextPrimary
			if ri == 0 {
				weight = 700
				col = ui.CurrentTokens().TextSecondary
			}
			cells = append(cells, kit.Label(cell, ui.FsXs, weight, col))
		}
		line := kit.Row(0, cells...)
		if ri == 0 {
			lines = append(lines, kit.Divider(1, ui.CurrentTokens().BorderHairline, 0), line, kit.Divider(1, ui.CurrentTokens().BorderHairline, 0))
			continue
		}
		lines = append(lines, line)
	}
	return kit.Column(4, lines...)
}

// humanTokens 把 token 数写成 1.2k / 3.4M。
func humanTokens(n int) string {
	switch {
	case n >= 1000000:
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
	case n >= 1000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	default:
		return fmt.Sprint(n)
	}
}
