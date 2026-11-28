//go:build windows

package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"win7harness/internal/bootstrap"
	"win7harness/internal/core"
	"win7harness/internal/eval"
	"win7harness/internal/selftest"
	"win7harness/internal/ui"
	"win7harness/internal/ui/kit"
	"win7harness/internal/win32"
)

// diagState 自检与日志页面的状态。自检在后台 goroutine 跑，故加锁。
type diagState struct {
	mu      sync.Mutex
	results []core.CheckResult
	ranAt   time.Time
	busy    bool
	loaded  bool

	st       *selftest.Report
	stBusy   bool
	stAt     time.Time
	stReport string

	evalRep  *eval.Report
	evalBusy bool
	evalAt   time.Time

	tab    int
	scroll float32
	height int32
	logs   []string
}

func (a *App) runChecksAsync() { a.runChecksNotify(false) }

func (a *App) runChecksNotify(notify bool) {
	a.diag.mu.Lock()
	if a.diag.busy {
		a.diag.mu.Unlock()
		return
	}
	a.diag.busy = true
	a.diag.loaded = true
	a.diag.mu.Unlock()

	var cfg *core.Config
	if a.cfg != nil {
		cfg = a.cfg.Clone()
	}
	core.SafeGo("app/diag.go:62", func() {
		res := core.RunChecks(cfg)
		a.diag.mu.Lock()
		a.diag.results = res
		a.diag.ranAt = time.Now()
		a.diag.busy = false
		a.diag.scroll = 0
		a.diag.mu.Unlock()
		core.Logf("自检完成：%d 项", len(res))
		if notify {
			fails, warns := 0, 0
			for _, r := range res {
				switch r.Status {
				case core.CheckFail:
					fails++
				case core.CheckWarn:
					warns++
				}
			}
			a.queueUI(func() {
				switch {
				case fails > 0:
					a.toast("自检发现 "+strconv.Itoa(fails)+" 项异常，按 Ctrl+K 搜“自检”查看", true)
				case warns > 0:
					a.toast("自检完成："+strconv.Itoa(warns)+" 项提醒，其余正常", false)
				default:
					a.toast("环境自检全部通过", false)
				}
			})
		}
		a.Redraw()
	})
}

// loadLogs 读取日志尾部到内存，供日志页展示。
func (a *App) loadLogs() {
	logs := core.LogTail(2000)
	a.diag.mu.Lock()
	a.diag.logs = logs
	a.diag.mu.Unlock()
}

func (a *App) runEndToEndAsync() {
	a.diag.mu.Lock()
	if a.diag.stBusy {
		a.diag.mu.Unlock()
		return
	}
	a.diag.stBusy = true
	a.diag.mu.Unlock()
	a.Redraw()

	cfg := a.cfg.Clone()
	core.SafeGo("app/diag.go:116", func() {
		opt := selftest.Options{
			Timeout: 40 * time.Second,
			ProviderTest: func() error {
				p := cfg.Provider()
				if strings.TrimSpace(p.APIKey) == "" && !isLocalEndpoint(p.BaseURL) {
					return selftest.ErrNotConfigured
				}
				c, _, warn := bootstrap.NewClient(cfg)
				if warn != "" {
					core.Logf("自检：%s", warn)
				}
				if c == nil {
					return selftest.ErrNotConfigured
				}
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				_, err := c.Ping(ctx)
				return err
			},
		}
		rep := selftest.Run(opt)
		md, _, err := rep.WriteReport(selftest.DefaultReportDir())

		a.diag.mu.Lock()
		a.diag.st = rep
		a.diag.stBusy = false
		a.diag.stAt = time.Now()
		a.diag.stReport = md
		a.diag.mu.Unlock()

		core.Logf("端到端自检完成：通过 %d，失败 %d，跳过 %d", rep.Count(selftest.StatusPass), rep.Count(selftest.StatusFail), rep.Count(selftest.StatusSkip))
		a.queueUI(func() {
			if err != nil {
				a.toast("自检报告保存失败："+err.Error(), true)
			} else if rep.OK() {
				a.toast(fmt.Sprintf("自检全部通过（通过率 %.0f%%）", rep.PassRate()), false)
			} else {
				a.toast(fmt.Sprintf("自检发现 %d 项失败，按 Ctrl+K 搜“自检”查看", rep.Count(selftest.StatusFail)), true)
			}
		})
		a.Redraw()

		// 自检完成后顺手把日志尾部载入，供日志 tab 查看
		a.loadLogs()
	})
}

// exportDiag 导出诊断包并在完成后提示路径。
func (a *App) exportDiag() {
	opt := a.withMetricsExtra(core.DefaultDiagOptions())
	core.SafeGo("app/diag.go:169", func() {
		p, err := core.ExportDiag("", opt)
		if err != nil {
			a.queueUI(func() { a.toast("导出失败："+err.Error(), true) })
			return
		}
		a.queueUI(func() { a.toast("诊断包已导出："+p, false) })
	})
}

// revealPath 在资源管理器中选中文件或打开目录。
func revealPath(path string, selectFile bool) {
	if selectFile {
		_ = exec.Command("explorer", "/select,"+path).Start()
		return
	}
	_ = exec.Command("explorer", path).Start()
}

const (
	diagTabChecks   = 0
	diagTabLogs     = 1
	diagTabEndToEnd = 2
	diagTabEval     = 3
	diagTabCrash    = 4
)

func (a *App) paintDiag(c *ui.Canvas) {
	th := a.th
	u := a.ui
	W, H := a.winW, a.winH

	c.FillRect(ui.R(0, 0, W, H), th.Bg)
	// 与其它 kit 页一致：左侧 rail 是全局导航，诊断页也不能少。
	a.paintRail(u, H)
	// 顶栏：统一页头（标题 + 一句用途说明 + 返回对话）
	if a.paintPageHeader(u, "diag.back", "自检与诊断", "检查配置、工具链与运行环境") {
		a.page = PageChat
	}

	// kit 内容区
	ui.SetTokens(a.th.Tokens())
	ktx := a.kit()
	ktx.Painter = kit.NewCanvasAdapter(c)
	ktx.BeginFrame()

	tabs := []struct {
		id  string
		lab string
		idx int
	}{
		{"diag.tab.checks", "环境自检", diagTabChecks},
		{"diag.tab.logs", "运行日志", diagTabLogs},
		{"diag.tab.e2e", "端到端自检", diagTabEndToEnd},
		{"diag.tab.eval", "评测基线", diagTabEval},
		{"diag.tab.crash", "崩溃报告", diagTabCrash},
	}
	tabEls := make([]kit.Element, 0, len(tabs))
	for i, t := range tabs {
		i := i
		id := t.id
		lab := t.lab
		idx := t.idx
		kind := kit.ButtonSecondary
		if a.diag.tab == idx {
			kind = kit.ButtonPrimary
		}
		tabEls = append(tabEls, kit.Button(id, lab, kind, func() {
			a.diag.tab = tabs[i].idx
			a.diag.scroll = 0
			switch tabs[i].idx {
			case diagTabChecks:
				if len(a.diag.results) == 0 {
					a.runChecksAsync()
				}
			case diagTabEval:
				if a.diag.evalRep == nil && !a.diag.evalBusy {
					a.runEvalAsync()
				}
			}
		}))
	}
	tabRow := kit.Row(8, tabEls...)

	var body kit.Element
	switch a.diag.tab {
	case diagTabChecks:
		body = diagChecksBody(ktx, a)
	case diagTabLogs:
		body = a.diagLogsBody(ktx)
	case diagTabEndToEnd:
		body = a.diagEndToEndBody(ktx)
	case diagTabEval:
		body = a.diagEvalBody(ktx)
	case diagTabCrash:
		body = a.diagCrashBody(ktx)
	default:
		body = kit.Label("未知页签", ui.FsSm, 400, ui.CurrentTokens().TextTertiary)
	}

	content := kit.Column(10, tabRow, body)
	area := kit.R(a.railW+16, 66, W-a.railW-32, H-66-8)
	content.Arrange(ktx, area)
	content.Paint(ktx, ktx.Painter)
}

func diagChecksBody(ktx *kit.Ctx, a *App) kit.Element {
	a.diag.mu.Lock()
	res := append([]core.CheckResult(nil), a.diag.results...)
	ranAt := a.diag.ranAt
	busy := a.diag.busy
	a.diag.mu.Unlock()

	head := "正在检测…"
	headerColIdx := 0
	switch {
	case busy || len(res) == 0:
		headerColIdx = -1
	default:
		var fails, warns int
		for _, r := range res {
			if r.Status == core.CheckFail {
				fails++
			} else if r.Status == core.CheckWarn {
				warns++
			}
		}
		switch {
		case fails > 0:
			head = fmt.Sprintf("发现 %d 个阻塞问题", fails)
			headerColIdx = 2
		case warns > 0:
			head = fmt.Sprintf("可以运行，但有 %d 项需要注意", warns)
			headerColIdx = 1
		default:
			head = "环境检查全部通过"
			headerColIdx = 0
		}
	}

	var headEle kit.Element
	subEle := ""
	if !ranAt.IsZero() {
		subEle = "检测于 " + ranAt.Format("15:04:05")
	} else {
		subEle = "…"
	}
	if len(res) == 0 && busy {
		headEle = kit.Label(head, ui.FsMd, 700, ui.CurrentTokens().TextSecondary)
	} else {
		switch headerColIdx {
		case 0:
			subEle = fmt.Sprintf("通过 %d · 注意 %d · 失败 %d", countOK(res), countWarn(res), countFail(res)) + " · " + subEle
		case 1:
			subEle = fmt.Sprintf("通过 %d · 注意 %d · 失败 %d", countOK(res), countWarn(res), countFail(res)) + " · " + subEle
		case -1:
			subEle = "检测中…"
		}
		col := ui.CurrentTokens().TextSecondary
		if headerColIdx == 2 {
			col = ui.CurrentTokens().StateDanger
		} else if headerColIdx == 1 {
			col = ui.CurrentTokens().StateWarn
		} else {
			col = ui.CurrentTokens().StateSuccess
		}
		_ = col
		switch headerColIdx {
		case 0:
			headEle = kit.Label(head, ui.FsMd, 700, ui.CurrentTokens().StateSuccess)
		case 1:
			headEle = kit.Label(head, ui.FsMd, 700, ui.CurrentTokens().StateWarn)
		case 2:
			headEle = kit.Label(head, ui.FsMd, 700, ui.CurrentTokens().StateDanger)
		default:
			headEle = kit.Label(head, ui.FsMd, 700, ui.CurrentTokens().TextSecondary)
		}
	}

	buttons := kit.Row(8,
		kit.Button("diag.run", "重新检测", kit.ButtonSecondary, func() { a.runChecksAsync() }),
		kit.Button("diag.export", "导出诊断包", kit.ButtonPrimary, func() { a.exportDiag() }),
	)

	summary := kit.Column(2,
		kit.Row(8,
			headEle,
			kit.Spacer(),
			buttons,
		),
		kit.Label(subEle, ui.FsXs, 400, ui.CurrentTokens().TextTertiary),
		kit.Divider(1, ui.CurrentTokens().BorderHairline, 0),
	)

	var itemEls []kit.Element
	for _, r := range res {
		itemEls = append(itemEls, diagItemRow(r))
	}

	return kit.Column(10, summary, kit.Scroll("diag:checks", 0, kit.Column(4, itemEls...)))
}

func diagItemRow(r core.CheckResult) kit.Element {
	var col uint32
	var label string
	switch r.Status {
	case core.CheckOK:
		label = "通过"
		col = ui.CurrentTokens().StateSuccess
	case core.CheckWarn:
		label = "注意"
		col = ui.CurrentTokens().StateWarn
	case core.CheckFail:
		label = "失败"
		col = ui.CurrentTokens().StateDanger
	default:
		label = "跳过"
		col = ui.CurrentTokens().TextTertiary
	}
	_ = col
	rows := []kit.Element{
		kit.Row(8,
			kit.Label(label, ui.FsXs, 700, ui.CurrentTokens().TextSecondary),
			kit.Label(r.Name, ui.FsSm, 700, ui.CurrentTokens().TextPrimary),
			kit.Spacer(),
		),
	}
	if r.Detail != "" {
		rows = append(rows, kit.Label(r.Detail, ui.FsXs, 400, ui.CurrentTokens().TextSecondary))
	}
	if r.Hint != "" {
		rows = append(rows, kit.Label("建议："+r.Hint, ui.FsXs, 400, ui.CurrentTokens().TextTertiary))
	}
	rows = append(rows, kit.Divider(1, ui.CurrentTokens().BorderHairline, 0))
	return kit.Column(2, rows...)
}

func countOK(res []core.CheckResult) int {
	n := 0
	for _, r := range res {
		if r.Status == core.CheckOK {
			n++
		}
	}
	return n
}

func countWarn(res []core.CheckResult) int {
	n := 0
	for _, r := range res {
		if r.Status == core.CheckWarn {
			n++
		}
	}
	return n
}

func countFail(res []core.CheckResult) int {
	n := 0
	for _, r := range res {
		if r.Status == core.CheckFail {
			n++
		}
	}
	return n
}

func (a *App) diagLogsBody(ktx *kit.Ctx) kit.Element {
	a.diag.mu.Lock()
	logs := append([]string(nil), a.diag.logs...)
	a.diag.mu.Unlock()

	header := kit.Row(8,
		kit.Label(fmt.Sprintf("日志（%d 行）", len(logs)), ui.FsSm, 700, ui.CurrentTokens().TextSecondary),
		kit.Spacer(),
		kit.Button("diag.log.refresh", "刷新", kit.ButtonGhost, func() { a.loadLogs() }),
		kit.Button("diag.log.dir", "打开目录", kit.ButtonGhost, func() { revealPath(core.LocalDataDir(), false) }),
		kit.Button("diag.log.clear", "清屏", kit.ButtonGhost, func() {
			a.diag.logs = nil
		}),
	)

	lines := []kit.Element{}
	for _, ln := range logs[max(0, len(logs)-220):] {
		lines = append(lines, kit.Label(ln, ui.FsXs, 400, ui.CurrentTokens().TextSecondary))
	}

	return kit.Column(8,
		header,
		kit.Scroll("diag:logs", 0, kit.Column(2, lines...)),
	)
}

func (a *App) diagEndToEndBody(ktx *kit.Ctx) kit.Element {
	a.diag.mu.Lock()
	rep := a.diag.st
	busy := a.diag.stBusy
	ranAt := a.diag.stAt
	reportPath := a.diag.stReport
	a.diag.mu.Unlock()

	header := kit.Row(8,
		kit.Label("端到端自检", ui.FsSm, 700, ui.CurrentTokens().TextSecondary),
		kit.Spacer(),
		kit.Button("e2e.run", "重新自检", kit.ButtonSecondary, func() { a.runEndToEndAsync() }),
		kit.Button("e2e.dir", "打开报告", kit.ButtonPrimary, func() { revealPath(selftest.DefaultReportDir(), false) }),
		kit.Button("e2e.checks", "环境自检", kit.ButtonGhost, func() { a.diag.tab = diagTabChecks }),
	)

	var body kit.Element
	if busy {
		body = kit.Column(6,
			kit.Label("正在跑端到端自检…", ui.FsSm, 700, ui.CurrentTokens().TextSecondary),
			kit.Label("真工具、真文件、真命令，耗时约 10 秒", ui.FsXs, 400, ui.CurrentTokens().TextTertiary),
		)
	} else if rep == nil {
		body = usageEmpty(ktx, "还没有跑过",
			"点上面的“重新自检”跑一次；真调工具、快照、命令，约 10 秒。")
		_ = header
	} else {
		pass := rep.Count(selftest.StatusPass)
		fail := rep.Count(selftest.StatusFail)
		skip := rep.Count(selftest.StatusSkip)
		_ = skip
		sub := fmt.Sprintf("通过 %d · 失败 %d · 跳过 %d · 用时 %.1fs", pass, fail, skip, float64(rep.DurMS)/1000)
		if !ranAt.IsZero() {
			sub += " · " + ranAt.Format("15:04:05")
		}
		head := fmt.Sprintf("通过率 %.0f%%", rep.PassRate())
		col := ui.CurrentTokens().StateSuccess
		if fail > 0 {
			col = ui.CurrentTokens().StateDanger
		}
		// 列表
		rows := []kit.Element{}
		for _, cs := range rep.Cases {
			st := "通过"
			stCol := ui.CurrentTokens().StateSuccess
			switch cs.Status {
			case selftest.StatusFail:
				st = "失败"
				stCol = ui.CurrentTokens().StateDanger
			case selftest.StatusSkip:
				st = "SKIP"
				stCol = ui.CurrentTokens().TextTertiary
			}
			rows = append(rows, kit.Row(6,
				kit.Label(st, ui.FsSm, 700, stCol),
				kit.Label(cs.ID+" "+cs.Name, ui.FsXs, 400, ui.CurrentTokens().TextPrimary),
				kit.Spacer(),
				kit.Label(fmt.Sprintf("%dms", cs.DurMS), ui.FsXs, 400, ui.CurrentTokens().TextTertiary),
			))
		}

		body = kit.Column(8,
			kit.Column(2,
				kit.Label(head, ui.FsLg, 700, col),
				kit.Label(sub, ui.FsSm, 400, ui.CurrentTokens().TextSecondary),
			),
			kit.Divider(1, ui.CurrentTokens().BorderHairline, 0),
			kit.Scroll("diag:e2e", 0, kit.Column(2, rows...)),
		)
		// 报告路径（如有）
		if reportPath != "" {
			body = kit.Column(8,
				body,
				kit.Label("报告文件："+reportPath, ui.FsXs, 400, ui.CurrentTokens().TextTertiary),
			)
		}
	}

	return kit.Column(8, header, body)
}

func (a *App) diagEvalBody(ktx *kit.Ctx) kit.Element {
	d := &a.diag
	d.mu.Lock()
	rep, busy, at := d.evalRep, d.evalBusy, d.evalAt
	d.mu.Unlock()

	header := kit.Row(8,
		kit.Label("评测基线", ui.FsSm, 700, ui.CurrentTokens().TextSecondary),
		kit.Spacer(),
		kit.Button("eval.run", "重新评测", kit.ButtonSecondary, func() { a.runEvalAsync() }),
		kit.Button("eval.report", "打开报告目录", kit.ButtonGhost, func() { revealPath(filepathJoinSafe(core.LocalDataDir(), "eval"), false) }),
	)

	var body kit.Element
	if busy {
		body = kit.Column(6,
			kit.Label("正在跑评测…", ui.FsSm, 700, ui.CurrentTokens().TextSecondary),
			kit.Label("不调模型，压工具链 / 索引 / 技能底座", ui.FsXs, 400, ui.CurrentTokens().TextTertiary),
		)
	} else if rep == nil {
		body = usageEmpty(ktx, "还没跑评测",
			"点“重新评测”，不调模型；跑完看每条用例的结果与悬浮理由。")
	} else {
		rate := rep.Score() * 100
		col := ui.CurrentTokens().StateSuccess
		if rep.Fail > 0 {
			col = ui.CurrentTokens().StateDanger
		} else if rep.Skip > 0 {
			col = ui.CurrentTokens().StateWarn
		}
		headerKm := kit.Column(2,
			kit.Label(fmt.Sprintf("%.1f%%", rate), ui.FsXl, 700, col),
			kit.Label(fmt.Sprintf("通过 %d · 失败 %d · 跳过 %d · 耗时 %.2fs · %s",
				rep.Pass, rep.Fail, rep.Skip, float64(rep.TotalMs)/1000, at.Format("15:04:05")), ui.FsXs, 400, ui.CurrentTokens().TextSecondary),
		)

		lines := []kit.Element{}
		for _, r := range rep.Results {
			mark, tone := "✓", ui.CurrentTokens().StateSuccess
			switch r.Status {
			case "fail":
				mark, tone = "✗", ui.CurrentTokens().StateDanger
			case "skip":
				mark, tone = "○", ui.CurrentTokens().TextTertiary
			}
			lines = append(lines, kit.Row(6,
				kit.Label(mark, ui.FsSm, 700, tone),
				kit.Label(r.ID, ui.FsXs, 400, ui.CurrentTokens().TextTertiary),
				kit.Label(r.Name, ui.FsXs, 400, ui.CurrentTokens().TextPrimary),
				kit.Spacer(),
				kit.Label(fmt.Sprintf("%dms", r.Ms), ui.FsXs, 400, ui.CurrentTokens().TextTertiary),
			))
			if r.Detail != "" && r.Status != "pass" {
				lines = append(lines, kit.Label("    "+r.Detail, ui.FsXs, 400, ui.CurrentTokens().TextTertiary))
			}
		}
		body = kit.Column(8,
			headerKm,
			kit.Divider(1, ui.CurrentTokens().BorderHairline, 0),
			kit.Scroll("diag:eval", 0, kit.Column(2, lines...)),
		)
	}
	return kit.Column(8, header, body)
}

func filepathJoinSafe(dir string, parts ...string) string {
	s := dir
	for _, p := range parts {
		if s == "" {
			s = p
			continue
		}
		s = s + string(os.PathSeparator) + p
	}
	return s
}

func (a *App) diagCrashBody(ktx *kit.Ctx) kit.Element {
	list := a.cachedCrashList()

	if len(list) == 0 {
		return kit.Column(6,
			usageEmpty(ktx, "没有异常报告",
				"（最近没崩过）\n"+"日志仍在持续记录："+core.LogPath()),
			kit.Left(kit.Button("crash.openlog", "打开日志目录", kit.ButtonSecondary, func() {
				revealPath(filepath.Dir(core.LogPath()), false)
			})),
		)
	}

	header := kit.Row(8,
		kit.Label(fmt.Sprintf("最近 %d 份异常报告（越靠上越新）", len(list)), ui.FsSm, 700, ui.CurrentTokens().TextSecondary),
		kit.Spacer(),
		kit.Button("crash.opendir", "打开报告目录", kit.ButtonGhost, func() { revealPath(core.CrashDir(), false) }),
	)

	rows := []kit.Element{}
	for _, p := range list {
		i := len(rows) / 2
		p := p
		_ = i
		size := int64(0)
		when := ""
		if sz, mt := a.cachedCrashMeta(p); sz > 0 {
			size = sz
			when = mt.Format("01-02 15:04:05")
		}
		base := filepath.Base(p)
		sub := fmt.Sprintf("%.1f KB · %s", float64(size)/1024, when)
		rows = append(rows, kit.Row(8,
			kit.Column(2,
				kit.Label(base, ui.FsSm, 700, ui.CurrentTokens().TextPrimary),
				kit.Label(sub, ui.FsXs, 400, ui.CurrentTokens().TextTertiary),
			),
			kit.Spacer(),
			kit.Button("crash.open:"+p, "打开", kit.ButtonGhost, func() {
				if !win32.ShellOpen(p) {
					a.toast("打不开 "+p, true)
				}
			}),
		))
	}
	return kit.Column(8, header, kit.Scroll("diag:crash", 0, kit.Column(4, rows...)))
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// isLocalEndpoint 判断接口地址是不是本机端点（这类服务不需要 API Key）。
func isLocalEndpoint(base string) bool {
	b := strings.ToLower(strings.TrimSpace(base))
	return strings.Contains(b, "127.0.0.1") || strings.Contains(b, "localhost") ||
		strings.Contains(b, "0.0.0.0") || strings.Contains(b, "[::1]")
}

// tabKind 按当前 tab 选对应按钮样式。
func tabKind(on bool) ButtonKind {
	if on {
		return BtnPrimary
	}
	return BtnGhost
}
