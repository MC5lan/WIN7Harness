package app

import (
	"fmt"
	"path/filepath"
	"sort"
	"time"

	"win7harness/internal/core"
	"win7harness/internal/ui"
	"win7harness/internal/ui/kit"
	"win7harness/internal/win32"
)

// 通知中心：把"你不在的几分钟里发生了什么"汇总在一页上。
//
// 数据全部来自既有记录，不新增落盘格式：
//   - 最近通知：本次会话的 toast 环形缓冲（tstHistory，错误级标红）
//   - 自动化足迹：定时任务 / 目录守护的落盘状态（上次触发、触发次数、是否启用）
//   - 异常报告：数据目录 crash/ 下的报告（新→旧）
//
// 为什么做成页面而不是继续 /notices 打印文字：文字在对话里看完就滚没了，
// 还顺手消耗模型上下文；页面能看全、能点（清空通知 / 打开报告目录 / 打开报告），
// 和扩展页、自动化页同一套 kit 组件与滚动。
func (a *App) paintNotices(c *ui.Canvas) {
	th := a.th
	u := a.ui
	W, H := a.winW, a.winH

	c.FillRect(ui.R(0, 0, W, H), th.Bg)
	a.paintRail(u, H)
	c.FillRect(ui.R(a.railW, 0, W-a.railW, 56), ui.Blend(th.Bg, th.Surface, 0.6))
	c.HLine(a.railW, 55, W-a.railW, th.BorderSoft)
	DrawLogo(c, a.railW+24, 16, 24, th.Accent, th.Bg)
	c.TextFace(a.railW+56, 18, "通知中心", ui.FsLg, 700, th.Text, ui.FaceUI)
	if u.Button("notice.back", "← 返回对话", ui.R(W-140, 14, 116, 30), BtnGhost) {
		a.page = PageChat
	}

	// kit 内容区
	ui.SetTokens(a.th.Tokens())
	ktx := a.kit()
	ktx.Painter = kit.NewCanvasAdapter(c)
	ktx.BeginFrame()

	areaW := W - a.railW - 32
	tk := ui.CurrentTokens()
	elems := []kit.Element{}

	// 启动恢复提示（如有）：上次异常退出时，这是最该先看到的一条。
	if s := core.StartupWarning(); s != "" {
		elems = append(elems, kit.Card(10, kit.Column(4,
			kit.Label("上次异常退出", ui.FsXs, 400, tk.StateWarn),
			kit.Label(kit.TextTrunc(ktx, s, ui.FsSm, 700, areaW-40, ui.FaceUI), ui.FsSm, 700, tk.TextPrimary),
		)))
	}

	// ---------- 最近通知 ----------
	hist := a.tstHistory
	elems = append(elems, kit.Row(8,
		kit.Label(fmt.Sprintf("最近通知（%d 条 · 本次会话）", len(hist)), ui.FsSm, 700, tk.TextSecondary),
		kit.Spacer(),
		kit.Button("notice.clear", "清空", kit.ButtonGhost, func() {
			a.tstHistory = nil
			a.Redraw()
		}),
	))
	if len(hist) == 0 {
		elems = append(elems, kit.Label("还没有通知。任务完成、失败、守护触发、定时运行都会记在这里。", ui.FsXs, 400, tk.TextTertiary))
	} else {
		for i := len(hist) - 1; i >= 0; i-- {
			t := hist[i]
			mark := "·"
			markCol := tk.TextTertiary
			if t.Err {
				mark = "!"
				markCol = tk.StateDanger
			}
			elems = append(elems, noticeHistRow(ktx, areaW, mark, t.Text, markCol, t.At))
		}
	}
	elems = append(elems, kit.Divider(1, tk.BorderHairline, 6))

	// ---------- 自动化足迹 ----------
	elems = append(elems, kit.Row(8,
		kit.Label("自动化足迹", ui.FsSm, 700, tk.TextSecondary),
		kit.Spacer(),
		kit.Button("notice.toauto", "去自动化页", kit.ButtonGhost, func() { a.page = PageAuto }),
	))
	if a.v27 == nil {
		elems = append(elems, kit.Label("自动化尚未初始化，重启程序后再试。", ui.FsXs, 400, tk.TextTertiary))
	} else {
		if a.v27.sched != nil {
			a.v27.schedMu.Lock()
			sched := make([]core.Schedule, 0, len(a.v27.sched.Items))
			for _, s := range a.v27.sched.Items {
				sched = append(sched, *s)
			}
			a.v27.schedMu.Unlock()
			elems = append(elems, a.noticeSchedRows(ktx, areaW, sched)...)
		} else {
			elems = append(elems, kit.Label("定时任务表未初始化。", ui.FsXs, 400, tk.TextTertiary))
		}
		if a.v27.watch != nil {
			a.v27.watchMu.Lock()
			watches := make([]core.WatchRule, 0, len(a.v27.watch.Items))
			for _, w := range a.v27.watch.Items {
				watches = append(watches, *w)
			}
			a.v27.watchMu.Unlock()
			elems = append(elems, a.noticeWatchRows(ktx, areaW, watches)...)
		} else {
			elems = append(elems, kit.Label("守护规则表未初始化。", ui.FsXs, 400, tk.TextTertiary))
		}
	}
	elems = append(elems, kit.Divider(1, tk.BorderHairline, 6))

	// ---------- 异常报告 ----------
	crashList := a.cachedCrashList()
	elems = append(elems, kit.Row(8,
		kit.Label(fmt.Sprintf("异常报告（%d 份）", len(crashList)), ui.FsSm, 700, tk.TextSecondary),
		kit.Spacer(),
		kit.Button("notice.crashdir", "打开报告目录", kit.ButtonGhost, func() {
			revealPath(core.CrashDir(), false)
		}),
	))
	if len(crashList) == 0 {
		elems = append(elems, kit.Label("没有崩溃记录。", ui.FsXs, 400, tk.TextTertiary))
	} else {
		textW := areaW - 14 - 110
		for i, p := range crashList {
			if i >= 5 {
				break
			}
			p := p
			base := filepath.Base(p)
			sub := "大小未知"
			if size, mod := a.cachedCrashMeta(p); size > 0 {
				sub = fmt.Sprintf("%.1f KB · %s", float64(size)/1024, mod.Format("01-02 15:04:05"))
			}
			elems = append(elems, extListRow(ktx, textW, base, sub, tk.TextTertiary,
				kit.Button("notice.crash:"+p, "打开", kit.ButtonSecondary, func() {
					if !win32.ShellOpen(p) {
						a.toast("打不开："+p, true)
					}
				})))
		}
	}

	content := kit.Column(10, elems...)
	scroll := kit.Scroll("notice:main", 0, content)
	area := kit.R(a.railW+16, 66, W-a.railW-32, H-66-8)
	scroll.Arrange(ktx, area)
	scroll.Paint(ktx, ktx.Painter)
}

// noticeHistRow 一条通知：色标点 + 时间 + 文本（过长省略），底部发丝线。
func noticeHistRow(ktx *kit.Ctx, areaW int32, mark, text string, markCol uint32, at time.Time) kit.Element {
	tk := ui.CurrentTokens()
	maxW := areaW - 14 - 6 - 8 - 60 - 8
	if maxW < 40 {
		maxW = 40
	}
	return kit.Column(2,
		kit.Row(8,
			kit.Label(mark, ui.FsXs, 700, markCol),
			kit.LabelFace(at.Format("15:04:05"), ui.FsXs, 400, tk.TextTertiary, ui.FaceMono),
			kit.Label(kit.TextTrunc(ktx, text, ui.FsXs, 400, maxW, ui.FaceUI), ui.FsXs, 400, tk.TextSecondary),
			kit.Spacer(),
		),
		kit.Divider(1, tk.BorderHairline, 0),
	)
}

// schedDesc 定时任务的一行摘要：频率 · 上次运行 · 已跑次数。
// （不复用 core 的 Describe——它带内部 ID，用户不该看见。）
func schedDesc(s core.Schedule) string {
	var when string
	switch {
	case s.EverySec > 0:
		when = "每 " + humanEverySec(s.EverySec)
	case s.DailyAt != "":
		when = "每天 " + s.DailyAt
	default:
		when = "未设置排期"
	}
	last := "从未运行"
	if !s.LastRun.IsZero() {
		last = "上次 " + s.LastRun.Format("01-02 15:04")
	}
	next := ""
	if !s.NextRun.IsZero() {
		next = " · 下次 " + s.NextRun.Format("01-02 15:04")
	}
	return fmt.Sprintf("%s · %s · 已跑 %d 次%s", when, last, s.Runs, next)
}

func humanEverySec(sec int) string {
	switch {
	case sec%86400 == 0:
		return fmt.Sprintf("%d 天", sec/86400)
	case sec%3600 == 0:
		return fmt.Sprintf("%d 小时", sec/3600)
	case sec%60 == 0:
		return fmt.Sprintf("%d 分钟", sec/60)
	default:
		return fmt.Sprintf("%d 秒", sec)
	}
}

// noticeSchedRows 定时任务摘要（按下次触发时间升序，最多 5 条）。
func (a *App) noticeSchedRows(ktx *kit.Ctx, areaW int32, sched []core.Schedule) []kit.Element {
	tk := ui.CurrentTokens()
	out := []kit.Element{kit.Label("定时任务", ui.FsXs, 700, tk.TextTertiary)}
	if len(sched) == 0 {
		out = append(out, kit.Label("没有定时任务。在聊天里用 /loop every 30m 提示词 可以登记一条。", ui.FsXs, 400, tk.TextTertiary))
		return out
	}
	sort.Slice(sched, func(i, j int) bool {
		if sched[i].NextRun.IsZero() {
			return false
		}
		if sched[j].NextRun.IsZero() {
			return true
		}
		return sched[i].NextRun.Before(sched[j].NextRun)
	})
	textW := areaW - 14 - 8
	n := 0
	for _, s := range sched {
		if n >= 5 {
			break
		}
		n++
		sub := schedDesc(s)
		subCol := tk.StateSuccess
		if !s.Enabled {
			sub = "已停用 · " + sub
			subCol = tk.TextTertiary
		}
		out = append(out, extListRow(ktx, textW, s.Prompt, sub, subCol))
	}
	return out
}

// noticeWatchRows 目录守护摘要（按最后触发时间降序，最多 5 条）。
func (a *App) noticeWatchRows(ktx *kit.Ctx, areaW int32, watches []core.WatchRule) []kit.Element {
	tk := ui.CurrentTokens()
	out := []kit.Element{kit.Label("目录守护", ui.FsXs, 700, tk.TextTertiary)}
	if len(watches) == 0 {
		out = append(out, kit.Label("没有守护规则。在聊天里用 /watch 目录 提示词 可以登记一条。", ui.FsXs, 400, tk.TextTertiary))
		return out
	}
	sort.Slice(watches, func(i, j int) bool { return watches[i].LastFire.After(watches[j].LastFire) })
	textW := areaW - 14 - 8
	n := 0
	for _, w := range watches {
		if n >= 5 {
			break
		}
		n++
		var sub string
		subCol := tk.StateSuccess
		if w.LastFire.IsZero() {
			sub = "从未触发"
			subCol = tk.TextTertiary
		} else {
			sub = fmt.Sprintf("触发 %d 次 · 最近 %s", w.Fires, w.LastFire.Format("01-02 15:04"))
		}
		if !w.Enabled {
			sub = "已停用 · " + sub
			subCol = tk.TextTertiary
		}
		out = append(out, extListRow(ktx, textW, w.Prompt, sub, subCol))
	}
	return out
}
