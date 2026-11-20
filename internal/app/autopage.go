package app

import (
	"fmt"
	"time"

	"win7harness/internal/core"
	"win7harness/internal/tools"
	"win7harness/internal/ui"
	"win7harness/internal/ui/kit"
)

const (
	autoTabQueue = iota
	autoTabSched
	autoTabWatch
	autoTabBg
)

func (a *App) paintAuto(c *ui.Canvas) {
	th := a.th
	u := a.ui
	W, H := a.winW, a.winH

	c.FillRect(ui.R(0, 0, W, H), th.Bg)
	a.paintRail(u, H)

	// 顶栏
	c.FillRect(ui.R(a.railW, 0, W-a.railW, 56), ui.Blend(th.Bg, th.Surface, 0.6))
	c.HLine(a.railW, 55, W-a.railW, th.BorderSoft)
	DrawLogo(c, a.railW+24, 16, 24, th.Accent, th.Bg)
	c.TextFace(a.railW+56, 18, "自动化", ui.FsLg, 700, th.Text, ui.FaceUI)
	if u.Button("auto.back", "← 返回对话", ui.R(W-140, 14, 116, 30), BtnGhost) {
		a.page = PageChat
	}

	// kit 内容区
	ui.SetTokens(a.th.Tokens())
	ktx := a.kit()
	ktx.Painter = kit.NewCanvasAdapter(c)
	ktx.BeginFrame()

	// 选项卡（带计数）
	tabs := []struct {
		id, label string
		idx       int
	}{
		{"auto.tab.q", "任务队列", autoTabQueue},
		{"auto.tab.s", "定时任务", autoTabSched},
		{"auto.tab.w", "目录守护", autoTabWatch},
		{"auto.tab.b", "后台任务", autoTabBg},
	}
	counts := []int{a.queueLen(), a.schedLen(), a.watchLen(), tools.BackgroundCount()}
	tabEls := make([]kit.Element, 0, len(tabs))
	for i, t := range tabs {
		i := i
		lbl := t.label
		if counts[i] > 0 {
			lbl = fmt.Sprintf("%s · %d", t.label, counts[i])
		}
		tabEls = append(tabEls, kit.Button(t.id, lbl, kit.ButtonSecondary, func() { a.autoTab = tabs[i].idx }))
	}

	// 主体：按 tab 渲染列表
	var body kit.Element
	switch a.autoTab {
	case autoTabQueue:
		body = a.autoQueueBody(ktx)
	case autoTabSched:
		body = a.autoSchedBody(ktx)
	case autoTabBg:
		body = a.autoBackgroundBody(ktx)
	default:
		body = a.autoWatchBody(ktx)
	}

	content := kit.Column(10,
		kit.Row(8, tabEls...),
		body,
	)
	area := kit.R(a.railW+16, 66, W-a.railW-32, H-66-8)
	content.Arrange(ktx, area)
	content.Paint(ktx, ktx.Painter)
}

func autoEmptyKit(ktx *kit.Ctx, title, hint string) kit.Element {
	return kit.Column(8, kit.Spacer(), kit.Label(title, ui.FsMd, 700, ui.CurrentTokens().TextTertiary),
		kit.Label(hint, ui.FsXs, 400, ui.CurrentTokens().TextTertiary))
}

func (a *App) autoQueueBody(ktx *kit.Ctx) kit.Element {
	if a.v29 == nil || a.v29.queue == nil {
		return autoEmptyKit(ktx, "队列还没初始化", "重启程序后再试")
	}
	items := a.v29.queue.List()
	if len(items) == 0 {
		return autoEmptyKit(ktx, "队列是空的",
			"定时任务、目录守护到点后、以及手机端发来的指令都会先在这里排队，等引擎空出来再执行")
	}
	src := map[string]string{"schedule": "定时", "watch": "守护", "remote": "手机", "manual": "手动"}
	srcLabel := map[string]string{}
	rows := []kit.Element{}
	for _, it := range items {
		it := it
		s := src[it.Source]
		if s == "" {
			s = it.Source
		}
		sub := fmt.Sprintf("%s · 入队 %s", s, it.At.Format("15:04:05"))
		if it.Note != "" {
			sub += " · " + it.Note
		}
		rows = append(rows, kit.Row(8,
			kit.Column(2,
				kit.Label(it.Prompt, ui.FsSm, 700, ui.CurrentTokens().TextPrimary),
				kit.Label(sub, ui.FsXs, 400, ui.CurrentTokens().TextTertiary),
			),
			kit.Spacer(),
			kit.Button("auto.q.del:"+it.ID, "移除", kit.ButtonGhost, func() {
				a.v29.queue.Remove(it.ID)
				a.Redraw()
			}),
		))
	}
	rows = append(rows, kit.Left(kit.Button("auto.q.clear", "清空队列", kit.ButtonGhost, func() {
		n := a.v29.queue.Clear()
		a.toast(fmt.Sprintf("已清空队列，丢弃 %d 个任务", n), false)
		a.Redraw()
	})))
	_ = srcLabel
	return kit.Scroll("auto:queue", 0, kit.Column(6, rows...))
}

func (a *App) autoSchedBody(ktx *kit.Ctx) kit.Element {
	if a.v27 == nil || a.v27.sched == nil {
		return autoEmptyKit(ktx, "定时任务未初始化", "重启程序后再试")
	}
	a.v27.schedMu.Lock()
	prompts := make([]string, 0, len(a.v27.sched.Items))
	metas := make([]string, 0, len(a.v27.sched.Items))
	ids := make([]string, 0, len(a.v27.sched.Items))
	ens := make([]bool, 0, len(a.v27.sched.Items))
	for _, s := range a.v27.sched.Items {
		prompts = append(prompts, s.Prompt)
		metas = append(metas, s.Describe())
		ids = append(ids, s.ID)
		ens = append(ens, s.Enabled)
	}
	a.v27.schedMu.Unlock()

	if len(prompts) == 0 {
		return autoEmptyKit(ktx, "还没有定时任务",
			"在聊天里用 /loop every 30m 巡检一次构建 就能登记一条；到点后任务先进队列，不会插队")
	}
	nextID, nextPrompt, nextMeta, nextEn, nextIDs, nextOn := "", "", "", false, []string{}, []bool{}
	_ = nextID
	_ = nextPrompt
	_ = nextMeta
	_ = nextEn
	_ = nextIDs
	_ = nextOn

	rows := []kit.Element{}
	for i := range prompts {
		i := i
		en := ens[i]
		toggle := "启用"
		if en {
			toggle = "停用"
		}
		labelColor := ui.CurrentTokens().TextTertiary
		if en {
			labelColor = ui.CurrentTokens().StateSuccess
		}
		rows = append(rows, kit.Row(8,
			kit.Column(2,
				kit.Label(prompts[i], ui.FsSm, 700, ui.CurrentTokens().TextPrimary),
				kit.Label(metas[i], ui.FsXs, 400, labelColor),
			),
			kit.Spacer(),
			kit.Button("auto.s.tog:"+ids[i], toggle, kit.ButtonSecondary, func() {
				a.v27.schedMu.Lock()
				for _, s := range a.v27.sched.Items {
					if s.ID == ids[i] {
						s.Enabled = !s.Enabled
						if s.Enabled {
							s.NextRun = s.NextRunAfter(time.Now())
						}
					}
				}
				err := a.v27.sched.Save()
				a.v27.schedMu.Unlock()
				if err != nil {
					a.toast("保存失败："+err.Error(), true)
				}
				a.Redraw()
			}),
			kit.Button("auto.s.del:"+ids[i], "删除", kit.ButtonGhost, func() {
				a.v27.schedMu.Lock()
				out := a.v27.sched.Items[:0]
				for _, s := range a.v27.sched.Items {
					if s.ID != ids[i] {
						out = append(out, s)
					}
				}
				a.v27.sched.Items = out
				err := a.v27.sched.Save()
				a.v27.schedMu.Unlock()
				if err != nil {
					a.toast("保存失败："+err.Error(), true)
				} else {
					a.toast("已删除定时任务", false)
				}
				a.Redraw()
			}),
		))
	}
	return kit.Scroll("auto:sched", 0, kit.Column(6, rows...))
}

func (a *App) autoWatchBody(ktx *kit.Ctx) kit.Element {
	if a.v27 == nil || a.v27.watch == nil {
		return autoEmptyKit(ktx, "目录守护未初始化", "重启程序后再试")
	}
	a.v27.watchMu.Lock()
	type row struct {
		id, title, sub string
		on             bool
	}
	rows := make([]row, 0, len(a.v27.watch.Items))
	for _, r := range a.v27.watch.Items {
		sub := "改了 " + r.Path + " 就叫我"
		if len(r.Patterns) > 0 {
			sub += "（只看 " + r.Patterns[0] + "）"
		}
		if r.Fires > 0 {
			sub += fmt.Sprintf(" · 已触发 %d 次", r.Fires)
		}
		rows = append(rows, row{id: r.ID, title: r.Prompt, sub: sub, on: r.Enabled})
	}
	a.v27.watchMu.Unlock()

	if len(rows) == 0 {
		return autoEmptyKit(ktx, "还没有守护规则",
			"在聊天里用 /watch 路径 就能登记：指定目录有改动时自动叫模型看一眼")
	}
	outRows := []kit.Element{}
	for i := range rows {
		i := i
		r := rows[i]
		color := ui.CurrentTokens().TextTertiary
		if r.on {
			color = ui.CurrentTokens().StateSuccess
		}
		toggle := "启用"
		if r.on {
			toggle = "停用"
		}
		outRows = append(outRows, kit.Row(8,
			kit.Column(2,
				kit.Label(r.title, ui.FsSm, 700, ui.CurrentTokens().TextPrimary),
				kit.Label(r.sub, ui.FsXs, 400, color),
			),
			kit.Spacer(),
			kit.Button("auto.w.tog:"+r.id, toggle, kit.ButtonSecondary, func() {
				a.v27.watchMu.Lock()
				a.v27.watch.SetEnabled(r.id, !r.on, time.Now())
				a.v27.watchMu.Unlock()
				a.Redraw()
			}),
			kit.Button("auto.w.del:"+r.id, "删除", kit.ButtonGhost, func() {
				a.v27.watchMu.Lock()
				a.v27.watch.Remove(r.id)
				a.v27.watchMu.Unlock()
				a.toast("已删除守护规则", false)
				a.Redraw()
			}),
		))
	}
	return kit.Scroll("auto:watch", 0, kit.Column(6, outRows...))
}

func (a *App) autoBackgroundBody(ktx *kit.Ctx) kit.Element {
	all := tools.BackgroundTasks()
	if len(all) == 0 {
		return autoEmptyKit(ktx, "没有后台任务",
			"模型执行 run_command background=true 时，dev server、watch、长编译会出现在这里；可查看状态并一键终止。")
	}
	rows := []kit.Element{}
	for _, t := range all {
		t := t
		tone := ui.CurrentTokens().StateSuccess
		state := t.Status
		if !t.Done {
			tone = ui.CurrentTokens().StateWarn
		} else if t.Killed {
			tone = ui.CurrentTokens().TextTertiary
		}
		sub := fmt.Sprintf("%s · 启动 %s · 输出 %s", state, t.Started.Format("15:04:05"), core.HumanSize(t.Bytes))
		if !t.Ended.IsZero() {
			sub += " · 结束 " + t.Ended.Format("15:04:05")
		}
		if t.Note != "" {
			sub += " · " + t.Note
		}
		var action kit.Element = kit.Spacer()
		if !t.Done {
			action = kit.Button("auto.bg.kill:"+t.ID, "终止", kit.ButtonGhost, func() {
				if tools.KillBackground(t.ID) {
					a.toast("已发出终止信号："+t.ID, false)
				} else {
					a.toast("任务已结束或不存在："+t.ID, true)
				}
				a.Redraw()
			})
		}
		rows = append(rows, kit.Column(4,
			kit.Row(8,
				kit.Column(2,
					kit.Label(t.Cmdline, ui.FsSm, 700, ui.CurrentTokens().TextPrimary),
					kit.Label(sub, ui.FsXs, 400, tone),
				),
				kit.Spacer(),
				action,
			),
			kit.Divider(1, ui.CurrentTokens().BorderHairline, 0),
		))
	}
	return kit.Scroll("auto:bg", 0, kit.Column(6, rows...))
}

func (a *App) queueLen() int {
	if a.v29 == nil || a.v29.queue == nil {
		return 0
	}
	return a.v29.queue.Len()
}

func (a *App) schedLen() int {
	if a.v27 == nil || a.v27.sched == nil {
		return 0
	}
	a.v27.schedMu.Lock()
	defer a.v27.schedMu.Unlock()
	return len(a.v27.sched.Items)
}

func (a *App) watchLen() int {
	if a.v27 == nil || a.v27.watch == nil {
		return 0
	}
	a.v27.watchMu.Lock()
	defer a.v27.watchMu.Unlock()
	return len(a.v27.watch.Items)
}
